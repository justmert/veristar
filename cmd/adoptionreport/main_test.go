package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/docs/reference/openapi"
)

var update = flag.Bool("update", false, "rewrite the golden evidence files")

// a key of the right shape; the test servers never check it beyond that
const testKey = "sa_op_0123456789abcdefghijABCDEFGHIJ0123456789"

type canned struct {
	status int
	header http.Header
	body   []byte
}

// contractServer answers from canned responses keyed by path and encoded
// query, and checks every request and every answer against the openapi
// document, so a fixture that the real api could not have produced fails
// the test instead of passing quietly
type contractServer struct {
	t      *testing.T
	srv    *httptest.Server
	router routers.Router
	mu     sync.Mutex
	resp   map[string][]canned
	hits   map[string]int
}

func newContractServer(t *testing.T) *contractServer {
	t.Helper()
	cs := &contractServer{t: t, resp: map[string][]canned{}, hits: map[string]int{}}
	cs.srv = httptest.NewServer(http.HandlerFunc(cs.serve))
	t.Cleanup(cs.srv.Close)
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(openapi.MetricsV1)
	require.NoError(t, err)
	require.NoError(t, doc.Validate(loader.Context))
	doc.Servers = openapi3.Servers{{URL: cs.srv.URL + "/api/metrics/v1"}}
	cs.router, err = gorillamux.NewRouter(doc)
	require.NoError(t, err)
	return cs
}

func (cs *contractServer) base() string { return cs.srv.URL + "/api/metrics/v1" }

// add queues an answer for one request; several answers for the same
// request are served in order, the last one repeatedly
func (cs *contractServer) add(pathQuery string, c canned) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if c.header == nil {
		c.header = http.Header{}
	}
	if c.header.Get("Content-Type") == "" {
		c.header.Set("Content-Type", "application/json")
	}
	if c.header.Get("X-Request-Id") == "" {
		c.header.Set("X-Request-Id", fmt.Sprintf("req-%d", len(cs.resp)+1))
	}
	cs.resp[pathQuery] = append(cs.resp[pathQuery], c)
}

func (cs *contractServer) addJSON(t *testing.T, pathQuery string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	cs.add(pathQuery, canned{status: http.StatusOK, body: b})
}

func (cs *contractServer) serve(w http.ResponseWriter, r *http.Request) {
	t := cs.t
	if r.Header.Get("Authorization") != "Bearer "+testKey {
		t.Errorf("request without the operator key: %q", r.Header.Get("Authorization"))
	}
	key := strings.TrimPrefix(r.URL.Path, "/api/metrics/v1")
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	cs.mu.Lock()
	list := cs.resp[key]
	n := cs.hits[key]
	cs.hits[key]++
	cs.mu.Unlock()
	if len(list) == 0 {
		t.Errorf("unexpected request %s", key)
		http.Error(w, "no fixture", http.StatusTeapot)
		return
	}
	c := list[min(n, len(list)-1)]
	route, params, err := cs.router.FindRoute(r)
	if err != nil {
		t.Errorf("%s: %v", key, err)
		http.Error(w, "no route", http.StatusTeapot)
		return
	}
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, IncludeResponseStatus: true}
	in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: opts}
	if err := openapi3filter.ValidateRequest(r.Context(), in); err != nil {
		t.Errorf("request %s does not match the contract: %v", key, err)
	}
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: c.status, Header: c.header, Options: opts}
	out.SetBodyBytes(c.body)
	if err := openapi3filter.ValidateResponse(r.Context(), out); err != nil {
		t.Errorf("fixture for %s does not match the contract: %v", key, err)
	}
	for k, v := range c.header {
		w.Header()[k] = v
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(c.status)
	_, _ = w.Write(c.body)
}

func noSleep(context.Context, time.Duration) error { return nil }

type result struct {
	code           int
	stdout, stderr string
	file           string
}

