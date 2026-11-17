//go:build apipath

// TestAPIPath is the api path integration verification with the four example
// apps: each app exports with PLATFORM_OTLP_ENDPOINT pointed at the real
// scf-otelcol behind the real edge caddy (otlp.<base domain>), with real
// nats and a token created through the real control plane api, and the
// fixed load plan of load_test.go. the stack is apipath_stack_test.go.
//
//	make collector-build
//	go test -tags apipath -count=1 -timeout 90m -run TestAPIPath -v ./tests/otelapps/
//
// (make apipath does both). OTELAPPS_APPS and OTELAPPS_MODES narrow the run
// as in the stand-in suite; the modes here are cumulative-explicit,
// traces-only, exponential and legacy-semconv (python only).
package otelapps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/stepuptest"
)

const (
	metricName = "http.server.request.duration"
	spanMetric = "scf.http.server.request.duration.from_spans"
	// what the apps put in OTEL_RESOURCE_ATTRIBUTES to pose as another tenant
	forgedTenant = "11111111-1111-4111-8111-111111111111"
	forgedLabel  = "forged-tenant"
	// the strings the apps put in internal span names and span events
	markerSpan  = "SA_MARKER_internal_span"
	markerEvent = "SA_MARKER_span_event"
	// refusal and acceptance after a registry change must be this fast
	registryDeadline = 5 * time.Second
)

// canonical bucket bounds in seconds (docs/reference/api-storage.md)
var canonicalBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

var apiModes = []string{"cumulative-explicit", "traces-only", "exponential", "legacy-semconv"}

func TestAPIPath(t *testing.T) {
	sel := selectedApps()
	if len(sel) == 0 {
		t.Fatalf("OTELAPPS_APPS=%q matches no app", os.Getenv("OTELAPPS_APPS"))
	}
	s := startAPIStack(t)

	owner := s.signIn(t, 424242, "apipath-owner")
	tenantID, status := owner.enroll(t, "API path "+s.id)
	if status != "active" {
		t.Fatalf("tenant status %s after enrollment in open mode, want active", status)
	}

	files := []string{filepath.Join(s.root, "deploy/compose/examples.yml"), filepath.Join(s.root, "tests/otelapps/apipath.compose.yml")}
	baseEnv := s.composeEnv(t.TempDir(), "unused")
	var names []string
	for _, a := range sel {
		names = append(names, a.name)
	}
	composeIn(t, composeProject, files, baseEnv, append([]string{"build"}, names...)...)

	for _, a := range sel {
		for _, m := range modes {
			if !slices.Contains(apiModes, m.name) || (m.only != "" && m.only != a.name) {
				continue
			}
			if want := os.Getenv("OTELAPPS_MODES"); want != "" && !strings.Contains(","+want+",", ","+m.name+",") {
				continue
			}
			t.Run(a.name+"/"+m.name, func(t *testing.T) { s.runCase(t, owner, files, tenantID, a, m) })
		}
	}

	t.Run("only allowlisted names and attributes reached jetstream", func(t *testing.T) {
		s.checkQueue(t, tenantID)
	})
	t.Run("a revoked token is refused within 5 s", func(t *testing.T) {
		tok := owner.newToken(t, tenantID, "revocation check")
		s.waitStatus(t, tok.Token, http.StatusOK, 10*time.Second)
		start := time.Now()
		if code, body := owner.call(t, http.MethodDelete, "/api/tenants/"+tenantID+"/otlp-tokens/"+tok.ID, nil, nil); code != http.StatusNoContent {
			t.Fatalf("revoke: %d %s", code, body)
		}
		took := s.waitStatus(t, tok.Token, http.StatusUnauthorized, registryDeadline)
		t.Logf("revoked token refused %s after the revoke call started (%s after it returned)", time.Since(start).Round(time.Millisecond), took.Round(time.Millisecond))
	})
	t.Run("a suspended tenant is refused within 5 s and accepted after resume", func(t *testing.T) {
		s.checkSuspension(t, owner, tenantID)
	})
}

// composeEnv is the environment of examples.yml with apipath.compose.yml.
func (s *apiStack) composeEnv(out, token string) []string {
	return []string{
		"OTELAPPS_OUT=" + out,
		"PLATFORM_OTLP_TOKEN=" + token,
		"PLATFORM_OTLP_ENDPOINT=https://" + otlpHost,
		fmt.Sprintf("METRICS_EXPORT_INTERVAL_MS=%d", exportInterval.Milliseconds()),
		"APIPATH_NETWORK=" + s.network,
		"APIPATH_CA_DIR=" + s.caDir,
		"APIPATH_FORGED=tenant.id=" + forgedLabel + ",scf.tenant.id=" + forgedTenant + ",scf.token.id=" + forgedTenant,
	}
}

