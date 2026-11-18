package operator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/coverageview"
	"github.com/justmert/veristar/internal/controlplane/exports"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/integrityview"
	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db"
)

// the csv exports of the global and compare views (contract 16.1). a list
// export takes the list's own query (range, filters, sort and order) and
// rows, the number of rows the console shows, and returns exactly those
// rows in that order, read with the list's own sql under cp_operator. a
// compare export returns one row per project and day of the compare
// answer. each export writes an audit row with the query it answered.

// Exports serves the four csv routes.
type Exports struct{ g *Global }

// NewExports wraps the global views.
func NewExports(g *Global) (*Exports, error) {
	if g == nil {
		return nil, errors.New("operator: exports need the global views")
	}
	return &Exports{g: g}, nil
}

// Routes registers the paths under the guarded /api/operator router.
func (e *Exports) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/web/projects.csv", httpx.HandlerFunc(e.webProjects))
	r.Method(http.MethodGet, "/api/projects.csv", httpx.HandlerFunc(e.apiProjects))
	r.Method(http.MethodGet, "/web/compare.csv", httpx.HandlerFunc(e.webCompare))
	r.Method(http.MethodGet, "/api/compare.csv", httpx.HandlerFunc(e.apiCompare))
	r.Method(http.MethodGet, "/tenants/{tenant_id}/onchain/ids/{chain_id}/functions.csv", httpx.HandlerFunc(e.contractFunctions))
}

// listRequest reads rows and returns the request the list parser sees:
// the same query without rows. a cursor is refused, because the console
// exports the rows from the first one on.
func listRequest(r *http.Request) (*http.Request, int, error) {
	q := r.URL.Query()
	if _, ok := q["cursor"]; ok {
		return nil, 0, httpx.BadQuery("an export starts at the first row; drop cursor and set rows")
	}
	if _, ok := q["limit"]; ok {
		return nil, 0, httpx.BadQuery("an export takes rows, not limit")
	}
	rows := httpx.DefaultLimit
	if v, ok, err := oneValue(q, "rows"); err != nil {
		return nil, 0, err
	} else if ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > exports.MaxRows {
			return nil, 0, httpx.BadQuery(fmt.Sprintf("rows must be an integer between 1 and %d", exports.MaxRows))
		}
		rows = n
	}
	q.Del("rows")
	return withQuery(r, q), rows, nil
}

func withQuery(r *http.Request, q url.Values) *http.Request {
	c := r.Clone(r.Context())
	u := *r.URL
	u.RawQuery = q.Encode()
	c.URL = &u
	return c
}

// auditExport writes the audit row of an export under cp_system.
func (e *Exports) auditExport(ctx context.Context, view string, q url.Values, rows int, tenantIDs []uuid.UUID) error {
	return auditExportIn(ctx, e.g.db, view, q, rows, tenantIDs)
}

// auditExportIn writes the audit row of an export of any operator view.
func auditExportIn(ctx context.Context, d *db.DB, view string, q url.Values, rows int, tenantIDs []uuid.UUID) error {
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	q.Del("cursor")
	ids := make([]string, len(tenantIDs))
	for i, id := range tenantIDs {
		ids[i] = id.String()
	}
	detail := map[string]any{"view": view, "query": q.Encode(), "rows": rows}
	if len(ids) > 0 {
		detail["tenants"] = ids
	}
	return d.WithSystem(ctx, func(tx pgx.Tx) error {
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: exports.ActionExport, Target: "operator:" + view, Detail: detail,
		})
	})
}

var summaryHeader = []string{
	"onchain_ids", "onchain_ids_verified", "onchain_operations", "onchain_transactions",
	"onchain_days_missing", "onchain_ids_failing",
	"onchain_weekly_active_accounts_average", "onchain_weekly_active_accounts_latest",
	"onchain_weeks_counted", "onchain_weeks_missing",
}

func summaryCells(s onchain.Summary) []string {
	w := s.WeeklyActive
	return []string{
		exports.Int(s.IDs), exports.Int(s.IDsVerified), exports.Int(s.Operations), exports.Int(s.Transactions),
		exports.Int(s.DaysMissing), exports.Int(s.IDsFailing),
		exports.Float(w.Average), exports.IntPtr(w.Latest), exports.Int(int64(w.WeeksCounted)), exports.Int(int64(w.WeeksMissing)),
	}
}

