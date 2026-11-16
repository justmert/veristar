package apimetrics_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/apimetrics"
	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// the expectations below are computed by sql written here, as superuser
// (which row-level security does not apply to) with the tenant named
// explicitly, the table picked from the answer's resolution and the
// source rule (metric rows win over span rows) written out again. nothing of the package under
// test is used except its json types.

func table(t *testing.T, res apimetrics.Resolution) string {
	t.Helper()
	switch res {
	case apimetrics.Minute:
		return "api.metrics_minute"
	case apimetrics.Hour:
		return "api.metrics_hourly"
	case apimetrics.Day:
		return "api.metrics_daily"
	}
	t.Fatalf("resolution %q", res)
	return ""
}

// selected is the rows of the range after the source rule and filters:
// r all rows of the tenant in the range, chosen the source per service,
// s the rows an answer may read. parameters: $1 tenant, $2 from, $3 to,
// $4 service, $5 route, $6 method.
const selected = `with r as (
  select * from %s where tenant_id = $1 and bucket_start >= $2 and bucket_start < $3
), chosen as (
  select service,
         case when bool_or(source = 'metric') then 'metric' else 'span' end as src,
         bool_or(source = 'metric') and bool_or(source = 'span') as ignored
  from r
  where $4::text is null or service = $4::text
  group by service
), s as (
  select r.* from r join chosen c on c.service = r.service and c.src = r.source
  where ($5::text is null or r.route = $5::text) and ($6::text is null or r.method = $6::text)
)
`

type query struct {
	tenant                 uuid.UUID
	rng                    apimetrics.RangeJSON
	service, route, method *string
}

func (q query) args() []any {
	return []any{q.tenant, time.Time(q.rng.From), time.Time(q.rng.To), q.service, q.route, q.method}
}

func pct(v *float64) apimetrics.Percentile {
	switch {
	case v == nil:
		return apimetrics.Percentile{}
	case math.IsInf(*v, 1):
		return apimetrics.Percentile{Valid: true, Open: true}
	default:
		return apimetrics.Percentile{Valid: true, Seconds: *v}
	}
}

// the summary columns over s, rates divided in sql. application rows are
// picked with case expressions rather than the filter clauses the queries
// under test use, then the infrastructure requests come last.
const summaryCols = `coalesce(sum(case when traffic_class = 'app' then count end), 0)::bigint as requests,
  coalesce(sum(case when traffic_class = 'app' and status_code / 100 = 4 then count end), 0)::bigint as e4,
  coalesce(sum(case when traffic_class = 'app' and status_code / 100 = 5 then count end), 0)::bigint as e5,
  coalesce(sum(case when traffic_class = 'app' and status_code / 100 = 4 then count end), 0)::float8
    / nullif(sum(case when traffic_class = 'app' then count end), 0)::float8 as r4,
  coalesce(sum(case when traffic_class = 'app' and status_code / 100 = 5 then count end), 0)::float8
    / nullif(sum(case when traffic_class = 'app' then count end), 0)::float8 as r5,
  api.histogram_quantile(0.5, api.sum_buckets(case when traffic_class = 'app' then counts end)) as p50,
  api.histogram_quantile(0.95, api.sum_buckets(case when traffic_class = 'app' then counts end)) as p95,
  api.histogram_quantile(0.99, api.sum_buckets(case when traffic_class = 'app' then counts end)) as p99,
  coalesce(bool_or(case when traffic_class = 'app' then lossy end), false) as lossy,
  coalesce(sum(case when traffic_class <> 'app' then count end), 0)::bigint as infra`

func scanSummary(row pgx.Row, extra ...any) (apimetrics.Summary, error) {
	var s apimetrics.Summary
	var p50, p95, p99 *float64
	dest := append(extra, &s.Requests, &s.Errors4xx, &s.Errors5xx, &s.Rate4xx, &s.Rate5xx, &p50, &p95, &p99, &s.Lossy,
		&s.InfrastructureRequests)
	if err := row.Scan(dest...); err != nil {
		return s, err
	}
	s.P50, s.P95, s.P99 = pct(p50), pct(p95), pct(p99)
	return s, nil
}

