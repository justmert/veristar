// Package verifydomains is the jobs task verify-domains
// (control-plane-api 5.8 and 5.12): it re-checks domain ownership and
// re-reads stellar.toml for every non-removed site of a non-removed tenant
// whose last check is older than a day. the results are evidence for
// operators; nothing here gates ingestion.
package verifydomains

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	"github.com/justmert/veristar/internal/db/systemq"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "verify-domains"

// Actor of the audit rows the task writes.
const Actor = "system:verify-domains"

// Every is how often the task looks for due sites. a site is due a day
// after its last check, so hourly keeps every check within 25 hours.
const Every = time.Hour

// MaxAge is how old a check may get before the site is checked again.
const MaxAge = 24 * time.Hour

// Concurrency is how many sites are checked at once; each check makes at
// most one dns lookup and two guarded fetches.
const Concurrency = 4

// batch caps the sites of one run, so one run stays well inside the hour;
// the oldest checks go first and the next run continues.
const batch = 500

// TxFunc runs fn in one fenced cp_system transaction
// (scheduler.Leader.SystemTx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Task checks due sites.
type Task struct {
	checker *domaincheck.Checker
	now     func() time.Time
	log     *slog.Logger
	checked *prometheus.CounterVec
}

// New returns a Task. now is the clock; tests inject one.
func New(c *domaincheck.Checker, now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Task, error) {
	if c == nil || c.Guard == nil || c.Resolver == nil || now == nil || log == nil || reg == nil {
		return nil, errors.New("verifydomains: needs a checker with guard and resolver, a clock, a log and a registerer")
	}
	t := &Task{checker: c, now: now, log: log, checked: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "jobs_domain_checks_total",
		Help: "Domain checks by the verify-domains task, by kind (verify, stellar_toml) and result (ok or the error code or toml status).",
	}, []string{"kind", "result"})}
	if err := reg.Register(t.checked); err != nil {
		return nil, fmt.Errorf("verifydomains: register metrics: %w", err)
	}
	return t, nil
}

// Result says what one run did.
type Result struct {
	Sites  int
	Failed int
}

// Run checks every due site once. a site whose write fails is counted and
// logged, and the run goes on; the error returned names how many failed,
// so the scheduler records the run as failed.
func (t *Task) Run(ctx context.Context, tx TxFunc) (Result, error) {
	cutoff := t.now().Add(-MaxAge)
	var due []systemq.ListSitesDueForCheckRow
	err := tx(ctx, func(x pgx.Tx) error {
		var qerr error
		due, qerr = systemq.New(x).ListSitesDueForCheck(ctx, systemq.ListSitesDueForCheckParams{Cutoff: &cutoff, RowLimit: batch})
		return qerr
	})
	if err != nil {
		return Result{}, fmt.Errorf("verifydomains: list due sites: %w", err)
	}
	jobs := make(chan systemq.ListSitesDueForCheckRow)
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	for range Concurrency {
		wg.Go(func() {
			for s := range jobs {
				if err := t.one(ctx, tx, s); err != nil {
					t.log.Error("domain check failed", "site_id", s.ID.String(), "error", err.Error())
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		})
	}
	for _, s := range due {
		if ctx.Err() != nil {
			break
		}
		jobs <- s
	}
	close(jobs)
	wg.Wait()
	res := Result{Sites: len(due), Failed: len(errs)}
	t.log.Info("domain checks done", "sites", res.Sites, "failed", res.Failed)
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if len(errs) > 0 {
		return res, fmt.Errorf("verifydomains: %d of %d sites failed: %w", len(errs), len(due), errors.Join(errs...))
	}
	return res, nil
}

// one checks a site: both verification methods and stellar.toml, network
// first, then one short fenced transaction for the writes.
func (t *Task) one(ctx context.Context, tx TxFunc, s systemq.ListSitesDueForCheckRow) error {
	o, err := t.checker.Verify(ctx, s.Domain, s.VerifyToken, "")
	if err != nil {
		return err
	}
	st := domaincheck.FetchStellarToml(ctx, t.checker.Guard, s.Domain)
	verifyResult := "ok"
	if !o.OK {
		verifyResult = o.Error
	}
	t.checked.WithLabelValues("verify", verifyResult).Inc()
	t.checked.WithLabelValues("stellar_toml", st.Status).Inc()
	prev := domaincheck.VerifyState{VerifiedAt: s.VerifiedAt, LastVerifyAt: s.LastVerifyAt, LastVerifyError: s.LastVerifyError}
	return tx(ctx, func(x pgx.Tx) error {
		if err := domaincheck.StoreVerifySystem(ctx, x, s.TenantID, s.ID, prev, o, Actor); err != nil {
			return err
		}
		return domaincheck.StoreSystem(ctx, x, s.TenantID, s.ID, st, Actor)
	})
}
