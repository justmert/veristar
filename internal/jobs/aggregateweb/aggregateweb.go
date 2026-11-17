// Package aggregateweb is the jobs task aggregate-web. for
// every active site and both of its bands it reads umami's own /stats and
// /metrics routes for each utc day and replaces that day's rows in
// agg.web_daily and agg.web_daily_dim. reading umami's api instead of its
// tables keeps the operator view equal to what tenants see in umami.
//
// a regular run reads today and the 3 days before it, so late events (a
// replayer backlog, an erasure) reach the aggregates; `jobs run
// aggregate-web --from <day> --to <day>` reads any range again.
// docs/reference/web-metrics.md ("Daily aggregates") records the umami
// behaviour this relies on.
package aggregateweb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/jobs/retainweb"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "aggregate-web"

// Every is the period of the task: hourly for the current day.
const Every = time.Hour

// RereadDays is how many days before today every regular run reads again.
const RereadDays = 3

// bands of a site
const (
	BandPrimary    = "primary"
	BandQuarantine = "quarantine"
)

// Dimensions are the /metrics types stored in agg.web_daily_dim, under the
// same names. referrer and domain return the same rows on umami 3.5.0
// (both read website_event.referrer_domain), so only referrer is stored.
var Dimensions = []string{"referrer", "country", "device", "browser", "os"}

// /metrics ranks without a tie-breaker (order by y desc only), so paging
// with offset can repeat or skip values that share a count. the task asks
// for one page large enough for every value instead, and grows it while a
// page comes back full.
const (
	firstLimit = 1000
	maxLimit   = 64000
)

// Reader is the part of the umami admin client the task uses.
type Reader interface {
	GetWebsite(ctx context.Context, id string) (umami.Website, bool, error)
	Stats(ctx context.Context, websiteID string, r umami.Range) (umami.Stats, error)
	Metrics(ctx context.Context, websiteID string, q umami.MetricsQuery) ([]umami.MetricRow, error)
}

// Sites is the registry snapshot of sites. the jobs role cannot read
// cp.sites; the registry is where ingestion learns which sites are active.
// Loaded is false until the first complete snapshot is built.
type Sites interface {
	Snapshot() map[string]registry.Site
	Loaded() bool
}

// ErrRegistryNotLoaded is returned by a run that starts before the site
// registry's first snapshot: that snapshot is empty, and a run over it would
// succeed without reading a single site, then wait an hour for the next one.
var ErrRegistryNotLoaded = errors.New("aggregateweb: site registry snapshot not loaded yet")

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Result counts what one run did.
type Result struct {
	Sites int
	// Days counts site days written (both bands of a site and day together)
	Days int
	// Skipped counts site days not written because the tenant is removed
	Skipped int
}

// Aggregator reads umami and writes the agg tables.
type Aggregator struct {
	umami  Reader
	sites  Sites
	now    func() time.Time
	// rawMonths is WEB_RAW_RETENTION_MONTHS: umami holds no raw rows of the
	// days before retain-web's cutoff, so reading such a day returns zeros
	rawMonths int
	log    *slog.Logger
	days   prometheus.Counter
	failed prometheus.Counter
}

// New returns an Aggregator and registers its metrics on reg.
func New(r Reader, sites Sites, now func() time.Time, log *slog.Logger, reg prometheus.Registerer) (*Aggregator, error) {
	if r == nil || sites == nil || now == nil || log == nil || reg == nil {
		return nil, errors.New("aggregateweb: needs an umami reader, sites, a clock, a log and a registerer")
	}
	a := &Aggregator{
		umami: r, sites: sites, now: now, log: log, rawMonths: config.DefaultWebRawRetentionMonths,
		days: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_aggregate_web_days_total",
			Help: "site days of agg.web_daily and agg.web_daily_dim replaced from umami.",
		}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_aggregate_web_failures_total",
			Help: "site days that could not be read from umami or written; the run reports them as an error.",
		}),
	}
	for _, c := range []prometheus.Collector{a.days, a.failed} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("aggregateweb: register metrics: %w", err)
		}
	}
	return a, nil
}