func (e *env) expSources(t *testing.T, q query) []apimetrics.Source {
	t.Helper()
	rows, err := e.su.Query(t.Context(), fmt.Sprintf(selected, table(t, q.rng.Resolution))+
		`select service, src, ignored from chosen order by service`, q.args()...)
	require.NoError(t, err)
	out := []apimetrics.Source{}
	for rows.Next() {
		var s apimetrics.Source
		require.NoError(t, rows.Scan(&s.Service, &s.Source, &s.SpanDataIgnored))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return out
}

func (e *env) expSummary(t *testing.T, q query) apimetrics.Summary {
	t.Helper()
	s, err := scanSummary(e.su.QueryRow(t.Context(),
		fmt.Sprintf(selected, table(t, q.rng.Resolution))+`select `+summaryCols+` from s`, q.args()...))
	require.NoError(t, err)
	return s
}

func step(res apimetrics.Resolution) string {
	return map[apimetrics.Resolution]string{
		apimetrics.Minute: "1 minute", apimetrics.Hour: "1 hour", apimetrics.Day: "1 day",
	}[res]
}

func (e *env) expTimeseries(t *testing.T, q query) []apimetrics.Point {
	t.Helper()
	sql := fmt.Sprintf(selected, table(t, q.rng.Resolution)) + `
select g.at, ` + summaryCols + `
from generate_series($2::timestamptz, $3::timestamptz - interval '` + step(q.rng.Resolution) + `', interval '` +
		step(q.rng.Resolution) + `') g(at)
left join s on s.bucket_start = g.at
group by g.at order by g.at`
	rows, err := e.su.Query(t.Context(), sql, q.args()...)
	require.NoError(t, err)
	out := []apimetrics.Point{}
	for rows.Next() {
		var at time.Time
		s, err := scanSummary(rows, &at)
		require.NoError(t, err)
		out = append(out, apimetrics.Point{At: httpx.Timestamp(at.UTC()), Summary: s})
	}
	require.NoError(t, rows.Err())
	return out
}

// expRoutes is the full route list in the order given by orderBy, an
// order by clause over the columns of g
func (e *env) expRoutes(t *testing.T, q query, orderBy string) []apimetrics.RouteItem {
	t.Helper()
	sql := fmt.Sprintf(selected, table(t, q.rng.Resolution)) + `
, g as (
  select service, method, route, min(source) as source, ` + summaryCols + `
  from s group by service, method, route
)
select * from g order by ` + orderBy
	rows, err := e.su.Query(t.Context(), sql, q.args()...)
	require.NoError(t, err)
	out := []apimetrics.RouteItem{}
	for rows.Next() {
		var it apimetrics.RouteItem
		s, err := scanSummary(rows, &it.Service, &it.Method, &it.Route, &it.Source)
		require.NoError(t, err)
		it.Summary = s
		out = append(out, it)
	}
	require.NoError(t, rows.Err())
	return out
}

func (e *env) expStatus(t *testing.T, q query) (int64, []apimetrics.ClassCount, []apimetrics.CodeCount) {
	t.Helper()
	tbl := table(t, q.rng.Resolution)
	cls := `case when status_code between 100 and 199 then '1xx' when status_code between 200 and 299 then '2xx'
  when status_code between 300 and 399 then '3xx' when status_code between 400 and 499 then '4xx'
  when status_code between 500 and 599 then '5xx' else 'unknown' end`
	var total int64
	require.NoError(t, e.su.QueryRow(t.Context(), fmt.Sprintf(selected, tbl)+`select coalesce(sum(count), 0)::bigint from s
where traffic_class = 'app'`, q.args()...).Scan(&total))
	rows, err := e.su.Query(t.Context(), fmt.Sprintf(selected, tbl)+`
select k.class, coalesce(sum(s.count), 0)::bigint
from unnest(array['1xx', '2xx', '3xx', '4xx', '5xx', 'unknown']) with ordinality k(class, n)
left join s on `+cls+` = k.class and s.traffic_class = 'app'
group by k.class, k.n order by k.n`, q.args()...)
	require.NoError(t, err)
	classes := []apimetrics.ClassCount{}
	for rows.Next() {
		var c apimetrics.ClassCount
		require.NoError(t, rows.Scan(&c.Class, &c.Requests))
		classes = append(classes, c)
	}
	require.NoError(t, rows.Err())
	rows, err = e.su.Query(t.Context(), fmt.Sprintf(selected, tbl)+`
select status_code, `+cls+`, sum(count)::bigint from s where traffic_class = 'app'
group by status_code having sum(count) > 0 order by status_code`, q.args()...)
	require.NoError(t, err)
	codes := []apimetrics.CodeCount{}
	for rows.Next() {
		var c apimetrics.CodeCount
		require.NoError(t, rows.Scan(&c.StatusCode, &c.Class, &c.Requests))
		codes = append(codes, c)
	}
	require.NoError(t, rows.Err())
	return total, classes, codes
}

func (e *env) expServices(t *testing.T, q query) []apimetrics.ServiceItem {
	t.Helper()
	rows, err := e.su.Query(t.Context(), fmt.Sprintf(selected, table(t, q.rng.Resolution))+`
select c.service, c.src, c.ignored,
       coalesce(sum(case when s.traffic_class = 'app' then s.count end), 0)::bigint,
       coalesce(sum(case when s.traffic_class <> 'app' then s.count end), 0)::bigint,
       coalesce(bool_or(case when s.traffic_class = 'app' then s.lossy end), false)
from chosen c join s on s.service = c.service
group by c.service, c.src, c.ignored order by c.service`, q.args()...)
	require.NoError(t, err)
	out := []apimetrics.ServiceItem{}
	for rows.Next() {
		var it apimetrics.ServiceItem
		require.NoError(t, rows.Scan(&it.Service, &it.Source.Source, &it.SpanDataIgnored, &it.Requests,
			&it.InfrastructureRequests, &it.Lossy))
		out = append(out, it)
	}
	require.NoError(t, rows.Err())
	return out
}

func (e *env) expWarnings(t *testing.T, q query) ([]apimetrics.WarningTotal, []apimetrics.WarningDay) {
	t.Helper()
	days := `select day, kind, count from api.ingest_warnings
where tenant_id = $1
  and day >= ($2::timestamptz at time zone 'UTC')::date
  and day <= (($3::timestamptz - interval '1 microsecond') at time zone 'UTC')::date`
	from, to := time.Time(q.rng.From), time.Time(q.rng.To)
	rows, err := e.su.Query(t.Context(), `
with d as (`+days+`)
select k.kind, coalesce(sum(d.count), 0)::bigint, to_char(max(d.day), 'YYYY-MM-DD')
from unnest(array['span_derived', 'lossy_histogram', 'cardinality_overflow', 'secret_in_path', 'coarse_buckets']) with ordinality k(kind, n)
left join d on d.kind = k.kind
group by k.kind, k.n order by k.n`, q.tenant, from, to)
	require.NoError(t, err)
	totals := []apimetrics.WarningTotal{}
	for rows.Next() {
		var w apimetrics.WarningTotal
		require.NoError(t, rows.Scan(&w.Kind, &w.Count, &w.LastDay))
		totals = append(totals, w)
	}
	require.NoError(t, rows.Err())
	rows, err = e.su.Query(t.Context(), `select to_char(day, 'YYYY-MM-DD'), kind, count from (`+days+`) d order by day, kind`,
		q.tenant, from, to)
	require.NoError(t, err)
	list := []apimetrics.WarningDay{}
	for rows.Next() {
		var w apimetrics.WarningDay
		require.NoError(t, rows.Scan(&w.Day, &w.Kind, &w.Count))
		list = append(list, w)
	}
	require.NoError(t, rows.Err())
	return totals, list
}

// expMinuteBuckets sums the minute rows of q's range per bucket of res,
// with the source rule applied, for the buckets that have requests
func (e *env) expMinuteBuckets(t *testing.T, q query, res apimetrics.Resolution) map[time.Time]apimetrics.Summary {
	t.Helper()
	unit := map[apimetrics.Resolution]string{apimetrics.Hour: "hour", apimetrics.Day: "day"}[res]
	require.NotEmpty(t, unit)
	rows, err := e.su.Query(t.Context(), fmt.Sprintf(selected, "api.metrics_minute")+`
select date_trunc('`+unit+`', bucket_start, 'UTC'), `+summaryCols+`
from s group by 1 having sum(count) > 0`, q.args()...)
	require.NoError(t, err)
	out := map[time.Time]apimetrics.Summary{}
	for rows.Next() {
		var at time.Time
		s, err := scanSummary(rows, &at)
		require.NoError(t, err)
		out[at.UTC()] = s
	}
	require.NoError(t, rows.Err())
	return out
}
