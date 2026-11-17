package tenantguardprocessor

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/justmert/veristar/collector/internal/schema"
	"github.com/justmert/veristar/collector/tenantauthextension"
	"github.com/justmert/veristar/internal/redact"
	"github.com/justmert/veristar/internal/routenorm"
	"github.com/justmert/veristar/internal/trafficclass"
)

// errors returned to the receiver, which maps the grpc codes to http:
// Unauthenticated 401, ResourceExhausted 429.
var (
	errNoAuth      = status.Error(codes.Unauthenticated, "request carries no tenant credential")
	errRateLimited = status.Error(codes.ResourceExhausted, "tenant rate limit exceeded, retry later")
)

// maxErrorTypeLen bounds error.type, which some sdks fill with exception
// messages rather than class names.
const maxErrorTypeLen = 64

// maxServiceNameLen bounds service.name for the same reason.
const maxServiceNameLen = 128

type guard struct {
	log *zap.Logger
	lim *limiter
	// invalid counts the data points, metrics and spans dropped because
	// their values cannot be stored, by signal and reason. no attribute
	// names the tenant.
	invalid metric.Int64Counter
}

func newGuard(cfg *Config, log *zap.Logger, now func() time.Time) *guard {
	c, _ := noop.NewMeterProvider().Meter("").Int64Counter("")
	return &guard{log: log, lim: newLimiter(cfg, now), invalid: c}
}

// useMeter records the dropped items in the collector's own metrics.
func (g *guard) useMeter(mp metric.MeterProvider) error {
	c, err := mp.Meter("github.com/justmert/veristar/collector/tenantguardprocessor").Int64Counter(
		"tenantguard_invalid_items",
		metric.WithDescription("data points, metrics and spans tenantguard dropped because their values cannot be stored, by signal and reason"),
		metric.WithUnit("{item}"))
	if err != nil {
		return err
	}
	g.invalid = c
	return nil
}

// reasons an item is dropped as invalid
const (
	invalidTime        = "time"
	invalidTemporality = "temporality"
	invalidBuckets     = "buckets"
	invalidCount       = "count"
	invalidSum         = "sum"
)

func (g *guard) dropped(ctx context.Context, signal, reason string, n int) {
	g.invalid.Add(ctx, int64(n), metric.WithAttributes(attribute.String("signal", signal), attribute.String("reason", reason)))
}

// MaxPointCount bounds the request count of one data point, and of each of
// its buckets: a trillion requests, a thousand a second for 31 years of one
// process. the writer refuses a larger point too, so a count can never
// overflow the sums the platform stores.
const MaxPointCount = 1_000_000_000_000

// validTimes reports whether a point's or span's times can be stored: a
// time after the epoch that fits a signed 64-bit nanosecond count, and a
// start not after it. a zero start is a point without one.
func validTimes(start, at pcommon.Timestamp) bool {
	return at > 0 && at <= math.MaxInt64 && start <= at
}

// checkHistogram returns why an explicit histogram point cannot be stored,
// or "". otlp allows a point without buckets, but its requests cannot be
// placed in any latency bucket.
func checkHistogram(dp pmetric.HistogramDataPoint) string {
	if !validTimes(dp.StartTimestamp(), dp.Timestamp()) {
		return invalidTime
	}
	if dp.Flags().NoRecordedValue() {
		return ""
	}
	if dp.Count() > MaxPointCount {
		return invalidCount
	}
	if dp.HasSum() && (math.IsNaN(dp.Sum()) || math.IsInf(dp.Sum(), 0)) {
		return invalidSum
	}
	bounds, counts := dp.ExplicitBounds(), dp.BucketCounts()
	if counts.Len() != bounds.Len()+1 {
		return invalidBuckets
	}
	for i := 0; i < bounds.Len(); i++ {
		b := bounds.At(i)
		if math.IsNaN(b) || math.IsInf(b, 0) || (i > 0 && b <= bounds.At(i-1)) {
			return invalidBuckets
		}
	}
	for i := 0; i < counts.Len(); i++ {
		if counts.At(i) > MaxPointCount {
			return invalidCount
		}
	}
	return ""
}

