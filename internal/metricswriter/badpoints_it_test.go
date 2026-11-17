//go:build metricswriterit

package metricswriter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
)

// TestMetricsWriterBadPointValues sends, for every kind of point value the
// tenant's sdk can get wrong, one message that holds a valid point and a
// bad one, past the collector, which would have dropped the bad one. the
// writer must store each valid point, leave out each bad one, and move
// nothing to API_DLQ.
func TestMetricsWriterBadPointValues(t *testing.T) {
	ctx := t.Context()
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	nc, js := jobstest.Connect(t, srv.URL)
	super, err := pg.Superuser(ctx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = super.Close(context.Background()) })
	var user, tenant, token uuid.UUID
	if err := super.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (4545, 'badpoints') returning id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := super.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ('it-bad', 'it-bad', 'active', $1) returning id`, user).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("it-bad"))
	if err := super.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'it', 'sa_otlp_xxxx', $2, $3) returning id`,
		tenant, h[:], user).Scan(&token); err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(badPoints))
	for name := range badPoints {
		names = append(names, name)
	}
	sort.Strings(names)
	now := time.Now().UTC()
	point := func(ms pmetric.MetricSlice, temp pmetric.AggregationTemporality, route string, at time.Time) pmetric.HistogramDataPoint {
		m := ms.AppendEmpty()
		m.SetName(metricDuration)
		m.SetUnit("s")
		hg := m.SetEmptyHistogram()
		hg.SetAggregationTemporality(temp)
		dp := hg.DataPoints().AppendEmpty()
		dp.SetStartTimestamp(pcommon.NewTimestampFromTime(at.Add(-time.Second)))
		dp.SetTimestamp(pcommon.NewTimestampFromTime(at))
		dp.ExplicitBounds().FromRaw([]float64{0.1, 1})
		dp.BucketCounts().FromRaw([]uint64{1, 2, 0})
		dp.SetCount(3)
		dp.SetSum(0.9)
		dp.Attributes().PutStr(attrMethod, "GET")
		dp.Attributes().PutStr(attrRoute, route)
		dp.Attributes().PutInt(attrStatusCode, 200)
		return dp
	}
	publish := func(i int, fn func(pmetric.MetricSlice, time.Time)) {
		md := pmetric.NewMetrics()
		rm := md.ResourceMetrics().AppendEmpty()
		a := rm.Resource().Attributes()
		a.PutStr(resTenantID, tenant.String())
		a.PutStr(resTokenID, token.String())
		a.PutStr(resResourceHash, strings.Repeat("ef", 32))
		a.PutStr(resServiceName, "api")
		fn(rm.ScopeMetrics().AppendEmpty().Metrics(), now.Add(-time.Duration(i+1)*time.Second))
		b, err := (&pmetric.ProtoMarshaler{}).MarshalMetrics(md)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if _, err := natsx.PublishDedup(ctx, js, natsx.StreamAPIMetrics, natsx.Subject(natsx.SubjectAPIMetrics, tenant.String()),
			hex.EncodeToString(sum[:]), b, nil); err != nil {
			t.Fatal(err)
		}
	}
	for i, name := range names {
		publish(i, func(ms pmetric.MetricSlice, at time.Time) {
			point(ms, pmetric.AggregationTemporalityDelta, "/good", at)
			badPoints[name](point(ms, pmetric.AggregationTemporalityDelta, "/bad", at))
		})
	}
	publish(len(names), func(ms pmetric.MetricSlice, at time.Time) {
		point(ms, pmetric.AggregationTemporalityDelta, "/good", at)
		point(ms, pmetric.AggregationTemporalityUnspecified, "/bad", at)
	})
	// a delta point that adds more than MaxPointRequests
	publish(len(names)+1, func(ms pmetric.MetricSlice, at time.Time) {
		point(ms, pmetric.AggregationTemporalityDelta, "/good", at)
		dp := point(ms, pmetric.AggregationTemporalityDelta, "/bad", at)
		dp.BucketCounts().FromRaw([]uint64{0, MaxPointRequests + 1, 0})
		dp.SetCount(MaxPointRequests + 1)
	})
	messages := len(names) + 2

	wcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := pdb.Open(ctx, wcfg, "badpoints-writer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wdb.Close)
	logs := &syncBuf{}
	w, err := New(Config{
		Settings: Settings{NodeID: "badpoints", OpsAddr: "127.0.0.1:0", Batch: 64, FirstPointThreshold: DefaultFirstPointThreshold,
			RouteCap: 300, ServiceCap: 50},
		NC: nc, Replicas: 1, DB: wdb, Log: slog.New(slog.NewTextHandler(logs, nil)), Registry: prometheus.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		<-done
		if t.Failed() {
			t.Logf("writer log:\n%s", tail(logs.String(), 6000))
		}
	})

	cons, err := js.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		info, err := cons.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 && info.AckFloor.Stream >= uint64(messages) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue not drained: pending %d, ack pending %d", info.NumPending, info.NumAckPending)
		}
		time.Sleep(250 * time.Millisecond)
	}
	var good, bad int64
	if err := super.QueryRow(ctx, `select coalesce(sum(count) filter (where route = '/good'), 0)::bigint,
	    coalesce(sum(count) filter (where route <> '/good'), 0)::bigint from api.metrics_minute where tenant_id = $1`, tenant).Scan(&good, &bad); err != nil {
		t.Fatal(err)
	}
	if good != int64(3*messages) || bad != 0 {
		t.Fatalf("stored %d valid and %d invalid requests, want %d and 0", good, bad, 3*messages)
	}
	dlq, err := js.Stream(ctx, natsx.StreamAPIDLQ)
	if err != nil {
		t.Fatal(err)
	}
	info, err := dlq.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("%d messages dead-lettered, want none", info.State.Msgs)
	}
	if !strings.Contains(logs.String(), "points with values that cannot be stored left out") {
		t.Error("the left-out points are not logged")
	}
}