// SetRawRetentionMonths sets WEB_RAW_RETENTION_MONTHS, the setting retain-web
// deletes raw umami rows with. it defaults to config.DefaultWebRawRetentionMonths.
func (a *Aggregator) SetRawRetentionMonths(months int) error {
	if months < 1 {
		return fmt.Errorf("aggregateweb: raw retention of %d months", months)
	}
	a.rawMonths = months
	return nil
}

// RawCutoff is the first day umami still holds raw rows for. retain-web
// deletes everything older, after which /stats answers zeros for a day; such
// a zero would replace an aggregate that agg.web_daily keeps for the life of
// the site, so no task reads a day before it.
func (a *Aggregator) RawCutoff() time.Time {
	return UTCDay(retainweb.Cutoff(a.now(), a.rawMonths))
}

// ErrBeforeRawCutoff is returned by a backfill that asks for a day whose raw
// rows retain-web already deleted; nothing is read or written.
var ErrBeforeRawCutoff = errors.New("aggregateweb: day is before the raw retention cutoff")

// UTCDay is the start of t's utc day.
func UTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// DayRange is the range umami's read routes need for one utc day. umami
// filters with "created_at between start and end", which includes both
// ends, so the end is the last millisecond of the day: with the next
// midnight as the end, an event at exactly 00:00:00 would count on two days
// (measured, docs/reference/web-metrics.md).
func DayRange(day time.Time) umami.Range {
	d := UTCDay(day)
	return umami.Range{Start: d, End: d.AddDate(0, 0, 1).Add(-time.Millisecond)}
}

// Run is one regular pass: today and the RereadDays days before it.
func (a *Aggregator) Run(ctx context.Context, tx TxFunc) (Result, error) {
	today := UTCDay(a.now())
	return a.Backfill(ctx, tx, today.AddDate(0, 0, -RereadDays), today)
}

// Backfill reads every utc day from from to to, both included, for every
// active site. a day after today is refused: umami has nothing for it yet,
// and a zero row would claim the day was read. a day before RawCutoff is
// refused as well: umami deleted its raw rows, and the zeros it answers
// would overwrite the kept aggregate. a site that fails is
// counted and reported in the returned error; the other sites still run,
// so one broken website does not hold back every tenant's numbers.
func (a *Aggregator) Backfill(ctx context.Context, tx TxFunc, from, to time.Time) (Result, error) {
	from, to = UTCDay(from), UTCDay(to)
	if to.Before(from) {
		return Result{}, fmt.Errorf("aggregateweb: --to %s is before --from %s", to.Format(time.DateOnly), from.Format(time.DateOnly))
	}
	if today := UTCDay(a.now()); to.After(today) {
		return Result{}, fmt.Errorf("aggregateweb: --to %s is after today %s (utc)", to.Format(time.DateOnly), today.Format(time.DateOnly))
	}
	if cutoff := a.RawCutoff(); from.Before(cutoff) {
		return Result{}, fmt.Errorf("%w: --from %s is before %s, the oldest day umami still holds (WEB_RAW_RETENTION_MONTHS %d)",
			ErrBeforeRawCutoff, from.Format(time.DateOnly), cutoff.Format(time.DateOnly), a.rawMonths)
	}
	if !a.sites.Loaded() {
		return Result{}, ErrRegistryNotLoaded
	}
	snap := a.sites.Snapshot()
	ids := make([]string, 0, len(snap))
	for id, s := range snap {
		if s.Active() {
			ids = append(ids, id)
		}
	}
	// a stable order makes logs of two runs comparable
	slices.Sort(ids)

	var res Result
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		s := snap[id]
		res.Sites++
		if err := a.checkWebsites(ctx, s); err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			a.failed.Inc()
			errs = append(errs, fmt.Errorf("site %s: %w", id, err))
			continue
		}
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			written, err := a.siteDay(ctx, tx, s, d)
			if err != nil {
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				a.failed.Inc()
				errs = append(errs, fmt.Errorf("site %s day %s: %w", id, d.Format(time.DateOnly), err))
				continue
			}
			if written {
				res.Days++
				a.days.Inc()
			} else {
				res.Skipped++
			}
		}
	}
	if len(errs) > 0 {
		a.log.Error("aggregate web", "sites", res.Sites, "days", res.Days, "failed", len(errs))
		return res, fmt.Errorf("aggregateweb: %d site days failed: %w", len(errs), errors.Join(errs...))
	}
	a.log.Info("aggregate web", "sites", res.Sites, "days", res.Days, "skipped", res.Skipped,
		"from", from.Format(time.DateOnly), "to", to.Format(time.DateOnly))
	return res, nil
}