// integrityHeader and integrityCells are the integrity grade of a list
// row over the range (contract 17.3)
var integrityHeader = []string{"integrity_grade", "integrity_days_a", "integrity_days_b", "integrity_days_c"}

func integrityCells(g IntegrityGrade) []string {
	return []string{exports.StrPtr(g.Grade), exports.Int(g.DaysA), exports.Int(g.DaysB), exports.Int(g.DaysC)}
}

// comparisonHeader and comparisonCells are the cohort, growth and
// percentile columns of a list row (contract 19.2)
var comparisonHeader = func() []string {
	out := []string{"cohort_program", "cohort_round", "cohort_category", "cohort_award_date", "cohort_size"}
	for _, m := range []string{"visitors", "pageviews", "requests", "error_rate", "onchain_operations"} {
		change := m + "_growth"
		if m == "error_rate" {
			change = m + "_change"
		}
		out = append(out, m+"_value", m+"_previous", change, m+"_percentile")
	}
	return out
}()

func comparisonCells(c Comparison) []string {
	out := []string{
		exports.StrPtr(c.Cohort.Program), exports.StrPtr(c.Cohort.Round), exports.StrPtr(c.Cohort.Category),
		exports.StrPtr(c.Cohort.AwardDate), exports.Int(c.CohortSize),
	}
	for _, k := range []KPI{c.KPIs.Visitors, c.KPIs.Pageviews, c.KPIs.Requests, c.KPIs.ErrorRate, c.KPIs.OnchainOperations} {
		out = append(out, exports.Float(k.Value), exports.Float(k.Previous), exports.Float(k.Change), exports.Float(k.Percentile))
	}
	return out
}

// coverageHeader and coverageCells are the estimate of unmeasured visits of
// a row (contract 17.5)
var coverageHeader = []string{"script_requests", "gpc_requests", "unmeasured_share"}

func coverageCells(c coverageview.Estimate) []string {
	return []string{exports.Int(c.ScriptRequests), exports.Int(c.GPCRequests), exports.Float(c.UnmeasuredShare)}
}

var tenantHeader = []string{
	"tenant_id", "slug", "name", "status", "created_at", "sites", "sites_verified", "sites_with_stellar_toml",
	"has_web_data", "has_api_data",
}

func tenantCells(t TenantRef, c SiteCounts, web, api bool) []string {
	return []string{
		t.ID.String(), exports.Text(t.Slug), exports.Text(t.Name), t.Status, exports.Time(time.Time(t.CreatedAt)),
		exports.Int(c.Sites), exports.Int(c.SitesVerified), exports.Int(c.SitesWithStellarToml),
		exports.Bool(web), exports.Bool(api),
	}
}

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func (e *Exports) webProjects(w http.ResponseWriter, r *http.Request) error {
	lr, rows, err := listRequest(r)
	if err != nil {
		return err
	}
	lq, err := e.g.parseList(lr, webProjectsRoute, webSorts, "pageviews")
	if err != nil {
		return err
	}
	lq.Limit = rows
	ctx := r.Context()
	var items []WebProject
	err = e.g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		res, err := tx.Query(ctx, webListCTE+webRowSelect+lq.orderBy(webSorts)+` limit @row_limit offset 0`, rowArgs(lq))
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(res, func(row pgx.CollectableRow) (WebProject, error) { return scanWebRow(row) })
		if err != nil {
			return err
		}
		return attachSummaries(ctx, tx, lq.Range, e.g.now(), len(items), func(i int) uuid.UUID { return items[i].Tenant.ID },
			func(i int, s onchain.Summary) { items[i].Onchain = s })
	})
	if err != nil {
		return fmt.Errorf("operator: web projects export: %w", err)
	}
	if err := e.auditExport(ctx, "web_projects", r.URL.Query(), len(items), nil); err != nil {
		return err
	}
	cw, err := exports.NewWriter(w, exports.FileName("veristar-web-projects", lq.Range.From.Format(time.DateOnly), lq.Range.To.Format(time.DateOnly)), cat(tenantHeader, []string{
		"pageviews", "visitors", "visits", "bounces", "totaltime_seconds", "bounce_rate", "avg_visit_duration_seconds",
		"quarantine_pageviews", "quarantine_visitors", "quarantine_share", "ingest_requests", "ingest_rejected", "rejected_share",
	}, integrityHeader, coverageHeader, summaryHeader, comparisonHeader))
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	for _, p := range items {
		if err := cw.Row(cat(tenantCells(p.Tenant, p.SiteCounts, p.HasWebData, p.HasAPIData), []string{
			exports.Int(p.Pageviews), exports.Int(p.Visitors), exports.Int(p.Visits), exports.Int(p.Bounces), exports.Int(p.Totaltime),
			exports.Float(p.BounceRate), exports.Float(p.AvgVisitDuration), exports.Int(p.QuarantinePageviews),
			exports.Int(p.QuarantineVisitors), exports.Float(p.QuarantineShare), exports.Int(p.IngestRequests),
			exports.Int(p.IngestRejected), exports.Float(p.RejectedShare),
		}, integrityCells(p.Integrity), coverageCells(p.Coverage), summaryCells(p.Onchain), comparisonCells(p.Comparison))...); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	if err := cw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}

