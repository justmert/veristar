// Package apirollup is the jobs task that rolls api minute rows up to hours
// and days. the writer marks every utc hour it writes minute
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
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/db/jobsq"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "api-rollup"

// Every is the period of the task (every 5 minutes).
const Every = 5 * time.Minute

// ClaimBatch is how many pending (tenant, hour) marks one transaction takes.
// it bounds the transaction: at the design volume a 5 minute run finds about
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
	// Parked counts claimed hours the database refused to recompute (a
	// data error, such as a sum past bigint); their marks went back
	// parked with the error, and the other hours carried on.
	Parked int
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
	parked   prometheus.Counter
	parkedN  prometheus.Gauge
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
	r.parked = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "jobs_api_rollup_parked_hours_total",
		Help: "pending hours the database refused to recompute; each was parked with its error and is no longer claimed.",
	})
	r.parkedN = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "jobs_api_rollup_parked_hours",
		Help: "pending hours parked now; above 0 needs an operator (docs/runbooks/alerts.md).",
	})
	for _, c := range []prometheus.Collector{r.hours, r.days, r.pending, r.orphaned, r.drifted, r.parked, r.parkedN} {
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
	first := r.lastDrift.IsZero()
	drift := first || r.now().Sub(r.lastDrift) >= DriftEvery
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
		progressed := res.Hours+res.Orphaned+res.Parked > 0
		if claimed < ClaimBatch || !progressed {
			// the check runs at most once per DriftEvery, after the backlog
			// is worked off, and what it marks is recomputed by one more
			// batch
			if !drift {
				break
			}
			drift = false
			r.lastDrift = r.now()
			n, err := r.checkDrift(ctx, tx, r.lastDrift, first)
			first = false
			if err != nil {
				return total, err
			}
			total.Drifted += n
			if n == 0 {
				break
			}
		}
	}
	if total.Hours+total.Busy+total.Orphaned+total.Drifted+total.Parked > 0 {
		r.log.Info("api rollup", "hours", total.Hours, "days", total.Days,
			"hourly_rows", total.HourlyRows, "daily_rows", total.DailyRows,
			"busy", total.Busy, "orphaned", total.Orphaned, "drifted", total.Drifted, "parked", total.Parked)
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
	r.Parked += b.Parked
}

func (r *Rollup) batch(ctx context.Context, tx TxFunc) (Result, int, error) {
	var res Result
	var claimedN int
	var parked int64
	work := func(tx pgx.Tx) error {
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
		days := map[tenantDay][]time.Time{}
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
			// each hour in its own savepoint: an hour the database refuses
			// (a count sum past bigint, from one tenant's absurd counts) is
			// parked, and the batch, with every other tenant's hours,
			// commits
			var n int64
			err = savepoint(ctx, tx, func(q *jobsq.Queries) error {
				if _, err := q.DeleteHourlyRows(ctx, jobsq.DeleteHourlyRowsParams{TenantID: c.TenantID, Hour: hour}); err != nil {
					return fmt.Errorf("delete hour %s of %s: %w", hour.Format(time.RFC3339), c.TenantID, err)
				}
				var err error
				n, err = q.RollupHour(ctx, jobsq.RollupHourParams{TenantID: c.TenantID, Hour: hour})
				if err != nil {
					return fmt.Errorf("roll up hour %s of %s: %w", hour.Format(time.RFC3339), c.TenantID, err)
				}
				return nil
			})
			if dataError(err) {
				if err := r.park(ctx, q, c.TenantID, hour, err); err != nil {
					return err
				}
				res.Parked++
				continue
			}
			if err != nil {
				return err
			}
			res.Hours++
			res.HourlyRows += n
			k := tenantDay{tenant: c.TenantID, day: utcDay(hour)}
			if _, ok := days[k]; !ok {
				order = append(order, k)
			}
			days[k] = append(days[k], hour)
		}
		// days after hours: a day is the sum of its hourly rows, which must
		// hold the hours just recomputed
		for _, k := range order {
			var n int64
			err := savepoint(ctx, tx, func(q *jobsq.Queries) error {
				if _, err := q.DeleteDailyRows(ctx, jobsq.DeleteDailyRowsParams{TenantID: k.tenant, Day: k.day}); err != nil {
					return fmt.Errorf("delete day %s of %s: %w", k.day.Format(time.DateOnly), k.tenant, err)
				}
				var err error
				n, err = q.RollupDay(ctx, jobsq.RollupDayParams{TenantID: k.tenant, Day: k.day})
				if err != nil {
					return fmt.Errorf("roll up day %s of %s: %w", k.day.Format(time.DateOnly), k.tenant, err)
				}
				return nil
			})
			if dataError(err) {
				// the day's hours were recomputed, but the day refuses their
				// sum: they are parked, so the day is not left behind them
				// unnoticed and readers keep taking those hours from minutes
				for _, hour := range days[k] {
					if err := r.park(ctx, q, k.tenant, hour, err); err != nil {
						return err
					}
					res.Parked++
				}
				continue
			}
			if err != nil {
				return err
			}
			res.Days++
			res.DailyRows += n
		}
		return nil
	}
	err := tx(ctx, func(tx pgx.Tx) error {
		if err := work(tx); err != nil {
			return err
		}
		// the parked hours of the whole table, for the gauge: a small
		// count, read in the batch so a run needs no extra transaction
		var err error
		parked, err = jobsq.New(tx).CountParkedRollupHours(ctx)
		return err
	})
	if err != nil {
		return Result{}, 0, fmt.Errorf("apirollup: %w", err)
	}
	r.pending.Set(float64(res.Hours))
	r.hours.Add(float64(res.Hours))
	r.days.Add(float64(res.Days))
	r.orphaned.Add(float64(res.Orphaned))
	r.parked.Add(float64(res.Parked))
	r.parkedN.Set(float64(parked))
	return res, claimedN, nil
}

