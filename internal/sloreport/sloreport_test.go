package sloreport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeGatus is a test fixture, not a gatus: it answers
// /api/v1/endpoints/{key}/statuses with the paging rules of gatus 5.37.0
// (api/util.go caps page 1 at maximum-number-of-results; the sql store
// orders newest first, offset (page-1)*size, and reverses each page so it
// reads oldest first).
type fakeGatus struct {
	max     int
	results map[string][]gatusResult // oldest first
}

func (g *fakeGatus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/endpoints/")
	key, ok2 := strings.CutSuffix(rest, "/statuses")
	if !ok || !ok2 {
		http.NotFound(w, r)
		return
	}
	rs, ok := g.results[key]
	if !ok {
		http.Error(w, "endpoint not found", http.StatusNotFound)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page == 1 && size > g.max {
		size = g.max
	}
	newest := slices.Clone(rs)
	slices.Reverse(newest)
	lo := min((page-1)*size, len(newest))
	hi := min(lo+size, len(newest))
	out := slices.Clone(newest[lo:hi])
	slices.Reverse(out)
	_ = json.NewEncoder(w).Encode(gatusStatus{Key: key, Results: out})
}

var day = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)

// series builds n results every 30 s from start; fail picks failures.
func series(start time.Time, n int, fail func(i int) bool) []gatusResult {
	out := make([]gatusResult, n)
	for i := range out {
		out[i] = gatusResult{Timestamp: start.Add(time.Duration(i) * 30 * time.Second), Success: !fail(i)}
		if fail(i) {
			out[i].Errors = []string{"gateway answered 503"}
		}
	}
	return out
}

func never(int) bool { return false }

func allKeys(rs []gatusResult) map[string][]gatusResult {
	m := map[string][]gatusResult{}
	for _, k := range Keys {
		m[k] = rs
	}
	return m
}

func collector(t *testing.T, g *fakeGatus, pageSize int) *Collector {
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return &Collector{Base: srv.URL + "/", PageSize: pageSize, HTTP: srv.Client(),
		Now: func() time.Time { return day.Add(36 * time.Hour) }}
}

func TestCollectKeepsTheDayAcrossPages(t *testing.T) {
	// one hour before the day to one hour after, 30 s apart
	rs := series(day.Add(-time.Hour), 26*120, never)
	g := &fakeGatus{max: 500, results: allKeys(rs)}
	// page size equal to the gatus maximum, so page 1 is full and paging
	// walks back past midnight
	got, err := collector(t, g, 500).Collect(context.Background(), day)
	require.NoError(t, err)
	require.Equal(t, "2026-11-12", got.Day)
	for _, k := range Keys {
		require.Len(t, got.Endpoints[k], 2880, k)
		require.Equal(t, day, got.Endpoints[k][0].Timestamp)
		require.Equal(t, day.Add(24*time.Hour-30*time.Second), got.Endpoints[k][2879].Timestamp)
	}
}

func TestCollectFollowsTheGatusCap(t *testing.T) {
	rs := series(day.Add(-time.Hour), 26*120, never)
	// gatus caps page 1 at 300 while 1000 is asked for: later pages must use
	// 300 too, or offsets skip results
	g := &fakeGatus{max: 300, results: allKeys(rs)}
	got, err := collector(t, g, 1000).Collect(context.Background(), day)
	require.NoError(t, err)
	require.Len(t, got.Endpoints[KeyIngestion], 2880)
}

func TestCollectRefusesATrimmedDay(t *testing.T) {
	// gatus holds only its maximum, which starts after midnight
	rs := series(day.Add(6*time.Hour), 500, never)
	g := &fakeGatus{max: 500, results: allKeys(rs)}
	_, err := collector(t, g, 500).Collect(context.Background(), day)
	require.ErrorContains(t, err, "trimmed")
}

func TestCollectAcceptsALateStart(t *testing.T) {
	// the endpoint started reporting at 06:00 and gatus holds all of it
	rs := series(day.Add(6*time.Hour), 100, never)
	g := &fakeGatus{max: 500, results: allKeys(rs)}
	got, err := collector(t, g, 500).Collect(context.Background(), day)
	require.NoError(t, err)
	require.Len(t, got.Endpoints[KeyDashboard], 100)
}

func TestCollectErrors(t *testing.T) {
	m := allKeys(series(day, 10, never))
	delete(m, KeyFreshnessAPI)
	g := &fakeGatus{max: 500, results: m}
	_, err := collector(t, g, 500).Collect(context.Background(), day)
	require.True(t, errors.Is(err, errNotFound), err)

	c := collector(t, &fakeGatus{max: 500, results: allKeys(nil)}, 500)
	c.Now = func() time.Time { return day.Add(23 * time.Hour) }
	_, err = c.Collect(context.Background(), day)
	require.True(t, errors.Is(err, errNotEnded), err)
}