// rowArgs are the list's arguments with the export's row count.
func rowArgs(lq listQuery) pgx.NamedArgs {
	a := lq.args()
	a["row_limit"] = lq.Limit
	return a
}

func attachSummaries(ctx context.Context, tx pgx.Tx, rg dayRange, now time.Time, n int, id func(int) uuid.UUID, set func(int, onchain.Summary)) error {
	ids := make([]uuid.UUID, n)
	for i := range n {
		ids[i] = id(i)
	}
	sums, err := onchain.Summaries(ctx, tx, ids, rg.onchain(), utcDay(now))
	if err != nil {
		return err
	}
	for i := range n {
		set(i, sums[ids[i]])
	}
	return nil
}

func percentileCells(p Percentiles) []string {
	return []string{
		exports.Float(p.P50Seconds), exports.Bool(p.P50Over10s), exports.Float(p.P95Seconds), exports.Bool(p.P95Over10s),
		exports.Float(p.P99Seconds), exports.Bool(p.P99Over10s),
	}
}

var numbersHeader = []string{
	"requests", "requests_4xx", "requests_5xx", "rate_4xx", "rate_5xx",
	"p50_seconds", "p50_over_10s", "p95_seconds", "p95_over_10s", "p99_seconds", "p99_over_10s",
	"infrastructure_requests",
}

func numbersCells(n APINumbers) []string {
	return cat([]string{
		exports.Int(n.Requests), exports.Int(n.Requests4xx), exports.Int(n.Requests5xx),
		exports.Float(n.Rate4xx), exports.Float(n.Rate5xx),
	}, percentileCells(n.Percentiles), []string{exports.Int(n.InfrastructureRequests)})
}

func (e *Exports) apiProjects(w http.ResponseWriter, r *http.Request) error {
	lr, rows, err := listRequest(r)
	if err != nil {
		return err
	}
	lq, err := e.g.parseList(lr, apiProjectsRoute, apiSorts, "requests")
	if err != nil {
		return err
	}
	lq.Limit = rows
	ctx := r.Context()
	var items []APIProject
	err = e.g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		res, err := tx.Query(ctx, apiListCTE+apiRowSelect+lq.orderBy(apiSorts)+` limit @row_limit offset 0`, rowArgs(lq))
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(res, func(row pgx.CollectableRow) (APIProject, error) { return scanAPIRow(row) })
		if err != nil {
			return err
		}
		return attachSummaries(ctx, tx, lq.Range, e.g.now(), len(items), func(i int) uuid.UUID { return items[i].Tenant.ID },
			func(i int, s onchain.Summary) { items[i].Onchain = s })
	})
	if err != nil {
		return fmt.Errorf("operator: api projects export: %w", err)
	}
	if err := e.auditExport(ctx, "api_projects", r.URL.Query(), len(items), nil); err != nil {
		return err
	}
	cw, err := exports.NewWriter(w, exports.FileName("veristar-api-projects", lq.Range.From.Format(time.DateOnly), lq.Range.To.Format(time.DateOnly)),
		cat(tenantHeader, numbersHeader, []string{"services", "routes", "span_services", "source", "lossy"}, summaryHeader, comparisonHeader))
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	for _, p := range items {
		if err := cw.Row(cat(tenantCells(p.Tenant, p.SiteCounts, p.HasWebData, p.HasAPIData), numbersCells(p.APINumbers), []string{
			exports.Int(p.Services), exports.Int(p.Routes), exports.Int(p.SpanServices), exports.StrPtr(p.Source), exports.Bool(p.Lossy),
		}, summaryCells(p.Onchain), comparisonCells(p.Comparison))...); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	if err := cw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}

