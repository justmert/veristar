// Package apirollup is the jobs task that rolls api minute rows up to hours
// and days (the design). the writer marks every utc hour it writes minute
// rows for in api.rollup_pending; each run claims those marks and, in the
// same fenced transaction, recomputes each claimed hour from its minutes and
// then each day that contains one from its hours. recomputing replaces the
// rows, so a run over unchanged minutes writes the same rows again and a
// claimed hour is never added twice. metric and span rows stay apart because
// source is part of every key.
package apirollup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/db/jobsq"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "api-rollup"

// Every is the period of the task (the design: every 5 minutes).
const Every = 5 * time.Minute

// ClaimBatch is how many pending (tenant, hour) marks one transaction takes.
// it bounds the transaction: at the plan's volume a 5 minute run finds about
// one mark per active tenant, and a backlog after an outage is worked off in
// batches instead of one long transaction that holds every mark's lock.
const ClaimBatch = 500

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Result counts what one run did.
type Result struct {
	Hours      int
	Days       int
	HourlyRows int64
	DailyRows  int64
}

// Rollup recomputes the pending hours.
type Rollup struct {
	log     *slog.Logger
	hours   prometheus.Counter
	days    prometheus.Counter
	pending prometheus.Gauge
}

// New returns a Rollup and registers its metrics.
func New(log *slog.Logger, reg prometheus.Registerer) (*Rollup, error) {
	if log == nil || reg == nil {
		return nil, errors.New("apirollup: needs a log and a registerer")
	}
	r := &Rollup{
		log: log,
		hours: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_api_rollup_hours_total",
			Help: "tenant hours of api.metrics_hourly recomputed from minute rows.",
		}),
		days: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_api_rollup_days_total",
			Help: "tenant days of api.metrics_daily recomputed from hourly rows.",
		}),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "jobs_api_rollup_last_batch_hours",
			Help: "tenant hours claimed by the last rollup transaction; ClaimBatch means a backlog.",
		}),
	}
	for _, c := range []prometheus.Collector{r.hours, r.days, r.pending} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("apirollup: register metrics: %w", err)
		}
	}
	return r, nil
}

type tenantDay struct {
	tenant uuid.UUID
	day    time.Time
}

// Run claims and recomputes pending hours in batches until none is left.
// each batch is one fenced transaction: a fenced-off or failing batch rolls
// back, which puts its marks back for the next leader.
func (r *Rollup) Run(ctx context.Context, tx TxFunc) (Result, error) {
	var total Result
	for {
		res, err := r.batch(ctx, tx)
		if err != nil {
			return total, err
		}
		total.Hours += res.Hours
		total.Days += res.Days
		total.HourlyRows += res.HourlyRows
		total.DailyRows += res.DailyRows
		if res.Hours < ClaimBatch {
			break
		}
	}
	if total.Hours > 0 {
		r.log.Info("api rollup", "hours", total.Hours, "days", total.Days,
			"hourly_rows", total.HourlyRows, "daily_rows", total.DailyRows)
	}
	return total, nil
}

func (r *Rollup) batch(ctx context.Context, tx TxFunc) (Result, error) {
	var res Result
	err := tx(ctx, func(tx pgx.Tx) error {
		res = Result{}
		q := jobsq.New(tx)
		claimed, err := q.ClaimRollupHours(ctx, ClaimBatch)
		if err != nil {
			return fmt.Errorf("claim: %w", err)
		}
		if len(claimed) == 0 {
			return nil
		}
		// an hour whose minute partition was dropped has no minutes left to
		// recompute from; replacing its hourly rows would erase them, so the
		// whole batch fails instead and its marks stay for a person to look at
		parts, err := q.ListMinutePartitions(ctx)
		if err != nil {
			return fmt.Errorf("minute partitions: %w", err)
		}
		have := make(map[time.Time]bool, len(parts))
		for _, d := range parts {
			have[utcDay(d)] = true
		}
		days := map[tenantDay]bool{}
		var order []tenantDay
		for _, c := range claimed {
			hour := c.Hour.UTC()
			if !have[utcDay(hour)] {
				return fmt.Errorf("tenant %s hour %s is pending but its minute partition is gone",
					c.TenantID, hour.Format(time.RFC3339))
			}
			if _, err := q.DeleteHourlyRows(ctx, jobsq.DeleteHourlyRowsParams{TenantID: c.TenantID, Hour: hour}); err != nil {
				return fmt.Errorf("delete hour %s of %s: %w", hour.Format(time.RFC3339), c.TenantID, err)
			}
			n, err := q.RollupHour(ctx, jobsq.RollupHourParams{TenantID: c.TenantID, Hour: hour})
			if err != nil {
				return fmt.Errorf("roll up hour %s of %s: %w", hour.Format(time.RFC3339), c.TenantID, err)
			}
			res.Hours++
			res.HourlyRows += n
			k := tenantDay{tenant: c.TenantID, day: utcDay(hour)}
			if !days[k] {
				days[k] = true
				order = append(order, k)
			}
		}
		// days after hours: a day is the sum of its hourly rows, which must
		// hold the hours just recomputed
		for _, k := range order {
			if _, err := q.DeleteDailyRows(ctx, jobsq.DeleteDailyRowsParams{TenantID: k.tenant, Day: k.day}); err != nil {
				return fmt.Errorf("delete day %s of %s: %w", k.day.Format(time.DateOnly), k.tenant, err)
			}
			n, err := q.RollupDay(ctx, jobsq.RollupDayParams{TenantID: k.tenant, Day: k.day})
			if err != nil {
				return fmt.Errorf("roll up day %s of %s: %w", k.day.Format(time.DateOnly), k.tenant, err)
			}
			res.Days++
			res.DailyRows += n
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("apirollup: %w", err)
	}
	r.pending.Set(float64(res.Hours))
	r.hours.Add(float64(res.Hours))
	r.days.Add(float64(res.Days))
	return res, nil
}

// utcDay is the start of t's utc day. dates read from postgres arrive as
// midnight utc already; the conversion makes hours and days comparable.
func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
