package tenantguardprocessor

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processortest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/justmert/veristar/collector/internal/schema"
	"github.com/justmert/veristar/collector/tenantauthextension"
	"github.com/justmert/veristar/internal/registry"
)

const seed = "SBU2RRGLXH3E5CQHTD3ODLDF2BWDCYUSSBLLZ5GNW7JXHDIYKXZWHOKR"
const account = "GA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVSGZ"

var testCfg = &Config{RequestsPerSecond: 1000, RequestsBurst: 1000, ItemsPerSecond: 1e6, ItemsBurst: 1e6, IdleBucketTTL: time.Minute}

func authed(tenant, token string) context.Context {
	return tenantauthextension.ContextWith(context.Background(), registry.OTLPToken{TenantID: tenant, TokenID: token, Status: registry.StatusActive})
}

func tenantMetrics() pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	ra := rm.Resource().Attributes()
	ra.PutStr("service.name", "api")
	ra.PutStr("scf.tenant.id", uuid.NewString()) // forged
	ra.PutStr("tenant.id", "other")              // forged
	ra.PutStr("host.name", "box-1")
	ra.PutStr("service.instance.id", "pod-7")
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("io.opentelemetry.http")
	sm.Scope().Attributes().PutStr("secret", "x")

	h := sm.Metrics().AppendEmpty()
	h.SetName(schema.MetricDuration)
	h.SetUnit("s")
	h.SetDescription("duration")
	dp := h.SetEmptyHistogram().DataPoints().AppendEmpty()
	dp.Attributes().PutStr("http.request.method", "GET")
	dp.Attributes().PutStr("http.route", "/accounts/"+account)
	dp.Attributes().PutInt("http.response.status_code", 200)
	dp.Attributes().PutStr("url.full", "https://x/accounts/"+account+"?k="+seed)
	dp.Attributes().PutStr("client.address", "203.0.113.9")
	dp.SetCount(3)
	dp.ExplicitBounds().FromRaw(schema.Bounds)
	dp.BucketCounts().FromRaw(make([]uint64, 15))
	ex := dp.Exemplars().AppendEmpty()
	ex.FilteredAttributes().PutStr("user", "u1")

	// the legacy name comes from another library (its own scope), so it is
	// not the http/dup copy of the stable metric above and is kept
	sm2 := rm.ScopeMetrics().AppendEmpty()
	sm2.Scope().SetName("legacy-lib")
	legacy := sm2.Metrics().AppendEmpty()
	legacy.SetName(schema.MetricLegacyDuration)
	legacy.SetUnit("ms")
	ldp := legacy.SetEmptyHistogram().DataPoints().AppendEmpty()
	ldp.Attributes().PutStr("http.method", "PROPFIND")
	ldp.Attributes().PutStr("http.target", "/keys/"+seed+"?x=1")
	ldp.Attributes().PutStr("http.status_code", "503")
	ldp.Attributes().PutStr("error.type", "timeout")

	exp := sm.Metrics().AppendEmpty()
	exp.SetName(schema.MetricDuration)
	exp.SetUnit("s")
	edp := exp.SetEmptyExponentialHistogram().DataPoints().AppendEmpty()
	edp.Attributes().PutStr("http.request.method", "post")
	edp.SetCount(1)

	for _, drop := range []struct {
		name, unit string
		gauge      bool
	}{
		{schema.MetricFromSpans, "s", false},
		{schema.MetricDuration, "ms", false},
		{schema.MetricLegacyDuration, "us", false},
		{"http.client.request.duration", "s", false},
		{"db.client.operation.duration", "s", false},
		{schema.MetricDuration, "s", true},
	} {
		m := sm.Metrics().AppendEmpty()
		m.SetName(drop.name)
		m.SetUnit(drop.unit)
		if drop.gauge {
			m.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(1)
		} else {
			m.SetEmptyHistogram().DataPoints().AppendEmpty().SetCount(1)
		}
	}
	return md
}

