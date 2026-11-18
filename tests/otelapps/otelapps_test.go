//go:build otelapps || apipath

// package otelapps proves the otel example apps dual-export complete
// metrics and traces. each case starts deploy/compose/examples.yml with one
// app and the two stock collectors, drives the app with the fixed load plan,
// stops the app so its sdk flushes, and compares what the tenant backend and
// the platform stand-in wrote to disk with what the driver sent.
//
//	go test -tags otelapps -count=1 -timeout 60m -v ./tests/otelapps/
//
// OTELAPPS_APPS limits the run to a comma separated list of app names and
// OTELAPPS_MODES to a comma separated list of mode names.
//
// with the build tag apipath the same apps and load plan run against the
// real platform instead of the stand-in (apipath_test.go, make apipath).
package otelapps

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type app struct {
	name  string
	port  int
	route func(canonical string) string
	// probe is the route under which the app's instrumentation records the
	// /healthz checks the tests poll, empty when the app keeps them out of
	// its telemetry. the java agent records every request tomcat serves; the
	// platform marks such routes as infrastructure traffic instead
	// (docs/onboarding/api/what-gets-dropped.md), so the cases leave them out
	// of the request counts and check that they were recorded.
	probe string
}

var apps = []app{
	{"otel-node-express", 47201, func(r string) string { return strings.ReplaceAll(r, "{id}", ":id") }, ""},
	{"otel-go-nethttp", 47202, func(r string) string { return r }, ""},
	{"otel-python-fastapi", 47203, func(r string) string { return strings.ReplaceAll(r, "{id}", "{account_id}") }, ""},
	{"otel-rust-actix", 47204, func(r string) string { return r }, ""},
	{"otel-java-spring", 47205, func(r string) string { return r }, "/healthz"},
}

type mode struct {
	name        string
	env         map[string]string
	metrics     bool
	temporality int
	exponential bool
	legacy      bool
	only        string // limits the case to one app
}

var modes = []mode{
	{name: "cumulative-explicit", env: map[string]string{}, metrics: true, temporality: temporalityCumulative},
	{name: "delta", env: map[string]string{"METRICS_TEMPORALITY": "delta"}, metrics: true, temporality: temporalityDelta},
	{name: "exponential", env: map[string]string{"METRICS_HISTOGRAM": "exponential"}, metrics: true, temporality: temporalityCumulative, exponential: true},
	{name: "delta-exponential", env: map[string]string{"METRICS_TEMPORALITY": "delta", "METRICS_HISTOGRAM": "exponential"}, metrics: true, temporality: temporalityDelta, exponential: true},
	{name: "traces-only", env: map[string]string{"EXPORT_SIGNALS": "traces"}},
	{name: "legacy-semconv", env: map[string]string{"OTEL_SEMCONV_STABILITY_OPT_IN": ""}, metrics: true, temporality: temporalityCumulative, legacy: true, only: "otel-python-fastapi"},
}

const (
	token          = "sa_otlp_otelapps_test_token"
	exportInterval = 2 * time.Second
	healthWait     = 2 * time.Minute
)

func repoRoot(t *testing.T) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// composeIn runs docker compose on the given files under a project name.
func composeIn(t *testing.T, project string, files []string, env []string, args ...string) string {
	t.Helper()
	full := []string{"compose"}
	for _, f := range files {
		full = append(full, "-f", f)
	}
	full = append(append(full, "-p", project), args...)
	cmd := exec.Command("docker", full...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(full, " "), err, out)
	}
	return string(out)
}

func selectedApps() []app {
	want := os.Getenv("OTELAPPS_APPS")
	if want == "" {
		return apps
	}
	var out []app
	for _, a := range apps {
		for _, w := range strings.Split(want, ",") {
			if strings.TrimSpace(w) == a.name {
				out = append(out, a)
			}
		}
	}
	return out
}

