package operator

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
)

const apiProjectsRoute = "operator-api-projects"

// apiSorts maps every sort of GET /api/operator/api/projects to its column.
// a percentile in the open bucket is +infinity in sql and sorts above every
// number; a project without requests has none and sorts last.
var apiSorts = map[string]string{
	"name": "lower(name)", "status": "status", "created_at": "created_at",
	"sites": "sites", "requests": "requests", "requests_4xx": "requests_4xx", "requests_5xx": "requests_5xx",
	"rate_4xx": "rate_4xx", "rate_5xx": "rate_5xx", "p50": "p50", "p95": "p95", "p99": "p99",
	"services": "services", "routes": "routes",
}

// apiChosenCTE selects the daily rows of the range that a project's
// numbers are made of, through api.operator_daily, which also counts the
// hours api-rollup has not reached yet (00022, control-plane-api 7.2): per tenant and service, metric rows when
// the service has any in the range, otherwise span rows (the design, the
// rule the tenant dashboard of this stage applies). it expects a cte t of
// tenants.
const apiChosenCTE = `
r as (
  select m.tenant_id, m.bucket_start, m.service, m.route, m.status_code, m.source, m.counts, m.count, m.lossy
  from api.operator_daily(@from_at::timestamptz, @to_end::timestamptz, array(select id from t)) m
  join t on t.id = m.tenant_id
),
svc as (
  select tenant_id, service,
         case when bool_or(source = 'metric') then 'metric' else 'span' end as source
  from r
  group by tenant_id, service
),
c as (
  select r.*
  from r
  join svc on svc.tenant_id = r.tenant_id and svc.service = r.service and svc.source = r.source
)`

// apiListCTE is baseCTE plus the api numbers and derived columns of a row.
const apiListCTE = `with ` + baseCTE + `,` + apiChosenCTE + `,
m as (
  select tenant_id,
         sum(count)::bigint as requests,
         coalesce(sum(count) filter (where status_code between 400 and 499), 0)::bigint as requests_4xx,
         coalesce(sum(count) filter (where status_code between 500 and 599), 0)::bigint as requests_5xx,
         api.sum_buckets(counts) as buckets,
         count(distinct service)::bigint as services,
         (count(distinct (service, route)) filter (where route <> ''))::bigint as routes,
         (count(distinct service) filter (where source = 'span'))::bigint as span_services,
         bool_or(lossy) as lossy
  from c
  group by tenant_id
),
x as (
  select t.id, t.slug, t.name, t.status, t.created_at,
         coalesce(s.sites, 0) as sites, coalesce(s.sites_verified, 0) as sites_verified,
         coalesce(s.sites_with_stellar_toml, 0) as sites_with_stellar_toml,
         coalesce(w.pageviews, 0) > 0 as has_web_data,
         coalesce(ap.has_api, false) as has_api_data,
         coalesce(m.requests, 0) as requests, coalesce(m.requests_4xx, 0) as requests_4xx,
         coalesce(m.requests_5xx, 0) as requests_5xx,
         coalesce(m.services, 0) as services, coalesce(m.routes, 0) as routes,
         coalesce(m.span_services, 0) as span_services, coalesce(m.lossy, false) as lossy,
         api.histogram_quantile(0.5, m.buckets) as p50,
         api.histogram_quantile(0.95, m.buckets) as p95,
         api.histogram_quantile(0.99, m.buckets) as p99
  from t
  left join s on s.tenant_id = t.id
  left join w on w.tenant_id = t.id
  left join ap on ap.tenant_id = t.id
  left join m on m.tenant_id = t.id
),
y as (
  select x.*,
         case when requests > 0 then requests_4xx::double precision / requests end as rate_4xx,
         case when requests > 0 then requests_5xx::double precision / requests end as rate_5xx
  from x
  where ` + derivedFilter + `
)`

