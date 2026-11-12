//go:build otelapps

// package otelapps proves the this stage example apps dual-export complete
// metrics and traces. each case starts deploy/compose/examples.yml with one
// app and the two stock collectors, drives the app with the fixed load plan,
// stops the app so its sdk flushes, and compares what the tenant backend and
// the platform stand-in wrote to disk with what the driver sent.
//
//	go test -tags otelapps -count=1 -timeout 60m -v ./tests/otelapps/
//
// OTELAPPS_APPS limits the run to a comma separated list of app names and
// OTELAPPS_MODES to a comma separated list of mode names.
package otelapps

import (
	"bytes"
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
}

var apps = []app{
	{"otel-node-express", 47201, func(r string) string { return strings.ReplaceAll(r, "{id}", ":id") }},
	{"otel-go-nethttp", 47202, func(r string) string { return r }},
	{"otel-python-fastapi", 47203, func(r string) string { return strings.ReplaceAll(r, "{id}", "{account_id}") }},
	{"otel-rust-actix", 47204, func(r string) string { return r }},
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
)

func repoRoot(t *testing.T) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func compose(t *testing.T, env []string, args ...string) string {
	t.Helper()
	full := append([]string{"compose", "-f", filepath.Join(repoRoot(t), "deploy/compose/examples.yml"), "-p", "sa-otelapps-test"}, args...)
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

func TestOtelApps(t *testing.T) {
	sel := selectedApps()
	if len(sel) == 0 {
		t.Fatalf("OTELAPPS_APPS=%q matches no app", os.Getenv("OTELAPPS_APPS"))
	}
	// one build up front, so cases only start containers
	baseEnv := []string{"OTELAPPS_OUT=" + t.TempDir(), "PLATFORM_OTLP_TOKEN=" + token}
	names := []string{}
	for _, a := range sel {
		names = append(names, a.name)
	}
	compose(t, baseEnv, append([]string{"build"}, names...)...)

	for _, a := range sel {
		for _, m := range modes {
			if m.only != "" && m.only != a.name {
				continue
			}
			if want := os.Getenv("OTELAPPS_MODES"); want != "" && !strings.Contains(","+want+",", ","+m.name+",") {
				continue
			}
			t.Run(a.name+"/"+m.name, func(t *testing.T) { runCase(t, a, m) })
		}
	}
}

func runCase(t *testing.T, a app, m mode) {
	out := t.TempDir()
	// the collectors run as uid 10001; precreated world-writable directories
	// keep the files deletable by the test user afterwards
	for _, d := range []string{out, filepath.Join(out, "tenant"), filepath.Join(out, "platform")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"OTELAPPS_OUT=" + out, "PLATFORM_OTLP_TOKEN=" + token, fmt.Sprintf("METRICS_EXPORT_INTERVAL_MS=%d", exportInterval.Milliseconds())}
	for k, v := range m.env {
		env = append(env, k+"="+v)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(compose(t, env, "logs", "--no-color", a.name))
		}
		compose(t, env, "down", "--volumes", "--remove-orphans")
	})
	compose(t, env, "up", "-d", "--no-build", "tenant-backend", "platform-standin", a.name)

	base := fmt.Sprintf("http://127.0.0.1:%d", a.port)
	waitHealthy(t, base+"/healthz")
	checkStandinAuth(t)

	// three export intervals of pause, so at least one periodic export
	// happens mid-run and the final one on shutdown carries the rest
	sent, err := driveLoad(base, func() { time.Sleep(3 * exportInterval) })
	if err != nil {
		t.Fatal(err)
	}
	want := map[requestKey]int64{}
	for k, v := range sent {
		want[requestKey{k.Method, a.route(k.Route), k.Status}] = v
	}
	for k, v := range expectedCounts() {
		if sent[k] != v {
			t.Fatalf("driver sent %d for %s, plan says %d", sent[k], k, v)
		}
	}

	// stopping sends sigterm; every app flushes both providers before exiting
	compose(t, env, "stop", "--timeout", "30", a.name)
	code := strings.TrimSpace(dockerOut(t, "inspect", "-f", "{{.State.ExitCode}}", "sa-otelapps-test-"+a.name+"-1"))
	if code != "0" {
		t.Errorf("%s exited with %s after sigterm, want 0", a.name, code)
	}
	logs := compose(t, env, "logs", "--no-color", a.name)
	for _, bad := range []string{"export failed", "failed to export", "Failed to export", "ExportError", "OTLPExporterError", "error sending"} {
		if strings.Contains(logs, bad) {
			t.Errorf("%s logged %q:\n%s", a.name, bad, logs)
		}
	}

	for _, backend := range []string{"tenant", "platform"} {
		dir := filepath.Join(out, backend)
		got := waitForCounts(t, dir, a.name, m, totalRequests())
		checkMetrics(t, backend, got.metrics, m, want)
		checkTraces(t, backend, got.traces, want)
	}
}

func dockerOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func waitHealthy(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
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
	t.Fatalf("%s not healthy within 60s", url)
}

// checkStandinAuth proves the platform stand-in refuses exports without the
// bearer token, so a passing case means the apps really sent it.
func checkStandinAuth(t *testing.T) {
	t.Helper()
	body := []byte(`{"resourceMetrics":[]}`)
	for _, h := range []string{"", "Bearer wrong"} {
		var res *http.Response
		var err error
		for i := 0; i < 50; i++ {
			req, _ := http.NewRequest("POST", "http://127.0.0.1:47210/v1/metrics", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if h != "" {
				req.Header.Set("Authorization", h)
			}
			if res, err = http.DefaultClient.Do(req); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("platform stand-in answered %d with authorization %q, want 401", res.StatusCode, h)
		}
	}
}

type received struct {
	metrics durationSummary
	traces  traceSummary
}

// waitForCounts polls the collector files until the server span total (and
// for metric modes the request count) reaches total, or 30 seconds pass. the
// file exporter flushes on an interval, so the last batch can trail the
// app's exit by a moment.
func waitForCounts(t *testing.T, dir, service string, m mode, total int64) received {
	t.Helper()
	var r received
	deadline := time.Now().Add(30 * time.Second)
	for {
		var err error
		if r.metrics, err = summarizeMetrics(filepath.Join(dir, "metrics.jsonl"), service); err != nil {
			t.Fatal(err)
		}
		if r.traces, err = summarizeTraces(filepath.Join(dir, "traces.jsonl"), service); err != nil {
			t.Fatal(err)
		}
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
