package jetstreamexporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter/exportertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/justmert/veristar/collector/internal/natsconn"
	"github.com/justmert/veristar/collector/internal/schema"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

func histogram(md pmetric.Metrics, tenant, name, unit string, extra map[string]any) {
	rm := md.ResourceMetrics().AppendEmpty()
	ra := rm.Resource().Attributes()
	if tenant != "" {
		ra.PutStr(schema.ResTenantID, tenant)
	}
	ra.PutStr(schema.ResTokenID, uuid.NewString())
	ra.PutStr(schema.ResServiceName, "api")
	ra.PutStr("host.name", "leak-host")
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	dp := m.SetEmptyHistogram().DataPoints().AppendEmpty()
	dp.SetCount(2)
	dp.ExplicitBounds().FromRaw(schema.Bounds)
	dp.BucketCounts().FromRaw(make([]uint64, 15))
	dp.Attributes().PutStr(schema.AttrMethod, "GET")
	for k, v := range extra {
		_ = dp.Attributes().PutEmpty(k).FromRaw(v)
	}
}

func TestSplitByTenantAndFilter(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	md := pmetric.NewMetrics()
	histogram(md, a, schema.MetricDuration, "s", map[string]any{"user.id": "leak-user"})
	histogram(md, b, schema.MetricFromSpans, "s", map[string]any{schema.AttrSpanMinute: int64(time.Minute)})
	md.ResourceMetrics().At(1).Resource().Attributes().PutStr(schema.ResBatchID, "b1")
	histogram(md, a, schema.MetricDuration, "s", map[string]any{schema.AttrLossy: true})
	histogram(md, b, "http.client.request.duration", "s", nil) // not published
	histogram(md, b, schema.MetricLegacyDuration, "ms", nil)   // transform missed it
	parts, dropped, err := Split(md)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].TenantID != a || parts[1].TenantID != b {
		t.Fatalf("parts %+v", parts)
	}
	// host.name on 5 resources, user.id, 2 metrics
	if dropped != 8 {
		t.Errorf("dropped = %d", dropped)
	}
	for _, p := range parts {
		sum := sha256.Sum256(p.Data)
		if p.TenantID == a && p.MsgID != hex.EncodeToString(sum[:]) {
			t.Errorf("msg id is not the payload sha-256")
		}
		if p.TenantID == b && p.MsgID != spanMsgID(b, []string{"b1"}) {
			t.Errorf("span msg id is not derived from the batch id")
		}
		var u pmetric.ProtoUnmarshaler
		got, err := u.UnmarshalMetrics(p.Data)
		if err != nil {
			t.Fatal(err)
		}
		rms := got.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			v, _ := rms.At(i).Resource().Attributes().Get(schema.ResTenantID)
			if v.Str() != p.TenantID {
				t.Errorf("resource of tenant %s in part of %s", v.Str(), p.TenantID)
			}
			for k := range rms.At(i).Resource().Attributes().All() {
				if !schema.ResourceAllowed[k] {
					t.Errorf("resource attribute %s published", k)
				}
			}
		}
		if p.TenantID == a && got.DataPointCount() != 2 {
			t.Errorf("tenant a points %d", got.DataPointCount())
		}
		if p.TenantID == b && got.DataPointCount() != 1 {
			t.Errorf("tenant b points %d", got.DataPointCount())
		}
	}
	// same input, same id: a retried request is deduplicated by jetstream
	again, _, _ := Split(md)
	if again[0].MsgID != parts[0].MsgID {
		t.Error("message id not deterministic")
	}
}

func TestSplitRefusesUnstamped(t *testing.T) {
	for _, tenant := range []string{"", "not-a-uuid", "../x"} {
		md := pmetric.NewMetrics()
		histogram(md, tenant, schema.MetricDuration, "s", nil)
		if _, _, err := Split(md); !errors.Is(err, errNoTenant) {
			t.Errorf("tenant %q: %v", tenant, err)
		}
	}
}

