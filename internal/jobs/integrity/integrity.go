// Package integrity is the jobs task integrity. for every site in the
// registry and every utc day of the reread window it reads the evidence the
// gateways reported (events by network class, distinct networks), the
// outcome counters (quarantine, proof failures) and the web aggregates
// (visitors, page views, the site's baseline), grades the day with the
// versioned rule set of internal/integrity and replaces the day's row in
// agg.web_integrity_daily. it then drops network keys of days that are no
// longer graded again.
//
// a regular run grades today and the integrity.RereadDays days before it,
// the window aggregate-web reads again, so a late aggregate reaches the
// grade; `jobs run integrity --from <day> --to <day>` grades any range.
package integrity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/db/jobsq"
	rules "github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/registry"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "integrity"

// Every is the period of the task, the same as aggregate-web's.
const Every = time.Hour

// RereadDays is how many days before today every regular run grades again.
// it matches aggregateweb.RereadDays, so every day aggregate-web rewrites
// is graded again from the new numbers.
const RereadDays = 3

// Sites is the registry snapshot of sites. the jobs role cannot read
// cp.sites; the registry holds every site the gateways counted.
type Sites interface {
	Snapshot() map[string]registry.Site
	Loaded() bool
}

// ErrRegistryNotLoaded is returned by a run that starts before the site
// registry's first snapshot.
var ErrRegistryNotLoaded = errors.New("integrity: site registry snapshot not loaded yet")

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Result counts what one run did.
type Result struct {
	Sites int
	// Days counts site days graded or found ungradable and written
	Days int
	// Waiting counts site days with events whose web aggregates are not
	// read yet; the next run grades them
	Waiting int
	// Pruned counts network rows deleted
	Pruned int64
}

// Grader computes and stores the grades.
type Grader struct {
	sites  Sites
	now    func() time.Time
	log    *slog.Logger
	days   *prometheus.CounterVec
	failed prometheus.Counter
}

// New returns a Grader and registers its metrics on reg.
func New(sites Sites, now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Grader, error) {
	if sites == nil || now == nil || log == nil || reg == nil {
		return nil, errors.New("integrity: needs sites, a clock, a log and a registerer")
	}
	g := &Grader{
		sites: sites, now: now, log: log,
		days: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_integrity_days_total",
			Help: "site days written to agg.web_integrity_daily, by grade (A, B, C, none).",
		}, []string{"grade"}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_integrity_failures_total",
			Help: "site days that could not be graded; the run reports them as an error.",
		}),
	}
	for _, c := range []prometheus.Collector{g.days, g.failed} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("integrity: register metrics: %w", err)
		}
	}
	return g, nil
}

// UTCDay is the start of t's utc day.
func UTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Run is one regular pass: today and the RereadDays days before it, then
// the prune of network keys older than that window.
func (g *Grader) Run(ctx context.Context, tx TxFunc) (Result, error) {
	today := UTCDay(g.now())
	from := today.AddDate(0, 0, -RereadDays)
	res, err := g.Backfill(ctx, tx, from, today)
	if err != nil {
		return res, err
	}
	err = tx(ctx, func(tx pgx.Tx) error {
		n, err := jobsq.New(tx).PruneNetworkSightings(ctx, from)
		res.Pruned = n
		return err
	})
	if err != nil {
		return res, fmt.Errorf("integrity: prune network sightings: %w", err)
	}
	return res, nil
}