// waitHealthy allows two minutes: a jvm with the java agent attached takes the
// longest to start, and on a loaded host that runs past one minute
func waitHealthy(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(healthWait)
	for time.Now().Before(deadline) {
		res, err := http.Get(url)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s not healthy within %s", url, healthWait)
}

type received struct {
	metrics durationSummary
	traces  traceSummary
	// requests recorded under the app's probe route, taken out of the
	// summaries above
	probeMetrics, probeSpans int64
}

// withoutProbe moves the requests recorded under route out of counts and
// returns how many there were. an empty route moves nothing.
func withoutProbe(counts map[requestKey]int64, route string) int64 {
	var n int64
	for k, v := range counts {
		if route != "" && k.Route == route {
			n += v
			delete(counts, k)
		}
	}
	return n
}

// waitForCounts polls the collector files until the server span total (and
// for metric modes the request count) reaches total, or 30 seconds pass. the
// file exporter flushes on an interval, so the last batch can trail the
// app's exit by a moment.
func waitForCounts(t *testing.T, dir string, a app, m mode, total int64) received {
	t.Helper()
	var r received
	deadline := time.Now().Add(30 * time.Second)
	for {
		var err error
		if r.metrics, err = summarizeMetrics(filepath.Join(dir, "metrics.jsonl"), a.name); err != nil {
			t.Fatal(err)
		}
		if r.traces, err = summarizeTraces(filepath.Join(dir, "traces.jsonl"), a.name); err != nil {
			t.Fatal(err)
		}
		r.probeMetrics = withoutProbe(r.metrics.Counts, a.probe)
		r.probeSpans = withoutProbe(r.traces.Server, a.probe)
		done := sum(r.traces.Server) >= total && (!m.metrics || sum(r.metrics.Counts) >= total)
		if done || time.Now().After(deadline) {
			return r
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func sum(m map[requestKey]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}

func diff(got, want map[requestKey]int64) string {
	keys := map[requestKey]bool{}
	for k := range got {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	var lines []string
	for k := range keys {
		if got[k] != want[k] {
			lines = append(lines, fmt.Sprintf("  %s: got %d want %d", k, got[k], want[k]))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func checkMetrics(t *testing.T, backend string, s durationSummary, m mode, want map[requestKey]int64) {
	t.Helper()
	if !m.metrics {
		if s.DataPoints != 0 || len(s.OtherMetrics) != 0 {
			t.Errorf("%s: traces-only run exported metrics: %d duration points, others %v", backend, s.DataPoints, s.OtherMetrics)
		}
		return
	}
	wantName, wantUnit := "http.server.request.duration", "s"
	if m.legacy {
		wantName, wantUnit = "http.server.duration", "ms"
	}
	if len(s.Names) != 1 || !s.Names[wantName] {
		t.Errorf("%s: duration metric names %v, want only %s", backend, s.Names, wantName)
	}
	if len(s.Units) != 1 || !s.Units[wantUnit] {
		t.Errorf("%s: duration units %v, want %s", backend, s.Units, wantUnit)
	}
	if len(s.Temporality) != 1 || !s.Temporality[m.temporality] {
		t.Errorf("%s: temporality %v, want %d", backend, s.Temporality, m.temporality)
	}
	if s.Exponential != m.exponential || s.Explicit == m.exponential {
		t.Errorf("%s: exponential=%v explicit=%v, want exponential=%v", backend, s.Exponential, s.Explicit, m.exponential)
	}
	// more than one collection timestamp proves a periodic export reached
	// the backend before the final one on shutdown
	if len(s.Exports) < 2 {
		t.Errorf("%s: %d collection timestamps, want periodic exports before shutdown", backend, len(s.Exports))
	}
	if d := diff(s.Counts, want); d != "" {
		t.Errorf("%s: metric request counts differ from what was sent:\n%s", backend, d)
	}
	t.Logf("%s: %s %s temporality=%d exponential=%v, %d requests in %d points over %d collections", backend, wantName, wantUnit,
		m.temporality, m.exponential, sum(s.Counts), s.DataPoints, len(s.Exports))
}

func checkTraces(t *testing.T, backend string, s traceSummary, want map[requestKey]int64) {
	t.Helper()
	if d := diff(s.Server, want); d != "" {
		t.Errorf("%s: server span counts differ from what was sent:\n%s", backend, d)
	}
	// every request that reaches an account lookup makes one marker span
	// with one event; both backends of this setup record traces unfiltered
	lookups := sum(want) - want[requestKey{"POST", "/accounts", 201}] - want[requestKey{"POST", "/accounts", 400}]
	if int64(s.MarkerSpans) != lookups || int64(s.MarkerEvents) != lookups {
		t.Errorf("%s: %d marker spans and %d marker events, want %d each", backend, s.MarkerSpans, s.MarkerEvents, lookups)
	}
	t.Logf("%s: %d server spans, %d internal marker spans, %d marker events", backend, sum(s.Server), s.MarkerSpans, s.MarkerEvents)
}

// checkProbe proves an app with a probe route recorded the health checks the
// test sent, so leaving that route out of the counts hid nothing else.
func checkProbe(t *testing.T, backend string, a app, m mode, r received) {
	t.Helper()
	if a.probe == "" {
		return
	}
	if r.probeSpans == 0 || (m.metrics && r.probeMetrics == 0) {
		t.Errorf("%s: %d server spans and %d metric requests under the probe route %s, want the health checks", backend, r.probeSpans, r.probeMetrics, a.probe)
	}
	t.Logf("%s: %d health checks recorded under %s (spans), %d (metrics), left out of the counts", backend, r.probeSpans, a.probe, r.probeMetrics)
}
