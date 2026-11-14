// Package apipartition is the jobs task that keeps partitions of the api
// metric tables ahead of the clock: minute partitions for
// today and the next 7 utc days, hourly partitions for this month and the
// next 3. the writer has no default partition to fall back on, so a missing
// partition would make every point of that day fail; creating a week ahead
// leaves days of margin for a jobs outage. expired partitions are dropped by
// apiretention, which owns the horizons.
package apipartition

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/db/jobsq"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "api-partition"

// Every is the period of the task. creation is idempotent and cheap, and
// hourly runs mean a week of margin is never more than an hour short.
const Every = time.Hour

// DaysAhead and MonthsAhead are the windows kept ahead of today (7
// days of daily, 3 months of monthly partitions).
const (
	DaysAhead   = 7
	MonthsAhead = 3
)

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Result lists what one run created.
type Result struct {
	MinuteDays   []time.Time
	HourlyMonths []time.Time
}

// Partitioner creates partitions ahead.
type Partitioner struct {
	now     func() time.Time
	log     *slog.Logger
	created *prometheus.CounterVec
}

// New returns a Partitioner. now is the clock; tests inject one.
func New(now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Partitioner, error) {
	if now == nil || log == nil || reg == nil {
		return nil, errors.New("apipartition: needs a clock, a log and a registerer")
	}
	p := &Partitioner{now: now, log: log, created: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_api_partitions_created_total",
		Help: "api metric partitions created ahead, by table (minute, hourly).",
	}, []string{"table"})}
	if err := reg.Register(p.created); err != nil {
		return nil, fmt.Errorf("apipartition: register metrics: %w", err)
	}
	// both series exist before the first creation, so an alert on their rate
	// can tell zero from absent
	p.created.WithLabelValues("minute")
	p.created.WithLabelValues("hourly")
	return p, nil
}

// Ensure creates every missing partition from today through DaysAhead days
// and from this month through MonthsAhead months, in one fenced
// transaction.
func (p *Partitioner) Ensure(ctx context.Context, tx TxFunc) (Result, error) {
	now := p.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var res Result
	err := tx(ctx, func(tx pgx.Tx) error {
		res = Result{}
		q := jobsq.New(tx)
		for i := range DaysAhead + 1 {
			d := today.AddDate(0, 0, i)
			created, err := q.CreateMinutePartition(ctx, d)
			if err != nil {
				return fmt.Errorf("minute partition %s: %w", d.Format(time.DateOnly), err)
			}
			if created {
				res.MinuteDays = append(res.MinuteDays, d)
			}
		}
		for i := range MonthsAhead + 1 {
			m := month.AddDate(0, i, 0)
			created, err := q.CreateHourlyPartition(ctx, m)
			if err != nil {
				return fmt.Errorf("hourly partition %s: %w", m.Format("2006-01"), err)
			}
			if created {
				res.HourlyMonths = append(res.HourlyMonths, m)
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("apipartition: %w", err)
	}
	p.created.WithLabelValues("minute").Add(float64(len(res.MinuteDays)))
	p.created.WithLabelValues("hourly").Add(float64(len(res.HourlyMonths)))
	if len(res.MinuteDays)+len(res.HourlyMonths) > 0 {
		p.log.Info("api partitions created", "minute_days", len(res.MinuteDays), "hourly_months", len(res.HourlyMonths))
	}
	return res, nil
}

// Dropped lists what DropExpired dropped and what it had to leave.
type Dropped struct {
	MinuteDays   []time.Time
	HourlyMonths []time.Time
	// Waiting are expired minute days that still have hours pending for the
	// rollup; they go on a later run, once the rollup has read them.
	Waiting []time.Time
}

// MinuteCutoff is the first utc day whose minute partition is kept: every
// day before it holds only data older than minuteDays days.
func MinuteCutoff(now time.Time, minuteDays int) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -minuteDays)
}

// HourlyCutoff is the first month whose hourly partition is kept. the
// current month counts as month zero, so at least hourlyMonths whole months
// before it stay.
func HourlyCutoff(now time.Time, hourlyMonths int) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -hourlyMonths, 0)
}

// DropExpired drops minute partitions of days before MinuteCutoff and
// hourly partitions of months before HourlyCutoff. each drop is its own
// fenced transaction, because a drop locks the parent table exclusively
// and the writer must not wait behind a whole batch of them. the caller
// owns the horizons and their floors (apiretention).
func (p *Partitioner) DropExpired(ctx context.Context, tx TxFunc, minuteDays, hourlyMonths int) (Dropped, error) {
	if minuteDays < 1 || hourlyMonths < 1 {
		return Dropped{}, fmt.Errorf("apipartition: horizons must be positive, have %d days and %d months", minuteDays, hourlyMonths)
	}
	now := p.now()
	minuteCut, hourlyCut := MinuteCutoff(now, minuteDays), HourlyCutoff(now, hourlyMonths)
	var days, months []time.Time
	err := tx(ctx, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		var err error
		if days, err = q.ListMinutePartitions(ctx); err != nil {
			return err
		}
		months, err = q.ListHourlyPartitions(ctx)
		return err
	})
	if err != nil {
		return Dropped{}, fmt.Errorf("apipartition: list partitions: %w", err)
	}
	var out Dropped
	for _, d := range days {
		d = d.UTC()
		if !d.Before(minuteCut) {
			continue
		}
		var dropped, waiting bool
		err := tx(ctx, func(tx pgx.Tx) error {
			q := jobsq.New(tx)
			pending, err := q.RollupPendingInDay(ctx, d)
			if err != nil {
				return err
			}
			if pending {
				waiting = true
				return nil
			}
			dropped, err = q.DropMinutePartition(ctx, d)
			return err
		})
		if err != nil {
			return out, fmt.Errorf("apipartition: drop minute partition %s: %w", d.Format(time.DateOnly), err)
		}
		switch {
		case waiting:
			out.Waiting = append(out.Waiting, d)
		case dropped:
			out.MinuteDays = append(out.MinuteDays, d)
		}
	}
	for _, m := range months {
		m = m.UTC()
		if !m.Before(hourlyCut) {
			continue
		}
		var dropped bool
		err := tx(ctx, func(tx pgx.Tx) error {
			var err error
			dropped, err = jobsq.New(tx).DropHourlyPartition(ctx, m)
			return err
		})
		if err != nil {
			return out, fmt.Errorf("apipartition: drop hourly partition %s: %w", m.Format("2006-01"), err)
		}
		if dropped {
			out.HourlyMonths = append(out.HourlyMonths, m)
		}
	}
	if len(out.Waiting) > 0 {
		p.log.Warn("expired minute partitions wait for the rollup", "days", len(out.Waiting))
	}
	if len(out.MinuteDays)+len(out.HourlyMonths) > 0 {
		p.log.Info("api partitions dropped", "minute_days", len(out.MinuteDays), "hourly_months", len(out.HourlyMonths))
	}
	return out, nil
}