func (s *apiStack) runCase(t *testing.T, owner *user, files []string, tenantID string, a app, m mode) {
	// a token per case, so every message can be told apart by scf.token.id
	tok := owner.newToken(t, tenantID, a.name+" "+m.name)
	out := t.TempDir()
	// the tenant backend runs as uid 10001; world-writable directories keep
	// the files deletable by the test user afterwards
	for _, d := range []string{out, filepath.Join(out, "tenant"), filepath.Join(out, "platform")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	env := s.composeEnv(out, tok.Token)
	for k, v := range m.env {
		env = append(env, k+"="+v)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(composeIn(t, composeProject, files, env, "logs", "--no-color", a.name))
		}
		composeIn(t, composeProject, files, env, "down", "--volumes", "--remove-orphans")
	})
	composeIn(t, composeProject, files, env, "up", "-d", "--no-build", "--no-deps", "tenant-backend", a.name)

	base := fmt.Sprintf("http://127.0.0.1:%d", a.port)
	waitHealthy(t, base+"/healthz")
	sent, err := driveLoad(base, func() { time.Sleep(3 * exportInterval) })
	if err != nil {
		t.Fatal(err)
	}
	want := map[requestKey]int64{}
	for k, v := range sent {
		want[requestKey{k.Method, a.route(k.Route), k.Status}] = v
	}

	composeIn(t, composeProject, files, env, "stop", "--timeout", "30", a.name)
	code := strings.TrimSpace(docker(t, "inspect", "-f", "{{.State.ExitCode}}", composeProject+"-"+a.name+"-1"))
	if code != "0" {
		t.Errorf("%s exited with %s after sigterm, want 0", a.name, code)
	}
	// every export to the platform, traces included, was answered with
	// success, so no sdk logged a failure
	logs := composeIn(t, composeProject, files, env, "logs", "--no-color", a.name)
	for _, bad := range []string{"export failed", "failed to export", "Failed to export", "ExportError", "OTLPExporterError", "error sending", "certificate", "401", "Unauthorized"} {
		if strings.Contains(logs, bad) {
			t.Errorf("%s logged %q:\n%s", a.name, bad, logs)
		}
	}

	// dual export: the tenant's own backend still got everything, full
	// traces with the internal marker spans and events included
	got := waitForCounts(t, filepath.Join(out, "tenant"), a.name, m, totalRequests())
	checkMetrics(t, "tenant", got.metrics, m, want)
	checkTraces(t, "tenant", got.traces, want)

	p := s.waitQueued(t, tok.ID, m, totalRequests())
	s.checkPlatform(t, p, tenantID, tok.ID, a, m, want)
	if m.legacy {
		checkLegacySeconds(t, filepath.Join(out, "tenant", "metrics.jsonl"), a.name, p)
	}
}

// platformSummary is what reached API_METRICS for one token.
type platformSummary struct {
	messages     int
	metric       map[requestKey]int64 // http.server.request.duration, from tenant metrics
	spans        map[requestKey]int64 // span-derived
	metricPoints int
	spanPoints   int
	units        map[string]bool
	bounds       map[string]bool // explicit bounds of every point, as text
	lossy        int             // metric points marked scf.lossy
	temporality  map[string]bool
	sums         map[requestKey]float64 // latest cumulative sum per key of the tenant metric
	pointBounds  map[requestKey][]float64
	problems     []string
}

type series struct {
	key   requestKey
	attrs string
	start pcommon.Timestamp
	res   string
}

func pointKey(attrs pcommon.Map) (requestKey, error) {
	method, _ := attrs.Get("http.request.method")
	route, _ := attrs.Get("http.route")
	st, ok := attrs.Get("http.response.status_code")
	if !ok {
		return requestKey{}, fmt.Errorf("point without http.response.status_code: %v", attrs.AsRaw())
	}
	return requestKey{method.Str(), route.Str(), int(st.Int())}, nil
}