// checkExponential is checkHistogram for a point the transform processor
// converts to the canonical explicit buckets afterwards.
func checkExponential(dp pmetric.ExponentialHistogramDataPoint) string {
	if !validTimes(dp.StartTimestamp(), dp.Timestamp()) {
		return invalidTime
	}
	if dp.Flags().NoRecordedValue() {
		return ""
	}
	if dp.Count() > MaxPointCount {
		return invalidCount
	}
	if dp.HasSum() && (math.IsNaN(dp.Sum()) || math.IsInf(dp.Sum(), 0)) {
		return invalidSum
	}
	return ""
}

// admit checks the credential and the tenant's buckets. it runs before any
// work on the payload, so a refused request costs almost nothing.
func (g *guard) admit(ctx context.Context, items int) (tenant, token string, err error) {
	tenant, token, err = tenantauthextension.FromContext(ctx)
	if err != nil {
		return "", "", errNoAuth
	}
	if !g.lim.allow(tenant, items) {
		g.log.Debug("tenant rate limited", zap.String("tenant_id", tenant), zap.Int("items", items))
		return "", "", errRateLimited
	}
	return tenant, token, nil
}

// resourceHash identifies an emitting process for the metrics writer:
// sha-256 over the original resource attributes in key order, each as
// key, type and value. the value encoding is pdata's AsString, which is
// stable for every type.
func resourceHash(attrs pcommon.Map) string {
	keys := make([]string, 0, attrs.Len())
	for k := range attrs.All() {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	h := sha256.New()
	for _, k := range keys {
		v, _ := attrs.Get(k)
		h.Write([]byte(k))
		h.Write([]byte{0, byte(v.Type())}) //nolint:gosec // value types are 0 to 7
		h.Write([]byte(v.AsString()))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// stamp replaces the resource attributes with the allowlisted set. the
// tenant and token come from the credential, never from the client, so a
// forged scf.tenant.id or tenant.id is simply dropped with everything else.
func stamp(res pcommon.Resource, tenant, token, hash string, flags int64) {
	attrs := res.Attributes()
	var svc string
	if v, ok := attrs.Get(schema.ResServiceName); ok {
		svc = v.AsString()
	}
	attrs.Clear()
	if svc != "" {
		s, f := redact.String(svc)
		if f.SecretSeed() {
			flags |= schema.FlagSecretSeed
		}
		attrs.PutStr(schema.ResServiceName, clean(s, maxServiceNameLen))
	}
	attrs.PutStr(schema.ResTenantID, tenant)
	attrs.PutStr(schema.ResTokenID, token)
	attrs.PutStr(schema.ResResourceHash, hash)
	if flags != 0 {
		attrs.PutInt(schema.ResFlags, flags)
	}
	res.SetDroppedAttributesCount(0)
}

// clean makes s storable in postgresql text: invalid utf-8 and control
// characters (nul is refused outright) become U+FFFD, and the result is cut
// to at most max bytes on a rune boundary, so a multibyte character at the
// limit is never split into an invalid tail.
func clean(s string, max int) string {
	if !storable(s) {
		var b strings.Builder
		b.Grow(len(s))
		for _, c := range strings.ToValidUTF8(s, string(utf8.RuneError)) {
			if control(c) {
				c = utf8.RuneError
			}
			b.WriteRune(c)
		}
		s = b.String()
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func control(c rune) bool {
	return c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0)
}

// storable reports whether s is valid utf-8 without control characters.
func storable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if control(c) {
			return false
		}
	}
	return true
}

// identity hashing: every field is length-prefixed, so no two different
// inputs give the same byte string
func hashField(h hash.Hash, s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	h.Write(n[:])
	h.Write([]byte(s))
}

// hashMap writes attrs in key order, each key with its value's type and
// value, so the int 200 and the string "200" differ.
func hashMap(h hash.Hash, attrs pcommon.Map) {
	keys := make([]string, 0, attrs.Len())
	for k := range attrs.All() {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	hashField(h, strconv.Itoa(len(keys)))
	for _, k := range keys {
		v, _ := attrs.Get(k)
		hashField(h, k)
		hashField(h, strconv.Itoa(int(v.Type())))
		hashField(h, v.AsString())
	}
}

// scopeIdentity is the hash input of an instrumentation scope, taken
// before clearScope empties it: the same metric recorded by two libraries
// is two streams.
func scopeIdentity(s pcommon.InstrumentationScope) []byte {
	h := sha256.New()
	hashField(h, s.Name())
	hashField(h, s.Version())
	hashMap(h, s.Attributes())
	return h.Sum(nil)
}

// streamID is the scf.stream value of a cumulative point: 128 bits of a
// sha-256 over its scope, the metric's name and unit as received (the
// legacy name is renamed only after tenantguard) and the point's original
// attributes. it is the same on every export of the stream and differs
// between any two streams the attribute rewriting would merge.
func streamID(scope []byte, m pmetric.Metric, attrs pcommon.Map) string {
	h := sha256.New()
	h.Write(scope)
	hashField(h, m.Name())
	hashField(h, m.Unit())
	hashMap(h, attrs)
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func clearScope(s pcommon.InstrumentationScope) {
	s.SetName("")
	s.SetVersion("")
	s.Attributes().Clear()
	s.SetDroppedAttributesCount(0)
}

func str(attrs pcommon.Map, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := attrs.Get(k); ok {
			if s := v.AsString(); s != "" {
				return s, true
			}
		}
	}
	return "", false
}

func statusCode(attrs pcommon.Map) (int64, bool) {
	for _, k := range []string{schema.AttrStatusCode, schema.LegacyStatusCode} {
		v, ok := attrs.Get(k)
		if !ok {
			continue
		}
		var n int64
		switch v.Type() {
		case pcommon.ValueTypeInt:
			n = v.Int()
		case pcommon.ValueTypeStr:
			p, err := strconv.ParseInt(v.Str(), 10, 64)
			if err != nil {
				continue
			}
			n = p
		case pcommon.ValueTypeDouble:
			n = int64(v.Double())
		default:
			continue
		}
		if n >= 100 && n <= 599 {
			return n, true
		}
	}
	return 0, false
}

// normalizeAttrs rewrites a data point's or span's attributes to the
// allowlisted keys, mapping legacy names, and returns the routenorm flags.
// the traffic class is decided here, before the user agent is dropped, and
// written only for infrastructure traffic.
func normalizeAttrs(attrs pcommon.Map) int64 {
	var flags int64
	method, hasMethod := str(attrs, schema.AttrMethod, schema.LegacyMethod)
	route, hasRoute := str(attrs, schema.AttrRoute)
	if !hasRoute {
		// a raw path (url.path, legacy http.target which may carry a query)
		// is normalized like a route; rule 1 removes the query
		route, hasRoute = str(attrs, schema.URLPath, schema.LegacyTarget)
	}
	code, hasCode := statusCode(attrs)
	errType, hasErr := str(attrs, schema.AttrErrorType)
	ua, _ := str(attrs, schema.AttrUserAgent, schema.LegacyUserAgent)
	synthetic, _ := str(attrs, schema.AttrSyntheticUAType)

	attrs.Clear()
	var normRoute string
	if hasMethod {
		// semconv lets instrumentation that treats methods as case
		// insensitive record the canonical name
		method = strings.ToUpper(method)
		if !schema.Methods[method] {
			method = schema.MethodOther
		}
		attrs.PutStr(schema.AttrMethod, method)
	}
	if hasRoute {
		r, f := routenorm.Normalize(route)
		if f&routenorm.FlagSecretSeed != 0 {
			flags |= schema.FlagSecretSeed
		}
		attrs.PutStr(schema.AttrRoute, r)
		normRoute = r
	}
	if hasCode {
		attrs.PutInt(schema.AttrStatusCode, code)
	}
	if hasErr {
		e, f := redact.String(errType)
		if f.SecretSeed() {
			flags |= schema.FlagSecretSeed
		}
		if len(e) > maxErrorTypeLen || !storable(e) {
			e = schema.MethodOther
		}
		attrs.PutStr(schema.AttrErrorType, e)
	}
	if c := trafficclass.Classify(trafficclass.Request{
		Method: method, Route: normRoute, UserAgent: ua, Synthetic: synthetic,
	}); c.Infrastructure() {
		attrs.PutStr(schema.AttrTrafficClass, string(c))
	}
	return flags
}

// normalizePoint normalizes a data point's attributes and gives a
// cumulative point its stream id, hashed from the attributes it arrived
// with. the id is written after normalizeAttrs cleared the map, so a
// tenant-supplied scf.stream is gone and only the computed one remains.
func normalizePoint(scope []byte, m pmetric.Metric, cumulative bool, attrs pcommon.Map) int64 {
	var id string
	if cumulative {
		id = streamID(scope, m, attrs)
	}
	flags := normalizeAttrs(attrs)
	if cumulative {
		attrs.PutStr(schema.AttrStream, id)
	}
	return flags
}

func knownTemporality(t pmetric.AggregationTemporality) bool {
	return t == pmetric.AggregationTemporalityDelta || t == pmetric.AggregationTemporalityCumulative
}

// keepMetric reports whether a tenant metric may continue: one of the two
// duration names, a histogram type and a duration unit. the span-derived
// name is refused here because only the connector, after this processor,
// may produce it.
func keepMetric(m pmetric.Metric) bool {
	switch m.Type() {
	case pmetric.MetricTypeHistogram, pmetric.MetricTypeExponentialHistogram:
	default:
		return false
	}
	switch m.Name() {
	case schema.MetricDuration:
		return m.Unit() == "s"
	case schema.MetricLegacyDuration:
		return m.Unit() == "ms" || m.Unit() == "s"
	}
	return false
}

// dupLegacy reports whether the legacy duration metrics of a scope are the
// http/dup copy of its stable one. sdks run with
// OTEL_SEMCONV_STABILITY_OPT_IN=http/dup record every request under both
// names from the same instrumentation scope; after the legacy one is scaled
// and renamed both would be stored, so the stable name wins. a
// legacy metric from another scope is another library's and is kept.
func dupLegacy(ms pmetric.MetricSlice) bool {
	for i := 0; i < ms.Len(); i++ {
		if m := ms.At(i); m.Name() == schema.MetricDuration && keepMetric(m) {
			return true
		}
	}
	return false
}

func (g *guard) processMetrics(ctx context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	tenant, token, err := g.admit(ctx, md.DataPointCount())
	if err != nil {
		return md, err
	}
	md.ResourceMetrics().RemoveIf(func(rm pmetric.ResourceMetrics) bool {
		hash := resourceHash(rm.Resource().Attributes())
		var flags int64
		rm.SetSchemaUrl("")
		rm.ScopeMetrics().RemoveIf(func(sm pmetric.ScopeMetrics) bool {
			scope := scopeIdentity(sm.Scope())
			clearScope(sm.Scope())
			sm.SetSchemaUrl("")
			dup := dupLegacy(sm.Metrics())
			sm.Metrics().RemoveIf(func(m pmetric.Metric) bool {
				if !keepMetric(m) || (dup && m.Name() == schema.MetricLegacyDuration) {
					return true
				}
				m.SetDescription("")
				m.Metadata().Clear()
				// a point the writer could not store is dropped here, alone,
				// so the rest of the export is stored
				switch m.Type() {
				case pmetric.MetricTypeHistogram:
					h := m.Histogram()
					if !knownTemporality(h.AggregationTemporality()) {
						g.dropped(ctx, "metrics", invalidTemporality, h.DataPoints().Len())
						return true
					}
					cumulative := h.AggregationTemporality() == pmetric.AggregationTemporalityCumulative
					dps := h.DataPoints()
					dps.RemoveIf(func(dp pmetric.HistogramDataPoint) bool {
						if r := checkHistogram(dp); r != "" {
							g.dropped(ctx, "metrics", r, 1)
							return true
						}
						flags |= normalizePoint(scope, m, cumulative, dp.Attributes())
						dp.Exemplars().RemoveIf(func(pmetric.Exemplar) bool { return true })
						return false
					})
					return dps.Len() == 0
				default:
					h := m.ExponentialHistogram()
					if !knownTemporality(h.AggregationTemporality()) {
						g.dropped(ctx, "metrics", invalidTemporality, h.DataPoints().Len())
						return true
					}
					cumulative := h.AggregationTemporality() == pmetric.AggregationTemporalityCumulative
					dps := h.DataPoints()
					dps.RemoveIf(func(dp pmetric.ExponentialHistogramDataPoint) bool {
						if r := checkExponential(dp); r != "" {
							g.dropped(ctx, "metrics", r, 1)
							return true
						}
						flags |= normalizePoint(scope, m, cumulative, dp.Attributes())
						dp.Exemplars().RemoveIf(func(pmetric.Exemplar) bool { return true })
						return false
					})
					return dps.Len() == 0
				}
			})
			return sm.Metrics().Len() == 0
		})
		if rm.ScopeMetrics().Len() == 0 {
			return true
		}
		stamp(rm.Resource(), tenant, token, hash, flags)
		return false
	})
	if md.ResourceMetrics().Len() == 0 {
		return md, processorhelper.ErrSkipProcessingData
	}
	return md, nil
}

func (g *guard) processTraces(ctx context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	tenant, token, err := g.admit(ctx, td.SpanCount())
	if err != nil {
		return td, err
	}
	td.ResourceSpans().RemoveIf(func(rs ptrace.ResourceSpans) bool {
		hash := resourceHash(rs.Resource().Attributes())
		var flags int64
		rs.SetSchemaUrl("")
		rs.ScopeSpans().RemoveIf(func(ss ptrace.ScopeSpans) bool {
			clearScope(ss.Scope())
			ss.SetSchemaUrl("")
			ss.Spans().RemoveIf(func(sp ptrace.Span) bool {
				if sp.Kind() != ptrace.SpanKindServer {
					return true
				}
				if _, ok := str(sp.Attributes(), schema.AttrMethod, schema.LegacyMethod); !ok {
					return true
				}
				// a span without a start, or with an end before the epoch or
				// past 2262, has no duration and no minute the writer could
				// store; it is dropped alone instead of failing the export
				if sp.StartTimestamp() == 0 || !validTimes(sp.StartTimestamp(), sp.EndTimestamp()) {
					g.dropped(ctx, "traces", invalidTime, 1)
					return true
				}
				flags |= normalizeAttrs(sp.Attributes())
				// the connector stamps its points with the time it ran; the
				// minute the request ended in keeps a retried or delayed
				// export in the right minute and makes its points equal
				sp.Attributes().PutInt(schema.AttrSpanMinute, endMinute(sp.EndTimestamp()))
				// the name usually holds the route or the raw path; the
				// method alone is safe and still names the span
				m, _ := sp.Attributes().Get(schema.AttrMethod)
				sp.SetName(m.Str())
				sp.Events().RemoveIf(func(ptrace.SpanEvent) bool { return true })
				sp.Links().RemoveIf(func(ptrace.SpanLink) bool { return true })
				sp.Status().SetMessage("")
				sp.TraceState().FromRaw("")
				sp.SetDroppedAttributesCount(0)
				sp.SetDroppedEventsCount(0)
				sp.SetDroppedLinksCount(0)
				return false
			})
			return ss.Spans().Len() == 0
		})
		if rs.ScopeSpans().Len() == 0 {
			return true
		}
		stamp(rs.Resource(), tenant, token, hash, flags)
		return false
	})
	if td.ResourceSpans().Len() == 0 {
		return td, processorhelper.ErrSkipProcessingData
	}
	id, err := batchID(td)
	if err != nil {
		return td, err
	}
	for i := 0; i < td.ResourceSpans().Len(); i++ {
		td.ResourceSpans().At(i).Resource().Attributes().PutStr(schema.ResBatchID, id)
	}
	return td, nil
}

// endMinute is the start of the utc minute t falls in, in unix nanoseconds.
func endMinute(t pcommon.Timestamp) int64 {
	ns := int64(t) //nolint:gosec // unix nanoseconds fit in int64 until 2262
	return ns - ns%int64(time.Minute)
}

// batchID is the sha-256 of the filtered export. the client retries an
// export with the same spans, so the retry gets the same id, while two
// different exports differ at least in their span ids. it is taken after
// filtering and stamping, so it covers the tenant and nothing the guard
// removed.
func batchID(td ptrace.Traces) (string, error) {
	b, err := (&ptrace.ProtoMarshaler{}).MarshalTraces(td)
	if err != nil {
		return "", status.Error(codes.Internal, "marshal traces for the batch id")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// processLogs authenticates and rate-limits, then discards everything: logs
// are accepted so tenant sdks see success, and never leave the collector.
func (g *guard) processLogs(ctx context.Context, ld plog.Logs) (plog.Logs, error) {
	if _, _, err := g.admit(ctx, ld.LogRecordCount()); err != nil {
		return ld, err
	}
	return ld, processorhelper.ErrSkipProcessingData
}