func writeDays(t *testing.T, dir string, days map[time.Time]map[string][]Result) {
	for d, eps := range days {
		_, err := WriteDay(dir, Day{Day: d.Format(dayLayout), CollectedAt: d.Add(25 * time.Hour), Source: "https://status.example.test",
			IntervalSeconds: IntervalSeconds, Endpoints: eps})
		require.NoError(t, err)
	}
}

func toResults(rs []gatusResult) []Result {
	out := make([]Result, len(rs))
	for i, r := range rs {
		out[i] = Result{Timestamp: r.Timestamp, Success: r.Success, Errors: r.Errors}
	}
	return out
}

// TestReportMatchesAHandCalculation checks the numbers against values worked
// out by hand. november 2026 through the 3rd: the 1st and 3rd have files,
// the 2nd is missing, so the period is 3 days = 259200 s.
//
// ingestion, 1st: 2880 results, failures at 100..103 (4); 3rd: 2880
// results, failures at 2878 and 2879 (ongoing at end of data).
//
//	results 5760, successes 5754, availability 5754/5760 = 99.8958%
//	coverage 5760*30/259200 = 66.6667%
//	budget (1-0.9999)*259200/60 = 0.432 min -> 0.43 min, used 6*30/60 = 3.00 min
//	verdict missed (99.8958 < 99.99)
//
// dashboard: no failures, 5760 results, 100.0000%, used 0.00 min,
// budget (1-0.999)*259200/60 = 4.32 min. coverage 66.6667% is below the
// 95% floor, so the verdict is not measured; ingestion stays missed because
// its 3.00 failed minutes already exceed its 0.43 minute budget.
func TestReportMatchesAHandCalculation(t *testing.T) {
	dir := t.TempDir()
	d1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	ok := toResults(series(d1, 2880, never))
	ok3 := toResults(series(d3, 2880, never))
	writeDays(t, dir, map[time.Time]map[string][]Result{
		d1: {KeyIngestion: toResults(series(d1, 2880, func(i int) bool { return i >= 100 && i <= 103 })),
			KeyDashboard: ok, KeyFreshnessWeb: ok, KeyFreshnessAPI: ok},
		d3: {KeyIngestion: toResults(series(d3, 2880, func(i int) bool { return i >= 2878 })),
			KeyDashboard: ok3, KeyFreshnessWeb: ok3, KeyFreshnessAPI: []Result{}},
	})
	m, err := Load(dir, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, []time.Time{time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}, m.Missing)

	ing := m.Stats[0]
	require.Equal(t, KeyIngestion, ing.Key)
	require.Equal(t, 5760, ing.Results)
	require.Equal(t, 5754, ing.Successes)
	require.Len(t, ing.Incidents, 2)
	require.Equal(t, d1.Add(100*30*time.Second), ing.Incidents[0].Start)
	require.Equal(t, d1.Add(104*30*time.Second), ing.Incidents[0].End)
	require.Equal(t, 4, ing.Incidents[0].Failed)
	require.True(t, ing.Incidents[1].Ongoing())

	out := Render(m)
	for _, want := range []string{
		"Covered range: 2026-11-01 to 2026-11-03 (UTC), 3 days. This is a partial month: it ends before 2026-11-30.",
		"Missing days: 1 (2026-11-02)",
		"| Ingestion (`slo_ingestion`) | 5760 | 5754 | 6 | 99.8958% | 99.9900% | missed | 66.6667% | 0.43 min | 3.00 min |",
		"| Dashboard (`slo_dashboard`) | 5760 | 5760 | 0 | 100.0000% | 99.9000% | not measured | 66.6667% | 4.32 min | 0.00 min |",
		"| API pipeline freshness (`slo_freshness-api`) | 2880 | 2880 | 0 | 100.0000% | none | reported only | 33.3333% | n/a | n/a |",
		"| Ingestion | 2026-11-01T00:50:00Z | 2026-11-01T00:52:00Z | 2m 00s | 4 | gateway answered 503 |",
		"| Ingestion | 2026-11-03T23:59:00Z | ongoing at end of data | at least 1m 00s | 2 | gateway answered 503 |",
	} {
		require.Contains(t, out, want)
	}
	// deterministic: the same input renders the same bytes
	m2, err := Load(dir, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, out, Render(m2))
	require.NotContains(t, out, "\u2014", "the report must not contain an em dash")
}

func TestIncidentSpansMidnight(t *testing.T) {
	dir := t.TempDir()
	d1 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	day1 := toResults(series(d1, 2880, func(i int) bool { return i == 2879 }))
	day2 := toResults(series(d2, 2880, func(i int) bool { return i == 0 }))
	writeDays(t, dir, map[time.Time]map[string][]Result{
		d1: {KeyIngestion: day1, KeyDashboard: day1, KeyFreshnessWeb: day1, KeyFreshnessAPI: day1},
		d2: {KeyIngestion: day2, KeyDashboard: day2, KeyFreshnessWeb: day2, KeyFreshnessAPI: day2},
	})
	m, err := Load(dir, d1, d2)
	require.NoError(t, err)
	require.Len(t, m.Stats[0].Incidents, 1)
	require.Equal(t, 2, m.Stats[0].Incidents[0].Failed)
	require.Equal(t, d2.Add(30*time.Second), m.Stats[0].Incidents[0].End)
}

