package metricswriter

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

var canonical = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

type payload struct {
	tenant, token, hash string
	flags               int64
	service             string
}

func build(t *testing.T, p payload, fn func(pmetric.MetricSlice)) []byte {
	t.Helper()
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	a := rm.Resource().Attributes()
	a.PutStr(resTenantID, p.tenant)
	a.PutStr(resTokenID, p.token)
	a.PutStr(resResourceHash, p.hash)
	if p.service != "" {
		a.PutStr(resServiceName, p.service)
	}
	if p.flags != 0 {
		a.PutInt(resFlags, p.flags)
	}
	fn(rm.ScopeMetrics().AppendEmpty().Metrics())
	b, err := (&pmetric.ProtoMarshaler{}).MarshalMetrics(md)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func histogram(ms pmetric.MetricSlice, name string, temp pmetric.AggregationTemporality, bounds []float64, counts []uint64, attrs map[string]any) pmetric.HistogramDataPoint {
	m := ms.AppendEmpty()
	m.SetName(name)
	m.SetUnit("s")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(temp)
	dp := h.DataPoints().AppendEmpty()
	dp.SetStartTimestamp(pcommon.Timestamp(1_000))
	dp.SetTimestamp(pcommon.Timestamp(2_000_000_000))
	dp.ExplicitBounds().FromRaw(bounds)
	dp.BucketCounts().FromRaw(counts)
	var n uint64
	for _, c := range counts {
		n += c
	}
	dp.SetCount(n)
	dp.SetSum(1.5)
	_ = dp.Attributes().FromRaw(attrs)
	return dp
}

func TestDecode(t *testing.T) {
	tenant := uuid.New()
	tok := uuid.New()
	p := payload{tenant: tenant.String(), token: tok.String(), hash: strings.Repeat("ab", 32), flags: 1, service: "api"}
	data := build(t, p, func(ms pmetric.MetricSlice) {
		counts := make([]uint64, 15)
		counts[0], counts[14] = 2, 1
		histogram(ms, metricDuration, pmetric.AggregationTemporalityCumulative, canonical, counts, map[string]any{
			attrMethod: "GET", attrRoute: "/a/{id}", attrStatusCode: int64(404), "error.type": "x",
		})
		// coarser bounds: lossy
		histogram(ms, metricFromSpans, pmetric.AggregationTemporalityDelta, []float64{0.1, 1}, []uint64{1, 2, 3}, map[string]any{
			attrMethod: "POST", attrLossy: false,
		})
	})
	msg, err := Decode(tenant, data)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Resources != 1 || msg.SecretSeeds != 1 || len(msg.Points) != 2 {
		t.Fatalf("message %+v", msg)
	}
	a, b := msg.Points[0], msg.Points[1]
	if a.Source != SourceMetric || a.Temporality != Cumulative || a.Method != http.MethodGet || a.Route != "/a/{id}" ||
		a.Status != 404 || a.Service != "api" || a.TokenID != tok || a.Lossy || a.Count != 3 || a.Counts[0] != 2 || a.Counts[14] != 1 {
		t.Fatalf("metric point %+v", a)
	}
	if len(a.Attrs) != 4 {
		t.Fatalf("identity attributes %v", a.Attrs)
	}
	if a.Class != "app" {
		t.Fatalf("metric point class %q, want app", a.Class)
	}
	if b.Source != SourceSpan || b.Temporality != Delta || !b.Lossy || b.Route != "" || b.Status != 0 || b.Count != 6 {
		t.Fatalf("span point %+v", b)
	}
	// 0.1 is a canonical bound, 1 is too; (0.1, 1] lands in the bucket of 1
	if b.Counts[5] != 1 || b.Counts[9] != 2 || b.Counts[14] != 3 {
		t.Fatalf("re-bucketed %v", b.Counts)
	}
}

func TestDecodeRefuses(t *testing.T) {
	tenant := uuid.New()
	good := payload{tenant: tenant.String(), token: uuid.NewString(), hash: strings.Repeat("ab", 32)}
	one := func(ms pmetric.MetricSlice) {
		histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, canonical, make([]uint64, 15), nil)
	}
	cases := map[string][]byte{
		"garbage":      []byte("not protobuf at all \xff\xff"),
		"other tenant": build(t, payload{tenant: uuid.NewString(), token: good.token, hash: good.hash}, one),
		"bad hash":     build(t, payload{tenant: good.tenant, token: good.token, hash: "abc"}, one),
		"no token":     build(t, payload{tenant: good.tenant, hash: good.hash}, one),
		"other metric": build(t, good, func(ms pmetric.MetricSlice) {
			histogram(ms, "http.client.duration", pmetric.AggregationTemporalityDelta, canonical, make([]uint64, 15), nil)
		}),
		"cumulative spans": build(t, good, func(ms pmetric.MetricSlice) {
			histogram(ms, metricFromSpans, pmetric.AggregationTemporalityCumulative, canonical, make([]uint64, 15), nil)
		}),
		// values postgresql refuses must never reach a batch
		"invalid utf-8 route": withAttrs(t, good, map[string]any{attrRoute: "/a/\xff/b"}),
		"nul in route":        withAttrs(t, good, map[string]any{attrRoute: "/a/\x00"}),
		"overlong route":      withAttrs(t, good, map[string]any{attrRoute: "/" + strings.Repeat("a", 1024)}),
		"overlong method":     withAttrs(t, good, map[string]any{attrMethod: strings.Repeat("G", 17)}),
		"invalid method":      withAttrs(t, good, map[string]any{attrMethod: "G\xffT"}),
		"invalid service":     build(t, payload{tenant: good.tenant, token: good.token, hash: good.hash, service: "api\xc3"}, one),
		"overlong service":    build(t, payload{tenant: good.tenant, token: good.token, hash: good.hash, service: strings.Repeat("s", 129)}, one),
		// app is the absence of the attribute, never a published value
		"app class":     withAttrs(t, good, map[string]any{attrClass: "app"}),
		"unknown class": withAttrs(t, good, map[string]any{attrClass: "bot"}),
		"class not str": withAttrs(t, good, map[string]any{attrClass: int64(1)}),
		// the stream id is the collector's, 16 bytes in hex
		"short stream id":   withAttrs(t, good, map[string]any{attrStream: "abcd"}),
		"stream id not hex": withAttrs(t, good, map[string]any{attrStream: strings.Repeat("z", 32)}),
		"stream id not str": withAttrs(t, good, map[string]any{attrStream: int64(7)}),
	}
	for name, data := range cases {
		if _, err := Decode(tenant, data); !errors.Is(err, ErrUndecodable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// badPoints sets each kind of point whose values come from the tenant's sdk
// and cannot be stored: the collector drops them, and one that still
// arrives is left out alone.
var badPoints = map[string]func(pmetric.HistogramDataPoint){
	"time zero":             func(dp pmetric.HistogramDataPoint) { dp.SetTimestamp(0) },
	"time past 2262":        func(dp pmetric.HistogramDataPoint) { dp.SetTimestamp(1 << 63) },
	"start past 2262":       func(dp pmetric.HistogramDataPoint) { dp.SetStartTimestamp(1 << 63) },
	"start after time":      func(dp pmetric.HistogramDataPoint) { dp.SetStartTimestamp(dp.Timestamp() + 1) },
	"bounds not increasing": func(dp pmetric.HistogramDataPoint) { dp.ExplicitBounds().FromRaw([]float64{1, 0.1}) },
	"bound infinite":        func(dp pmetric.HistogramDataPoint) { dp.ExplicitBounds().FromRaw([]float64{0.1, math.Inf(1)}) },
	"bound nan":             func(dp pmetric.HistogramDataPoint) { dp.ExplicitBounds().FromRaw([]float64{0.1, math.NaN()}) },
	"no buckets":            func(dp pmetric.HistogramDataPoint) { dp.ExplicitBounds().FromRaw(nil); dp.BucketCounts().FromRaw(nil) },
	"buckets short":         func(dp pmetric.HistogramDataPoint) { dp.BucketCounts().FromRaw([]uint64{3}) },
	"count huge":            func(dp pmetric.HistogramDataPoint) { dp.SetCount(MaxPointCount + 1) },
	"bucket huge":           func(dp pmetric.HistogramDataPoint) { dp.BucketCounts().FromRaw([]uint64{1, MaxPointCount + 1, 0}) },
	"sum nan":               func(dp pmetric.HistogramDataPoint) { dp.SetSum(math.NaN()) },
	"sum huge":              func(dp pmetric.HistogramDataPoint) { dp.SetSum(1e308) },
	"sum huge negative":     func(dp pmetric.HistogramDataPoint) { dp.SetSum(-1e19) },
}

func TestDecodeLeavesOutBadPointsAlone(t *testing.T) {
	tenant := uuid.New()
	good := payload{tenant: tenant.String(), token: uuid.NewString(), hash: strings.Repeat("ab", 32)}
	for name, spoil := range badPoints {
		data := build(t, good, func(ms pmetric.MetricSlice) {
			histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, []float64{0.1, 1}, []uint64{1, 2, 0}, map[string]any{attrRoute: "/good"})
			spoil(histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, []float64{0.1, 1}, []uint64{1, 2, 0}, map[string]any{attrRoute: "/bad"}))
		})
		msg, err := Decode(tenant, data)
		if err != nil {
			t.Errorf("%s: the message is refused: %v", name, err)
			continue
		}
		if len(msg.Points) != 1 || msg.Points[0].Route != "/good" || msg.Rejected != 1 || msg.Reason == "" {
			t.Errorf("%s: points %d, rejected %d (%q)", name, len(msg.Points), msg.Rejected, msg.Reason)
		}
	}
	// a temporality the tenant's sdk left unspecified loses that metric only
	data := build(t, good, func(ms pmetric.MetricSlice) {
		histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, canonical, make([]uint64, 15), map[string]any{attrRoute: "/good"})
		histogram(ms, metricDuration, pmetric.AggregationTemporalityUnspecified, canonical, make([]uint64, 15), map[string]any{attrRoute: "/bad"})
	})
	msg, err := Decode(tenant, data)
	if err != nil || len(msg.Points) != 1 || msg.Rejected != 1 {
		t.Errorf("unspecified temporality: %v, points %d, rejected %d", err, len(msg.Points), msg.Rejected)
	}
}