// Percentiles are p50, p95 and p99 in seconds. a percentile in the open
// bucket above 10 s has no number: its seconds are null and
// its over_10s flag is true. without requests all six are null or false.
type Percentiles struct {
	P50Seconds *float64 `json:"p50_seconds"`
	P50Over10s bool     `json:"p50_over_10s"`
	P95Seconds *float64 `json:"p95_seconds"`
	P95Over10s bool     `json:"p95_over_10s"`
	P99Seconds *float64 `json:"p99_seconds"`
	P99Over10s bool     `json:"p99_over_10s"`
}

func percentile(v *float64) (*float64, bool) {
	if v == nil {
		return nil, false
	}
	if math.IsInf(*v, 1) {
		return nil, true
	}
	return v, false
}

func newPercentiles(p50, p95, p99 *float64) Percentiles {
	var p Percentiles
	p.P50Seconds, p.P50Over10s = percentile(p50)
	p.P95Seconds, p.P95Over10s = percentile(p95)
	p.P99Seconds, p.P99Over10s = percentile(p99)
	return p
}

// APINumbers are the request numbers of a project, a service or a day.
type APINumbers struct {
	Requests    int64    `json:"requests"`
	Requests4xx int64    `json:"requests_4xx"`
	Requests5xx int64    `json:"requests_5xx"`
	Rate4xx     *float64 `json:"rate_4xx"`
	Rate5xx     *float64 `json:"rate_5xx"`
	Percentiles
}

// source names the data a project's numbers come from: metric or span when
// every service uses one kind, mixed when they differ, null without data.
func source(services, spanServices int64) *string {
	var s string
	switch {
	case services == 0:
		return nil
	case spanServices == 0:
		s = "metric"
	case spanServices == services:
		s = "span"
	default:
		s = "mixed"
	}
	return &s
}

// APIProject is one row of GET /api/operator/api/projects (contract 8.5).
type APIProject struct {
	Tenant TenantRef `json:"tenant"`
	SiteCounts
	HasWebData bool `json:"has_web_data"`
	HasAPIData bool `json:"has_api_data"`
	APINumbers
	Services     int64   `json:"services"`
	Routes       int64   `json:"routes"`
	SpanServices int64   `json:"span_services"`
	Source       *string `json:"source"`
	Lossy        bool    `json:"lossy"`
}