// BackfillSite reads one site's days from from to to again, whatever the
// site's status: an erasure can concern a suspended site, whose aggregates
// must still stop counting erased visits. days after today are left out,
// because umami has nothing for them yet, and so are days before RawCutoff,
// whose raw rows are gone and whose kept aggregate must not become zero. it
// returns the site days written.
func (a *Aggregator) BackfillSite(ctx context.Context, tx TxFunc, s registry.Site, from, to time.Time) (int, error) {
	from, to = UTCDay(from), UTCDay(to)
	if today := UTCDay(a.now()); to.After(today) {
		to = today
	}
	if cutoff := a.RawCutoff(); from.Before(cutoff) {
		from = cutoff
	}
	if to.Before(from) {
		return 0, nil
	}
	if err := a.checkWebsites(ctx, s); err != nil {
		return 0, fmt.Errorf("aggregateweb: site %s: %w", s.ID, err)
	}
	n := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		written, err := a.siteDay(ctx, tx, s, d)
		if err != nil {
			a.failed.Inc()
			return n, fmt.Errorf("aggregateweb: site %s day %s: %w", s.ID, d.Format(time.DateOnly), err)
		}
		if written {
			n++
			a.days.Inc()
		}
	}
	return n, nil
}

// Site returns a site of the registry snapshot. before the first snapshot
// every site would look removed, so that is ErrRegistryNotLoaded instead.
func (a *Aggregator) Site(id string) (registry.Site, bool, error) {
	if !a.sites.Loaded() {
		return registry.Site{}, false, ErrRegistryNotLoaded
	}
	s, ok := a.sites.Snapshot()[id]
	return s, ok, nil
}

// checkWebsites confirms that both websites of a site exist in umami. umami
// answers /stats and /metrics of an unknown website id with zeros for the
// admin, so without this check a website that was never created, or was
// deleted, would be stored as a day without traffic.
func (a *Aggregator) checkWebsites(ctx context.Context, s registry.Site) error {
	if s.QuarantineWebsiteID == "" {
		return errors.New("site has no quarantine website id")
	}
	for _, id := range []string{s.ID, s.QuarantineWebsiteID} {
		w, ok, err := a.umami.GetWebsite(ctx, id)
		if err != nil {
			return fmt.Errorf("umami website %s: %w", id, err)
		}
		if !ok {
			return fmt.Errorf("umami website %s does not exist", id)
		}
		if w.ID != id {
			return fmt.Errorf("umami answered website %s for %s", w.ID, id)
		}
	}
	return nil
}

// bandRead is what umami returned for one website and day.
type bandRead struct {
	band  string
	stats umami.StatsValues
	dims  []dimRow
}

type dimRow struct {
	dimension string
	key       string
	value     int64
}

