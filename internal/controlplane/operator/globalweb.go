package operator

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

const webProjectsRoute = "operator-web-projects"

// webSorts maps every sort of GET /api/operator/web/projects to its column.
var webSorts = map[string]string{
	"name": "lower(name)", "status": "status", "created_at": "created_at",
	"sites": "sites", "sites_verified": "sites_verified", "sites_with_stellar_toml": "sites_with_stellar_toml",
	"pageviews": "pageviews", "visitors": "visitors", "visits": "visits", "bounces": "bounces",
	"totaltime": "totaltime", "bounce_rate": "bounce_rate", "avg_visit_duration": "avg_visit_duration",
	"quarantine_pageviews": "quarantine_pageviews", "quarantine_visitors": "quarantine_visitors",
	"quarantine_share": "quarantine_share", "ingest_requests": "ingest_requests",
	"ingest_rejected": "ingest_rejected", "rejected_share": "rejected_share",
}

// webDims are the breakdowns of the web views, in contract order.
var webDims = []string{"referrer", "country", "device", "browser", "os"}

// listTopDims are the breakdowns a list row carries (top
// referrers, countries, devices and browsers).
var listTopDims = []string{"referrer", "country", "device", "browser"}

// listTopN is how many values of each breakdown a list row carries.
const listTopN = 5

// webListCTE is baseCTE plus the ingest outcomes and the derived columns of
// a web list row. a request is any gateway outcome (accepted, quarantined or
// rejected_<reason>); the replayer's bot and dlq outcomes describe accepted
// events again and are not requests.
const webListCTE = `with ` + baseCTE + `,
i as (
  select h.tenant_id,
         coalesce(sum(h.count) filter (where starts_with(h.outcome, 'rejected_')), 0)::bigint as rejected,
         coalesce(sum(h.count) filter (where h.outcome in ('accepted', 'quarantined')
                                         or starts_with(h.outcome, 'rejected_')), 0)::bigint as requests
  from cp.web_ingest_hourly h
  join t on t.id = h.tenant_id
  where h.hour >= @from_at::timestamptz and h.hour < @to_end::timestamptz
  group by h.tenant_id
),
x as (
  select t.id, t.slug, t.name, t.status, t.created_at,
         coalesce(s.sites, 0) as sites, coalesce(s.sites_verified, 0) as sites_verified,
         coalesce(s.sites_with_stellar_toml, 0) as sites_with_stellar_toml,
         coalesce(w.pageviews, 0) as pageviews, coalesce(w.visitors, 0) as visitors,
         coalesce(w.visits, 0) as visits, coalesce(w.bounces, 0) as bounces,
         coalesce(w.totaltime, 0) as totaltime,
         coalesce(w.quarantine_pageviews, 0) as quarantine_pageviews,
         coalesce(w.quarantine_visitors, 0) as quarantine_visitors,
         coalesce(w.pageviews, 0) > 0 as has_web_data,
         coalesce(ap.has_api, false) as has_api_data,
         coalesce(i.requests, 0) as ingest_requests, coalesce(i.rejected, 0) as ingest_rejected
  from t
  left join s on s.tenant_id = t.id
  left join w on w.tenant_id = t.id
  left join ap on ap.tenant_id = t.id
  left join i on i.tenant_id = t.id
),
y as (
  select x.*,
         case when visits > 0 then bounces::double precision / visits end as bounce_rate,
         case when visits > 0 then totaltime::double precision / visits end as avg_visit_duration,
         case when pageviews + quarantine_pageviews > 0
              then quarantine_pageviews::double precision / (pageviews + quarantine_pageviews) end as quarantine_share,
         case when ingest_requests > 0 then ingest_rejected::double precision / ingest_requests end as rejected_share
  from x
  where ` + derivedFilter + `
)`