var compareTenantHeader = []string{"tenant_id", "slug", "name", "day"}

// compareCohortHeader and compareCohortCells place a compare row in the
// project's cohort: its award date and whether the day is on or after it
var compareCohortHeader = []string{"cohort_program", "cohort_round", "cohort_category", "cohort_award_date", "after_award"}

func compareCohortCells(c Cohort, day string) []string {
	after := ""
	if c.AwardDate != nil {
		after = exports.Bool(day >= *c.AwardDate)
	}
	return []string{exports.StrPtr(c.Program), exports.StrPtr(c.Round), exports.StrPtr(c.Category), exports.StrPtr(c.AwardDate), after}
}

var compareChainHeader = []string{
	"onchain_ids_read", "onchain_operations", "onchain_transactions",
	"onchain_week", "onchain_week_active_accounts", "onchain_week_missing",
}

// chainCells is a day's on-chain cells with the weekly active accounts of
// its iso week; a day of a week that is not whole inside the range has
// empty week cells.
func chainCells(p onchain.DayTotal, weeks []onchain.Week) []string {
	out := []string{exports.Int(p.IDsRead), exports.IntPtr(p.Operations), exports.IntPtr(p.Transactions), "", "", ""}
	for _, w := range weeks {
		if p.Day >= w.From && p.Day <= w.To {
			out[3], out[4], out[5] = w.Week, exports.IntPtr(w.ActiveAccounts), exports.StrPtr(w.Missing)
		}
	}
	return out
}

func (e *Exports) webCompare(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	rg, err := e.g.parseRange(q)
	if err != nil {
		return err
	}
	ids, err := parseTenants(q)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var tenants []TenantRef
	var series map[uuid.UUID][]WebPoint
	var chainOf map[uuid.UUID]CompareOnchain
	var grades map[integrityview.DayKey]integrityview.Summary
	var cohorts map[uuid.UUID]Cohort
	var cov coverageview.Counts
	err = e.g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		var err error
		if tenants, err = compareTenants(ctx, tx, ids); err != nil {
			return err
		}
		if series, _, err = webSeries(ctx, tx, ids, rg); err != nil {
			return err
		}
		if grades, err = integrityview.ByTenantDay(ctx, tx, ids, rg.From, rg.To); err != nil {
			return err
		}
		if cohorts, err = loadCohorts(ctx, tx, ids); err != nil {
			return err
		}
		if cov, err = coverageview.Load(ctx, tx, ids, rg.From, rg.To); err != nil {
			return err
		}
		chainOf, err = compareOnchain(ctx, tx, ids, rg, e.g.now())
		return err
	})
	if err != nil {
		return fmt.Errorf("operator: web compare export: %w", err)
	}
	rows := len(tenants) * len(rg.days())
	if err := e.auditExport(ctx, "web_compare", q, rows, ids); err != nil {
		return err
	}
	cw, err := exports.NewWriter(w, exports.FileName("veristar-web-compare", rg.From.Format(time.DateOnly), rg.To.Format(time.DateOnly)), cat(compareTenantHeader, []string{
		"aggregated", "pageviews", "visitors", "visits", "bounces", "totaltime_seconds", "bounce_rate",
		"avg_visit_duration_seconds", "quarantine_pageviews", "integrity_grade",
	}, coverageHeader, compareChainHeader, compareCohortHeader))
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	for _, t := range tenants {
		chainSeries := chainOf[t.ID].Series
		for i, p := range series[t.ID] {
			if err := cw.Row(cat([]string{t.ID.String(), exports.Text(t.Slug), exports.Text(t.Name), p.Day}, []string{
				exports.Bool(p.Aggregated), exports.IntPtr(p.Pageviews), exports.IntPtr(p.Visitors), exports.IntPtr(p.Visits),
				exports.IntPtr(p.Bounces), exports.IntPtr(p.Totaltime), exports.Float(p.BounceRate), exports.Float(p.AvgVisitDuration),
				exports.IntPtr(p.QuarantinePageviews),
				exports.StrPtr(grades[integrityview.DayKey{Tenant: t.ID, Day: p.Day}].Grade),
			}, coverageCells(cov.TenantDay(t.ID, p.Day)), chainCells(chainSeries[i], chainOf[t.ID].Weeks), compareCohortCells(cohorts[t.ID], p.Day))...); err != nil {
				panic(http.ErrAbortHandler)
			}
		}
	}
	if err := cw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}