func (s *apiStack) summarize(tokenID string) platformSummary {
	p := platformSummary{metric: map[requestKey]int64{}, spans: map[requestKey]int64{}, units: map[string]bool{},
		bounds: map[string]bool{}, temporality: map[string]bool{}, sums: map[requestKey]float64{}, pointBounds: map[requestKey][]float64{}}
	type latest struct {
		t     pcommon.Timestamp
		count uint64
		sum   float64
	}
	cum := map[series]latest{}
	for _, q := range s.messages(tokenID) {
		p.messages++
		rms := q.md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			res, _ := rm.Resource().Attributes().Get("scf.resource.hash")
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				ms := sms.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					m := ms.At(k)
					if m.Type() != pmetric.MetricTypeHistogram {
						p.problems = append(p.problems, fmt.Sprintf("metric %s of type %s", m.Name(), m.Type()))
						continue
					}
					p.units[m.Unit()] = true
					h := m.Histogram()
					p.temporality[m.Name()+" "+h.AggregationTemporality().String()] = true
					dps := h.DataPoints()
					for l := 0; l < dps.Len(); l++ {
						dp := dps.At(l)
						p.bounds[fmt.Sprint(dp.ExplicitBounds().AsRaw())] = true
						key, err := pointKey(dp.Attributes())
						if err != nil {
							p.problems = append(p.problems, err.Error())
							continue
						}
						switch m.Name() {
						case spanMetric:
							p.spanPoints++
							if h.AggregationTemporality() != pmetric.AggregationTemporalityDelta {
								p.problems = append(p.problems, "span-derived point is not delta")
							}
							p.spans[key] += int64(dp.Count())
						case metricName:
							p.metricPoints++
							if v, ok := dp.Attributes().Get("scf.lossy"); ok && v.Bool() {
								p.lossy++
							}
							p.pointBounds[key] = dp.ExplicitBounds().AsRaw()
							if h.AggregationTemporality() == pmetric.AggregationTemporalityDelta {
								p.metric[key] += int64(dp.Count())
								continue
							}
							ab, _ := json.Marshal(dp.Attributes().AsRaw())
							sr := series{key, string(ab), dp.StartTimestamp(), res.Str()}
							if prev, ok := cum[sr]; !ok || dp.Timestamp() >= prev.t {
								cum[sr] = latest{dp.Timestamp(), dp.Count(), dp.Sum()}
							}
						default:
							p.problems = append(p.problems, "metric "+m.Name()+" queued")
						}
					}
				}
			}
		}
	}
	for sr, v := range cum {
		p.metric[sr.key] += int64(v.count)
		p.sums[sr.key] += v.sum
	}
	return p
}