// Backfill grades every utc day from from to to, both included, for every
// site of the registry. a day after today is refused. a site day that fails
// is counted and reported; the others still run.
func (g *Grader) Backfill(ctx context.Context, tx TxFunc, from, to time.Time) (Result, error) {
	from, to = UTCDay(from), UTCDay(to)
	today := UTCDay(g.now())
	if to.Before(from) {
		return Result{}, fmt.Errorf("integrity: --to %s is before --from %s", to.Format(time.DateOnly), from.Format(time.DateOnly))
	}
	if to.After(today) {
		return Result{}, fmt.Errorf("integrity: --to %s is after today %s (utc)", to.Format(time.DateOnly), today.Format(time.DateOnly))
	}
	if !g.sites.Loaded() {
		return Result{}, ErrRegistryNotLoaded
	}
	snap := g.sites.Snapshot()
	ids := make([]string, 0, len(snap))
	for id := range snap {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var res Result
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Sites++
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			state, err := g.siteDay(ctx, tx, snap[id], d, today)
			if err != nil {
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				g.failed.Inc()
				errs = append(errs, fmt.Errorf("site %s day %s: %w", id, d.Format(time.DateOnly), err))
				continue
			}
			switch state {
			case dayWritten:
				res.Days++
			case dayWaiting:
				res.Waiting++
			}
		}
	}
	if len(errs) > 0 {
		g.log.Error("integrity", "sites", res.Sites, "days", res.Days, "failed", len(errs))
		return res, fmt.Errorf("integrity: %d site days failed: %w", len(errs), errors.Join(errs...))
	}
	g.log.Info("integrity", "sites", res.Sites, "days", res.Days, "waiting", res.Waiting,
		"from", from.Format(time.DateOnly), "to", to.Format(time.DateOnly))
	return res, nil
}

type dayState int

const (
	daySkipped dayState = iota
	dayWaiting
	dayWritten
)

// Median is the median of xs, the lower middle value for an even count.
// the lower one keeps the baseline from rising on a single busy day.
func Median(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[(len(s)-1)/2]
}

// siteDay reads one site's evidence for one day, grades it and writes the
// row, all in one fenced transaction under the tenant's shared agg lock.
func (g *Grader) siteDay(ctx context.Context, tx TxFunc, s registry.Site, day, today time.Time) (dayState, error) {
	siteID, err := uuid.Parse(s.ID)
	if err != nil {
		return daySkipped, fmt.Errorf("site id: %w", err)
	}
	tenantID, err := uuid.Parse(s.TenantID)
	if err != nil {
		return daySkipped, fmt.Errorf("tenant id: %w", err)
	}
	state := daySkipped
	var grade string
	err = tx(ctx, func(tx pgx.Tx) error {
		state = daySkipped
		q := jobsq.New(tx)
		if err := q.LockAggTenant(ctx, tenantID.String()); err != nil {
			return fmt.Errorf("lock tenant: %w", err)
		}
		status, err := q.AggTenantStatus(ctx, tenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("tenant %s is not in cp.tenants", tenantID)
		}
		if err != nil {
			return fmt.Errorf("tenant status: %w", err)
		}
		// the purge deletes a removed tenant's rows; writing them again
		// would undo it
		if status == "removed" {
			return nil
		}
		in, ok, err := readInputs(ctx, q, siteID, day)
		if err != nil {
			return err
		}
		if !ok {
			// a day with events but no web aggregate yet waits for
			// aggregate-web; a day without anything has no row at all
			if in.Events > 0 {
				state = dayWaiting
			}
			return nil
		}
		r := rules.Grade(in)
		reasons, err := json.Marshal(r.Reasons)
		if err != nil {
			return fmt.Errorf("encode reasons: %w", err)
		}
		p := jobsq.UpsertIntegrityDayParams{
			SiteID: siteID, TenantID: tenantID, Day: day, RuleVersion: rules.RuleVersion,
			Closed: day.Before(today), Events: in.Events, Quarantined: in.Quarantined,
			ProofFailures: in.ProofFailures, NetAccess: in.NetAccess, NetOrganisation: in.NetOrganisation,
			NetHosting: in.NetHosting, NetAnonymiser: in.NetAnonymiser, NetUnknown: in.NetUnknown,
			Networks: in.Networks, Visitors: in.Visitors, Pageviews: in.Pageviews,
			BaselineVisitors: in.BaselineVisitors, BaselineDays: int32(in.BaselineDaysUsed), //nolint:gosec // at most BaselineDays
			SignalViews: in.SignalViews, Framed: in.Framed, NeverVisible: in.NeverVisible,
			Reasons: reasons,
		}
		if r.Grade != "" {
			p.Grade = &r.Grade
		}
		if err := q.UpsertIntegrityDay(ctx, p); err != nil {
			return fmt.Errorf("upsert: %w", err)
		}
		state, grade = dayWritten, r.Grade
		return nil
	})
	if err != nil {
		return daySkipped, err
	}
	if state == dayWritten {
		label := grade
		if label == "" {
			label = "none"
		}
		g.days.WithLabelValues(label).Inc()
	}
	return state, nil
}