func runReport(t *testing.T, cs *contractServer, args ...string) result {
	t.Helper()
	out := t.TempDir()
	var so, se bytes.Buffer
	env := func(k string) string {
		if k == "SA_OPERATOR_KEY" {
			return testKey
		}
		return ""
	}
	all := append([]string{"-api", cs.base(), "-out", out}, args...)
	code := run(t.Context(), all, env, &so, &se, cs.srv.Client(), func() time.Time {
		return time.Date(2026, 11, 13, 6, 5, 39, 0, time.UTC)
	}, noSleep)
	r := result{code: code, stdout: so.String(), stderr: se.String()}
	if code == 0 {
		b, err := os.ReadFile(strings.TrimSpace(strings.SplitN(r.stdout, "\n", 2)[0]))
		require.NoError(t, err)
		r.file = string(b)
	}
	return r
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), got)
}

// TestRecordedLocalRun replays the answers recorded from the real machine
// api of the local dev stack on 2026-11-13 (adoptionreport -raw), checks each
// one against the contract, and requires the same evidence file the real run
// wrote, except for the api base url. the /web/daily answer carries the
// integrity field the api gained later, as the api answers it for days
// without a grade, so its body hash in the sources table differs from the
// original run's
func TestRecordedLocalRun(t *testing.T) {
	dir := filepath.Join("testdata", "recorded", "local-2026-11-13")
	var index []recorded
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &index))
	require.NotEmpty(t, index)
	cs := newContractServer(t)
	for _, r := range index {
		body, err := os.ReadFile(filepath.Join(dir, r.File))
		require.NoError(t, err)
		h := http.Header{}
		h.Set("X-Request-Id", r.RequestID)
		cs.add(r.Path, canned{status: r.Status, header: h, body: body})
	}
	res := runReport(t, cs, "-tenants", "01a1d37e-a3c3-724a-b427-12f2b3aa38dd", "-from", "2026-10-31", "-to", "2026-11-13")
	require.Equal(t, 0, res.code, res.stderr)
	got := strings.ReplaceAll(res.file, cs.base(), "http://localhost:8085/api/metrics/v1")
	golden(t, filepath.Join("testdata", "recorded", "local-2026-11-13.golden.md"), got)
	require.Contains(t, res.stdout, "longest run 1 day (2026-11-13), below the 14-day threshold")
}

// the synthetic fixtures below are test data only: they exercise runs, gaps,
// paging, quarantine, the open percentile bucket and retries, which one day
// of local traffic cannot show. every one is validated against the contract.

const (
	tenantA = "0190a000-0000-7000-8000-00000000000a"
	tenantB = "0190a000-0000-7000-8000-00000000000b"
)

func ptr[T any](v T) *T { return &v }

func q(route string, kv ...string) string {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return route + "?" + v.Encode()
}

func tenantRow(id, name string) tenant {
	return tenant{ID: id, Slug: strings.ToLower(strings.ReplaceAll(name, " ", "-")), Name: name, Status: "active",
		CreatedAt: "2026-10-01T12:00:00.000Z", Sites: 2, SitesVerified: 1, SitesWithStellarToml: 1, Verified: true, StellarToml: true}
}

func days(from string, n int) []string {
	d, _ := parseDay(from)
	out := make([]string, n)
	for i := range out {
		out[i] = d.AddDate(0, 0, i).Format(dayLayout)
	}
	return out
}

