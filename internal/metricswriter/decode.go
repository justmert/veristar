package metricswriter

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/justmert/veristar/internal/histo"
	"github.com/justmert/veristar/internal/trafficclass"
)

// names published by the collector (docs/reference/collector.md). they are
// repeated here because collector/internal/schema is internal to the
// collector module tree.
const (
	metricDuration  = "http.server.request.duration"
	metricFromSpans = "scf.http.server.request.duration.from_spans"

	resServiceName  = "service.name"
	resTenantID     = "scf.tenant.id"
	resTokenID      = "scf.token.id" //nolint:gosec // an attribute name, not a credential
	resResourceHash = "scf.resource.hash"
	resFlags        = "scf.flags"

	attrMethod     = "http.request.method"
	attrRoute      = "http.route"
	attrStatusCode = "http.response.status_code"
	attrLossy      = "scf.lossy"
	attrClass      = "scf.traffic.class"

	// flagSecretSeed is bit 1 of scf.flags: a secret seed was redacted
	flagSecretSeed int64 = 1
)

// sources of a row
const (
	SourceMetric = "metric"
	SourceSpan   = "span"
)

// Decoded is one data point with everything the writer stores about it.
type Decoded struct {
	Point
	TokenID uuid.UUID
	Source  string
	Service string
	Method  string
	Route   string
	Status  int16
	// Class is the traffic class tenantguard decided (internal/trafficclass);
	// a point without scf.traffic.class is application traffic.
	Class string
	// SecretSeed is the resource's scf.flags bit: a secret seed was redacted
	// from something this resource sent.
	SecretSeed bool
	// Coarse marks a point whose source buckets are much wider than the
	// canonical ones (histo.Coarse).
	Coarse bool
}

// Message is the decoded content of one API_METRICS message.
type Message struct {
	Tenant uuid.UUID
	Points []Decoded
	// Resources is the number of resources in the message, and SecretSeeds
	// how many of them carry the secret seed flag.
	Resources   int
	SecretSeeds int
}

// byte limits of the text columns of api.metrics_minute and
// api.route_registry (00015)
const (
	maxServiceLen = 128
	maxMethodLen  = 16
	maxRouteLen   = 1024
)

// checkText refuses a value postgresql would refuse in a text column:
// invalid utf-8 (sqlstate 22021), a nul byte (also 22021), or more bytes than
// the column's check allows (23514). such a value in a batch fails the whole
// transaction, so it is caught here and only its own message goes to the
// dlq. control characters are refused as well: the collector
// never produces them.
func checkText(what, s string, limit int) error {
	if len(s) > limit {
		return undecodable("%s of %d bytes, the column allows %d", what, len(s), limit)
	}
	if !utf8.ValidString(s) {
		return undecodable("%s %q is not valid utf-8", what, s)
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0) {
			return undecodable("%s %q holds control character %U", what, s, c)
		}
	}
	return nil
}

// ErrUndecodable marks a message that can never be applied. the writer
// moves it to API_DLQ.
var ErrUndecodable = errors.New("metricswriter: undecodable message")

func undecodable(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUndecodable, fmt.Sprintf(format, a...))
}

// Decode reads one API_METRICS payload for the tenant of its subject. the
// collector guarantees the schema; anything else means a producer that is
// not the collector, or a collector bug, and the whole message is refused
// so nothing half-applied reaches the tables.
func Decode(tenant uuid.UUID, data []byte) (Message, error) {
	md, err := (&pmetric.ProtoUnmarshaler{}).UnmarshalMetrics(data)
	if err != nil {
		return Message{}, undecodable("protobuf: %v", err)
	}
	msg := Message{Tenant: tenant}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		res, err := decodeResource(tenant, rm.Resource().Attributes())
		if err != nil {
			return Message{}, err
		}
		msg.Resources++
		if res.SecretSeed {
			msg.SecretSeeds++
		}
		sms := rm.ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				pts, err := decodeMetric(res, ms.At(k))
				if err != nil {
					return Message{}, err
				}
				msg.Points = append(msg.Points, pts...)
			}
		}
	}
	return msg, nil
}

func decodeResource(tenant uuid.UUID, attrs pcommon.Map) (Decoded, error) {
	var d Decoded
	t, ok := attrs.Get(resTenantID)
	if !ok || t.Type() != pcommon.ValueTypeStr {
		return d, undecodable("resource without %s", resTenantID)
	}
	if id, err := uuid.Parse(t.Str()); err != nil || id != tenant {
		// the subject is what the collector chose from the same attribute,
		// so a mismatch means the message was not built by the collector
		return d, undecodable("resource tenant %q does not match subject tenant %s", t.Str(), tenant)
	}
	tok, ok := attrs.Get(resTokenID)
	if !ok || tok.Type() != pcommon.ValueTypeStr {
		return d, undecodable("resource without %s", resTokenID)
	}
	tokenID, err := uuid.Parse(tok.Str())
	if err != nil {
		return d, undecodable("token id %q: %v", tok.Str(), err)
	}
	h, ok := attrs.Get(resResourceHash)
	if !ok || h.Type() != pcommon.ValueTypeStr {
		return d, undecodable("resource without %s", resResourceHash)
	}
	if b, err := hex.DecodeString(h.Str()); err != nil || len(b) != 32 {
		return d, undecodable("resource hash %q is not 32 bytes of hex", h.Str())
	}
	d.Tenant = tenant.String()
	d.TokenID = tokenID
	d.ResourceHash = h.Str()
	if v, ok := attrs.Get(resServiceName); ok {
		d.Service = v.AsString()
		if err := checkText("service.name", d.Service, maxServiceLen); err != nil {
			return d, err
		}
	}
	if v, ok := attrs.Get(resFlags); ok {
		if v.Type() != pcommon.ValueTypeInt {
			return d, undecodable("%s is %s, not int", resFlags, v.Type())
		}
		d.SecretSeed = v.Int()&flagSecretSeed != 0
	}
	return d, nil
}

