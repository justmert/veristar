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

	"github.com/justmert/veristar/internal/db/jobsq"
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
	// Busy counts claimed hours a writer still had open; their marks went
	// back for a later run.
	Busy int
	// Orphaned counts claimed hours whose minute partition was gone.
	Orphaned int
	// Drifted counts hours the consistency check found disagreeing with
	// their minutes without a mark, and marked again.
	Drifted int
}

// DriftWindow is how far back the consistency check compares hourly rows
// with their minutes. a lost mark shows up within minutes, and a day bounds
// what each run scans.
const DriftWindow = 24 * time.Hour

// DriftEvery is how often the consistency check runs. it scans
// a day of minute rows, so it runs hourly rather than in every 5 minute
// rollup run; a lost mark is a bug path, and an hour is soon enough.
const DriftEvery = time.Hour

// Rollup recomputes the pending hours.
type Rollup struct {
	log      *slog.Logger
	now      func() time.Time
	hours    prometheus.Counter
	days     prometheus.Counter
	pending  prometheus.Gauge
	orphaned prometheus.Counter
	drifted  prometheus.Counter
	// lastDrift is when the consistency check last ran in this process;
	// the scheduler runs one Run at a time, so it needs no lock
	lastDrift time.Time
}

// New returns a Rollup and registers its metrics.
func New(log *slog.Logger, reg prometheus.Registerer) (*Rollup, error) {
	if log == nil || reg == nil {
		return nil, errors.New("apirollup: needs a log and a registerer")
	}
	r := &Rollup{
		log: log,
		now: time.Now,
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
	r.orphaned = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "jobs_api_rollup_orphaned_marks_total",
		Help: "pending hours whose minute partition was gone; their hourly rows were kept as they were. any increase needs a look.",
	})
	r.drifted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "jobs_api_rollup_drifted_hours_total",
		Help: "hours whose hourly rows disagreed with their minute rows without a pending mark; marked again. any increase means a lost mark.",
	})
	for _, c := range []prometheus.Collector{r.hours, r.days, r.pending, r.orphaned, r.drifted} {
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
	drift := r.lastDrift.IsZero() || r.now().Sub(r.lastDrift) >= DriftEvery
	for {
		res, claimed, err := r.batch(ctx, tx)
		if err != nil {
			return total, err
		}
		total.add(res)
		// a full batch is followed by another only when it got somewhere:
		// marks of hours a writer still holds go back and, being oldest,
		// would be claimed again at once, so a batch of busy marks would
		// loop back to back until the writers paused. they
		// wait for the next run instead.
		progressed := res.Hours+res.Orphaned > 0
		if claimed < ClaimBatch || !progressed {
			// the check runs at most once per DriftEvery, after the backlog
			// is worked off, and what it marks is recomputed by one more
			// batch
			if !drift {
				break
			}
			drift = false
			r.lastDrift = r.now()
			n, err := r.checkDrift(ctx, tx, r.lastDrift)
			if err != nil {
				return total, err
			}
			total.Drifted += n
			if n == 0 {
				break
			}
		}
	}
	if total.Hours+total.Busy+total.Orphaned+total.Drifted > 0 {
		r.log.Info("api rollup", "hours", total.Hours, "days", total.Days,
			"hourly_rows", total.HourlyRows, "daily_rows", total.DailyRows,
			"busy", total.Busy, "orphaned", total.Orphaned, "drifted", total.Drifted)
	}
	return total, nil
}

func (r *Result) add(b Result) {
	r.Hours += b.Hours
	r.Days += b.Days
	r.HourlyRows += b.HourlyRows
	r.DailyRows += b.DailyRows
	r.Busy += b.Busy
	r.Orphaned += b.Orphaned
}

func (r *Rollup) batch(ctx context.Context, tx TxFunc) (Result, int, error) {
	var res Result
	var claimedN int
	err := tx(ctx, func(tx pgx.Tx) error {
		res = Result{}
		q := jobsq.New(tx)
		claimed, err := q.ClaimRollupHours(ctx, ClaimBatch)
		if err != nil {
			return fmt.Errorf("claim: %w", err)
		}
		claimedN = len(claimed)
		if len(claimed) == 0 {
			return nil
		}
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
				// the minutes of this hour are gone (a partition dropped or
				// restored by hand: retention waits for pending days), so
				// the hourly rows stay the only record and are left as they
				// are. failing the batch would stop every other tenant's
				// rollup until a person intervened; the mark is
				// dropped with an error log and a counter to alert on.
				res.Orphaned++
				r.log.Error("pending hour has no minute partition, its hourly rows are kept as they are",
					"tenant", c.TenantID.String(), "hour", hour.Format(time.RFC3339))
				continue
			}
			// a writer that is still writing minute rows into this hour
			// holds its lock; recomputing now would miss those rows and,
			// with the mark gone, they would never reach the hourly and
			// daily rows. the mark goes back for a later run.
			locked, err := q.TryLockRollupHour(ctx, jobsq.TryLockRollupHourParams{TenantID: c.TenantID, Hour: hour})
			if err != nil {
				return fmt.Errorf("lock hour %s of %s: %w", hour.Format(time.RFC3339), c.TenantID, err)
			}
			if !locked {
				if err := q.RestoreRollupMark(ctx, jobsq.RestoreRollupMarkParams{TenantID: c.TenantID, Hour: hour}); err != nil {
					return fmt.Errorf("restore mark %s of %s: %w", hour.Format(time.RFC3339), c.TenantID, err)
				}
				res.Busy++
				continue
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
		return Result{}, 0, fmt.Errorf("apirollup: %w", err)
	}
	r.pending.Set(float64(res.Hours))
	r.hours.Add(float64(res.Hours))
	r.days.Add(float64(res.Days))
	r.orphaned.Add(float64(res.Orphaned))
	return res, claimedN, nil
}

// checkDrift marks again every recent hour whose hourly rows disagree with
// its minute rows and that no mark covers. with the hour locks nothing
// should be found; a finding means a mark was lost some other way, and it is
// repaired by the next batch and reported.
func (r *Rollup) checkDrift(ctx context.Context, tx TxFunc, now time.Time) (int, error) {
	to := now.UTC().Truncate(time.Hour)
	from := to.Add(-DriftWindow)
	var marked []jobsq.MarkDriftedHoursRow
	err := tx(ctx, func(tx pgx.Tx) error {
		var err error
		marked, err = jobsq.New(tx).MarkDriftedHours(ctx, jobsq.MarkDriftedHoursParams{FromHour: from, ToHour: to})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("apirollup: consistency check: %w", err)
	}
	for _, m := range marked {
		r.log.Error("hourly rows disagree with their minute rows without a pending mark, the hour is marked again",
			"tenant", m.TenantID.String(), "hour", m.Hour.UTC().Format(time.RFC3339))
	}
	r.drifted.Add(float64(len(marked)))
	return len(marked), nil
}

// utcDay is the start of t's utc day. dates read from postgres arrive as
// midnight utc already; the conversion makes hours and days comparable.
func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