func TestMetricsFilteredAndStamped(t *testing.T) {
	tenant, token := uuid.NewString(), uuid.NewString()
	g := newGuard(testCfg, processortest.NewNopSettings(Type).Logger, time.Now)
	md := tenantMetrics()
	origHash := resourceHash(md.ResourceMetrics().At(0).Resource().Attributes())
	out, err := g.processMetrics(authed(tenant, token), md)
	if err != nil {
		t.Fatal(err)
	}
	if out.ResourceMetrics().Len() != 1 {
		t.Fatalf("resources = %d", out.ResourceMetrics().Len())
	}
	rm := out.ResourceMetrics().At(0)
	ra := rm.Resource().Attributes()
	want := map[string]any{
		"service.name": "api", "scf.tenant.id": tenant, "scf.token.id": token,
		"scf.resource.hash": origHash, "scf.flags": schema.FlagSecretSeed,
	}
	if got := ra.AsRaw(); len(got) != len(want) {
		t.Fatalf("resource attributes %v", got)
	}
	for k, v := range want {
		if got := ra.AsRaw()[k]; got != v {
			t.Errorf("resource %s = %v, want %v", k, got, v)
		}
	}
	sm := rm.ScopeMetrics().At(0)
	if sm.Scope().Name() != "" || sm.Scope().Attributes().Len() != 0 {
		t.Errorf("scope not cleared")
	}
	ms := sm.Metrics()
	if ms.Len() != 2 || rm.ScopeMetrics().Len() != 2 {
		var names []string
		for i := 0; i < ms.Len(); i++ {
			names = append(names, ms.At(i).Name()+"/"+ms.At(i).Unit())
		}
		t.Fatalf("kept metrics %v", names)
	}
	stable := ms.At(0).Histogram().DataPoints().At(0)
	if got := stable.Attributes().AsRaw(); len(got) != 3 || got["http.route"] != "/accounts/{account}" || got["http.request.method"] != "GET" || got["http.response.status_code"] != int64(200) {
		t.Errorf("stable attrs %v", got)
	}
	if stable.Exemplars().Len() != 0 {
		t.Errorf("exemplars kept")
	}
	if ms.At(0).Description() != "" {
		t.Errorf("description kept")
	}
	lm := rm.ScopeMetrics().At(1).Metrics().At(0)
	leg := lm.Histogram().DataPoints().At(0).Attributes().AsRaw()
	if leg["http.request.method"] != "_OTHER" || leg["http.route"] != "/keys/{redacted-secret}" || leg["http.response.status_code"] != int64(503) || leg["error.type"] != "timeout" || len(leg) != 4 {
		t.Errorf("legacy attrs %v", leg)
	}
	if lm.Name() != schema.MetricLegacyDuration || lm.Unit() != "ms" {
		t.Errorf("legacy metric renamed early: %s %s", lm.Name(), lm.Unit())
	}
	// semconv lets instrumentation that treats methods case-insensitively
	// record the canonical name, so "post" is POST
	if m := ms.At(1).ExponentialHistogram().DataPoints().At(0).Attributes().AsRaw(); m["http.request.method"] != "POST" {
		t.Errorf("exp attrs %v", m)
	}
	// no byte of the seed anywhere
	var pm pmetric.ProtoMarshaler
	b, err := pm.MarshalMetrics(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{seed, "203.0.113.9", "box-1", "pod-7", "io.opentelemetry", "u1", "other"} {
		if strings.Contains(string(b), s) {
			t.Errorf("output contains %q", s)
		}
	}
}

func TestResourceHashDistinguishesProcesses(t *testing.T) {
	a, b := tenantMetrics(), tenantMetrics()
	// the forged tenant id differs between the two (uuid), so align it
	a.ResourceMetrics().At(0).Resource().Attributes().PutStr("scf.tenant.id", "x")
	b.ResourceMetrics().At(0).Resource().Attributes().PutStr("scf.tenant.id", "x")
	ha := resourceHash(a.ResourceMetrics().At(0).Resource().Attributes())
	if hb := resourceHash(b.ResourceMetrics().At(0).Resource().Attributes()); ha != hb {
		t.Fatal("same resource, different hash")
	}
	b.ResourceMetrics().At(0).Resource().Attributes().PutStr("service.instance.id", "pod-8")
	if hb := resourceHash(b.ResourceMetrics().At(0).Resource().Attributes()); ha == hb {
		t.Fatal("different resource, same hash")
	}
	// type is part of the hash: int 1 and string "1" differ
	c, d := pcommon.NewMap(), pcommon.NewMap()
	c.PutInt("k", 1)
	d.PutStr("k", "1")
	if resourceHash(c) == resourceHash(d) {
		t.Fatal("type not hashed")
	}
}

func TestRefusesWithoutAuth(t *testing.T) {
	g := newGuard(testCfg, processortest.NewNopSettings(Type).Logger, time.Now)
	ctx := context.Background()
	if _, err := g.processMetrics(ctx, tenantMetrics()); status.Code(err) != codes.Unauthenticated {
		t.Errorf("metrics: %v", err)
	}
	if _, err := g.processTraces(ctx, ptrace.NewTraces()); status.Code(err) != codes.Unauthenticated {
		t.Errorf("traces: %v", err)
	}
	if _, err := g.processLogs(ctx, plog.NewLogs()); status.Code(err) != codes.Unauthenticated {
		t.Errorf("logs: %v", err)
	}
}

func spans() ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "api")
	rs.Resource().Attributes().PutStr("scf.tenant.id", "forged")
	ss := rs.ScopeSpans().AppendEmpty()
	add := func(kind ptrace.SpanKind, name string, attrs map[string]any) ptrace.Span {
		sp := ss.Spans().AppendEmpty()
		sp.SetKind(kind)
		sp.SetName(name)
		_ = sp.Attributes().FromRaw(attrs)
		sp.SetStartTimestamp(pcommon.Timestamp(1_000_000_000))
		sp.SetEndTimestamp(pcommon.Timestamp(1_250_000_000))
		sp.Events().AppendEmpty().SetName("event-marker")
		sp.Links().AppendEmpty().Attributes().PutStr("link", "link-marker")
		sp.Status().SetMessage("status-marker")
		return sp
	}
	add(ptrace.SpanKindServer, "GET /accounts/"+account, map[string]any{"http.request.method": "GET", "url.path": "/accounts/" + account, "http.response.status_code": 200, "user.id": "attr-marker"})
	add(ptrace.SpanKindServer, "GET legacy", map[string]any{"http.method": "GET", "http.target": "/ledgers/42?x=1", "http.status_code": 404})
	add(ptrace.SpanKindServer, "POST", map[string]any{"http.request.method": "POST", "http.route": "/tx/:hash", "url.path": "/tx/abc"})
	add(ptrace.SpanKindClient, "client-marker", map[string]any{"http.request.method": "GET"})
	add(ptrace.SpanKindInternal, "internal-marker", map[string]any{"http.request.method": "GET"})
	add(ptrace.SpanKindServer, "rpc-marker", map[string]any{"rpc.system": "grpc"})
	return td
}