// WebProject is one row of GET /api/operator/web/projects (contract 8.3).
type WebProject struct {
	Tenant TenantRef `json:"tenant"`
	SiteCounts
	HasWebData          bool                  `json:"has_web_data"`
	HasAPIData          bool                  `json:"has_api_data"`
	Pageviews           int64                 `json:"pageviews"`
	Visitors            int64                 `json:"visitors"`
	Visits              int64                 `json:"visits"`
	Bounces             int64                 `json:"bounces"`
	Totaltime           int64                 `json:"totaltime"`
	BounceRate          *float64              `json:"bounce_rate"`
	AvgVisitDuration    *float64              `json:"avg_visit_duration"`
	QuarantinePageviews int64                 `json:"quarantine_pageviews"`
	QuarantineVisitors  int64                 `json:"quarantine_visitors"`
	QuarantineShare     *float64              `json:"quarantine_share"`
	IngestRequests      int64                 `json:"ingest_requests"`
	IngestRejected      int64                 `json:"ingest_rejected"`
	RejectedShare       *float64              `json:"rejected_share"`
	Top                 map[string][]KeyValue `json:"top"`
}

// KeyValue is one value of a breakdown: umami's x and the sum of its daily y.
type KeyValue struct {
	Key   string `json:"key"`
	Value int64  `json:"value"`
}

