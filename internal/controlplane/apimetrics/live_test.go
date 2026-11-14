package apimetrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/apimetrics"
	"github.com/justmert/veristar/internal/db/tenantq"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(s string) *string { return &s }

var paramRe = regexp.MustCompile(`@([a-z_]+)|sqlc\.narg\(([a-z_]+)\)`)

// namedQuery reads one query from db/queries/tenant/apimetrics.sql and
// numbers its parameters by first appearance, as sqlc does, so a test can
// explain the exact statement the generated code sends
func namedQuery(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "queries", "tenant", "apimetrics.sql"))
	require.NoError(t, err)
	_, rest, ok := strings.Cut(string(b), "-- name: "+name+" ")
	require.True(t, ok, name)
	body, _, _ := strings.Cut(rest, "-- name: ")
	_, body, _ = strings.Cut(body, "\n")
	nums := map[string]int{}
	return strings.TrimSuffix(strings.TrimSpace(paramRe.ReplaceAllStringFunc(body, func(m string) string {
		sm := paramRe.FindStringSubmatch(m)
		n := sm[1] + sm[2]
		if _, ok := nums[n]; !ok {
			nums[n] = len(nums) + 1
		}
		return "$" + strconv.Itoa(nums[n])
	})), ";")
}

const (
	svcBoth   = "svc-both"   // metric and span rows: only metric is read
	svcSpans  = "svc-spans"  // span rows only
	svcSwitch = "svc-switch" // spans first, metrics later in the same range
	accounts  = "/v1/accounts/{id}"
	quoted    = "/v1/o'brien/{id}"
)