func (e *Exports) apiCompare(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	rg, err := e.g.parseRange(q)
	if err != nil {
		return err
	}
	ids, err := parseTenants(q)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var tenants []TenantRef
	var series map[uuid.UUID][]APIPoint
	var chainOf map[uuid.UUID]CompareOnchain
	var cohorts map[uuid.UUID]Cohort
	err = e.g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		var err error
		if tenants, err = compareTenants(ctx, tx, ids); err != nil {
			return err
		}
		args := pgx.NamedArgs{"ids": ids, "from_at": rg.From, "to_end": rg.end()}
		if series, err = apiSeries(ctx, tx, args, rg); err != nil {
			return err
		}
		if cohorts, err = loadCohorts(ctx, tx, ids); err != nil {
			return err
		}
		chainOf, err = compareOnchain(ctx, tx, ids, rg, e.g.now())
		return err
	})
	if err != nil {
		return fmt.Errorf("operator: api compare export: %w", err)
	}
	rows := len(tenants) * len(rg.days())
	if err := e.auditExport(ctx, "api_compare", q, rows, ids); err != nil {
		return err
	}
	cw, err := exports.NewWriter(w, exports.FileName("veristar-api-compare", rg.From.Format(time.DateOnly), rg.To.Format(time.DateOnly)),
		cat(compareTenantHeader, numbersHeader, compareChainHeader, compareCohortHeader))
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	for _, t := range tenants {
		s := series[t.ID]
		if s == nil {
			s = emptyAPISeries(rg)
		}
		chainSeries := chainOf[t.ID].Series
		for i, p := range s {
			if err := cw.Row(cat([]string{t.ID.String(), exports.Text(t.Slug), exports.Text(t.Name), p.Day},
				numbersCells(p.APINumbers), chainCells(chainSeries[i], chainOf[t.ID].Weeks), compareCohortCells(cohorts[t.ID], p.Day))...); err != nil {
				panic(http.ErrAbortHandler)
			}
		}
	}
	if err := cw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}

// contractFunctions serves GET /tenants/{tenant_id}/onchain/ids/{chain_id}/functions.csv:
// a declared contract's calls per function, caller and day, as the
// tenant detail shows them.
func (e *Exports) contractFunctions(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.ParseID(chi.URLParam(r, "tenant_id"))
	if err != nil {
		return err
	}
	chainID, err := httpx.ParseID(chi.URLParam(r, "chain_id"))
	if err != nil {
		return err
	}
	now := e.g.now()
	rg, err := onchain.ParseRange(r.URL.Query(), now, onchain.DefaultRangeDays, onchain.MaxRangeDays)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var c onchain.ContractFunctions
	err = e.g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		if _, err := compareTenants(ctx, tx, []uuid.UUID{id}); err != nil {
			return err
		}
		v, ok, lerr := onchain.LoadFunctions(ctx, tx, id, chainID, rg, utcDay(now))
		if lerr != nil {
			return lerr
		}
		if !ok {
			return httpx.NotFound()
		}
		c = v
		return nil
	})
	if err != nil {
		return err
	}
	q := r.URL.Query()
	q.Set("chain_id", chainID.String())
	if err := e.auditExport(ctx, "contract_functions", q, exports.FunctionRows(c), []uuid.UUID{id}); err != nil {
		return err
	}
	name := exports.FileName("veristar-functions", c.Strkey[:8], rg.From.Format(time.DateOnly), rg.To.Format(time.DateOnly))
	if err := exports.WriteFunctions(w, name, c); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}