func (g *Global) webProjects(w http.ResponseWriter, r *http.Request) error {
	lq, err := g.parseList(r, webProjectsRoute, webSorts, "pageviews")
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := GlobalList[WebProject]{
		From: lq.Range.From.Format(time.DateOnly), To: lq.Range.To.Format(time.DateOnly), Items: []WebProject{},
	}
	err = g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		args := lq.args()
		if err := tx.QueryRow(ctx, webListCTE+` select count(*) from y`, args).Scan(&out.Total); err != nil {
			return fmt.Errorf("count: %w", err)
		}
		rows, err := tx.Query(ctx, webListCTE+`
			select id, slug, name, status, created_at, sites, sites_verified, sites_with_stellar_toml,
			       has_web_data, has_api_data, pageviews, visitors, visits, bounces, totaltime,
			       bounce_rate, avg_visit_duration, quarantine_pageviews, quarantine_visitors, quarantine_share,
			       ingest_requests, ingest_rejected, rejected_share
			from y`+lq.orderBy(webSorts)+` limit @row_limit offset @row_offset`, args)
		if err != nil {
			return fmt.Errorf("page: %w", err)
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (WebProject, error) {
			var p WebProject
			var at time.Time
			err := row.Scan(&p.Tenant.ID, &p.Tenant.Slug, &p.Tenant.Name, &p.Tenant.Status, &at,
				&p.Sites, &p.SitesVerified, &p.SitesWithStellarToml, &p.HasWebData, &p.HasAPIData,
				&p.Pageviews, &p.Visitors, &p.Visits, &p.Bounces, &p.Totaltime,
				&p.BounceRate, &p.AvgVisitDuration, &p.QuarantinePageviews, &p.QuarantineVisitors, &p.QuarantineShare,
				&p.IngestRequests, &p.IngestRejected, &p.RejectedShare)
			p.Tenant.CreatedAt = httpx.Timestamp(at)
			return p, err
		})
		if err != nil {
			return fmt.Errorf("page: %w", err)
		}
		if len(items) > lq.Limit {
			items = items[:lq.Limit]
			out.NextCursor = lq.nextCursor(webProjectsRoute, lq.Offset+lq.Limit)
		}
		ids := make([]uuid.UUID, len(items))
		for i, p := range items {
			ids[i] = p.Tenant.ID
		}
		tops, err := topValues(ctx, tx, ids, lq.Range, listTopDims, listTopN)
		if err != nil {
			return err
		}
		for i := range items {
			items[i].Top = tops[items[i].Tenant.ID].items
		}
		out.Items = items
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: web projects: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// breakdowns holds the top values per dimension of one project and how many
// distinct values each dimension has over the range.
type breakdowns struct {
	items map[string][]KeyValue
	keys  map[string]int64
}

// topValues sums agg.web_daily_dim of the primary band over the range per
// project, dimension and key, and keeps the n largest of each dimension
// (ties by key). every dimension is present, empty when it has no value.
func topValues(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, rg dayRange, dims []string, n int) (map[uuid.UUID]breakdowns, error) {
	out := make(map[uuid.UUID]breakdowns, len(ids))
	for _, id := range ids {
		b := breakdowns{items: map[string][]KeyValue{}, keys: map[string]int64{}}
		for _, d := range dims {
			b.items[d] = []KeyValue{}
			b.keys[d] = 0
		}
		out[id] = b
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		select tenant_id, dimension, key, value, keys from (
		  select tenant_id, dimension, key, sum(value)::bigint as value,
		         row_number() over (partition by tenant_id, dimension order by sum(value) desc, key) as rn,
		         count(*) over (partition by tenant_id, dimension) as keys
		  from agg.web_daily_dim
		  where tenant_id = any(@ids::uuid[]) and band = 'primary'
		    and day between @from_day::date and @to_day::date and dimension = any(@dims::text[])
		  group by tenant_id, dimension, key
		) r
		where rn <= @n
		order by tenant_id, dimension, rn`,
		pgx.NamedArgs{"ids": ids, "from_day": rg.From, "to_day": rg.To, "dims": dims, "n": n})
	if err != nil {
		return nil, fmt.Errorf("breakdowns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var dim string
		var kv KeyValue
		var keys int64
		if err := rows.Scan(&id, &dim, &kv.Key, &kv.Value, &keys); err != nil {
			return nil, fmt.Errorf("breakdowns: %w", err)
		}
		b := out[id]
		b.items[dim] = append(b.items[dim], kv)
		b.keys[dim] = keys
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("breakdowns: %w", err)
	}
	return out, nil
}

// WebTotals are a project's web numbers over the range, primary band, with
// the quarantine band beside them.
type WebTotals struct {
	Pageviews           int64    `json:"pageviews"`
	Visitors            int64    `json:"visitors"`
	Visits              int64    `json:"visits"`
	Bounces             int64    `json:"bounces"`
	Totaltime           int64    `json:"totaltime"`
	BounceRate          *float64 `json:"bounce_rate"`
	AvgVisitDuration    *float64 `json:"avg_visit_duration"`
	QuarantinePageviews int64    `json:"quarantine_pageviews"`
	QuarantineVisitors  int64    `json:"quarantine_visitors"`
	QuarantineShare     *float64 `json:"quarantine_share"`
}

func (t *WebTotals) derive() {
	t.BounceRate = ratio(t.Bounces, t.Visits)
	t.AvgVisitDuration = ratio(t.Totaltime, t.Visits)
	t.QuarantineShare = ratio(t.QuarantinePageviews, t.Pageviews+t.QuarantinePageviews)
}

// WebPoint is one day of a compare series. Aggregated is false when no site
// of the project has a row for the day yet; every number is then null.
type WebPoint struct {
	Day                 string   `json:"day"`
	Aggregated          bool     `json:"aggregated"`
	Pageviews           *int64   `json:"pageviews"`
	Visitors            *int64   `json:"visitors"`
	Visits              *int64   `json:"visits"`
	Bounces             *int64   `json:"bounces"`
	Totaltime           *int64   `json:"totaltime"`
	BounceRate          *float64 `json:"bounce_rate"`
	AvgVisitDuration    *float64 `json:"avg_visit_duration"`
	QuarantinePageviews *int64   `json:"quarantine_pageviews"`
}

// Breakdown is one dimension of a compare answer.
type Breakdown struct {
	Items     []KeyValue `json:"items"`
	TotalKeys int64      `json:"total_keys"`
}

// WebCompareProject is one project of GET /api/operator/web/compare.
type WebCompareProject struct {
	Tenant     TenantRef            `json:"tenant"`
	Totals     WebTotals            `json:"totals"`
	Series     []WebPoint           `json:"series"`
	Breakdowns map[string]Breakdown `json:"breakdowns"`
}

// WebCompare is the answer of GET /api/operator/web/compare (contract 8.4).
type WebCompare struct {
	From     string              `json:"from"`
	To       string              `json:"to"`
	Days     []string            `json:"days"`
	Projects []WebCompareProject `json:"projects"`
}

func dayStrings(rg dayRange) []string {
	ds := rg.days()
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Format(time.DateOnly)
	}
	return out
}

func (g *Global) webCompare(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	rg, err := g.parseRange(q)
	if err != nil {
		return err
	}
	ids, err := parseTenants(q)
	if err != nil {
		return err
	}
	n, err := parseLimitParam(q, "breakdown_limit", 10, 500)
	if err != nil {
		return err
	}
	ctx := r.Context()
	out := WebCompare{From: rg.From.Format(time.DateOnly), To: rg.To.Format(time.DateOnly), Days: dayStrings(rg)}
	err = g.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		tenants, err := compareTenants(ctx, tx, ids)
		if err != nil {
			return err
		}
		series, quarantineVisitors, err := webSeries(ctx, tx, ids, rg)
		if err != nil {
			return err
		}
		tops, err := topValues(ctx, tx, ids, rg, webDims, n)
		if err != nil {
			return err
		}
		for _, t := range tenants {
			p := WebCompareProject{Tenant: t, Series: series[t.ID], Breakdowns: map[string]Breakdown{}}
			for _, pt := range p.Series {
				if !pt.Aggregated {
					continue
				}
				p.Totals.Pageviews += *pt.Pageviews
				p.Totals.Visitors += *pt.Visitors
				p.Totals.Visits += *pt.Visits
				p.Totals.Bounces += *pt.Bounces
				p.Totals.Totaltime += *pt.Totaltime
				p.Totals.QuarantinePageviews += *pt.QuarantinePageviews
			}
			p.Totals.QuarantineVisitors = quarantineVisitors[t.ID]
			p.Totals.derive()
			b := tops[t.ID]
			for _, d := range webDims {
				p.Breakdowns[d] = Breakdown{Items: b.items[d], TotalKeys: b.keys[d]}
			}
			out.Projects = append(out.Projects, p)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: web compare: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// webSeries reads one point per project and day of the range. a day
// without any agg.web_daily row of the project stays not aggregated. the
// quarantine visitors are summed per project for the totals; the daily
// points show only the quarantine page views.
func webSeries(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, rg dayRange) (map[uuid.UUID][]WebPoint, map[uuid.UUID]int64, error) {
	days := rg.days()
	out := map[uuid.UUID][]WebPoint{}
	for _, id := range ids {
		pts := make([]WebPoint, len(days))
		for i, d := range days {
			pts[i] = WebPoint{Day: d.Format(time.DateOnly)}
		}
		out[id] = pts
	}
	rows, err := tx.Query(ctx, `
		select tenant_id, day,
		       coalesce(sum(pageviews) filter (where band = 'primary'), 0)::bigint,
		       coalesce(sum(visitors) filter (where band = 'primary'), 0)::bigint,
		       coalesce(sum(visits) filter (where band = 'primary'), 0)::bigint,
		       coalesce(sum(bounces) filter (where band = 'primary'), 0)::bigint,
		       coalesce(sum(totaltime) filter (where band = 'primary'), 0)::bigint,
		       coalesce(sum(pageviews) filter (where band = 'quarantine'), 0)::bigint,
		       coalesce(sum(visitors) filter (where band = 'quarantine'), 0)::bigint
		from agg.web_daily
		where tenant_id = any(@ids::uuid[]) and day between @from_day::date and @to_day::date
		group by tenant_id, day`,
		pgx.NamedArgs{"ids": ids, "from_day": rg.From, "to_day": rg.To})
	if err != nil {
		return nil, nil, fmt.Errorf("series: %w", err)
	}
	defer rows.Close()
	qv := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var day time.Time
		var pv, vs, vi, bo, tt, qp, qvis int64
		if err := rows.Scan(&id, &day, &pv, &vs, &vi, &bo, &tt, &qp, &qvis); err != nil {
			return nil, nil, fmt.Errorf("series: %w", err)
		}
		i := int(utcDay(day).Sub(rg.From).Hours() / 24)
		pts := out[id]
		if i < 0 || i >= len(pts) {
			return nil, nil, fmt.Errorf("series: day %s outside the range", day.Format(time.DateOnly))
		}
		pts[i] = WebPoint{
			Day: pts[i].Day, Aggregated: true, Pageviews: &pv, Visitors: &vs, Visits: &vi, Bounces: &bo,
			Totaltime: &tt, BounceRate: ratio(bo, vi), AvgVisitDuration: ratio(tt, vi), QuarantinePageviews: &qp,
		}
		qv[id] += qvis
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("series: %w", err)
	}
	return out, qv, nil
}