func TestTracesFiltered(t *testing.T) {
	tenant, token := uuid.NewString(), uuid.NewString()
	g := newGuard(testCfg, processortest.NewNopSettings(Type).Logger, time.Now)
	out, err := g.processTraces(authed(tenant, token), spans())
	if err != nil {
		t.Fatal(err)
	}
	rs := out.ResourceSpans().At(0)
	if v, _ := rs.Resource().Attributes().Get("scf.tenant.id"); v.Str() != tenant {
		t.Fatalf("tenant %v", v.Str())
	}
	sp := rs.ScopeSpans().At(0).Spans()
	if sp.Len() != 3 {
		t.Fatalf("kept %d spans", sp.Len())
	}
	wantRoutes := []string{"/accounts/{account}", "/ledgers/{id}", "/tx/:hash"}
	for i := 0; i < sp.Len(); i++ {
		s := sp.At(i)
		r, _ := s.Attributes().Get("http.route")
		if r.Str() != wantRoutes[i] {
			t.Errorf("span %d route %q", i, r.Str())
		}
		if s.Events().Len() != 0 || s.Links().Len() != 0 || s.Status().Message() != "" {
			t.Errorf("span %d keeps events, links or status message", i)
		}
		for k := range s.Attributes().All() {
			if k != "http.request.method" && k != "http.route" && k != "http.response.status_code" && k != "error.type" && k != schema.AttrSpanMinute {
				t.Errorf("span %d keeps %s", i, k)
			}
		}
	}
	var pm ptrace.ProtoMarshaler
	b, _ := pm.MarshalTraces(out)
	for _, m := range []string{"marker", account, "forged"} {
		if strings.Contains(string(b), m) {
			t.Errorf("output contains %q", m)
		}
	}
}

func TestLogsDiscarded(t *testing.T) {
	sink := new(consumertest.LogsSink)
	cfg := *testCfg
	p, err := NewFactory().CreateLogs(context.Background(), processortest.NewNopSettings(Type), &cfg, sink)
	if err != nil {
		t.Fatal(err)
	}
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("log-marker")
	if err := p.ConsumeLogs(authed(uuid.NewString(), uuid.NewString()), ld); err != nil {
		t.Fatalf("logs must be answered with success: %v", err)
	}
	if sink.LogRecordCount() != 0 {
		t.Fatal("logs were forwarded")
	}
}

func TestRateLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	cfg := &Config{RequestsPerSecond: 2, RequestsBurst: 3, ItemsPerSecond: 10, ItemsBurst: 20, IdleBucketTTL: time.Minute}
	g := newGuard(cfg, processortest.NewNopSettings(Type).Logger, clock)
	a, b := authed(uuid.NewString(), uuid.NewString()), authed(uuid.NewString(), uuid.NewString())
	for i := 0; i < 3; i++ {
		if _, err := g.processLogs(a, plog.NewLogs()); err != nil && status.Code(err) == codes.ResourceExhausted {
			t.Fatalf("request %d limited", i)
		}
	}
	if _, err := g.processLogs(a, plog.NewLogs()); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("4th request: %v", err)
	}
	// another tenant has its own buckets
	if _, err := g.processLogs(b, plog.NewLogs()); status.Code(err) == codes.ResourceExhausted {
		t.Fatal("tenant b limited by tenant a")
	}
	now = now.Add(500 * time.Millisecond) // one request token back
	if _, err := g.processLogs(a, plog.NewLogs()); status.Code(err) == codes.ResourceExhausted {
		t.Fatal("refill did not happen")
	}
	// item budget: 20 burst, refill 10/s
	now = now.Add(10 * time.Second)
	if !g.lim.allow("items", 20) || g.lim.allow("items", 1) {
		t.Fatal("item burst not enforced")
	}
	now = now.Add(time.Second)
	if !g.lim.allow("items", 10) {
		t.Fatal("item refill not applied")
	}
	if g.lim.allow("big", 21) {
		t.Fatal("request above the burst accepted")
	}
	// idle tenants are forgotten
	now = now.Add(2 * time.Minute)
	g.lim.allow("trigger-sweep", 1)
	g.lim.mu.Lock()
	n := len(g.lim.tenants)
	g.lim.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d tenants kept after the idle ttl", n)
	}
}