// waitQueued drains API_METRICS until the token's span-derived requests
// (and for metric modes its metric requests) reach total, then drains once
// more after a pause so a late duplicate would be counted too.
func (s *apiStack) waitQueued(t *testing.T, tokenID string, m mode, total int64) platformSummary {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		s.drain(t)
		p := s.summarize(tokenID)
		if sum(p.spans) >= total && (!m.metrics || sum(p.metric) >= total) {
			time.Sleep(3 * time.Second)
			s.drain(t)
			return s.summarize(tokenID)
		}
		if time.Now().After(deadline) {
			t.Logf("jetstream after 60 s: %d metric and %d span-derived requests of %d", sum(p.metric), sum(p.spans), total)
			return p
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (s *apiStack) checkPlatform(t *testing.T, p platformSummary, tenantID, tokenID string, a app, m mode, want map[requestKey]int64) {
	t.Helper()
	for _, pr := range p.problems {
		t.Error(pr)
	}
	// tenant identity comes from the token: subject and resource name the
	// token's tenant although the go, python and rust apps sent forged ids
	for _, q := range s.messages(tokenID) {
		if q.subject != natsx.Subject(natsx.SubjectAPIMetrics, tenantID) {
			t.Errorf("message on %s, want the token's tenant %s", q.subject, tenantID)
		}
		rms := q.md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			at := rms.At(i).Resource().Attributes()
			for k, v := range map[string]string{"scf.tenant.id": tenantID, "scf.token.id": tokenID, "service.name": a.name} {
				if got, _ := at.Get(k); got.Str() != v {
					t.Errorf("resource %s = %q, want %q", k, got.Str(), v)
				}
			}
		}
	}

	// span-derived metric in every mode: the apps always export traces
	if d := diff(p.spans, want); d != "" {
		t.Errorf("span-derived request counts on jetstream differ from what was sent:\n%s", d)
	}
	if !m.metrics {
		if p.metricPoints != 0 {
			t.Errorf("traces-only run queued %d %s points", p.metricPoints, metricName)
		}
	} else if d := diff(p.metric, want); d != "" {
		t.Errorf("%s request counts on jetstream differ from what was sent:\n%s", metricName, d)
	}

	// routes are the apps' templates after normalization, never raw paths
	allowed := map[string]bool{}
	for k := range expectedCounts() {
		allowed[a.route(k.Route)] = true
	}
	for _, mp := range []map[requestKey]int64{p.spans, p.metric} {
		for k := range mp {
			if !allowed[k.Route] {
				t.Errorf("route %q queued, want one of the app's templates", k.Route)
			}
		}
	}

	if len(p.units) != 1 || !p.units["s"] {
		t.Errorf("units %v, want only s", p.units)
	}
	canon := fmt.Sprint(canonicalBounds)
	if m.exponential {
		// re-bucketed onto the canonical explicit layout and marked lossy
		if p.lossy != p.metricPoints || p.metricPoints == 0 {
			t.Errorf("%d of %d metric points marked scf.lossy, want all", p.lossy, p.metricPoints)
		}
		if len(p.bounds) != 1 || !p.bounds[canon] {
			t.Errorf("bounds %v, want only the canonical %s", keys(p.bounds), canon)
		}
	} else if p.lossy != 0 {
		t.Errorf("%d explicit points marked scf.lossy", p.lossy)
	}
	if !m.metrics && (len(p.bounds) != 1 || !p.bounds[canon]) {
		t.Errorf("span-derived bounds %v, want the canonical %s", keys(p.bounds), canon)
	}
	t.Logf("jetstream: %d messages, %d metric requests in %d points, %d span-derived requests in %d points, temporality %v, bounds %v",
		p.messages, sum(p.metric), p.metricPoints, sum(p.spans), p.spanPoints, keys(p.temporality), keys(p.bounds))
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkLegacySeconds compares the python app's legacy http.server.duration
// in milliseconds, as its own backend got it, with what reached jetstream:
// the same series in seconds, sums and bounds scaled by 1/1000.
func checkLegacySeconds(t *testing.T, tenantFile, service string, p platformSummary) {
	t.Helper()
	type latest struct {
		t      int64
		sum    float64
		bounds []float64
	}
	type stream struct {
		key   requestKey
		start string
	}
	ms := map[stream]latest{}
	err := readLines(tenantFile, func(b []byte) error {
		var l metricsLine
		if err := json.Unmarshal(b, &l); err != nil {
			return err
		}
		for _, rm := range l.ResourceMetrics {
			if rm.Resource.Attributes.get("service.name") != service {
				continue
			}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name != "http.server.duration" || m.Unit != "ms" || m.Histogram == nil {
						continue
					}
					for _, dp := range m.Histogram.DataPoints {
						st, err := strconv.Atoi(dp.Attributes.get("http.status_code"))
						if err != nil || dp.Sum == nil {
							return fmt.Errorf("legacy point without status or sum: %v", dp.Attributes)
						}
						k := stream{requestKey{dp.Attributes.get("http.method"), dp.Attributes.get("http.target"), st}, dp.StartTimeNano}
						ts, _ := strconv.ParseInt(dp.TimeNano, 10, 64)
						if prev, ok := ms[k]; !ok || ts >= prev.t {
							ms[k] = latest{ts, *dp.Sum, dp.ExplicitBounds}
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("the tenant backend got no legacy http.server.duration in ms")
	}
	sums := map[requestKey]float64{}
	bounds := map[requestKey][]float64{}
	for k, v := range ms {
		sums[k.key] += v.sum
		bounds[k.key] = v.bounds
	}
	for k, msSum := range sums {
		got, ok := p.sums[k]
		if !ok {
			t.Errorf("%s: no seconds series on jetstream", k)
			continue
		}
		if math.Abs(got-msSum/1000) > 1e-9*math.Max(1, msSum) {
			t.Errorf("%s: sum %.9f s on jetstream, tenant backend %.6f ms", k, got, msSum)
		}
		pb := p.pointBounds[k]
		if len(pb) != len(bounds[k]) || len(pb) == 0 {
			t.Errorf("%s: bounds %v on jetstream, %v ms at the tenant backend", k, pb, bounds[k])
			continue
		}
		for i := range pb {
			if math.Abs(pb[i]-bounds[k][i]/1000) > 1e-12 {
				t.Errorf("%s: bound %d is %v, want %v", k, i, pb[i], bounds[k][i]/1000)
			}
		}
	}
	for k := range sums {
		t.Logf("%s: %.3f ms at the tenant backend, %.6f s on jetstream, top bound %v s", k, sums[k], p.sums[k], p.pointBounds[k][len(p.pointBounds[k])-1])
		break
	}
}

// checkQueue scans every message read in the run: only the two metric
// names, only allowlisted attributes, only the test tenant's subject, and
// no byte of span names, span events or forged ids.
func (s *apiStack) checkQueue(t *testing.T, tenantID string) {
	s.drain(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		t.Fatal("no messages")
	}
	resAllowed := map[string]bool{"service.name": true, "scf.tenant.id": true, "scf.token.id": true, "scf.resource.hash": true, "scf.flags": true}
	dpAllowed := map[string]bool{"http.request.method": true, "http.route": true, "http.response.status_code": true, "error.type": true, "scf.lossy": true, "scf.stream": true}
	names := map[string]int{}
	for _, q := range s.seen {
		if q.subject != natsx.Subject(natsx.SubjectAPIMetrics, tenantID) {
			t.Errorf("message on %s", q.subject)
		}
		for _, bad := range []string{"SA_MARKER", markerSpan, markerEvent, forgedTenant, forgedLabel, "account.lookup", "url.path", "http.target", "user_agent"} {
			if bytes.Contains(q.data, []byte(bad)) {
				t.Errorf("message on %s contains %q", q.subject, bad)
			}
		}
		rms := q.md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			for k := range rms.At(i).Resource().Attributes().All() {
				if !resAllowed[k] {
					t.Errorf("resource attribute %s queued", k)
				}
			}
			sms := rms.At(i).ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				ms := sms.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					m := ms.At(k)
					names[m.Name()]++
					if m.Name() != metricName && m.Name() != spanMetric {
						t.Errorf("metric %s queued", m.Name())
					}
					if m.Type() != pmetric.MetricTypeHistogram {
						t.Errorf("metric type %s queued", m.Type())
						continue
					}
					dps := m.Histogram().DataPoints()
					for l := 0; l < dps.Len(); l++ {
						if dps.At(l).Exemplars().Len() != 0 {
							t.Error("exemplars queued")
						}
						for a := range dps.At(l).Attributes().All() {
							if !dpAllowed[a] {
								t.Errorf("attribute %s queued", a)
							}
						}
					}
				}
			}
		}
	}
	t.Logf("%d messages scanned, metrics %v", len(s.seen), names)
}

// waitStatus posts an empty otlp metrics request through the edge until it
// gets want, and returns how long that took.
func (s *apiStack) waitStatus(t *testing.T, token string, want int, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	last, body := 0, ""
	for time.Since(start) <= within {
		last, body = s.otlpPost(t, "/v1/metrics", token, emptyMetrics(t))
		if last == want {
			return time.Since(start)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no %d within %s; last %d %s", want, within, last, body)
	return 0
}

// checkSuspension suspends the tenant through the operator api: the
// collector must refuse the tenant's token within 5 s, token creation is
// refused, and after resume the token is accepted again.
func (s *apiStack) checkSuspension(t *testing.T, owner *user, tenantID string) {
	tok := owner.newToken(t, tenantID, "suspension check")
	s.waitStatus(t, tok.Token, http.StatusOK, 10*time.Second)
	op := s.signIn(t, operatorGitHubID, "sa-operator")
	// operator routes need a passkey step-up on top of the sign-in; the
	// control plane's roster bootstrap opened the first key's enrollment
	var keys stepuptest.Keys
	keys.StepUpMe(t, appOrigin, func(method, path string, body any) (int, []byte) {
		code, b := op.call(t, method, path, body, nil)
		return code, []byte(b)
	})

	start := time.Now()
	if code, body := op.call(t, http.MethodPost, "/api/operator/tenants/"+tenantID+"/suspend", map[string]any{"reason": "api path check"}, nil); code != http.StatusOK {
		t.Fatalf("suspend: %d %s", code, body)
	}
	returned := time.Since(start)
	took := s.waitStatus(t, tok.Token, http.StatusUnauthorized, registryDeadline-returned)
	t.Logf("suspended tenant's token refused %s after the suspend call started", (returned + took).Round(time.Millisecond))

	if code, body := owner.call(t, http.MethodPost, "/api/tenants/"+tenantID+"/otlp-tokens", map[string]any{"name": "while suspended"}, nil); code != http.StatusConflict || !strings.Contains(body, "tenant_suspended") {
		t.Errorf("token create while suspended: %d %s, want 409 tenant_suspended", code, body)
	}

	start = time.Now()
	if code, body := op.call(t, http.MethodPost, "/api/operator/tenants/"+tenantID+"/resume", map[string]any{}, nil); code != http.StatusOK {
		t.Fatalf("resume: %d %s", code, body)
	}
	returned = time.Since(start)
	took = s.waitStatus(t, tok.Token, http.StatusOK, registryDeadline-returned)
	t.Logf("token accepted again %s after the resume call started", (returned + took).Round(time.Millisecond))
}