func TestDecodeTrafficClass(t *testing.T) {
	tenant := uuid.New()
	good := payload{tenant: tenant.String(), token: uuid.NewString(), hash: strings.Repeat("ab", 32)}
	for _, c := range []string{"preflight", "health", "probe"} {
		msg, err := Decode(tenant, withAttrs(t, good, map[string]any{attrMethod: "GET", attrClass: c}))
		if err != nil {
			t.Fatal(err)
		}
		p := msg.Points[0]
		if p.Class != c {
			t.Errorf("class %q, want %q", p.Class, c)
		}
		// the class is part of the stream identity, so an application and
		// an infrastructure stream of one route never mix
		if len(p.Attrs) != 2 {
			t.Errorf("identity attributes %v", p.Attrs)
		}
	}
}

func withAttrs(t *testing.T, p payload, attrs map[string]any) []byte {
	t.Helper()
	return build(t, p, func(ms pmetric.MetricSlice) {
		histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, canonical, make([]uint64, 15), attrs)
	})
}

// a source histogram whose buckets are far wider than the
// canonical ones puts every request at its bucket's upper bound. such a
// point is marked coarse, beyond the plain lossy flag.
func TestDecodeMarksCoarseBuckets(t *testing.T) {
	tenant := uuid.New()
	p := payload{tenant: tenant.String(), token: uuid.NewString(), hash: strings.Repeat("ab", 32)}
	// an sdk's generic default bounds, meant for milliseconds, on a metric
	// in seconds: every request under 5 s falls in (0, 5]
	generic := []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
	coarse := make([]uint64, len(generic)+1)
	coarse[1] = 100
	// prometheus' defaults are a subset of the canonical bounds
	prom := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	fine := make([]uint64, len(prom)+1)
	fine[2] = 100
	data := build(t, p, func(ms pmetric.MetricSlice) {
		histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, generic, coarse, map[string]any{attrRoute: "/coarse"})
		histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, prom, fine, map[string]any{attrRoute: "/fine"})
		histogram(ms, metricDuration, pmetric.AggregationTemporalityDelta, []float64{0.1, 1}, []uint64{1, 2, 3}, map[string]any{attrRoute: "/lossy"})
	})
	msg, err := Decode(tenant, data)
	if err != nil {
		t.Fatal(err)
	}
	if !msg.Points[0].Coarse || !msg.Points[0].Lossy {
		t.Errorf("generic bounds in seconds not marked coarse: %+v", msg.Points[0])
	}
	if msg.Points[1].Coarse {
		t.Errorf("prometheus defaults marked coarse")
	}
	// (1, +inf) holds half the requests and spans 2.5 to 10 s
	if !msg.Points[2].Coarse {
		t.Errorf("an open bucket from 1 s holding half the requests not marked coarse")
	}
}

func TestTenantOf(t *testing.T) {
	id := uuid.New()
	if got, err := tenantOf("api.metrics." + id.String()); err != nil || got != id {
		t.Fatalf("%v %v", got, err)
	}
	for _, s := range []string{"api.metrics.x", "web.events." + id.String(), "api.metrics." + id.String() + ".x"} {
		if _, err := tenantOf(s); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}