func (g *Global) apiProjects(w http.ResponseWriter, r *http.Request) error {
	lq, err := g.parseList(r, apiProjectsRoute, apiSorts, "requests")
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := GlobalList[APIProject]{
		From: lq.Range.From.Format(time.DateOnly), To: lq.Range.To.Format(time.DateOnly), Items: []APIProject{},
	}
	err = g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		args := lq.args()
		if err := tx.QueryRow(ctx, apiListCTE+` select count(*) from y`, args).Scan(&out.Total); err != nil {
			return fmt.Errorf("count: %w", err)
		}
		rows, err := tx.Query(ctx, apiListCTE+`
			select id, slug, name, status, created_at, sites, sites_verified, sites_with_stellar_toml,
			       has_web_data, has_api_data, requests, requests_4xx, requests_5xx, rate_4xx, rate_5xx,
			       p50, p95, p99, services, routes, span_services, lossy
			from y`+lq.orderBy(apiSorts)+` limit @row_limit offset @row_offset`, args)
		if err != nil {
			return fmt.Errorf("page: %w", err)
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (APIProject, error) {
			var p APIProject
			var at time.Time
			var p50, p95, p99 *float64
			err := row.Scan(&p.Tenant.ID, &p.Tenant.Slug, &p.Tenant.Name, &p.Tenant.Status, &at,
				&p.Sites, &p.SitesVerified, &p.SitesWithStellarToml, &p.HasWebData, &p.HasAPIData,
				&p.Requests, &p.Requests4xx, &p.Requests5xx, &p.Rate4xx, &p.Rate5xx,
				&p50, &p95, &p99, &p.Services, &p.Routes, &p.SpanServices, &p.Lossy)
			p.Tenant.CreatedAt = httpx.Timestamp(at)
			p.Percentiles = newPercentiles(p50, p95, p99)
			p.Source = source(p.Services, p.SpanServices)
			return p, err
		})
		if err != nil {
			return fmt.Errorf("page: %w", err)
		}
		if len(items) > lq.Limit {
			items = items[:lq.Limit]
			out.NextCursor = lq.nextCursor(apiProjectsRoute, lq.Offset+lq.Limit)
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: api projects: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// APIPoint is one day of an api compare series. a day without requests has
// zero requests and null rates and percentiles.
type APIPoint struct {
	Day string `json:"day"`
	APINumbers
}

// APIService is one service of a compared project over the range.
type APIService struct {
	Service string `json:"service"`
	Source  string `json:"source"`
	APINumbers
	Routes int64 `json:"routes"`
	Lossy  bool  `json:"lossy"`
}

// APITotals are a compared project's numbers over the range.
type APITotals struct {
	APINumbers
	Services     int64   `json:"services"`
	Routes       int64   `json:"routes"`
	SpanServices int64   `json:"span_services"`
	Source       *string `json:"source"`
	Lossy        bool    `json:"lossy"`
}

// APICompareProject is one project of GET /api/operator/api/compare.
type APICompareProject struct {
	Tenant   TenantRef    `json:"tenant"`
	Totals   APITotals    `json:"totals"`
	Series   []APIPoint   `json:"series"`
	Services []APIService `json:"services"`
}

// APICompare is the answer of GET /api/operator/api/compare (contract 8.6).
type APICompare struct {
	From     string              `json:"from"`
	To       string              `json:"to"`
	Days     []string            `json:"days"`
	Projects []APICompareProject `json:"projects"`
}

// apiCompareCTE is apiChosenCTE over the compared tenants only.
const apiCompareCTE = `with t as (select unnest(@ids::uuid[]) as id),` + apiChosenCTE

func (g *Global) apiCompare(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	rg, err := g.parseRange(q)
	if err != nil {
		return err
	}
	ids, err := parseTenants(q)
	if err != nil {
		return err
	}
	desc := true
	if v, ok, err := oneValue(q, "service_order"); err != nil {
		return err
	} else if ok {
		if v != "asc" && v != "desc" {
			return httpx.BadQuery("service_order must be asc or desc")
		}
		desc = v == "desc"
	}
	ctx := r.Context()
	out := APICompare{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Days: dayStrings(rg)}
	err = g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		tenants, err := compareTenants(ctx, tx, ids)
		if err != nil {
			return err
		}
		args := pgx.NamedArgs{"ids": ids, "from_at": rg.From, "to_end": rg.end()}
		totals, err := apiTotals(ctx, tx, args)
		if err != nil {
			return err
		}
		series, err := apiSeries(ctx, tx, args, rg)
		if err != nil {
			return err
		}
		services, err := apiServices(ctx, tx, args, desc)
		if err != nil {
			return err
		}
		for _, t := range tenants {
			p := APICompareProject{Tenant: t, Totals: totals[t.ID], Series: series[t.ID], Services: services[t.ID]}
			if p.Services == nil {
				p.Services = []APIService{}
			}
			if p.Series == nil {
				p.Series = emptyAPISeries(rg)
			}
			out.Projects = append(out.Projects, p)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: api compare: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func emptyAPISeries(rg dayRange) []APIPoint {
	ds := dayStrings(rg)
	out := make([]APIPoint, len(ds))
	for i, d := range ds {
		out[i] = APIPoint{Day: d}
	}
	return out
}

// numbersCols are the select columns of APINumbers over rows of c.
const numbersCols = `
  sum(count)::bigint,
  coalesce(sum(count) filter (where status_code between 400 and 499), 0)::bigint,
  coalesce(sum(count) filter (where status_code between 500 and 599), 0)::bigint,
  api.histogram_quantile(0.5, api.sum_buckets(counts)),
  api.histogram_quantile(0.95, api.sum_buckets(counts)),
  api.histogram_quantile(0.99, api.sum_buckets(counts))`

func scanNumbers(dst *APINumbers, scan func(...any) error, before ...any) error {
	var p50, p95, p99 *float64
	if err := scan(append(before, &dst.Requests, &dst.Requests4xx, &dst.Requests5xx, &p50, &p95, &p99)...); err != nil {
		return err
	}
	dst.Rate4xx = ratio(dst.Requests4xx, dst.Requests)
	dst.Rate5xx = ratio(dst.Requests5xx, dst.Requests)
	dst.Percentiles = newPercentiles(p50, p95, p99)
	return nil
}

func apiTotals(ctx context.Context, tx pgx.Tx, args pgx.NamedArgs) (map[uuid.UUID]APITotals, error) {
	rows, err := tx.Query(ctx, apiCompareCTE+`
		select tenant_id, count(distinct service)::bigint,
		       (count(distinct (service, route)) filter (where route <> ''))::bigint,
		       (count(distinct service) filter (where source = 'span'))::bigint,
		       bool_or(lossy),`+numbersCols+`
		from c group by tenant_id`, args)
	if err != nil {
		return nil, fmt.Errorf("api totals: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]APITotals{}
	for rows.Next() {
		var id uuid.UUID
		var t APITotals
		if err := scanNumbers(&t.APINumbers, rows.Scan, &id, &t.Services, &t.Routes, &t.SpanServices, &t.Lossy); err != nil {
			return nil, fmt.Errorf("api totals: %w", err)
		}
		t.Source = source(t.Services, t.SpanServices)
		out[id] = t
	}
	return out, rows.Err()
}

func apiSeries(ctx context.Context, tx pgx.Tx, args pgx.NamedArgs, rg dayRange) (map[uuid.UUID][]APIPoint, error) {
	rows, err := tx.Query(ctx, apiCompareCTE+`
		select tenant_id, bucket_start,`+numbersCols+`
		from c group by tenant_id, bucket_start`, args)
	if err != nil {
		return nil, fmt.Errorf("api series: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]APIPoint{}
	for rows.Next() {
		var id uuid.UUID
		var day time.Time
		var n APINumbers
		if err := scanNumbers(&n, rows.Scan, &id, &day); err != nil {
			return nil, fmt.Errorf("api series: %w", err)
		}
		pts, ok := out[id]
		if !ok {
			pts = emptyAPISeries(rg)
			out[id] = pts
		}
		i := int(utcDay(day).Sub(rg.From).Hours() / 24)
		if i < 0 || i >= len(pts) {
			return nil, fmt.Errorf("api series: day %s outside the range", day.Format(time.DateOnly))
		}
		pts[i].APINumbers = n
	}
	return out, rows.Err()
}

func apiServices(ctx context.Context, tx pgx.Tx, args pgx.NamedArgs, desc bool) (map[uuid.UUID][]APIService, error) {
	dir := "asc"
	if desc {
		dir = "desc"
	}
	rows, err := tx.Query(ctx, apiCompareCTE+`
		select tenant_id, service, min(source),
		       (count(distinct route) filter (where route <> ''))::bigint, bool_or(lossy),`+numbersCols+`
		from c group by tenant_id, service
		order by tenant_id, sum(count) `+dir+`, service`, args)
	if err != nil {
		return nil, fmt.Errorf("api services: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]APIService{}
	for rows.Next() {
		var id uuid.UUID
		var s APIService
		if err := scanNumbers(&s.APINumbers, rows.Scan, &id, &s.Service, &s.Source, &s.Routes, &s.Lossy); err != nil {
			return nil, fmt.Errorf("api services: %w", err)
		}
		out[id] = append(out[id], s)
	}
	return out, rows.Err()
}
