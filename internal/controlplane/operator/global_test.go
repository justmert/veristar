package operator

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/db/dbtest"
)

// globalFixture is a platform database holding web aggregates, edge
// outcomes and api daily rows for a handful of projects, written as the
// jobs task and the rollup would write them.
type globalFixture struct {
	pg      *dbtest.Postgres
	h       http.Handler
	g       *Global
	today   time.Time
	tenants map[string]uuid.UUID
}

const fixtureDays = 10

func newGlobalFixture(t *testing.T) *globalFixture {
	t.Helper()
	d, pg := reviewDB(t)
	g, err := NewGlobal(d)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Route("/api/operator", g.Routes)
	f := &globalFixture{pg: pg, h: r, g: g, today: utcDay(time.Now()), tenants: map[string]uuid.UUID{}}

	c, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := c.Exec(t.Context(), sql, args...)
		require.NoError(t, err, sql)
	}

	type site struct{ verified, badCheck, toml, removed bool }
	type tenant struct {
		name, status string
		internal     bool
		created      time.Time
		sites        []site
		web          bool
		api          string // "", metric, span, mixed, slow
	}
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	list := []tenant{
		{name: "aurora", status: "active", created: base.AddDate(0, 1, 0), web: true, api: "metric",
			sites: []site{{verified: true, toml: true}, {verified: false}}},
		{name: "borealis", status: "active", created: base.AddDate(0, 1, 4), web: true, api: "span",
			sites: []site{{verified: true, badCheck: true}}},
		{name: "comet", status: "suspended", created: base.AddDate(0, 1, 9), api: "mixed",
			sites: []site{{verified: true}, {toml: true, removed: true}}},
		{name: "dune", status: "pending", created: base.AddDate(0, 1, 19)},
		{name: "eclipse", status: "removed", created: base, web: true, sites: []site{{verified: true, toml: true}}},
		{name: "flare", status: "active", created: base.AddDate(0, 1, 25), web: true, api: "slow",
			sites: []site{{toml: true}}},
		{name: "probe", status: "active", internal: true, created: base, web: true, api: "metric",
			sites: []site{{verified: true, toml: true}}},
	}
	rng := rand.New(rand.NewPCG(11, 12))
	first := f.today.AddDate(0, 0, -(fixtureDays - 1))
	for i, tn := range list {
		id := uuid.Must(uuid.NewV7())
		f.tenants[tn.name] = id
		exec(`insert into cp.tenants (id, slug, name, status, is_internal, created_at) values ($1, $2, $3, $4, $5, $6)`,
			id, tn.name, strings.ToUpper(tn.name[:1])+tn.name[1:], tn.status, tn.internal, tn.created)
		var siteIDs []uuid.UUID
		for j, s := range tn.sites {
			sid := uuid.Must(uuid.NewV7())
			siteIDs = append(siteIDs, sid)
			status := "active"
			if s.removed {
				status = "removed"
			}
			var verifiedAt *time.Time
			var verifyErr *string
			if s.verified {
				at := tn.created.Add(time.Hour)
				verifiedAt = &at
			}
			if s.badCheck {
				e := "txt_missing"
				verifyErr = &e
			}
			toml := `{"status":"not_found"}`
			if s.toml {
				toml = `{"status":"found"}`
			}
			exec(`insert into cp.sites (id, tenant_id, name, domain, status, verify_token, verified_at, last_verify_error, stellar_toml)
				values ($1, $2, $3, $3, $4, 'x', $5, $6, $7)`,
				sid, id, fmt.Sprintf("s%d.%s.example", j, tn.name), status, verifiedAt, verifyErr, toml)
		}
		if tn.web {
			for _, sid := range siteIDs {
				// two days outside the range and one missing day inside it
				for d := first.AddDate(0, 0, -2); !d.After(f.today); d = d.AddDate(0, 0, 1) {
					if d.Equal(first.AddDate(0, 0, 3)) && sid == siteIDs[0] && tn.name == "aurora" {
						continue
					}
					for _, band := range []string{"primary", "quarantine"} {
						pv := int64(rng.IntN(80))
						if band == "quarantine" {
							pv = int64(rng.IntN(8))
						}
						visits := pv / 2
						visitors := visits - visits/4
						bounces := visits / 3
						exec(`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
							values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
							sid, id, d, band, pv, visitors, visits, bounces, visits*int64(rng.IntN(300)))
						for _, dim := range webDims {
							for k := range 1 + rng.IntN(7) {
								exec(`insert into agg.web_daily_dim (site_id, tenant_id, day, band, dimension, key, value)
									values ($1, $2, $3, $4, $5, $6, $7)`,
									sid, id, d, band, dim, fmt.Sprintf("%s-%d", dim, k), 1+rng.IntN(20))
							}
						}
					}
				}
				for _, o := range []string{"accepted", "quarantined", "rejected_origin", "rejected_gpc", "bot", "dlq"} {
					for h := range 3 {
						exec(`insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count) values ($1, $2, $3, $4, $5)`,
							sid, id, f.today.AddDate(0, 0, -h*4).Add(time.Duration(i)*time.Hour), o, 1+rng.IntN(50))
					}
				}
			}
		}
		if tn.api != "" {
			for d := first.AddDate(0, 0, -1); !d.After(f.today); d = d.AddDate(0, 0, 1) {
				for _, svc := range []string{"api", "worker", ""} {
					sources := []string{"metric"}
					switch tn.api {
					case "span":
						sources = []string{"span"}
					case "mixed":
						if svc == "api" {
							sources = []string{"metric", "span"}
						} else {
							sources = []string{"span"}
						}
					}
					for _, src := range sources {
						for _, st := range []int{200, 404, 503, 0} {
							for _, route := range []string{"/a", "/b/{id}", ""} {
								if rng.IntN(3) == 0 {
									continue
								}
								var counts [15]int64
								var total int64
								for b := range counts {
									if rng.IntN(3) == 0 {
										counts[b] = int64(rng.IntN(40))
									}
								}
								if tn.api == "slow" {
									counts[14] += 500
								}
								for _, n := range counts {
									total += n
								}
								exec(`insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy)
									values ($1, $2, $3, 'GET', $4, $5, $6, $7, $8, $9, $10)`,
									id, d, svc, route, st, src, counts[:], float64(total)*0.05, total, rng.IntN(9) == 0)
							}
						}
					}
				}
			}
		}
	}
	return f
}

func (f *globalFixture) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	return call(t, f.h, path)
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(body, &v), string(body))
	return v
}

// superQuery runs an independent query past row-level security.
func (f *globalFixture) superQuery(t *testing.T, sql string, args ...any) pgx.Rows {
	t.Helper()
	c, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	rows, err := c.Query(t.Context(), sql, args...)
	require.NoError(t, err)
	return rows
}

func (f *globalFixture) rangeQuery() string {
	return "from=" + f.today.AddDate(0, 0, -(fixtureDays-1)).Format(time.DateOnly) + "&to=" + f.today.Format(time.DateOnly)
}

func idsOf[T any](items []T, id func(T) uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, it := range items {
		out[i] = id(it)
	}
	return out
}

// collect pages through a list with limit 2 and returns every item.
func collect[T any](t *testing.T, f *globalFixture, path string) ([]T, int64) {
	t.Helper()
	var all []T
	var total int64 = -1
	next := ""
	for range 20 {
		p := path + "&limit=2"
		if next != "" {
			p += "&cursor=" + url.QueryEscape(next)
		}
		code, body := f.get(t, p)
		require.Equal(t, http.StatusOK, code, string(body))
		page := decode[GlobalList[T]](t, body)
		if total >= 0 {
			require.Equal(t, total, page.Total, "the total is the same on every page")
		}
		total = page.Total
		all = append(all, page.Items...)
		if page.NextCursor == nil {
			return all, total
		}
		next = *page.NextCursor
	}
	t.Fatal("pagination did not end")
	return nil, 0
}

type sortVal struct {
	null bool
	f    float64
	s    string
}

func compareSort(a, b sortVal) int {
	if a.s != "" || b.s != "" {
		return strings.Compare(a.s, b.s)
	}
	return cmp.Compare(a.f, b.f)
}

// checkOrder sorts a copy of items the way contract 8.2 says (value, nulls
// last in both directions, id as the tie-breaker in the same direction)
// and compares it with the order the endpoint returned.
func checkOrder[T any](t *testing.T, items []T, desc bool, val func(T) sortVal, id func(T) uuid.UUID, what string) {
	t.Helper()
	want := slices.Clone(items)
	slices.SortStableFunc(want, func(a, b T) int {
		va, vb := val(a), val(b)
		if va.null != vb.null {
			if va.null {
				return 1
			}
			return -1
		}
		c := 0
		if !va.null {
			c = compareSort(va, vb)
		}
		if c == 0 {
			c = strings.Compare(id(a).String(), id(b).String())
		}
		if desc {
			c = -c
		}
		return c
	})
	require.Equal(t, idsOf(want, id), idsOf(items, id), what)
}

func num(v *float64) sortVal {
	if v == nil {
		return sortVal{null: true}
	}
	return sortVal{f: *v}
}

func pct(s *float64, over bool) sortVal {
	if over {
		return sortVal{f: math.Inf(1)}
	}
	return num(s)
}

func intv(v int64) sortVal { return sortVal{f: float64(v)} }

func webSortValue(p WebProject, sort string) sortVal {
	switch sort {
	case "name":
		return sortVal{s: strings.ToLower(p.Tenant.Name)}
	case "status":
		return sortVal{s: p.Tenant.Status}
	case "created_at":
		return sortVal{s: time.Time(p.Tenant.CreatedAt).Format(time.RFC3339Nano)}
	case "sites":
		return intv(p.Sites)
	case "sites_verified":
		return intv(p.SitesVerified)
	case "sites_with_stellar_toml":
		return intv(p.SitesWithStellarToml)
	case "pageviews":
		return intv(p.Pageviews)
	case "visitors":
		return intv(p.Visitors)
	case "visits":
		return intv(p.Visits)
	case "bounces":
		return intv(p.Bounces)
	case "totaltime":
		return intv(p.Totaltime)
	case "bounce_rate":
		return num(p.BounceRate)
	case "avg_visit_duration":
		return num(p.AvgVisitDuration)
	case "quarantine_pageviews":
		return intv(p.QuarantinePageviews)
	case "quarantine_visitors":
		return intv(p.QuarantineVisitors)
	case "quarantine_share":
		return num(p.QuarantineShare)
	case "ingest_requests":
		return intv(p.IngestRequests)
	case "ingest_rejected":
		return intv(p.IngestRejected)
	case "rejected_share":
		return num(p.RejectedShare)
	}
	panic("unknown web sort " + sort)
}

func apiSortValue(p APIProject, sort string) sortVal {
	switch sort {
	case "name":
		return sortVal{s: strings.ToLower(p.Tenant.Name)}
	case "status":
		return sortVal{s: p.Tenant.Status}
	case "created_at":
		return sortVal{s: time.Time(p.Tenant.CreatedAt).Format(time.RFC3339Nano)}
	case "sites":
		return intv(p.Sites)
	case "requests":
		return intv(p.Requests)
	case "requests_4xx":
		return intv(p.Requests4xx)
	case "requests_5xx":
		return intv(p.Requests5xx)
	case "rate_4xx":
		return num(p.Rate4xx)
	case "rate_5xx":
		return num(p.Rate5xx)
	case "p50":
		return pct(p.P50Seconds, p.P50Over10s)
	case "p95":
		return pct(p.P95Seconds, p.P95Over10s)
	case "p99":
		return pct(p.P99Seconds, p.P99Over10s)
	case "services":
		return intv(p.Services)
	case "routes":
		return intv(p.Routes)
	}
	panic("unknown api sort " + sort)
}

func webID(p WebProject) uuid.UUID { return p.Tenant.ID }
func apiID(p APIProject) uuid.UUID { return p.Tenant.ID }

func TestGlobalWebProjects(t *testing.T) {
	f := newGlobalFixture(t)
	rq := f.rangeQuery()

	all, total := collect[WebProject](t, f, "/api/operator/web/projects?"+rq+"&sort=name&order=asc")
	require.Equal(t, int64(6), total, "every project except the internal one")
	require.Len(t, all, 6)
	for _, p := range all {
		require.NotEqual(t, f.tenants["probe"], p.Tenant.ID, "internal tenants are never listed")
	}
	require.Equal(t, []string{"Aurora", "Borealis", "Comet", "Dune", "Eclipse", "Flare"}, func() []string {
		var n []string
		for _, p := range all {
			n = append(n, p.Tenant.Name)
		}
		return n
	}())

	t.Run("derived numbers equal independent sql", func(t *testing.T) {
		first := f.today.AddDate(0, 0, -(fixtureDays - 1))
		rows := f.superQuery(t, `
			select t.id,
			  coalesce((select sum(pageviews) from agg.web_daily d where d.tenant_id = t.id and d.band = 'primary' and d.day >= $1 and d.day <= $2), 0)::bigint,
			  coalesce((select sum(visitors) from agg.web_daily d where d.tenant_id = t.id and d.band = 'primary' and d.day >= $1 and d.day <= $2), 0)::bigint,
			  coalesce((select sum(visits) from agg.web_daily d where d.tenant_id = t.id and d.band = 'primary' and d.day >= $1 and d.day <= $2), 0)::bigint,
			  coalesce((select sum(bounces) from agg.web_daily d where d.tenant_id = t.id and d.band = 'primary' and d.day >= $1 and d.day <= $2), 0)::bigint,
			  coalesce((select sum(totaltime) from agg.web_daily d where d.tenant_id = t.id and d.band = 'primary' and d.day >= $1 and d.day <= $2), 0)::bigint,
			  coalesce((select sum(pageviews) from agg.web_daily d where d.tenant_id = t.id and d.band = 'quarantine' and d.day >= $1 and d.day <= $2), 0)::bigint,
			  coalesce((select sum(count) from cp.web_ingest_hourly h where h.tenant_id = t.id and h.outcome like 'rejected%'
			    and h.hour >= $1::date and h.hour < $2::date + 1), 0)::bigint,
			  coalesce((select sum(count) from cp.web_ingest_hourly h where h.tenant_id = t.id and h.outcome not in ('bot', 'dlq')
			    and h.hour >= $1::date and h.hour < $2::date + 1), 0)::bigint
			from cp.tenants t where not t.is_internal`, first, f.today)
		type want struct{ pv, vis, visits, bounces, tt, qpv, rej, req int64 }
		wants := map[uuid.UUID]want{}
		for rows.Next() {
			var id uuid.UUID
			var w want
			require.NoError(t, rows.Scan(&id, &w.pv, &w.vis, &w.visits, &w.bounces, &w.tt, &w.qpv, &w.rej, &w.req))
			wants[id] = w
		}
		require.NoError(t, rows.Err())
		for _, p := range all {
			w := wants[p.Tenant.ID]
			require.Equal(t, [8]int64{w.pv, w.vis, w.visits, w.bounces, w.tt, w.qpv, w.rej, w.req},
				[8]int64{p.Pageviews, p.Visitors, p.Visits, p.Bounces, p.Totaltime, p.QuarantinePageviews, p.IngestRejected, p.IngestRequests},
				p.Tenant.Name)
			require.Equal(t, ratio(w.bounces, w.visits), p.BounceRate, p.Tenant.Name)
			require.Equal(t, ratio(w.tt, w.visits), p.AvgVisitDuration, p.Tenant.Name)
			require.Equal(t, ratio(w.qpv, w.pv+w.qpv), p.QuarantineShare, p.Tenant.Name)
			require.Equal(t, ratio(w.rej, w.req), p.RejectedShare, p.Tenant.Name)
			require.Equal(t, w.pv > 0, p.HasWebData, p.Tenant.Name)
		}
		// the top values are the largest sums of the range, ties by key
		for _, p := range all {
			for _, dim := range listTopDims {
				rows := f.superQuery(t, `select key, sum(value)::bigint from agg.web_daily_dim
					where tenant_id = $1 and band = 'primary' and dimension = $2 and day >= $3 and day <= $4
					group by key order by 2 desc, key limit 5`, p.Tenant.ID, dim, first, f.today)
				want, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (KeyValue, error) {
					var kv KeyValue
					return kv, r.Scan(&kv.Key, &kv.Value)
				})
				require.NoError(t, err)
				require.Equal(t, want, p.Top[dim], "%s %s", p.Tenant.Name, dim)
			}
		}
	})

	t.Run("site facts", func(t *testing.T) {
		by := map[string]WebProject{}
		for _, p := range all {
			by[p.Tenant.Name] = p
		}
		require.Equal(t, SiteCounts{Sites: 2, SitesVerified: 1, SitesWithStellarToml: 1}, by["Aurora"].SiteCounts)
		require.Equal(t, SiteCounts{Sites: 1}, by["Borealis"].SiteCounts, "a failing check is not verified")
		require.Equal(t, SiteCounts{Sites: 1, SitesVerified: 1}, by["Comet"].SiteCounts, "removed sites do not count")
		require.True(t, by["Comet"].HasAPIData)
		require.False(t, by["Dune"].HasAPIData)
	})

	t.Run("every filter", func(t *testing.T) {
		names := func(q string) []string {
			items, _ := collect[WebProject](t, f, "/api/operator/web/projects?"+rq+"&sort=name&order=asc&"+q)
			out := []string{}
			for _, p := range items {
				out = append(out, p.Tenant.Name)
			}
			return out
		}
		cases := map[string][]string{
			"status=active":                                   {"Aurora", "Borealis", "Flare"},
			"status=suspended,removed":                        {"Comet", "Eclipse"},
			"verified=true":                                   {"Aurora", "Comet", "Eclipse"},
			"verified=false":                                  {"Borealis", "Dune", "Flare"},
			"stellar_toml=true":                               {"Aurora", "Eclipse", "Flare"},
			"stellar_toml=false":                              {"Borealis", "Comet", "Dune"},
			"has_web_data=true":                               {"Aurora", "Borealis", "Eclipse", "Flare"},
			"has_web_data=false":                              {"Comet", "Dune"},
			"has_api_data=true":                               {"Aurora", "Borealis", "Comet", "Flare"},
			"has_api_data=false":                              {"Dune", "Eclipse"},
			"enrolled_from=2026-10-05":                        {"Borealis", "Comet", "Dune", "Flare"},
			"enrolled_to=2026-10-05":                          {"Aurora", "Borealis", "Eclipse"},
			"enrolled_from=2026-10-02&enrolled_to=2026-10-10": {"Borealis", "Comet"},
			"q=OR":    {"Aurora", "Borealis"},
			"q=dune":  {"Dune"},
			"q=probe": {},
			"status=active&verified=true&has_api_data=true": {"Aurora"},
		}
		for q, want := range cases {
			require.Equal(t, want, names(q), q)
		}
	})

	t.Run("every sort column in both orders", func(t *testing.T) {
		for sort := range webSorts {
			for _, order := range []string{"asc", "desc"} {
				items, _ := collect[WebProject](t, f, "/api/operator/web/projects?"+rq+"&sort="+sort+"&order="+order)
				require.Len(t, items, 6)
				checkOrder(t, items, order == "desc", func(p WebProject) sortVal { return webSortValue(p, sort) }, webID, sort+" "+order)
			}
		}
		code, body := f.get(t, "/api/operator/web/projects?"+rq)
		require.Equal(t, http.StatusOK, code)
		page := decode[GlobalList[WebProject]](t, body)
		checkOrder(t, page.Items, true, func(p WebProject) sortVal { return webSortValue(p, "pageviews") }, webID, "default sort")
	})

	t.Run("cursors are bound to their query", func(t *testing.T) {
		code, body := f.get(t, "/api/operator/web/projects?"+rq+"&sort=visits&limit=2")
		require.Equal(t, http.StatusOK, code)
		next := decode[GlobalList[WebProject]](t, body).NextCursor
		require.NotNil(t, next)
		for _, other := range []string{rq + "&sort=visitors", rq + "&sort=visits&order=asc", rq + "&sort=visits&status=active",
			"sort=visits&from=" + f.today.AddDate(0, 0, -3).Format(time.DateOnly)} {
			code, body = f.get(t, "/api/operator/web/projects?"+other+"&cursor="+url.QueryEscape(*next))
			require.Equal(t, http.StatusBadRequest, code, other)
			require.Contains(t, string(body), "invalid_cursor")
		}
		code, _ = f.get(t, "/api/operator/api/projects?"+rq+"&sort=visits&cursor="+url.QueryEscape(*next))
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("bad queries", func(t *testing.T) {
		tomorrow := f.today.AddDate(0, 0, 1).Format(time.DateOnly)
		for _, bad := range []string{
			"from=2026-13-01", "to=" + tomorrow, "from=2026-11-10&to=2026-11-01", "from=2024-01-01&to=2026-01-02",
			"status=gone", "verified=yes", "stellar_toml=1", "has_web_data=", "has_api_data=TRUE",
			"enrolled_from=x", "enrolled_from=2026-10-10&enrolled_to=2026-10-01", "q=", "sort=slug", "order=up",
			"limit=0", "limit=201", "cursor=x", "sort=name&sort=status",
		} {
			code, body := f.get(t, "/api/operator/web/projects?"+bad)
			require.Equal(t, http.StatusBadRequest, code, bad)
			require.Regexp(t, `invalid_(query|cursor)`, string(body), bad)
		}
	})
}

func TestGlobalAPIProjects(t *testing.T) {
	f := newGlobalFixture(t)
	rq := f.rangeQuery()
	all, total := collect[APIProject](t, f, "/api/operator/api/projects?"+rq+"&sort=name&order=asc")
	require.Equal(t, int64(6), total)

	t.Run("numbers equal independent sql", func(t *testing.T) {
		first := f.today.AddDate(0, 0, -(fixtureDays - 1))
		// the source rule written another way: a service uses span rows
		// only when no metric row of it exists in the range
		rows := f.superQuery(t, `
			with r as (
			  select * from api.metrics_daily m
			  where m.bucket_start >= $1::date and m.bucket_start < $2::date + 1
			    and (m.source = 'metric' or not exists (
			      select 1 from api.metrics_daily o where o.tenant_id = m.tenant_id and o.service = m.service
			        and o.source = 'metric' and o.bucket_start >= $1::date and o.bucket_start < $2::date + 1))
			)
			select t.id,
			  coalesce((select sum(count) from r where r.tenant_id = t.id), 0)::bigint,
			  coalesce((select sum(count) from r where r.tenant_id = t.id and status_code >= 400 and status_code < 500), 0)::bigint,
			  coalesce((select sum(count) from r where r.tenant_id = t.id and status_code >= 500 and status_code < 600), 0)::bigint,
			  (select api.histogram_quantile(0.5, api.sum_buckets(counts)) from r where r.tenant_id = t.id),
			  (select api.histogram_quantile(0.95, api.sum_buckets(counts)) from r where r.tenant_id = t.id),
			  (select api.histogram_quantile(0.99, api.sum_buckets(counts)) from r where r.tenant_id = t.id),
			  (select count(distinct service) from r where r.tenant_id = t.id)::bigint,
			  (select count(*) from (select distinct service, route from r where r.tenant_id = t.id and route <> '') x)::bigint,
			  (select count(distinct service) from r where r.tenant_id = t.id and source = 'span')::bigint,
			  coalesce((select bool_or(lossy) from r where r.tenant_id = t.id), false)
			from cp.tenants t where not t.is_internal`, first, f.today)
		type want struct {
			req, r4, r5             int64
			p50, p95, p99           *float64
			services, routes, spans int64
			lossy                   bool
		}
		wants := map[uuid.UUID]want{}
		for rows.Next() {
			var id uuid.UUID
			var w want
			require.NoError(t, rows.Scan(&id, &w.req, &w.r4, &w.r5, &w.p50, &w.p95, &w.p99, &w.services, &w.routes, &w.spans, &w.lossy))
			wants[id] = w
		}
		require.NoError(t, rows.Err())
		by := map[string]APIProject{}
		for _, p := range all {
			by[p.Tenant.Name] = p
			w := wants[p.Tenant.ID]
			require.Equal(t, [3]int64{w.req, w.r4, w.r5}, [3]int64{p.Requests, p.Requests4xx, p.Requests5xx}, p.Tenant.Name)
			require.Equal(t, ratio(w.r4, w.req), p.Rate4xx, p.Tenant.Name)
			require.Equal(t, ratio(w.r5, w.req), p.Rate5xx, p.Tenant.Name)
			require.Equal(t, newPercentiles(w.p50, w.p95, w.p99), p.Percentiles, p.Tenant.Name)
			require.Equal(t, [3]int64{w.services, w.routes, w.spans}, [3]int64{p.Services, p.Routes, p.SpanServices}, p.Tenant.Name)
			require.Equal(t, w.lossy, p.Lossy, p.Tenant.Name)
			require.Equal(t, w.req > 0, p.HasAPIData, p.Tenant.Name)
		}
		require.Equal(t, "metric", *by["Aurora"].Source)
		require.Equal(t, "span", *by["Borealis"].Source)
		require.Equal(t, "mixed", *by["Comet"].Source)
		require.Nil(t, by["Dune"].Source)
		require.Nil(t, by["Dune"].P50Seconds)
		require.False(t, by["Dune"].P99Over10s)
		require.True(t, by["Flare"].P99Over10s, "a percentile in the open bucket is reported as over 10 s")
		require.Nil(t, by["Flare"].P99Seconds)
	})

	t.Run("every sort column in both orders", func(t *testing.T) {
		for sort := range apiSorts {
			for _, order := range []string{"asc", "desc"} {
				items, _ := collect[APIProject](t, f, "/api/operator/api/projects?"+rq+"&sort="+sort+"&order="+order)
				require.Len(t, items, 6)
				checkOrder(t, items, order == "desc", func(p APIProject) sortVal { return apiSortValue(p, sort) }, apiID, sort+" "+order)
			}
		}
		// lowest and highest usage
		code, body := f.get(t, "/api/operator/api/projects?"+rq+"&sort=requests&order=desc&has_api_data=true&limit=1")
		require.Equal(t, http.StatusOK, code)
		top := decode[GlobalList[APIProject]](t, body).Items[0]
		code, body = f.get(t, "/api/operator/api/projects?"+rq+"&sort=requests&order=asc&has_api_data=true&limit=1")
		require.Equal(t, http.StatusOK, code)
		bottom := decode[GlobalList[APIProject]](t, body).Items[0]
		for _, p := range all {
			if p.HasAPIData {
				require.LessOrEqual(t, p.Requests, top.Requests)
				require.GreaterOrEqual(t, p.Requests, bottom.Requests)
			}
		}
	})

	t.Run("filters", func(t *testing.T) {
		items, _ := collect[APIProject](t, f, "/api/operator/api/projects?"+rq+"&sort=name&order=asc&has_api_data=true&stellar_toml=false")
		var got []string
		for _, p := range items {
			got = append(got, p.Tenant.Name)
		}
		require.Equal(t, []string{"Borealis", "Comet"}, got)
	})
}

func TestGlobalCompare(t *testing.T) {
	f := newGlobalFixture(t)
	rq := f.rangeQuery()
	first := f.today.AddDate(0, 0, -(fixtureDays - 1))
	ids := []uuid.UUID{f.tenants["flare"], f.tenants["aurora"], f.tenants["dune"]}
	tq := "&tenants=" + ids[0].String() + "," + ids[1].String() + "," + ids[2].String()

	t.Run("web", func(t *testing.T) {
		code, body := f.get(t, "/api/operator/web/compare?"+rq+tq+"&breakdown_limit=3")
		require.Equal(t, http.StatusOK, code, string(body))
		c := decode[WebCompare](t, body)
		require.Len(t, c.Days, fixtureDays)
		require.Equal(t, first.Format(time.DateOnly), c.Days[0])
		require.Len(t, c.Projects, 3)
		for i, p := range c.Projects {
			require.Equal(t, ids[i], p.Tenant.ID, "projects come in the order asked")
			require.Len(t, p.Series, fixtureDays)
			for j, pt := range p.Series {
				require.Equal(t, c.Days[j], pt.Day, "series are aligned on days")
			}
			for _, d := range webDims {
				require.Contains(t, p.Breakdowns, d)
				require.LessOrEqual(t, len(p.Breakdowns[d].Items), 3)
			}
		}
		// independent sql per day, and the aurora day without rows
		aurora := c.Projects[1]
		for _, pt := range aurora.Series {
			var pv, vis, visits, bounces, tt, qpv int64
			var n int
			rows := f.superQuery(t, `select count(*)::int,
				coalesce(sum(pageviews) filter (where band = 'primary'), 0)::bigint, coalesce(sum(visitors) filter (where band = 'primary'), 0)::bigint,
				coalesce(sum(visits) filter (where band = 'primary'), 0)::bigint, coalesce(sum(bounces) filter (where band = 'primary'), 0)::bigint,
				coalesce(sum(totaltime) filter (where band = 'primary'), 0)::bigint, coalesce(sum(pageviews) filter (where band = 'quarantine'), 0)::bigint
				from agg.web_daily where tenant_id = $1 and day = $2::date`, ids[1], pt.Day)
			require.True(t, rows.Next())
			require.NoError(t, rows.Scan(&n, &pv, &vis, &visits, &bounces, &tt, &qpv))
			rows.Close()
			require.Equal(t, n > 0, pt.Aggregated, pt.Day)
			if n == 0 {
				require.Nil(t, pt.Pageviews)
				continue
			}
			require.Equal(t, [6]int64{pv, vis, visits, bounces, tt, qpv},
				[6]int64{*pt.Pageviews, *pt.Visitors, *pt.Visits, *pt.Bounces, *pt.Totaltime, *pt.QuarantinePageviews}, pt.Day)
			require.Equal(t, ratio(bounces, visits), pt.BounceRate)
			require.Equal(t, ratio(tt, visits), pt.AvgVisitDuration)
		}
		// totals equal the list row of the same range
		code, body = f.get(t, "/api/operator/web/projects?"+rq+"&q=aurora")
		require.Equal(t, http.StatusOK, code)
		row := decode[GlobalList[WebProject]](t, body).Items[0]
		require.Equal(t, WebTotals{
			Pageviews: row.Pageviews, Visitors: row.Visitors, Visits: row.Visits, Bounces: row.Bounces, Totaltime: row.Totaltime,
			BounceRate: row.BounceRate, AvgVisitDuration: row.AvgVisitDuration, QuarantinePageviews: row.QuarantinePageviews,
			QuarantineVisitors: row.QuarantineVisitors, QuarantineShare: row.QuarantineShare,
		}, aurora.Totals)
		require.Equal(t, row.Top["country"][:3], aurora.Breakdowns["country"].Items)
		var keys int64
		rows := f.superQuery(t, `select count(distinct key) from agg.web_daily_dim where tenant_id = $1 and band = 'primary'
			and dimension = 'os' and day >= $2 and day <= $3`, ids[1], first, f.today)
		require.True(t, rows.Next())
		require.NoError(t, rows.Scan(&keys))
		rows.Close()
		require.Equal(t, keys, aurora.Breakdowns["os"].TotalKeys)
		dune := c.Projects[2]
		require.Equal(t, WebTotals{}, dune.Totals)
		require.Empty(t, dune.Breakdowns["referrer"].Items)
		for _, pt := range dune.Series {
			require.False(t, pt.Aggregated)
		}
	})

	t.Run("api", func(t *testing.T) {
		code, body := f.get(t, "/api/operator/api/compare?"+rq+tq)
		require.Equal(t, http.StatusOK, code, string(body))
		c := decode[APICompare](t, body)
		require.Len(t, c.Projects, 3)
		code, body = f.get(t, "/api/operator/api/projects?"+rq+"&sort=name&order=asc")
		require.Equal(t, http.StatusOK, code)
		list := map[uuid.UUID]APIProject{}
		for _, p := range decode[GlobalList[APIProject]](t, body).Items {
			list[p.Tenant.ID] = p
		}
		for i, p := range c.Projects {
			require.Equal(t, ids[i], p.Tenant.ID)
			l := list[p.Tenant.ID]
			require.Equal(t, APITotals{APINumbers: l.APINumbers, Services: l.Services, Routes: l.Routes,
				SpanServices: l.SpanServices, Source: l.Source, Lossy: l.Lossy}, p.Totals)
			require.Len(t, p.Series, fixtureDays)
			var sum int64
			for j, pt := range p.Series {
				require.Equal(t, c.Days[j], pt.Day)
				sum += pt.Requests
			}
			require.Equal(t, p.Totals.Requests, sum, "the daily series adds up to the total")
			var svcSum int64
			for j, s := range p.Services {
				svcSum += s.Requests
				if j > 0 {
					require.GreaterOrEqual(t, p.Services[j-1].Requests, s.Requests, "services by requests, highest first")
				}
			}
			require.Equal(t, p.Totals.Requests, svcSum)
		}
		require.Len(t, c.Projects[2].Services, 0)
		require.Equal(t, int64(0), c.Projects[2].Totals.Requests)
		// ascending puts the lowest usage service first
		code, body = f.get(t, "/api/operator/api/compare?"+rq+"&tenants="+ids[1].String()+"&service_order=asc")
		require.Equal(t, http.StatusOK, code)
		asc := decode[APICompare](t, body).Projects[0].Services
		for j := 1; j < len(asc); j++ {
			require.LessOrEqual(t, asc[j-1].Requests, asc[j].Requests)
		}
		// the comet api service has metric and span rows and uses metric
		code, body = f.get(t, "/api/operator/api/compare?"+rq+"&tenants="+f.tenants["comet"].String())
		require.Equal(t, http.StatusOK, code)
		for _, s := range decode[APICompare](t, body).Projects[0].Services {
			if s.Service == "api" {
				require.Equal(t, "metric", s.Source)
			} else {
				require.Equal(t, "span", s.Source)
			}
		}
	})

	t.Run("bad requests", func(t *testing.T) {
		six := make([]string, 6)
		for i := range six {
			six[i] = uuid.NewString()
		}
		for _, path := range []string{"/api/operator/web/compare", "/api/operator/api/compare"} {
			for q, status := range map[string]int{
				"":                                  http.StatusBadRequest,
				"tenants=":                          http.StatusBadRequest,
				"tenants=" + strings.Join(six, ","): http.StatusBadRequest,
				"tenants=" + ids[0].String() + "," + ids[0].String(): http.StatusBadRequest,
				"tenants=NOT-A-UUID":                     http.StatusBadRequest,
				"tenants=" + uuid.NewString():            http.StatusNotFound,
				"tenants=" + f.tenants["probe"].String(): http.StatusNotFound,
			} {
				code, body := f.get(t, path+"?"+q)
				require.Equal(t, status, code, "%s?%s: %s", path, q, body)
			}
		}
		code, _ := f.get(t, "/api/operator/web/compare?tenants="+ids[0].String()+"&breakdown_limit=501")
		require.Equal(t, http.StatusBadRequest, code)
		code, _ = f.get(t, "/api/operator/api/compare?tenants="+ids[0].String()+"&service_order=up")
		require.Equal(t, http.StatusBadRequest, code)
	})
}
