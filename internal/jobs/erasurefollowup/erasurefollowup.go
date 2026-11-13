// Package erasurefollowup is the jobs task erasure-followup (the design,
// control-plane-api 10.5). it finishes what an erasure run in the control
// plane leaves to jobs: it reads the affected utc days of agg.web_daily
// again from umami, deletes every aggregate of a reset site, and fails a run
// whose control plane process stopped renewing its heartbeat.
package erasurefollowup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/erasure"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/jobs/aggregateweb"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "erasure-followup"

// Every is the period of the task.
const Every = time.Minute

// batch bounds the requests handled per run.
const batch = 100

// TxFunc runs fn in one fenced transaction (scheduler.Leader.Tx or SystemTx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Result counts what one run did.
type Result struct {
	Stale        int
	Reaggregated int
	SiteDays     int
}

// Followup runs the task.
type Followup struct {
	agg *aggregateweb.Aggregator
	now func() time.Time
	log *slog.Logger
}

// New returns the task.
func New(agg *aggregateweb.Aggregator, now func() time.Time, log *slog.Logger) (*Followup, error) {
	if agg == nil || now == nil || log == nil {
		return nil, errors.New("erasurefollowup: needs the aggregator, a clock and a log")
	}
	return &Followup{agg: agg, now: now, log: log}, nil
}

// Run fails stale runs, then reads the aggregates of finished requests
// again. jobsTx writes agg (role jobs), systemTx reads and marks requests
// (role cp_system). a request whose aggregates fail stays unmarked and is
// tried again next run.
func (f *Followup) Run(ctx context.Context, jobsTx, systemTx TxFunc) (Result, error) {
	var res Result
	var pending []systemq.CpErasureRequest
	err := systemTx(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		before := f.now().Add(-erasure.StaleAfter)
		stale, err := q.FailStaleErasureRequests(ctx, &before)
		if err != nil {
			return err
		}
		res.Stale = len(stale)
		pending, err = q.ListErasureReaggregation(ctx, batch)
		return err
	})
	if err != nil {
		return res, fmt.Errorf("erasurefollowup: %w", err)
	}
	var errs []error
	for _, r := range pending {
		n, err := f.one(ctx, jobsTx, r)
		res.SiteDays += n
		if err != nil {
			errs = append(errs, fmt.Errorf("request %s: %w", r.ID, err))
			continue
		}
		if err := systemTx(ctx, func(tx pgx.Tx) error {
			_, err := systemq.New(tx).SetErasureReaggregated(ctx, r.ID)
			return err
		}); err != nil {
			errs = append(errs, fmt.Errorf("request %s: mark reaggregated: %w", r.ID, err))
			continue
		}
		res.Reaggregated++
	}
	f.log.Info("erasure followup", "stale", res.Stale, "reaggregated", res.Reaggregated, "site_days", res.SiteDays, "failed", len(errs))
	if len(errs) > 0 {
		return res, fmt.Errorf("erasurefollowup: %w", errors.Join(errs...))
	}
	return res, nil
}

func (f *Followup) one(ctx context.Context, jobsTx TxFunc, r systemq.CpErasureRequest) (int, error) {
	site, ok := f.agg.Site(r.SiteID.String())
	if !ok {
		// a removed site is gone from the registry; its aggregates go with
		// the tenant purge or stay by removal_keep_aggregates, which is
		// not this task's decision
		return 0, nil
	}
	tx := aggregateweb.TxFunc(jobsTx)
	if r.Kind == erasure.KindSiteReset {
		if err := jobsTx(ctx, func(tx pgx.Tx) error {
			q := jobsq.New(tx)
			if err := q.LockAggTenant(ctx, r.TenantID.String()); err != nil {
				return err
			}
			_, err := q.DeleteSiteAggregates(ctx, r.SiteID)
			return err
		}); err != nil {
			return 0, err
		}
		// the regular run may have read umami before the reset; reading the
		// days it covers again writes what umami holds now
		today := aggregateweb.UTCDay(f.now())
		return f.agg.BackfillSite(ctx, tx, site, today.AddDate(0, 0, -aggregateweb.RereadDays), today)
	}
	return f.agg.BackfillSite(ctx, tx, site, *r.AffectedFrom, *r.AffectedTo)
}