// TestRunsGapsAndPaging: tenant a has page views on 1 to 20 november except
// the 6th (zero) and the 11th (never aggregated), so its longest run is 9
// days (12 to 20); the threshold is set to 9 so it passes, and tenant b,
// with 8 days, does not. the tenant list and a's daily rows come in two
// pages each; the first tenant page is refused once with 429.
func TestRunsGapsAndPaging(t *testing.T) {
	cs := newContractServer(t)
	from, to := "2026-11-01", "2026-11-20"

	retry := http.Header{}
	retry.Set("Retry-After", "2")
	cs.add("/tenants", canned{status: http.StatusTooManyRequests, header: retry,
		body: []byte(`{"error":{"code":"rate_limited","message":"slow down","request_id":"r429"}}`)})
	cs.addJSON(t, "/tenants", tenantList{Items: []tenant{tenantRow(tenantA, "Project A")}, NextCursor: ptr("c1")})
	cs.addJSON(t, q("/tenants", "cursor", "c1"), tenantList{Items: []tenant{tenantRow(tenantB, "Project | B")}})

	var aRows []webDaily
	for i, d := range days(from, 20) {
		if d == "2026-11-11" {
			continue
		}
		pv := int64(10 + i)
		if d == "2026-11-06" {
			pv = 0
		}
		aRows = append(aRows,
			webDaily{TenantID: tenantA, Day: d, Band: "primary", Sites: 2, Pageviews: pv, Visitors: pv / 2, Visits: pv / 2, Bounces: pv / 4, Totaltime: 30 * pv, Integrity: noGrade()},
			webDaily{TenantID: tenantA, Day: d, Band: "quarantine", Sites: 2, Pageviews: 1, Visitors: 1, Visits: 1, Bounces: 1, Integrity: noGrade()})
	}
	cs.addJSON(t, q("/web/daily", "tenant", tenantA, "from", from, "to", to), webDailyList{From: from, To: to, Items: aRows[:20], NextCursor: ptr("p2")})
	cs.addJSON(t, q("/web/daily", "tenant", tenantA, "from", from, "to", to, "cursor", "p2"), webDailyList{From: from, To: to, Items: aRows[20:]})
	cs.addJSON(t, q("/web/dimensions", "tenant", tenantA, "from", from, "to", to), webDimensionList{From: from, To: to, Items: []webDimension{
		{TenantID: tenantA, Day: "2026-11-01", Band: "primary", Dimension: "country", Key: "DE", Value: 3},
		{TenantID: tenantA, Day: "2026-11-02", Band: "primary", Dimension: "country", Key: "DE", Value: 2},
		{TenantID: tenantA, Day: "2026-11-02", Band: "primary", Dimension: "country", Key: "NG", Value: 4},
		{TenantID: tenantA, Day: "2026-11-02", Band: "quarantine", Dimension: "country", Key: "US", Value: 9},
		{TenantID: tenantA, Day: "2026-11-02", Band: "primary", Dimension: "referrer", Key: "a|b.example", Value: 1},
	}})
	cs.addJSON(t, q("/api/daily", "tenant", tenantA, "from", from, "to", to), apiDailyList{From: from, To: to, Items: []apiDaily{
		{TenantID: tenantA, Day: "2026-11-19", Services: 1, Source: "metric", apiNumbers: apiNumbers{Requests: 100, Requests4xx: 10, Requests5xx: 1,
			Rate4xx: ptr(0.1), Rate5xx: ptr(0.01), P50Seconds: ptr(0.02), P95Seconds: ptr(1.5), P99Seconds: nil, P99Over10s: true}},
		{TenantID: tenantA, Day: "2026-11-20", Services: 2, SpanServices: 1, Source: "mixed", apiNumbers: apiNumbers{Requests: 50,
			Rate4xx: ptr(0.0), Rate5xx: ptr(0.0), P50Seconds: ptr(0.01), P95Seconds: nil, P95Over10s: true, P99Seconds: nil, P99Over10s: true, Lossy: true}},
	}})
	cs.addJSON(t, q("/api/routes/daily", "tenant", tenantA, "from", "2026-11-20", "to", "2026-11-20"), apiRouteDailyList{From: "2026-11-20", To: "2026-11-20", Items: []apiRouteDaily{
		{TenantID: tenantA, Day: "2026-11-20", Service: "svc", Method: "GET", Route: "", Source: "span", apiNumbers: apiNumbers{Requests: 20,
			Rate4xx: ptr(0.0), Rate5xx: ptr(0.0), P50Seconds: ptr(0.01), P95Seconds: ptr(0.02), P99Seconds: ptr(0.03)}},
		{TenantID: tenantA, Day: "2026-11-20", Service: "svc", Method: "GET", Route: "/a/{id}", Source: "metric", apiNumbers: apiNumbers{Requests: 30,
			Rate4xx: ptr(0.0), Rate5xx: ptr(0.0), P50Seconds: ptr(0.01), P95Seconds: nil, P95Over10s: true, P99Seconds: nil, P99Over10s: true, Lossy: true}},
	}})

	var bRows []webDaily
	for _, d := range days("2026-11-13", 8) {
		bRows = append(bRows, webDaily{TenantID: tenantB, Day: d, Band: "primary", Sites: 1, Pageviews: 3, Visitors: 1, Visits: 1, Bounces: 0, Totaltime: 60, Integrity: noGrade()})
	}
	cs.addJSON(t, q("/web/daily", "tenant", tenantB, "from", from, "to", to), webDailyList{From: from, To: to, Items: bRows})
	cs.addJSON(t, q("/web/dimensions", "tenant", tenantB, "from", from, "to", to), webDimensionList{From: from, To: to, Items: []webDimension{}})
	cs.addJSON(t, q("/api/daily", "tenant", tenantB, "from", from, "to", to), apiDailyList{From: from, To: to, Items: []apiDaily{}})

	res := runReport(t, cs, "-tenants", tenantA+","+tenantB, "-from", from, "-to", to, "-min-days", "9", "-now", "2026-11-21T08:00:00Z")
	require.Equal(t, 0, res.code, res.stderr)
	require.Equal(t, 2, cs.hits["/tenants"], "the 429 is retried once")
	f := strings.ReplaceAll(res.file, cs.base(), "http://contract.test/api/metrics/v1")
	golden(t, filepath.Join("testdata", "synthetic.golden.md"), f)

	require.Contains(t, f, "| Project A | `"+tenantA+"` | active | 2026-10-01 | 2026-11-01 | 9 days (2026-11-12 to 2026-11-20) | yes | 2 of 20 |")
	require.Contains(t, f, "| Project \\| B | `"+tenantB+"` | active | 2026-10-01 | 2026-11-13 | 8 days (2026-11-13 to 2026-11-20) | no | 0 of 20 |")
	require.Contains(t, f, "| 2026-11-11 | not aggregated |")
	require.Contains(t, f, "| 2026-11-06 | 0 | 0 | 0 | none (no visits) | none (no visits) | 1 | no |")
	require.Contains(t, f, "| Days the aggregation never read | 1 |")
	// quarantine rows are reported, never counted: DE 5 and NG 4, no US
	require.Contains(t, f, "| DE | 5 |\n| NG | 4 |\n")
	require.NotContains(t, f, "| US |")
	require.Contains(t, f, "| a\\|b.example | 1 |")
	require.Contains(t, f, "Quarantined page views in the range, not counted above: 19.")
	require.Contains(t, f, "| 2026-11-19 | 100 | 89 | 10 | 1 | 20.0 ms | 1.500 s | metric | 1 |")
	require.Contains(t, f, "| 2026-11-20 | 50 | 50 | 0 | 0 | 10.0 ms | >10 s | mixed, approximate | 2 |")
	require.Contains(t, f, "| svc | GET | /a/{id} | 30 | 0 | 0 | 10.0 ms | >10 s | metric |\n| svc | GET | (empty) | 20 |")
	require.Contains(t, f, "No API requests in the range.")
	require.Contains(t, res.stdout, tenantA+" Project A: longest run 9 days (2026-11-12 to 2026-11-20), meets the 9-day threshold")
}