// the instances of one configured processor share the buckets
func TestLimiterSharedAcrossSignals(t *testing.T) {
	cfg := &Config{RequestsPerSecond: 0.001, RequestsBurst: 2, ItemsPerSecond: 1, ItemsBurst: 1000, IdleBucketTTL: time.Minute}
	set := processortest.NewNopSettings(Type)
	f := NewFactory()
	ctx := authed(uuid.NewString(), uuid.NewString())
	lp, err := f.CreateLogs(context.Background(), set, cfg, new(consumertest.LogsSink))
	if err != nil {
		t.Fatal(err)
	}
	tp, err := f.CreateTraces(context.Background(), set, cfg, new(consumertest.TracesSink))
	if err != nil {
		t.Fatal(err)
	}
	if err := lp.ConsumeLogs(ctx, plog.NewLogs()); err != nil {
		t.Fatal(err)
	}
	if err := tp.ConsumeTraces(ctx, ptrace.NewTraces()); err != nil {
		t.Fatal(err)
	}
	if err := lp.ConsumeLogs(ctx, plog.NewLogs()); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("third request across signals: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (&Config{}).Validate(); err == nil {
		t.Fatal("empty config accepted")
	}
	if err := testCfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

// sdks in OTEL_SEMCONV_STABILITY_OPT_IN=http/dup mode record every request
// under both names from one instrumentation scope; after the legacy name is
// scaled and renamed both would be stored. the stable one wins.
func TestHTTPDupKeepsStableNameOnly(t *testing.T) {
	for _, temp := range []pmetric.AggregationTemporality{pmetric.AggregationTemporalityDelta, pmetric.AggregationTemporalityCumulative} {
		md := pmetric.NewMetrics()
		rm := md.ResourceMetrics().AppendEmpty()
		rm.Resource().Attributes().PutStr("service.name", "api")
		dup := rm.ScopeMetrics().AppendEmpty()
		dup.Scope().SetName("otelhttp")
		for _, n := range []struct{ name, unit string }{{schema.MetricLegacyDuration, "ms"}, {schema.MetricDuration, "s"}} {
			m := dup.Metrics().AppendEmpty()
			m.SetName(n.name)
			m.SetUnit(n.unit)
			h := m.SetEmptyHistogram()
			h.SetAggregationTemporality(temp)
			dp := h.DataPoints().AppendEmpty()
			dp.Attributes().PutStr("http.request.method", "GET")
			dp.SetCount(1)
		}
		only := rm.ScopeMetrics().AppendEmpty()
		only.Scope().SetName("old-lib")
		m := only.Metrics().AppendEmpty()
		m.SetName(schema.MetricLegacyDuration)
		m.SetUnit("ms")
		m.SetEmptyHistogram().DataPoints().AppendEmpty().SetCount(1)

		g := newGuard(testCfg, processortest.NewNopSettings(Type).Logger, time.Now)
		out, err := g.processMetrics(authed(uuid.NewString(), uuid.NewString()), md)
		if err != nil {
			t.Fatal(err)
		}
		sms := out.ResourceMetrics().At(0).ScopeMetrics()
		if sms.Len() != 2 {
			t.Fatalf("%s: scopes = %d", temp, sms.Len())
		}
		if ms := sms.At(0).Metrics(); ms.Len() != 1 || ms.At(0).Name() != schema.MetricDuration {
			t.Errorf("%s: dup scope keeps %d metrics, first %s", temp, ms.Len(), ms.At(0).Name())
		}
		if ms := sms.At(1).Metrics(); ms.Len() != 1 || ms.At(0).Name() != schema.MetricLegacyDuration {
			t.Errorf("%s: legacy-only scope lost its metric", temp)
		}
	}
}

// service.name is cut on a rune boundary and invalid bytes are replaced,
// so what leaves the collector is always storable.
func TestServiceNameStorable(t *testing.T) {
	for _, in := range []string{
		strings.Repeat("a", 127) + "é",
		"api\xff\xfe-" + strings.Repeat("b", 200),
		"a\x00b",
	} {
		md := tenantMetrics()
		md.ResourceMetrics().At(0).Resource().Attributes().PutStr("service.name", in)
		g := newGuard(testCfg, processortest.NewNopSettings(Type).Logger, time.Now)
		out, err := g.processMetrics(authed(uuid.NewString(), uuid.NewString()), md)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := out.ResourceMetrics().At(0).Resource().Attributes().Get("service.name")
		s := v.Str()
		if !utf8.ValidString(s) || len(s) > maxServiceNameLen || strings.ContainsRune(s, 0) {
			t.Errorf("service.name %q from %q is not storable (%d bytes)", s, in, len(s))
		}
	}
}

// error.type with invalid bytes would reach the writer's stream identity
// and the span-derived rows; it becomes _OTHER like an overlong one.
func TestErrorTypeStorable(t *testing.T) {
	attrs := pcommon.NewMap()
	attrs.PutStr("http.request.method", "GET")
	attrs.PutStr("error.type", "bad\xffvalue")
	normalizeAttrs(attrs)
	if v, _ := attrs.Get("error.type"); v.Str() != schema.MethodOther {
		t.Errorf("error.type = %q", v.Str())
	}
}

// a retried export carries the same spans; its derived points must get the
// same message id and the minute the requests ended in.
func TestTracesCarryBatchIDAndEndMinute(t *testing.T) {
	tenant, token := uuid.NewString(), uuid.NewString()
	g := newGuard(testCfg, processortest.NewNopSettings(Type).Logger, time.Now)
	run := func(td ptrace.Traces) (string, ptrace.Traces) {
		out, err := g.processTraces(authed(tenant, token), td)
		if err != nil {
			t.Fatal(err)
		}
		v, ok := out.ResourceSpans().At(0).Resource().Attributes().Get(schema.ResBatchID)
		if !ok || len(v.Str()) != 64 {
			t.Fatalf("batch id %q", v.Str())
		}
		return v.Str(), out
	}
	a, out := run(spans())
	b, _ := run(spans())
	if a != b {
		t.Errorf("same spans, different batch ids %s and %s", a, b)
	}
	other := spans()
	other.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).SetSpanID(pcommon.SpanID{1, 2, 3})
	if c, _ := run(other); c == a {
		t.Error("different spans, same batch id")
	}
	sp := out.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	v, ok := sp.Attributes().Get(schema.AttrSpanMinute)
	// the spans end at 1.25 s after the epoch, in the first minute
	if !ok || v.Type() != pcommon.ValueTypeInt || v.Int() != 0 {
		t.Errorf("span minute %v", v.AsRaw())
	}
	end := pcommon.NewTimestampFromTime(time.Date(2026, 11, 13, 10, 7, 59, 900, time.UTC))
	late := spans()
	late.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).SetEndTimestamp(end)
	_, out = run(late)
	v, _ = out.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().Get(schema.AttrSpanMinute)
	if want := time.Date(2026, 11, 13, 10, 7, 0, 0, time.UTC).UnixNano(); v.Int() != want {
		t.Errorf("span minute %d, want %d", v.Int(), want)
	}
}