// savepoint runs fn inside a savepoint of tx and rolls back to it when fn
// fails, so the transaction can go on.
func savepoint(ctx context.Context, tx pgx.Tx, fn func(*jobsq.Queries) error) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("savepoint: %w", err)
	}
	if err := fn(jobsq.New(sp)); err != nil {
		_ = sp.Rollback(ctx)
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return fmt.Errorf("release savepoint: %w", err)
	}
	return nil
}

// dataError reports a data exception (sqlstate class 22, numeric value out
// of range among them) or an integrity violation (23): the stored values
// refuse the result, so recomputing again gives the same error.
func dataError(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (strings.HasPrefix(pe.Code, "22") || strings.HasPrefix(pe.Code, "23"))
}

// park puts a claimed hour's mark back, parked with the error.
func (r *Rollup) park(ctx context.Context, q *jobsq.Queries, tenant uuid.UUID, hour time.Time, cause error) error {
	r.log.Error("the database refused to recompute a pending hour, it is parked and left out of later runs",
		"tenant", tenant.String(), "hour", hour.Format(time.RFC3339), "error", cause.Error())
	if err := q.ParkRollupHour(ctx, jobsq.ParkRollupHourParams{TenantID: tenant, Hour: hour, Error: cause.Error()}); err != nil {
		return fmt.Errorf("park hour %s of %s: %w", hour.Format(time.RFC3339), tenant, err)
	}
	return nil
}

// checkDrift marks again every recent hour whose hourly rows disagree with
// its minute rows and that no mark covers. with the hour locks nothing
// should be found; a finding means a mark was lost some other way, and it is
// repaired by the next batch and reported.
//
// the first check of a process reaches back to when the traffic class rule
// set took effect, at most as far as minute rows are always kept. a rollup
// of the release before it, still running on another node during the
// rolling deploy, stores every class of an hour under 'app'; the per-class
// comparison finds such an hour, and recomputing it fixes its day as well.
func (r *Rollup) checkDrift(ctx context.Context, tx TxFunc, now time.Time, first bool) (int, error) {
	to := now.UTC().Truncate(time.Hour)
	from := to.Add(-DriftWindow)
	var marked []jobsq.MarkDriftedHoursRow
	err := tx(ctx, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		if first {
			applied, err := q.TrafficClassRulesFrom(ctx)
			if err != nil {
				return fmt.Errorf("traffic class rules: %w", err)
			}
			oldest := to.AddDate(0, 0, -config.MinMinuteRetentionDays)
			if a := applied.UTC().Truncate(time.Hour); a.Before(from) {
				from = a
				if from.Before(oldest) {
					from = oldest
				}
			}
		}
		var err error
		marked, err = q.MarkDriftedHours(ctx, jobsq.MarkDriftedHoursParams{FromHour: from, ToHour: to})
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
