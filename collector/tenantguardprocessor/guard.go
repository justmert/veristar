package tenantguardprocessor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
}

func newGuard(cfg *Config, log *zap.Logger, now func() time.Time) *guard {
	return &guard{log: log, lim: newLimiter(cfg, now)}
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

// resourceHash identifies an emitting process for the metrics writer (phase
// 9): sha-256 over the original resource attributes in key order, each as
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

// normalizeAttrs rewrites a data point's or span's attributes to the four
// allowlisted keys, mapping legacy names, and returns the routenorm flags.
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

	attrs.Clear()
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
	return flags
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
			clearScope(sm.Scope())
			sm.SetSchemaUrl("")
			dup := dupLegacy(sm.Metrics())
			sm.Metrics().RemoveIf(func(m pmetric.Metric) bool {
				if !keepMetric(m) || (dup && m.Name() == schema.MetricLegacyDuration) {
					return true
				}
				m.SetDescription("")
				m.Metadata().Clear()
				switch m.Type() {
				case pmetric.MetricTypeHistogram:
					dps := m.Histogram().DataPoints()
					for i := 0; i < dps.Len(); i++ {
						dp := dps.At(i)
						flags |= normalizeAttrs(dp.Attributes())
						dp.Exemplars().RemoveIf(func(pmetric.Exemplar) bool { return true })
					}
					return dps.Len() == 0
				default:
					dps := m.ExponentialHistogram().DataPoints()
					for i := 0; i < dps.Len(); i++ {
						dp := dps.At(i)
						flags |= normalizeAttrs(dp.Attributes())
						dp.Exemplars().RemoveIf(func(pmetric.Exemplar) bool { return true })
					}
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
