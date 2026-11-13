// Package apiretention is the jobs task that enforces api metric retention
// (the design): minute rows for API_MINUTE_RETENTION_DAYS (30), hourly
// rows for API_HOURLY_RETENTION_MONTHS (36, never below 24), daily rows
// forever. metric rows expire by whole partitions (apipartition.DropExpired);
// the writer's bookkeeping expires by rows: ingest_dedup ids after 8 days,
// series_state and resource_seen after 7 idle days, and route_registry
// days older than API_METRICS can still deliver points for.
package apiretention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/db/jobsq"
	"github.com/justmert/stellar-analytics/internal/jobs/apipartition"
	"github.com/justmert/stellar-analytics/internal/metricswriter"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "api-retention"

// Every is the period of the task. horizons are whole days and months, so
// hourly runs drop a partition at most an hour after it expires.
const Every = time.Hour

// DedupMaxAge outlives API_METRICS' 7-day max age by a day, so an id is
// never pruned while its message can still be delivered.
const DedupMaxAge = 8 * 24 * time.Hour

// IdleMaxAge is how long a series or resource the writer stopped seeing is
// remembered. a stream silent for a week has restarted or gone; if it comes
// back, the first-point rule treats it as new.
const IdleMaxAge = 7 * 24 * time.Hour

// RouteDaysKept is how many utc days of route caps are kept, today
// included. a backlog on API_METRICS can deliver points up to 7 days old,
// and their day's cap must still hold what it admitted, or a late batch
// would admit another full set of routes for that day.
const RouteDaysKept = 8

// ErrBelowFloor is returned for an hourly horizon under the rfp's 24 months.
var ErrBelowFloor = fmt.Errorf("apiretention: hourly retention below %d months is refused", config.MinHourlyRetentionMonths)

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Result counts what one run removed.
type Result struct {
	Dropped       apipartition.Dropped
	IngestDedup   int64
	SeriesState   int64
	ResourceSeen  int64
	RouteRegistry int64
}

// Retention enforces the horizons.
type Retention struct {
	cfg     config.APIRetention
	parts   *apipartition.Partitioner
	now     func() time.Time
	log     *slog.Logger
	removed *prometheus.CounterVec
}

// New returns a Retention. it refuses an hourly horizon below 24 months
// itself, so no caller can bypass the floor by building the config by hand.
// parts must share the clock now.
func New(cfg config.APIRetention, parts *apipartition.Partitioner, now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Retention, error) {
	if parts == nil || now == nil || log == nil || reg == nil {
		return nil, errors.New("apiretention: needs a partitioner, a clock, a log and a registerer")
	}
	if cfg.HourlyMonths < config.MinHourlyRetentionMonths {
		return nil, fmt.Errorf("%w: have %d", ErrBelowFloor, cfg.HourlyMonths)
	}
	if cfg.MinuteDays < config.MinMinuteRetentionDays {
		return nil, fmt.Errorf("apiretention: minute retention must be at least %d days, the writer's late-data window, have %d",
			config.MinMinuteRetentionDays, cfg.MinuteDays)
	}
	r := &Retention{cfg: cfg, parts: parts, now: now, log: log, removed: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_api_retention_removed_total",
		Help: "api rows and partitions removed by retention, by kind.",
	}, []string{"kind"})}
	if err := reg.Register(r.removed); err != nil {
		return nil, fmt.Errorf("apiretention: register metrics: %w", err)
	}
	for _, k := range kinds {
		r.removed.WithLabelValues(k)
	}
	return r, nil
}

var kinds = []string{"minute_partition", "hourly_partition", "ingest_dedup", "series_state", "resource_seen", "route_registry"}

// Run drops expired partitions, then prunes the bookkeeping tables in one
// fenced transaction.
func (r *Retention) Run(ctx context.Context, tx TxFunc) (Result, error) {
	var res Result
	dropped, err := r.parts.DropExpired(ctx, apipartition.TxFunc(tx), r.cfg.MinuteDays, r.cfg.HourlyMonths)
	// what was dropped before a failure is counted; the failure still stops
	// the run so the scheduler reports it
	res.Dropped = dropped
	r.removed.WithLabelValues("minute_partition").Add(float64(len(dropped.MinuteDays)))
	r.removed.WithLabelValues("hourly_partition").Add(float64(len(dropped.HourlyMonths)))
	if err != nil {
		return res, fmt.Errorf("apiretention: %w", err)
	}

	now := r.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	err = tx(ctx, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		var err error
		if res.IngestDedup, err = q.PruneIngestDedup(ctx, now.Add(-DedupMaxAge)); err != nil {
			return fmt.Errorf("ingest_dedup: %w", err)
		}
		if res.SeriesState, err = q.PruneSeriesState(ctx, now.Add(-IdleMaxAge)); err != nil {
			return fmt.Errorf("series_state: %w", err)
		}
		// the writer caps only active streams, so the stored
		// ones are bounded here, by evicting the longest idle
		evicted, err := q.EvictIdleSeriesStates(ctx, jobsq.EvictIdleSeriesStatesParams{
			IdleBefore: now.Add(-metricswriter.ActiveSeriesWindow), MaxStored: metricswriter.MaxStoredSeriesPerTenant,
		})
		if err != nil {
			return fmt.Errorf("series_state eviction: %w", err)
		}
		res.SeriesState += evicted
		if res.ResourceSeen, err = q.PruneResourceSeen(ctx, now.Add(-IdleMaxAge)); err != nil {
			return fmt.Errorf("resource_seen: %w", err)
		}
		if res.RouteRegistry, err = q.PruneRouteRegistry(ctx, today.AddDate(0, 0, 1-RouteDaysKept)); err != nil {
			return fmt.Errorf("route_registry: %w", err)
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("apiretention: prune: %w", err)
	}
	r.removed.WithLabelValues("ingest_dedup").Add(float64(res.IngestDedup))
	r.removed.WithLabelValues("series_state").Add(float64(res.SeriesState))
	r.removed.WithLabelValues("resource_seen").Add(float64(res.ResourceSeen))
	r.removed.WithLabelValues("route_registry").Add(float64(res.RouteRegistry))
	r.log.Info("api retention", "minute_partitions", len(dropped.MinuteDays), "hourly_partitions", len(dropped.HourlyMonths),
		"waiting_for_rollup", len(dropped.Waiting), "ingest_dedup", res.IngestDedup, "series_state", res.SeriesState,
		"resource_seen", res.ResourceSeen, "route_registry", res.RouteRegistry)
	return res, nil
}