func TestRefusals(t *testing.T) {
	cs := newContractServer(t)
	cs.addJSON(t, "/tenants", tenantList{Items: []tenant{tenantRow(tenantA, "Project A")}})
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"no tenants", nil, 2, "-tenants is required"},
		{"bad id", []string{"-tenants", "nope"}, 2, `"nope" is not a tenant id`},
		{"twice", []string{"-tenants", tenantA + "," + tenantA}, 2, "listed twice"},
		{"to after today", []string{"-tenants", tenantA, "-to", "2026-11-14"}, 2, "after today"},
		{"from after to", []string{"-tenants", tenantA, "-from", "2026-11-10", "-to", "2026-11-09"}, 2, "is after -to"},
		{"too long", []string{"-tenants", tenantA, "-from", "2025-11-01", "-to", "2026-11-12"}, 2, "366 days"},
		{"not in scope", []string{"-tenants", tenantB, "-from", "2026-11-12", "-to", "2026-11-12"}, 1, "is not listed by /tenants"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := runReport(t, cs, c.args...)
			require.Equal(t, c.code, res.code)
			require.Contains(t, res.stderr, c.want)
		})
	}
}

func TestKeyAndAPIFlag(t *testing.T) {
	var so, se bytes.Buffer
	never := func(context.Context, time.Duration) error { return nil }
	code := run(t.Context(), []string{"-api", "https://app.analytics.example/api/metrics/v1", "-tenants", tenantA},
		func(string) string { return "sa_op_short" }, &so, &se, http.DefaultClient, time.Now, never)
	require.Equal(t, 2, code)
	require.Contains(t, se.String(), "SA_OPERATOR_KEY")
	se.Reset()
	code = run(t.Context(), []string{"-api", "https://app.analytics.example/api/other", "-tenants", tenantA},
		func(string) string { return testKey }, &so, &se, http.DefaultClient, time.Now, never)
	require.Equal(t, 2, code)
	require.Contains(t, se.String(), "ending in /api/metrics/v1")
}