func decodeMetric(res Decoded, m pmetric.Metric) ([]Decoded, error) {
	var source string
	switch m.Name() {
	case metricDuration:
		source = SourceMetric
	case metricFromSpans:
		source = SourceSpan
	default:
		return nil, undecodable("metric %q is not published by the collector", m.Name())
	}
	if m.Type() != pmetric.MetricTypeHistogram || m.Unit() != "s" {
		return nil, undecodable("metric %s is %s in %q, want an explicit histogram in s", m.Name(), m.Type(), m.Unit())
	}
	h := m.Histogram()
	var temp Temporality
	switch h.AggregationTemporality() {
	case pmetric.AggregationTemporalityDelta:
		temp = Delta
	case pmetric.AggregationTemporalityCumulative:
		temp = Cumulative
	default:
		return nil, undecodable("metric %s has temporality %s", m.Name(), h.AggregationTemporality())
	}
	if source == SourceSpan && temp != Delta {
		return nil, undecodable("span-derived metric is not delta")
	}
	dps := h.DataPoints()
	out := make([]Decoded, 0, dps.Len())
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		d := res
		d.Metric = m.Name()
		d.Source = source
		d.Temporality = temp
		if err := decodePoint(&d, dp); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func decodePoint(d *Decoded, dp pmetric.HistogramDataPoint) error {
	d.Start = int64(dp.StartTimestamp()) //nolint:gosec // unix nanoseconds fit in int64 until 2262
	d.Time = int64(dp.Timestamp())       //nolint:gosec // as above
	if d.Start < 0 || d.Time <= 0 {
		return undecodable("data point times start %d, time %d", d.Start, d.Time)
	}
	d.NoRecordedValue = dp.Flags().NoRecordedValue()
	d.Class = string(trafficclass.ClassApp)
	for k, v := range dp.Attributes().All() {
		switch k {
		case attrLossy:
			d.Lossy = v.Type() == pcommon.ValueTypeBool && v.Bool()
			continue
		case attrMethod:
			d.Method = v.AsString()
			if err := checkText("method", d.Method, maxMethodLen); err != nil {
				return err
			}
		case attrRoute:
			d.Route = v.AsString()
			if err := checkText("route", d.Route, maxRouteLen); err != nil {
				return err
			}
		case attrClass:
			// only the infrastructure classes are ever published; app is
			// the absence of the attribute
			c := trafficclass.Class(v.AsString())
			if v.Type() != pcommon.ValueTypeStr || !c.Infrastructure() {
				return undecodable("traffic class %s", v.AsString())
			}
			d.Class = string(c)
		case attrStatusCode:
			if v.Type() != pcommon.ValueTypeInt || v.Int() < 100 || v.Int() > 599 {
				return undecodable("status code %s", v.AsString())
			}
			d.Status = int16(v.Int()) //nolint:gosec // checked to be 100 to 599 above
		}
		// every published attribute except the lossy marker is part of the
		// stream identity, error.type included, so streams that differ only
		// in it are not mixed up before they are merged into one row
		d.Attrs = append(d.Attrs, Attr{Key: k, Value: attrValue(v)})
	}
	if d.NoRecordedValue {
		return nil
	}
	d.Count = dp.Count()
	d.Sum = dp.Sum()
	src := dp.BucketCounts().AsRaw()
	bounds := dp.ExplicitBounds().AsRaw()
	if len(src) != len(bounds)+1 {
		// otlp allows a histogram without buckets, but then its requests
		// cannot be placed in any bucket of the canonical layout
		return undecodable("data point has %d buckets for %d bounds", len(src), len(bounds))
	}
	counts, exact, err := histo.Rebucket(bounds, src)
	if err != nil {
		return undecodable("re-bucket: %v", err)
	}
	if !exact {
		d.Lossy = true
		d.Coarse = histo.Coarse(bounds, src)
	}
	for i, c := range counts {
		if c < 0 {
			return undecodable("re-bucket produced a negative count")
		}
		d.Counts[i] = uint64(c)
	}
	return nil
}

// attrValue renders a value with its type, so the int 200 and the string
// "200" cannot be one stream.
func attrValue(v pcommon.Value) string {
	return strconv.Itoa(int(v.Type())) + ":" + v.AsString()
}