func TestReadDayRejectsForeignResults(t *testing.T) {
	dir := t.TempDir()
	d := Day{Day: "2026-11-01", IntervalSeconds: IntervalSeconds, Endpoints: map[string][]Result{
		KeyIngestion: {{Timestamp: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Success: true}},
	}}
	b, err := json.Marshal(d)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "2026-11-01.json"), b, 0o600))
	_, err = ReadDay(dir, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	require.ErrorContains(t, err, "outside the day")
}

func TestPartialDay(t *testing.T) {
	// 06:00 to 12:00 of a day that has not ended: collected until 12:00 and
	// counted over twelve hours, not twenty-four
	rs := series(day.Add(6*time.Hour), 6*120+50, func(i int) bool { return i == 3 })
	g := &fakeGatus{max: 5000, results: allKeys(rs)}
	c := collector(t, g, 5000)
	c.Now = func() time.Time { return day.Add(13 * time.Hour) }
	_, err := c.Collect(context.Background(), day)
	require.ErrorContains(t, err, "ended")
	c.Until = day.Add(25 * time.Hour)
	_, err = c.Collect(context.Background(), day)
	require.ErrorContains(t, err, "not in the future")
	c.Until = day.Add(12 * time.Hour)
	got, err := c.Collect(context.Background(), day)
	require.NoError(t, err)
	require.NotNil(t, got.Until)
	require.Len(t, got.Endpoints[KeyIngestion], 6*120)
	dir := t.TempDir()
	_, err = WriteDay(dir, got)
	require.NoError(t, err)
	m, err := Load(dir, day, day)
	require.NoError(t, err)
	// eleven missing days count whole, the partial day twelve hours
	require.InDelta(t, 11*86400.0+12*3600, m.PeriodSeconds(), 0.001)
	out := Render(m)
	require.Contains(t, out, "Partial days: 2026-11-12 until 2026-11-12T12:00:00Z")
}

func TestHeartbeatGapsAreUnmeasured(t *testing.T) {
	// gatus writes a failed result when no push arrived in time; that is a
	// probe outage, not a platform failure
	rs := []Result{
		{Timestamp: day.Add(time.Hour), Success: true},
		{Timestamp: day.Add(time.Hour + 2*time.Minute), Success: false, Errors: []string{"heartbeat: no update received within 2m0s"}},
		{Timestamp: day.Add(time.Hour + 3*time.Minute), Success: false, Errors: []string{"event: status 503"}},
		{Timestamp: day.Add(time.Hour + 4*time.Minute), Success: true},
	}
	dir := t.TempDir()
	writeDays(t, dir, map[time.Time]map[string][]Result{day: {KeyIngestion: rs, KeyDashboard: rs, KeyFreshnessWeb: rs, KeyFreshnessAPI: rs}})
	m, err := Load(dir, day, day)
	require.NoError(t, err)
	require.Equal(t, 1, m.Gaps[KeyIngestion])
	for _, s := range m.Stats {
		require.Equal(t, 3, s.Results)
		require.Equal(t, 2, s.Successes)
		require.Len(t, s.Incidents, 1)
	}
	require.Contains(t, Render(m), "Probe gaps: slo_ingestion 1")
}

// TestVerdictCoverageFloor pins the verdict rules of docs/sla.md: one day of
// clean data in a 30 day month is not a met objective, a failure beyond the
// whole budget is missed at any coverage, and at full coverage availability
// decides.
func TestVerdictCoverageFloor(t *testing.T) {
	month := 30 * 86400.0
	perDay := 86400 / IntervalSeconds
	cases := []struct {
		name string
		s    Stats
		want string
	}{
		{"no results", Stats{}, "not measured"},
		{"one clean day", Stats{Results: perDay, Successes: perDay}, "not measured"},
		{"one day, failures beyond the month budget", Stats{Results: perDay, Successes: perDay - 10}, "missed"},
		{"just under the floor", Stats{Results: perDay * 28, Successes: perDay * 28}, "not measured"},
		{"full month clean", Stats{Results: perDay * 30, Successes: perDay * 30}, "met"},
		{"full month, availability below objective", Stats{Results: perDay * 30, Successes: perDay*30 - 9}, "missed"},
		{"full month, availability at objective", Stats{Results: perDay * 30, Successes: perDay*30 - 8}, "met"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, Verdict(c.s, 99.99, month))
		})
	}
}