// readInputs gathers one site day's evidence. ok is false when the day has
// no web aggregate yet, or nothing at all.
func readInputs(ctx context.Context, q *jobsq.Queries, siteID uuid.UUID, day time.Time) (rules.Inputs, bool, error) {
	var in rules.Inputs
	o, err := q.IntegrityOutcomes(ctx, jobsq.IntegrityOutcomesParams{SiteID: siteID, FromHour: day, ToHour: day.AddDate(0, 0, 1)})
	if err != nil {
		return in, false, fmt.Errorf("outcomes: %w", err)
	}
	in.Events, in.Quarantined, in.ProofFailures = o.Events, o.Quarantined, o.ProofFailures
	ev, err := q.IntegrityEvidence(ctx, jobsq.IntegrityEvidenceParams{SiteID: siteID, Day: day})
	if err != nil {
		return in, false, fmt.Errorf("evidence: %w", err)
	}
	for _, r := range ev {
		switch rules.NetClass(r.Class) {
		case rules.NetAccess:
			in.NetAccess = r.Count
		case rules.NetOrganisation:
			in.NetOrganisation = r.Count
		case rules.NetHosting:
			in.NetHosting = r.Count
		case rules.NetAnonymiser:
			in.NetAnonymiser = r.Count
		case rules.NetUnknown:
			in.NetUnknown = r.Count
		default:
			return in, false, fmt.Errorf("evidence: unknown class %q", r.Class)
		}
	}
	sigs, err := q.IntegritySignals(ctx, jobsq.IntegritySignalsParams{SiteID: siteID, Day: day})
	if err != nil {
		return in, false, fmt.Errorf("signals: %w", err)
	}
	for _, r := range sigs {
		switch r.Signal {
		case rules.SignalViews:
			in.SignalViews = r.Count
		case rules.SignalFramed:
			in.Framed = r.Count
		case rules.SignalNeverVisible:
			in.NeverVisible = r.Count
		default:
			return in, false, fmt.Errorf("signals: unknown signal %q", r.Signal)
		}
	}
	if in.Networks, err = q.IntegrityNetworks(ctx, jobsq.IntegrityNetworksParams{SiteID: siteID, Day: day}); err != nil {
		return in, false, fmt.Errorf("networks: %w", err)
	}
	w, err := q.IntegrityWebDay(ctx, jobsq.IntegrityWebDayParams{SiteID: siteID, Day: day})
	if err != nil {
		return in, false, fmt.Errorf("web day: %w", err)
	}
	if !w.Ok {
		return in, false, nil
	}
	in.Visitors, in.Pageviews = w.Visitors, w.Pageviews
	base, err := q.IntegrityBaseline(ctx, jobsq.IntegrityBaselineParams{SiteID: siteID, Day: day, Since: day.AddDate(0, 0, -rules.BaselineDays)})
	if err != nil {
		return in, false, fmt.Errorf("baseline: %w", err)
	}
	if len(base) > 0 {
		m := Median(base)
		in.BaselineVisitors = &m
		in.BaselineDaysUsed = len(base)
	}
	return in, true, nil
}