// fixture is tenant a's data around now plus backdated rows over 24
// months and one point in 2027, beyond the hourly horizon; tenant b has
// rows at the same times; tenant c holds the percentile cases.
func fixture(a, b, c uuid.UUID) ([]point, []warning) {
	ps := []point{
		{a, at("2031-06-15T09:30:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{2: 50, 3: 10}), false},
		{a, at("2031-06-15T09:45:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{1: 40, 2: 20, 4: 5}), false},
		{a, at("2031-06-15T09:45:00Z"), svcBoth, "GET", accounts, 200, "span", h(map[int]int64{1: 7}), false},
		{a, at("2031-06-15T10:00:00Z"), svcBoth, "GET", accounts, 404, "metric", h(map[int]int64{0: 12}), false},
		{a, at("2031-06-15T10:00:00Z"), svcBoth, "POST", "/v1/tx", 201, "metric", h(map[int]int64{5: 8, 6: 2}), true},
		{a, at("2031-06-15T10:30:00Z"), svcBoth, "POST", "/v1/tx", 503, "metric", h(map[int]int64{14: 3, 9: 1}), false},
		{a, at("2031-06-15T10:30:00Z"), svcBoth, "GET", quoted, 500, "metric", h(map[int]int64{3: 2}), false},
		{a, at("2031-06-15T10:15:00Z"), svcSpans, "GET", "/health", 200, "span", h(map[int]int64{0: 30}), false},
		{a, at("2031-06-15T10:15:00Z"), svcSpans, "GET", "/health", 0, "span", h(map[int]int64{0: 4}), false},
		{a, at("2031-06-15T09:40:00Z"), svcSwitch, "GET", "/v2/items", 200, "span", h(map[int]int64{2: 9}), false},
		{a, at("2031-06-15T10:20:00Z"), svcSwitch, "GET", "/v2/items", 200, "metric", h(map[int]int64{2: 11, 7: 1}), false},
		{a, at("2031-06-15T10:20:00Z"), svcSwitch, "DELETE", "/v2/items/{id}", 429, "metric", h(map[int]int64{1: 3}), false},
		// the preceding day, for the overview comparison of 24h
		{a, at("2031-06-14T05:00:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{2: 17}), false},
		{a, at("2031-06-14T05:00:00Z"), svcSpans, "GET", "/health", 200, "span", h(map[int]int64{0: 6}), false},
		// inside 30 days
		{a, at("2031-06-01T12:07:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{2: 100}), false},
		{a, at("2031-05-20T03:00:00Z"), svcSpans, "GET", "/health", 200, "span", h(map[int]int64{0: 20}), false},
		// 40 days back: before the minute horizon of 2031-05-16
		{a, at("2031-05-06T10:10:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{3: 6}), false},
		// before the hourly horizon of 2028-06-01
		{a, at("2027-03-02T12:00:00Z"), svcBoth, "GET", accounts, 500, "metric", h(map[int]int64{4: 5}), false},
		{a, at("2027-03-02T13:00:00Z"), svcSpans, "GET", "/health", 200, "span", h(map[int]int64{0: 2}), false},
		// the day before the 24mo range: must not be counted there
		{a, at("2029-06-15T06:00:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{2: 1000}), false},

		{b, at("2031-06-15T10:00:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{2: 999}), false},
		{b, at("2031-06-01T12:07:00Z"), svcBoth, "GET", accounts, 200, "metric", h(map[int]int64{2: 999}), false},
		{b, at("2031-06-15T10:00:00Z"), "svc-b-only", "GET", "/b", 200, "span", h(map[int]int64{2: 5}), false},

		// p50 of the summed buckets is near 2.5 ms; the mean of the two
		// minutes' p50s is near 1.9 s
		{c, at("2031-06-15T10:10:00Z"), "lat", "GET", "/p", 200, "metric", h(map[int]int64{0: 99}), false},
		{c, at("2031-06-15T10:11:00Z"), "lat", "GET", "/p", 200, "metric", h(map[int]int64{11: 1}), false},
		// p99 in the open bucket, p50 a number
		{c, at("2031-06-15T10:12:00Z"), "lat", "GET", "/slow", 200, "metric", h(map[int]int64{0: 95, 14: 5}), false},
	}
	// one point a month from the first day of the 24mo range on
	for m := range 24 {
		ps = append(ps, point{a, at("2029-06-16T06:00:00Z").AddDate(0, m, 0), svcBoth, "GET", accounts, 200, "metric",
			h(map[int]int64{2: int64(m + 1)}), false})
	}
	ws := []warning{
		{a, at("2031-06-15T00:00:00Z"), "secret_in_path", 1},
		{a, at("2031-06-14T00:00:00Z"), "lossy_histogram", 4},
		{a, at("2031-06-01T00:00:00Z"), "span_derived", 30},
		{a, at("2031-05-01T00:00:00Z"), "cardinality_overflow", 2},
		{b, at("2031-06-15T00:00:00Z"), "secret_in_path", 50},
	}
	return ps, ws
}

// every range of the comparison: each preset and the three custom cases
var ranges = []string{
	"range=1h", "range=24h", "range=7d", "range=30d", "range=90d", "range=12mo", "range=24mo",
	"range=custom&from=2031-06-15T08:10:30Z&to=2031-06-15T10:05:00Z",
	"range=custom&from=2031-05-06T10:00:00Z&to=2031-05-06T11:00:00Z",
	"range=custom&from=2027-03-01T05:00:00Z&to=2027-03-03T05:00:00Z",
}

func filterSets() []struct {
	qs                     string
	service, route, method *string
} {
	return []struct {
		qs                     string
		service, route, method *string
	}{
		{qs: ""},
		{qs: "&service=" + svcBoth, service: ptr(svcBoth)},
		{qs: "&route=" + url.QueryEscape(accounts), route: ptr(accounts)},
		{qs: "&method=POST", method: ptr("POST")},
		{qs: "&service=" + svcSwitch + "&method=GET", service: ptr(svcSwitch), method: ptr("GET")},
		{qs: "&route=" + url.QueryEscape(quoted), route: ptr(quoted)},
	}
}

func TestAPIMetrics(t *testing.T) {
	e := start(t)
	userA, tenantA := e.member(t, "active")
	userB, tenantB := e.member(t, "suspended")
	userC, tenantC := e.member(t, "pending")
	ps, ws := fixture(tenantA, tenantB, tenantC)
	e.write(t, ps, ws)
	ca, cb, cc := e.client(t, userA), e.client(t, userB), e.client(t, userC)
	pathA := "/tenants/" + tenantA.String() + "/api-metrics/"

	t.Run("presets resolve to the documented range and resolution", func(t *testing.T) {
		cases := []struct {
			qs, preset, from, to string
			res                  apimetrics.Resolution
			note                 *string
		}{
			{"range=1h", "1h", "2031-06-15T09:31:00Z", "2031-06-15T10:31:00Z", apimetrics.Minute, nil},
			{"", "24h", "2031-06-14T11:00:00Z", "2031-06-15T11:00:00Z", apimetrics.Hour, nil},
			{"range=24h", "24h", "2031-06-14T11:00:00Z", "2031-06-15T11:00:00Z", apimetrics.Hour, nil},
			{"range=7d", "7d", "2031-06-08T11:00:00Z", "2031-06-15T11:00:00Z", apimetrics.Hour, nil},
			{"range=30d", "30d", "2031-05-16T11:00:00Z", "2031-06-15T11:00:00Z", apimetrics.Hour, nil},
			{"range=90d", "90d", "2031-03-18T00:00:00Z", "2031-06-16T00:00:00Z", apimetrics.Day, nil},
			{"range=12mo", "12mo", "2030-06-16T00:00:00Z", "2031-06-16T00:00:00Z", apimetrics.Day, nil},
			{"range=24mo", "24mo", "2029-06-16T00:00:00Z", "2031-06-16T00:00:00Z", apimetrics.Day, nil},
			{"range=custom&from=2031-06-15T08:10:30Z&to=2031-06-15T10:05:00Z", "custom",
				"2031-06-15T08:10:00Z", "2031-06-15T10:05:00Z", apimetrics.Minute, nil},
			{"range=custom&from=2031-06-15T04:00:00%2B02:00&to=2031-06-15T08:00:00Z", "custom",
				"2031-06-15T02:00:00Z", "2031-06-15T08:00:00Z", apimetrics.Minute, nil},
			{"range=custom&from=2031-06-15T02:00:00Z&to=2031-06-15T08:00:01Z", "custom",
				"2031-06-15T02:00:00Z", "2031-06-15T09:00:00Z", apimetrics.Hour, nil},
			{"range=custom&from=2031-05-01T00:00:00Z&to=2031-06-01T00:00:00Z", "custom",
				"2031-05-01T00:00:00Z", "2031-06-01T00:00:00Z", apimetrics.Hour, nil},
			{"range=custom&from=2031-05-01T00:00:00Z&to=2031-06-01T00:00:01Z", "custom",
				"2031-05-01T00:00:00Z", "2031-06-02T00:00:00Z", apimetrics.Day, nil},
			{"range=custom&from=2031-05-06T10:00:00Z&to=2031-05-06T11:00:00Z", "custom",
				"2031-05-06T10:00:00Z", "2031-05-06T11:00:00Z", apimetrics.Hour, ptr(apimetrics.NoteOlderThanMinuteRetention)},
			{"range=custom&from=2027-03-01T05:00:00Z&to=2027-03-03T05:00:00Z", "custom",
				"2027-03-01T00:00:00Z", "2027-03-04T00:00:00Z", apimetrics.Day, ptr(apimetrics.NoteOlderThanHourlyRetention)},
		}
		for _, tc := range cases {
			var ov apimetrics.Overview
			ca.ok(t, pathA+"overview?"+tc.qs, &ov)
			r := ov.Current.Range
			require.Equal(t, tc.preset, r.Preset, tc.qs)
			require.Equal(t, at(tc.from), time.Time(r.From), tc.qs)
			require.Equal(t, at(tc.to), time.Time(r.To), tc.qs)
			require.Equal(t, tc.res, r.Resolution, tc.qs)
			require.Equal(t, tc.res.BucketSeconds(), r.BucketSeconds, tc.qs)
			require.Equal(t, tc.note, r.ResolutionNote, tc.qs)
			// the comparison range sits right before, with the same length
			p := ov.Previous.Range
			require.Equal(t, "previous", p.Preset)
			require.Equal(t, time.Time(r.From), time.Time(p.To), tc.qs)
			require.Equal(t, time.Time(r.To).Sub(time.Time(r.From)), time.Time(p.To).Sub(time.Time(p.From)), tc.qs)
		}
		// the documented bucket counts of a timeseries
		for qs, n := range map[string]int{
			"range=1h": 60, "range=24h": 24, "range=7d": 168, "range=30d": 720, "range=90d": 90,
			"range=12mo": 365, "range=24mo": 730,
		} {
			var ts apimetrics.Timeseries
			ca.ok(t, pathA+"timeseries?"+qs, &ts)
			require.Len(t, ts.Points, n, qs)
		}
	})

	t.Run("every endpoint equals independent sql", func(t *testing.T) {
		for _, rq := range ranges {
			for _, fs := range filterSets() {
				qs := rq + fs.qs
				var ov apimetrics.Overview
				ca.ok(t, pathA+"overview?"+qs, &ov)
				for _, p := range []apimetrics.Period{ov.Current, ov.Previous} {
					q := query{tenant: tenantA, rng: p.Range, service: fs.service, route: fs.route, method: fs.method}
					require.Equal(t, e.expSources(t, q), p.Sources, qs)
					require.Equal(t, e.expSummary(t, q), p.Summary, qs)
				}
				q := query{tenant: tenantA, rng: ov.Current.Range, service: fs.service, route: fs.route, method: fs.method}

				var ts apimetrics.Timeseries
				ca.ok(t, pathA+"timeseries?"+qs, &ts)
				require.Equal(t, ov.Current.Range, ts.Range)
				require.Equal(t, e.expSources(t, q), ts.Sources, qs)
				require.Equal(t, e.expTimeseries(t, q), ts.Points, qs)

				var st apimetrics.Status
				ca.ok(t, pathA+"status?"+qs, &st)
				total, classes, codes := e.expStatus(t, q)
				require.Equal(t, e.expSources(t, q), st.Sources, qs)
				require.Equal(t, total, st.Requests, qs)
				require.Equal(t, classes, st.Classes, qs)
				require.Equal(t, codes, st.Codes, qs)

				// every sort, each page by page with a small limit
				for _, srt := range []struct{ qs, order string }{
					{"", "requests desc, service, method, route"},
					{"&sort=p99", "p99 desc nulls last, service, method, route"},
					{"&sort=p50&order=asc", "p50 asc nulls last, service, method, route"},
					{"&sort=route", "service, method, route"},
					{"&sort=route&order=desc", "service desc, method desc, route desc, service, method, route"},
					{"&sort=rate_5xx&order=asc", "r5 asc nulls last, service, method, route"},
					{"&sort=errors_4xx", "e4 desc, service, method, route"},
				} {
					want := e.expRoutes(t, q, srt.order)
					got := []apimetrics.RouteItem{}
					cursor := ""
					for range 50 {
						var page apimetrics.RouteList
						ca.ok(t, pathA+"routes?"+qs+srt.qs+"&limit=2"+cursor, &page)
						require.Equal(t, int64(len(want)), page.Total, qs+srt.qs)
						require.Equal(t, e.expSources(t, q), page.Sources)
						require.LessOrEqual(t, len(page.Items), 2)
						got = append(got, page.Items...)
						if page.NextCursor == nil {
							break
						}
						cursor = "&cursor=" + *page.NextCursor
					}
					require.Equal(t, want, got, qs+srt.qs)
				}
			}
			var sv apimetrics.Services
			ca.ok(t, pathA+"services?"+rq, &sv)
			q := query{tenant: tenantA, rng: sv.Range}
			require.Equal(t, e.expServices(t, q), sv.Items, rq)

			var wn apimetrics.Warnings
			ca.ok(t, pathA+"warnings?"+rq, &wn)
			totals, days := e.expWarnings(t, query{tenant: tenantA, rng: wn.Range})
			require.Equal(t, totals, wn.Totals, rq)
			require.Equal(t, days, wn.Days, rq)
		}
	})

	t.Run("known values", func(t *testing.T) {
		// written by hand from the fixture, so a shared mistake in the
		// answer and the expectation sql cannot pass unseen
		var ov apimetrics.Overview
		ca.ok(t, pathA+"overview?range=1h", &ov)
		// minute rows 09:45 to 10:30, svc-both metric only, svc-spans spans,
		// svc-switch metric only: 65+12+10+4+2 + 34 + 12+3
		require.Equal(t, int64(142), ov.Current.Summary.Requests)
		require.Equal(t, int64(15), ov.Current.Summary.Errors4xx)
		require.Equal(t, int64(6), ov.Current.Summary.Errors5xx)
		require.True(t, ov.Current.Summary.Lossy)

		var wn apimetrics.Warnings
		ca.ok(t, pathA+"warnings?range=24h", &wn)
		require.Equal(t, []apimetrics.WarningDay{
			{Day: "2031-06-14", Kind: "lossy_histogram", Count: 4},
			{Day: "2031-06-15", Kind: "secret_in_path", Count: 1},
		}, wn.Days)
		require.Equal(t, "2031-06-15", *wn.Totals[3].LastDay)
		require.Nil(t, wn.Totals[0].LastDay)
		// the coarse_buckets kind came last, so it is listed last
		require.Len(t, wn.Totals, 5)
		require.Equal(t, "coarse_buckets", wn.Totals[4].Kind)
		require.Zero(t, wn.Totals[4].Count)
	})

	t.Run("source selection at minute, hourly and daily resolution", func(t *testing.T) {
		want := []apimetrics.Source{
			{Service: svcBoth, Source: "metric", SpanDataIgnored: true},
			{Service: svcSpans, Source: "span"},
			{Service: svcSwitch, Source: "metric", SpanDataIgnored: true},
		}
		for qs, res := range map[string]apimetrics.Resolution{
			"range=1h": apimetrics.Minute, "range=24h": apimetrics.Hour, "range=90d": apimetrics.Day,
		} {
			var sv apimetrics.Services
			ca.ok(t, pathA+"services?"+qs, &sv)
			require.Equal(t, res, sv.Range.Resolution)
			got := make([]apimetrics.Source, len(sv.Items))
			for i, it := range sv.Items {
				got[i] = it.Source
			}
			require.Equal(t, want, got, qs)
			// svc-both: metric counts only (65 at 09:45 + 12 + 10 + 4 + 2 in
			// the last hour), never metric plus span (which would add 7)
			if qs == "range=1h" {
				require.Equal(t, int64(93), sv.Items[0].Requests)
				require.Equal(t, int64(34), sv.Items[1].Requests)
				// svc-switch: the 9 span requests at 09:40 are left out
				require.Equal(t, int64(15), sv.Items[2].Requests)
			}
		}
		// a route filter does not change the source of a service: svc-switch
		// stays metric although /v2/items has span rows too
		var ov apimetrics.Overview
		ca.ok(t, pathA+"overview?range=1h&route=%2Fv2%2Fitems", &ov)
		require.Equal(t, int64(12), ov.Current.Summary.Requests)
	})

	t.Run("percentiles come from summed buckets, never averages", func(t *testing.T) {
		pathC := "/tenants/" + tenantC.String() + "/api-metrics/"
		var ov apimetrics.Overview
		cc.ok(t, pathC+"overview?range=1h&route=%2Fp", &ov)
		var summed, m1, m2 float64
		minute := `select counts from api.metrics_minute where tenant_id = $1 and route = '/p' order by bucket_start`
		require.NoError(t, e.su.QueryRow(t.Context(), `select
			(select api.histogram_quantile(0.5::float8, api.sum_buckets(counts))
			 from api.metrics_minute where tenant_id = $1 and route = '/p'),
			api.histogram_quantile(0.5::float8, (`+minute+` limit 1)),
			api.histogram_quantile(0.5::float8, (`+minute+` offset 1 limit 1))`, tenantC).Scan(&summed, &m1, &m2))
		p50 := ov.Current.Summary.P50
		require.True(t, p50.Valid)
		require.False(t, p50.Open)
		require.Equal(t, summed, p50.Seconds)
		require.InDelta(t, 0.005*50.0/99.0, p50.Seconds, 1e-12)
		require.NotEqual(t, (m1+m2)/2, p50.Seconds)
		require.Greater(t, (m1+m2)/2, 1.0, "the mean of minute percentiles is far from the true p50")

		// the timeseries keeps each minute's own percentile
		var ts apimetrics.Timeseries
		cc.ok(t, pathC+"timeseries?range=1h&route=%2Fp", &ts)
		var seen []float64
		for _, p := range ts.Points {
			if p.Requests > 0 {
				seen = append(seen, p.P50.Seconds)
			}
		}
		require.Equal(t, []float64{m1, m2}, seen)
	})

	t.Run("open bucket is reported as >10 s", func(t *testing.T) {
		pathC := "/tenants/" + tenantC.String() + "/api-metrics/"
		st, raw := cc.get(t, pathC+"overview?range=1h&route=%2Fslow")
		require.Equal(t, http.StatusOK, st)
		var doc struct {
			Current struct {
				Summary map[string]json.RawMessage `json:"summary"`
			} `json:"current"`
		}
		require.NoError(t, json.Unmarshal(raw, &doc))
		require.JSONEq(t, `">10 s"`, string(doc.Current.Summary["p99"]))
		// p95 reaches rank 95 exactly at the top of the first bucket
		require.JSONEq(t, `0.005`, string(doc.Current.Summary["p95"]))
		var p50 float64
		require.NoError(t, json.Unmarshal(doc.Current.Summary["p50"], &p50))
		require.InDelta(t, 0.005*50.0/95.0, p50, 1e-12)

		// the routes list sorts it above every number
		var page apimetrics.RouteList
		cc.ok(t, pathC+"routes?range=1h&sort=p99", &page)
		require.Equal(t, "/slow", page.Items[0].Route)
		require.True(t, page.Items[0].P99.Open)
		// and tenant a's 503 route, whose p99 is open too
		ca.ok(t, pathA+"routes?range=1h&sort=p99&limit=1", &page)
		require.Equal(t, "/v1/tx", page.Items[0].Route)
		require.True(t, page.Items[0].P99.Open)
	})

	t.Run("backdated 24 months return daily data", func(t *testing.T) {
		var ts apimetrics.Timeseries
		ca.ok(t, pathA+"timeseries?range=24mo&service="+svcBoth+"&route="+url.QueryEscape(accounts)+"&method=GET", &ts)
		require.Equal(t, apimetrics.Day, ts.Range.Resolution)
		require.Len(t, ts.Points, 730)
		got := map[string]int64{}
		for _, p := range ts.Points {
			if p.Requests > 0 {
				got[time.Time(p.At).Format(time.DateOnly)] = p.Requests
			}
		}
		// one point a month from 2029-06-16 on (m+1 requests in month m);
		// the 2029-06-15 point lies before the range. then the recent days:
		// 05-06 has 6, 06-01 100, 06-14 17, and 06-15 60 + 65 + 12 (every
		// status of the route counts; the 7 span requests do not)
		want := map[string]int64{"2031-05-06": 6, "2031-06-01": 100, "2031-06-14": 17, "2031-06-15": 137}
		for m := range 24 {
			want[at("2029-06-16T00:00:00Z").AddDate(0, m, 0).Format(time.DateOnly)] = int64(m + 1)
		}
		require.Equal(t, want, got)
	})

	t.Run("isolation", func(t *testing.T) {
		// a member of tenant b gets 404 for every route of tenant a
		for _, p := range []string{"overview", "timeseries", "routes", "status", "services", "warnings"} {
			st, code := cb.errCode(t, pathA+p+"?range=24h")
			require.Equal(t, http.StatusNotFound, st, p)
			require.Equal(t, "not_found", code, p)
			// also with an invalid query: membership is checked first
			st, _ = cb.errCode(t, pathA+p+"?range=bogus")
			require.Equal(t, http.StatusNotFound, st, p)
		}
		// tenant b reads only its own rows
		var sv apimetrics.Services
		cb.ok(t, "/tenants/"+tenantB.String()+"/api-metrics/services?range=1h", &sv)
		require.Len(t, sv.Items, 2)
		for _, it := range sv.Items {
			require.Equal(t, map[string]int64{svcBoth: 999, "svc-b-only": 5}[it.Service], it.Requests, it.Service)
		}

		// the query layer under the wrong tenant: row-level security returns
		// nothing of tenant a's, whatever the handler asked for
		rng := apimetrics.Range{From: at("2031-06-15T09:00:00Z"), To: at("2031-06-15T11:00:00Z"), Resolution: apimetrics.Minute}
		wrong := uuid.Must(uuid.NewV7())
		for _, id := range []uuid.UUID{wrong, tenantC} {
			require.NoError(t, e.d.WithTenant(t.Context(), id, func(tx pgx.Tx) error {
				q := tenantq.New(tx)
				// what a handler for tenant a would send, sources included
				sum, err := q.APIMetricSummary(t.Context(), tenantq.APIMetricSummaryParams{
					Resolution: "minute", RangeFrom: rng.From, RangeTo: rng.To,
					MetricServices: []string{svcBoth, svcSwitch}, SpanServices: []string{svcSpans},
				})
				require.NoError(t, err)
				require.Zero(t, sum.Requests)
				sel, err := apimetrics.Select(t.Context(), q, rng, ptr(svcBoth))
				require.NoError(t, err)
				require.Empty(t, sel.Sources)
				rows, err := q.APIMetricRoutes(t.Context(), tenantq.APIMetricRoutesParams{
					SortKey: "requests", Descending: true, RowLimit: 100, Resolution: "minute",
					RangeFrom: rng.From, RangeTo: rng.To, MetricServices: []string{svcBoth}, SpanServices: []string{},
				})
				require.NoError(t, err)
				require.Empty(t, rows)
				ws, err := q.APIMetricWarnings(t.Context(), tenantq.APIMetricWarningsParams{
					FirstDay: at("2031-05-01T00:00:00Z"), LastDay: at("2031-06-15T00:00:00Z"),
				})
				require.NoError(t, err)
				require.Empty(t, ws)
				// and without any predicate at all: policy alone
				for _, tbl := range []string{"api.metrics_minute", "api.metrics_hourly", "api.metrics_daily", "api.ingest_warnings"} {
					var n int64
					require.NoError(t, tx.QueryRow(t.Context(), `select count(*) from `+tbl+` where tenant_id = $1`, tenantA).Scan(&n))
					require.Zero(t, n, tbl)
				}
				return nil
			}))
		}
		// the right tenant does see the rows, so the zeros above are not an
		// empty table
		require.NoError(t, e.d.WithTenant(t.Context(), tenantA, func(tx pgx.Tx) error {
			var n int64
			require.NoError(t, tx.QueryRow(t.Context(), `select count(*) from api.metrics_minute`).Scan(&n))
			require.Positive(t, n)
			return nil
		}))
	})

	t.Run("injection attempts are literal or refused", func(t *testing.T) {
		literal := []string{
			"' or '1'='1",
			"x'); drop table api.metrics_daily; --",
			"%", "_", `\`, "svc-both' --",
			"$1", "svc-both\" or 1=1",
		}
		for _, v := range literal {
			for _, key := range []string{"service", "route", "method"} {
				if key == "method" && len(v) > 16 {
					continue
				}
				var ov apimetrics.Overview
				ca.ok(t, pathA+"overview?range=24h&"+key+"="+url.QueryEscape(v), &ov)
				require.Zero(t, ov.Current.Summary.Requests, key+"="+v)
				if key == "service" {
					// route and method never narrow the source list (7.3)
					require.Empty(t, ov.Current.Sources)
				}
				var page apimetrics.RouteList
				ca.ok(t, pathA+"routes?range=24h&"+key+"="+url.QueryEscape(v), &page)
				require.Empty(t, page.Items)
			}
		}
		var n int
		require.NoError(t, e.su.QueryRow(t.Context(), `select count(*) from api.metrics_daily`).Scan(&n))
		require.Positive(t, n, "the table survived")

		// a stored value with a quote matches itself exactly
		var ov apimetrics.Overview
		ca.ok(t, pathA+"overview?range=1h&route="+url.QueryEscape(quoted), &ov)
		require.Equal(t, int64(2), ov.Current.Summary.Requests)

		refused := []string{
			"service=a%00b", "route=%2Fa%0Ab", "method=GET%7F", "route=%FF%FE",
			"service=" + strings.Repeat("s", 129), "method=" + strings.Repeat("M", 17),
			"route=" + strings.Repeat("r", 1025),
			"service=a&service=b", "range=24h&range=1h", "bogus=1", "range=7d;drop",
			"range=custom", "range=custom&from=2031-06-15T00:00:00Z",
			"range=custom&from=2031-06-15T00:00:00Z&to=2031-06-14T00:00:00Z",
			"range=custom&from=1999-12-31T00:00:00Z&to=2000-01-02T00:00:00Z",
			"range=custom&from=2029-01-01T00:00:00Z&to=2031-06-01T00:00:00Z",
			"range=custom&from=2031-06-15T00:00:00Z&to=2031-06-17T00:00:00Z",
			"range=custom&from=yesterday&to=2031-06-15T00:00:00Z",
			"range=24h&from=2031-06-15T00:00:00Z",
		}
		for _, qs := range refused {
			st, code := ca.errCode(t, pathA+"overview?"+qs)
			require.Equal(t, http.StatusBadRequest, st, qs)
			require.Equal(t, "invalid_query", code, qs)
		}
		for _, qs := range []string{"sort=requests;drop", "order=sideways", "limit=0", "limit=201", "limit=x"} {
			st, code := ca.errCode(t, pathA+"routes?range=24h&"+qs)
			require.Equal(t, http.StatusBadRequest, st, qs)
			require.Equal(t, "invalid_query", code, qs)
		}
		// filters are refused where the contract does not take them
		for _, p := range []string{"services", "warnings"} {
			st, code := ca.errCode(t, pathA+p+"?range=24h&service="+svcBoth)
			require.Equal(t, http.StatusBadRequest, st, p)
			require.Equal(t, "invalid_query", code, p)
		}
	})

	t.Run("cursors are bound to their list", func(t *testing.T) {
		var page apimetrics.RouteList
		ca.ok(t, pathA+"routes?range=24h&limit=1", &page)
		require.NotNil(t, page.NextCursor)
		cur := url.QueryEscape(*page.NextCursor)
		for _, qs := range []string{
			"range=24h&limit=1&sort=p50&cursor=" + cur,
			"range=24h&limit=1&order=asc&cursor=" + cur,
			"range=7d&limit=1&cursor=" + cur,
			"range=24h&limit=1&service=" + svcBoth + "&cursor=" + cur,
			"range=24h&limit=1&cursor=not-a-cursor",
		} {
			st, code := ca.errCode(t, pathA+"routes?"+qs)
			require.Equal(t, http.StatusBadRequest, st, qs)
			require.Equal(t, "invalid_cursor", code, qs)
		}
		// a different limit keeps the same list, so the cursor stays valid
		ca.ok(t, pathA+"routes?range=24h&limit=3&cursor="+cur, &page)
		require.NotEmpty(t, page.Items)
	})

	t.Run("hourly and daily answers include hours the rollup has not reached", func(t *testing.T) {
		userD, tenantD := e.member(t, "active")
		cd := e.client(t, userD)
		pathD := "/tenants/" + tenantD.String() + "/api-metrics/"
		// first batch rolled up, so the second batch lands partly in hours
		// and days that already have (now stale) hourly and daily rows
		e.write(t, []point{
			{tenantD, at("2031-06-15T08:10:00Z"), "fresh", "GET", "/a", 200, "metric", h(map[int]int64{2: 10}), false},
			{tenantD, at("2031-06-15T10:01:00Z"), "fresh", "GET", "/a", 200, "metric", h(map[int]int64{3: 5}), false},
			{tenantD, at("2031-06-10T12:00:00Z"), "fresh", "GET", "/a", 500, "metric", h(map[int]int64{4: 3}), false},
		}, nil)
		e.writeMinutes(t, []point{
			{tenantD, at("2031-06-15T10:02:00Z"), "fresh", "GET", "/a", 200, "metric", h(map[int]int64{3: 7}), false},
			{tenantD, at("2031-06-15T10:02:00Z"), "fresh", "GET", "/a", 200, "span", h(map[int]int64{1: 50}), false},
			{tenantD, at("2031-06-15T09:50:00Z"), "fresh-span", "GET", "/h", 0, "span", h(map[int]int64{0: 4}), false},
			{tenantD, at("2031-06-15T10:03:00Z"), "fresh-span", "GET", "/h", 200, "span", h(map[int]int64{0: 1}), false},
			{tenantD, at("2031-06-12T03:00:00Z"), "fresh", "POST", "/b", 201, "metric", h(map[int]int64{14: 2, 2: 1}), true},
			{tenantD, at("2031-05-01T00:00:00Z"), "fresh", "GET", "/a", 404, "metric", h(map[int]int64{0: 6}), false},
		}, nil)
		var pending int
		require.NoError(t, e.su.QueryRow(t.Context(), `select count(*) from api.rollup_pending where tenant_id = $1`, tenantD).Scan(&pending))
		require.Equal(t, 4, pending, "hours 10:00 and 09:00 of 06-15, 06-12 03:00 and 05-01 00:00")

		type views struct {
			ov apimetrics.Overview
			ts apimetrics.Timeseries
			rt apimetrics.RouteList
			st apimetrics.Status
			sv apimetrics.Services
		}
		fetch := func(qs string) views {
			var v views
			cd.ok(t, pathD+"overview?"+qs, &v.ov)
			cd.ok(t, pathD+"timeseries?"+qs, &v.ts)
			cd.ok(t, pathD+"routes?"+qs+"&limit=200", &v.rt)
			cd.ok(t, pathD+"status?"+qs, &v.st)
			cd.ok(t, pathD+"services?"+qs, &v.sv)
			return v
		}
		presets := []string{"range=24h", "range=7d", "range=90d"}
		before := map[string]views{}
		for _, qs := range presets {
			v := fetch(qs)
			before[qs] = v
			require.NotEqual(t, apimetrics.Minute, v.ov.Current.Range.Resolution, qs)
			// the expectation reads only metrics_minute over the same range
			rng := v.ov.Current.Range
			rng.Resolution = apimetrics.Minute
			q := query{tenant: tenantD, rng: rng}
			require.Equal(t, e.expSources(t, q), v.ov.Current.Sources, qs)
			require.Equal(t, e.expSummary(t, q), v.ov.Current.Summary, qs)
			require.Equal(t, e.expRoutes(t, q, "requests desc, service, method, route"), v.rt.Items, qs)
			total, classes, codes := e.expStatus(t, q)
			require.Equal(t, total, v.st.Requests, qs)
			require.Equal(t, classes, v.st.Classes, qs)
			require.Equal(t, codes, v.st.Codes, qs)
			require.Equal(t, e.expServices(t, q), v.sv.Items, qs)
			buckets := e.expMinuteBuckets(t, q, v.ts.Range.Resolution)
			got := map[time.Time]apimetrics.Summary{}
			for _, p := range v.ts.Points {
				if p.Requests > 0 {
					got[time.Time(p.At)] = p.Summary
				}
			}
			require.Equal(t, buckets, got, qs)
		}
		// by hand: 10 + 5 + 7 (span 50 ignored) + 4 + 1 in the last 24 hours
		require.Equal(t, int64(27), before["range=24h"].ov.Current.Summary.Requests)
		require.Equal(t, int64(27+3+3), before["range=7d"].ov.Current.Summary.Requests)
		require.Equal(t, int64(27+3+3+6), before["range=90d"].ov.Current.Summary.Requests)

		e.rollup(t)
		require.NoError(t, e.su.QueryRow(t.Context(), `select count(*) from api.rollup_pending where tenant_id = $1`, tenantD).Scan(&pending))
		require.Zero(t, pending)
		for _, qs := range presets {
			after := fetch(qs)
			b := before[qs]
			require.Equal(t, b.ov, after.ov, qs)
			require.Equal(t, b.ts, after.ts, qs)
			require.Equal(t, b.rt, after.rt, qs)
			require.Equal(t, b.st, after.st, qs)
			require.Equal(t, b.sv, after.sv, qs)
		}
	})

	t.Run("only the table of the resolution is read", func(t *testing.T) {
		// the queries union all three tables; the branches of the other two
		// must cost nothing. explain analyze shows a branch that ran as a scan
		// with loops, one that was cut as never executed or not at all. this
		// runs with no hour pending, so the minute branches of hourly and
		// daily answers must not run either
		require.NoError(t, e.d.WithTenant(t.Context(), tenantA, func(tx pgx.Tx) error {
			for res, tbl := range map[string]string{"minute": "metrics_minute", "hour": "metrics_hourly", "day": "metrics_daily"} {
				rows, err := tx.Query(t.Context(), `explain (analyze, costs off, timing off) `+namedQuery(t, "APIMetricSummary"),
					res, at("2031-06-14T00:00:00Z"), at("2031-06-16T00:00:00Z"),
					[]string{svcBoth}, []string{svcSpans}, nil, nil)
				require.NoError(t, err)
				var plan []string
				for rows.Next() {
					var line string
					require.NoError(t, rows.Scan(&line))
					plan = append(plan, line)
				}
				require.NoError(t, rows.Err())
				read := false
				for _, line := range plan {
					read = read || (strings.Contains(line, tbl) && !strings.Contains(line, "never executed"))
				}
				require.True(t, read, "%s not read for %s:\n%s", tbl, res, strings.Join(plan, "\n"))
				for _, other := range []string{"metrics_minute", "metrics_hourly", "metrics_daily"} {
					for _, line := range plan {
						if other != tbl && strings.Contains(line, other) {
							require.Contains(t, line, "never executed", "%s read for %s:\n%s", other, res, strings.Join(plan, "\n"))
						}
					}
				}
			}
			return nil
		}))
	})

	t.Run("signed out is 401", func(t *testing.T) {
		hc := e.srv.Client()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, e.srv.URL+"/api"+pathA+"overview", nil)
		require.NoError(t, err)
		res, err := hc.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})
}