// siteDay reads both bands of a site for one day from umami, then writes
// them in one fenced transaction. it reports false when the tenant is
// removed, in which case nothing is written.
func (a *Aggregator) siteDay(ctx context.Context, tx TxFunc, s registry.Site, day time.Time) (bool, error) {
	siteID, err := uuid.Parse(s.ID)
	if err != nil {
		return false, fmt.Errorf("site id: %w", err)
	}
	tenantID, err := uuid.Parse(s.TenantID)
	if err != nil {
		return false, fmt.Errorf("tenant id: %w", err)
	}
	reads := make([]bandRead, 0, 2)
	for _, b := range []struct{ band, website string }{
		{BandPrimary, s.ID}, {BandQuarantine, s.QuarantineWebsiteID},
	} {
		r, err := a.read(ctx, b.website, day)
		if err != nil {
			return false, fmt.Errorf("%s website %s: %w", b.band, b.website, err)
		}
		r.band = b.band
		reads = append(reads, r)
	}

	written := false
	err = tx(ctx, func(tx pgx.Tx) error {
		written = false
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
		for _, r := range reads {
			if err := q.UpsertWebDaily(ctx, jobsq.UpsertWebDailyParams{
				SiteID: siteID, TenantID: tenantID, Day: day, Band: r.band,
				Pageviews: r.stats.Pageviews, Visitors: r.stats.Visitors, Visits: r.stats.Visits,
				Bounces: r.stats.Bounces, Totaltime: r.stats.Totaltime,
			}); err != nil {
				return fmt.Errorf("upsert %s: %w", r.band, err)
			}
			// replacing the whole set drops a key umami no longer returns
			// (an erased session, a reset website)
			if _, err := q.DeleteWebDailyDims(ctx, jobsq.DeleteWebDailyDimsParams{SiteID: siteID, Day: day, Band: r.band}); err != nil {
				return fmt.Errorf("delete %s dimensions: %w", r.band, err)
			}
			if len(r.dims) == 0 {
				continue
			}
			p := jobsq.InsertWebDailyDimsParams{SiteID: siteID, TenantID: tenantID, Day: day, Band: r.band,
				Dimensions: make([]string, len(r.dims)), Keys: make([]string, len(r.dims)), DimValues: make([]int64, len(r.dims))}
			for i, d := range r.dims {
				p.Dimensions[i], p.Keys[i], p.DimValues[i] = d.dimension, d.key, d.value
			}
			n, err := q.InsertWebDailyDims(ctx, p)
			if err != nil {
				return fmt.Errorf("insert %s dimensions: %w", r.band, err)
			}
			if n != int64(len(r.dims)) {
				return fmt.Errorf("inserted %d of %d %s dimension rows", n, len(r.dims), r.band)
			}
		}
		written = true
		return nil
	})
	return written, err
}

// read asks umami for one website's day: /stats and every dimension.
func (a *Aggregator) read(ctx context.Context, website string, day time.Time) (bandRead, error) {
	r := DayRange(day)
	st, err := a.umami.Stats(ctx, website, r)
	if err != nil {
		return bandRead{}, fmt.Errorf("stats: %w", err)
	}
	out := bandRead{stats: st.StatsValues}
	for _, dim := range Dimensions {
		rows, err := a.metrics(ctx, website, dim, r)
		if err != nil {
			return bandRead{}, err
		}
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			// umami leaves out empty values ("column != ''"), so an empty
			// or repeated key, or a count below one, means the answer is
			// not what this task was written against
			if row.X == "" || row.Y < 1 || seen[row.X] {
				return bandRead{}, fmt.Errorf("metrics %s: unexpected row %q = %d", dim, row.X, row.Y)
			}
			seen[row.X] = true
			out.dims = append(out.dims, dimRow{dimension: dim, key: row.X, value: row.Y})
		}
	}
	return out, nil
}

// metrics reads every value of one dimension in a single page.
func (a *Aggregator) metrics(ctx context.Context, website, dim string, r umami.Range) ([]umami.MetricRow, error) {
	for limit := firstLimit; ; limit *= 2 {
		rows, err := a.umami.Metrics(ctx, website, umami.MetricsQuery{Range: r, Type: dim, Limit: limit})
		if err != nil {
			return nil, fmt.Errorf("metrics %s: %w", dim, err)
		}
		if len(rows) < limit {
			return rows, nil
		}
		if limit*2 > maxLimit {
			return nil, fmt.Errorf("metrics %s: more than %d values in one day", dim, limit)
		}
	}
}