func TestPublishToJetStream(t *testing.T) {
	creds := map[string]string{}
	for _, u := range natsx.Users() {
		creds[u.Name] = "pw-" + u.Name
	}
	auth, err := natsx.RenderAuthorization(natsx.Users(), creds)
	if err != nil {
		t.Fatal(err)
	}
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	nc, err := nats.Connect(srv.URL, nats.UserInfo("platformctl", creds["platformctl"]), nats.CustomInboxPrefix(natsx.InboxPrefix("platformctl")),
		nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := natsx.Topology(1)
	ctx := context.Background()
	if _, err := natsx.Apply(ctx, js, spec); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{NATS: natsconn.Config{URL: srv.URL, User: "collector", Password: configopaque.String(creds["collector"])}, PublishTimeout: 2 * time.Second}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	exp, err := NewFactory().CreateMetrics(ctx, exportertest.NewNopSettings(Type), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := exp.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	tenant := uuid.NewString()
	md := pmetric.NewMetrics()
	histogram(md, tenant, schema.MetricDuration, "s", nil)
	if err := exp.ConsumeMetrics(ctx, md); err != nil {
		t.Fatal(err)
	}
	// the same payload again is acknowledged and stored once
	if err := exp.ConsumeMetrics(ctx, md); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, natsx.StreamAPIMetrics)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx, jetstream.WithSubjectFilter(natsx.Subject(natsx.SubjectAPIMetrics, tenant)))
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("stored %d messages, want 1", info.State.Msgs)
	}
	msg, err := stream.GetLastMsgForSubject(ctx, natsx.Subject(natsx.SubjectAPIMetrics, tenant))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(msg.Data)
	if msg.Header.Get(jetstream.MsgIDHeader) != hex.EncodeToString(sum[:]) {
		t.Fatal("Nats-Msg-Id is not the payload sha-256")
	}

	// unstamped data is a permanent error, not a retry
	bad := pmetric.NewMetrics()
	histogram(bad, "", schema.MetricDuration, "s", nil)
	if err := exp.ConsumeMetrics(ctx, bad); !consumererror.IsPermanent(err) {
		t.Fatalf("unstamped: %v", err)
	}

	// nats down: the request fails with Unavailable within the timeout
	srv.Stop(t)
	start := time.Now()
	md2 := pmetric.NewMetrics()
	histogram(md2, tenant, schema.MetricDuration, "s", map[string]any{schema.AttrRoute: "/down"})
	err = exp.ConsumeMetrics(ctx, md2)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("nats stopped: %v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	srv.Start(t)
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = exp.ConsumeMetrics(ctx, md2)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no recovery after restart: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	info, err = stream.Info(ctx, jetstream.WithSubjectFilter(natsx.Subject(natsx.SubjectAPIMetrics, tenant)))
	if err != nil {
		t.Fatal(err)
	}
	// the publish buffered during the outage and the retry share one id
	if info.State.Msgs != 2 {
		t.Fatalf("after recovery %d messages, want 2", info.State.Msgs)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (&Config{NATS: natsconn.Config{URL: "nats://x:4222"}}).Validate(); err == nil {
		t.Fatal("missing timeout accepted")
	}
}

// spanExport builds what the connector emits for one trace export: points
// stamped with the moment it ran, in map order.
func spanExport(tenant, batch string, now time.Time, reverse bool) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	ra := rm.Resource().Attributes()
	ra.PutStr(schema.ResTenantID, tenant)
	ra.PutStr(schema.ResTokenID, "8a1c3c1e-0f53-4e02-9d0b-6a7f0f0e3a11")
	ra.PutStr(schema.ResBatchID, batch)
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(schema.MetricFromSpans)
	m.SetUnit("s")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
	minutes := []int64{1_763_000_040_000_000_000, 1_763_000_100_000_000_000}
	if reverse {
		minutes[0], minutes[1] = minutes[1], minutes[0]
	}
	for _, min := range minutes {
		dp := h.DataPoints().AppendEmpty()
		dp.SetTimestamp(pcommonTS(now))
		dp.SetCount(1)
		dp.ExplicitBounds().FromRaw(schema.Bounds)
		bc := make([]uint64, 15)
		bc[3] = 1
		dp.BucketCounts().FromRaw(bc)
		dp.Attributes().PutStr(schema.AttrMethod, "GET")
		dp.Attributes().PutInt(schema.AttrSpanMinute, min)
	}
	return md
}

// a retried trace export is aggregated again at another moment and in
// another map order; its message must still be recognised as the same one,
// and its points must carry the minute the requests ended in.
func TestSpanPartsDeduplicateOnRetry(t *testing.T) {
	tenant := uuid.NewString()
	first, _, err := Split(spanExport(tenant, "batch-1", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	retry, _, err := Split(spanExport(tenant, "batch-1", time.Now().Add(7*time.Second), true))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(retry) != 1 {
		t.Fatalf("parts %d and %d", len(first), len(retry))
	}
	if first[0].MsgID != retry[0].MsgID {
		t.Errorf("retry got message id %s, first %s", retry[0].MsgID, first[0].MsgID)
	}
	other, _, err := Split(spanExport(tenant, "batch-2", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	if other[0].MsgID == first[0].MsgID {
		t.Error("different exports share a message id")
	}
	var u pmetric.ProtoUnmarshaler
	got, err := u.UnmarshalMetrics(first[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	rm := got.ResourceMetrics().At(0)
	if _, ok := rm.Resource().Attributes().Get(schema.ResBatchID); ok {
		t.Error("batch id published")
	}
	dps := rm.ScopeMetrics().At(0).Metrics().At(0).Histogram().DataPoints()
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		if _, ok := dp.Attributes().Get(schema.AttrSpanMinute); ok {
			t.Error("span minute published")
		}
		if ts := int64(dp.Timestamp()); ts%int64(time.Minute) != 0 || ts < 1_763_000_040_000_000_000 {
			t.Errorf("point time %d is not the span minute", ts)
		}
	}
	// without the guard's stamps the pipeline is misconfigured
	bad := spanExport(tenant, "batch-1", time.Now(), false)
	bad.ResourceMetrics().At(0).Resource().Attributes().Remove(schema.ResBatchID)
	if _, _, err := Split(bad); err == nil {
		t.Error("span points without a batch id accepted")
	}
	bad = spanExport(tenant, "batch-1", time.Now(), false)
	bad.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Histogram().DataPoints().At(0).Attributes().Remove(schema.AttrSpanMinute)
	if _, _, err := Split(bad); err == nil {
		t.Error("span point without a minute accepted")
	}
}

func pcommonTS(t time.Time) pcommon.Timestamp { return pcommon.NewTimestampFromTime(t) }
