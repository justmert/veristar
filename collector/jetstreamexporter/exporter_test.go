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
	"go.opentelemetry.io/collector/pdata/pmetric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/justmert/stellar-analytics/collector/internal/natsconn"
	"github.com/justmert/stellar-analytics/collector/internal/schema"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
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
	histogram(md, b, schema.MetricFromSpans, "s", nil)
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
		if p.MsgID != hex.EncodeToString(sum[:]) {
			t.Errorf("msg id is not the payload sha-256")
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