// TestContractErrorsAndDrift: an error envelope is reported with its code
// and request id, and an answer carrying a field the contract does not
// define stops the report instead of being read partially
func TestContractErrorsAndDrift(t *testing.T) {
	cs := newContractServer(t)
	cs.add("/tenants", canned{status: http.StatusUnauthorized, header: http.Header{"Www-Authenticate": {"Bearer"}, "X-Request-Id": {"rid-401"}},
		body: []byte(`{"error":{"code":"unauthenticated","message":"key revoked","request_id":"rid-401"}}`)})
	res := runReport(t, cs, "-tenants", tenantA)
	require.Equal(t, 1, res.code)
	require.Contains(t, res.stderr, "401 unauthenticated: key revoked (request rid-401)")

	hc := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"x"}},
			Body: io.NopCloser(strings.NewReader(`{"items":[],"next_cursor":null,"extra":1}`))}, nil
	})}
	base, _ := url.Parse("http://unused/api/metrics/v1")
	c := &client{base: base, key: testKey, hc: hc, sleep: noSleep}
	var tl tenantList
	err := c.get(t.Context(), "/tenants", url.Values{}, &tl)
	require.ErrorContains(t, err, `unknown field "extra"`)
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestRawRecorder: -raw keeps each body and an index the replay test reads
func TestRawRecorder(t *testing.T) {
	cs := newContractServer(t)
	cs.addJSON(t, "/tenants", tenantList{Items: []tenant{tenantRow(tenantA, "Project A")}})
	from := "2026-11-12"
	cs.addJSON(t, q("/web/daily", "tenant", tenantA, "from", from, "to", from), webDailyList{From: from, To: from, Items: []webDaily{}})
	cs.addJSON(t, q("/web/dimensions", "tenant", tenantA, "from", from, "to", from), webDimensionList{From: from, To: from, Items: []webDimension{}})
	cs.addJSON(t, q("/api/daily", "tenant", tenantA, "from", from, "to", from), apiDailyList{From: from, To: from, Items: []apiDaily{}})
	raw := t.TempDir()
	res := runReport(t, cs, "-tenants", tenantA, "-from", from, "-to", from, "-raw", raw)
	require.Equal(t, 0, res.code, res.stderr)
	var index []recorded
	b, err := os.ReadFile(filepath.Join(raw, "index.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &index))
	require.Len(t, index, 4)
	require.Equal(t, "/tenants", index[0].Path)
	body, err := os.ReadFile(filepath.Join(raw, index[0].File))
	require.NoError(t, err)
	require.Contains(t, string(body), "Project A")
	require.Contains(t, res.file, "0 days")
}

// noGrade is the integrity of a day without a graded site day, as the api
// answers it
func noGrade() integritySummary { return integritySummary{Reasons: []rangeReason{}} }
