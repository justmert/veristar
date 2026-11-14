//go:build metricswriterit

package metricswriter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
)

// TestMetricsWriterPoisonMessages runs the poison message case against a real postgresql
// and a real nats server, with the writer's own Run loop: poison messages of
// one tenant sit in the same fetched batch as normal traffic of another
// tenant and of the poison tenant itself. three kinds of poison:
//
//   - a route that is not valid utf-8, which postgresql refuses with 22021.
//     Decode must catch it before the batch.
//   - a route the database refuses with a check violation (23514, not the
//     "no partition" kind). the test adds the constraint, standing in for
//     any data error nobody foresaw. it must be dead-lettered at once.
//   - a route a trigger refuses with a plain raise (P0001), an error that is
//     not a data error. it must be retried alone, then dead-lettered after a
//     bounded number of deliveries.
//
// the other messages must all be applied, exactly, and the queue must drain.
func TestMetricsWriterPoisonMessages(t *testing.T) {
	ctx := t.Context()
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	nc, js := jobstest.Connect(t, srv.URL)
	super, err := pg.Superuser(ctx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = super.Close(context.Background()) })

	for _, q := range []string{
		`alter table api.metrics_minute add constraint it_poison_check check (route <> '/it-poison-check')`,
		`create function api.it_poison() returns trigger language plpgsql as $$
begin
  if new.route = '/it-poison-raise' then
    raise exception 'it poison';
  end if;
  return new;
end $$`,
		`grant execute on function api.it_poison() to metrics_writer`,
		`create trigger it_poison before insert on api.metrics_minute for each row execute function api.it_poison()`,
	} {
		if _, err := super.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	var user uuid.UUID
	if err := super.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (4343, 'poison') returning id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	type tenantRow struct{ id, token uuid.UUID }
	newTenant := func(slug string) tenantRow {
		var tr tenantRow
		if err := super.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ($1, $1, 'active', $2) returning id`, slug, user).Scan(&tr.id); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256([]byte(slug))
		if err := super.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'it', 'sa_otlp_xxxx', $2, $3) returning id`,
			tr.id, h[:], user).Scan(&tr.token); err != nil {
			t.Fatal(err)
		}
		return tr
	}
	good, poison := newTenant("it-good"), newTenant("it-poison")

	now := time.Now().UTC()
	seq := 0
	publish := func(tr tenantRow, route string, n uint64) {
		t.Helper()
		seq++
		md := pmetric.NewMetrics()
		rm := md.ResourceMetrics().AppendEmpty()
		a := rm.Resource().Attributes()
		a.PutStr(resTenantID, tr.id.String())
		a.PutStr(resTokenID, tr.token.String())
		a.PutStr(resResourceHash, strings.Repeat("cd", 32))
		a.PutStr(resServiceName, "api")
		m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		m.SetName(metricDuration)
		m.SetUnit("s")
		h := m.SetEmptyHistogram()
		h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		dp := h.DataPoints().AppendEmpty()
		dp.SetStartTimestamp(pcommon.NewTimestampFromTime(now.Add(-time.Duration(seq+1) * time.Second)))
		dp.SetTimestamp(pcommon.NewTimestampFromTime(now.Add(-time.Duration(seq) * time.Second)))
		dp.ExplicitBounds().FromRaw(canonicalBounds)
		counts := make([]uint64, 15)
		counts[2] = n
		dp.BucketCounts().FromRaw(counts)
		dp.SetCount(n)
		dp.SetSum(0.02 * float64(n))
		dp.Attributes().PutStr(attrMethod, "GET")
		dp.Attributes().PutStr(attrRoute, route)
		dp.Attributes().PutInt(attrStatusCode, 200)
		b, err := (&pmetric.ProtoMarshaler{}).MarshalMetrics(md)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if _, err := natsx.PublishDedup(ctx, js, natsx.StreamAPIMetrics, natsx.Subject(natsx.SubjectAPIMetrics, tr.id.String()),
			hex.EncodeToString(sum[:]), b, nil); err != nil {
			t.Fatal(err)
		}
	}
	// queued before the writer starts, so its first fetch holds all of them
	var want uint64
	for i := range 30 {
		publish(good, "/ok", 3)
		want += 3
		switch i {
		case 5:
			publish(poison, "/a/\xff/b", 1)
		case 12:
			publish(poison, "/it-poison-check", 1)
		case 20:
			publish(poison, "/it-poison-raise", 1)
		case 25:
			publish(poison, "/fine", 2)
		}
	}

	wcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	wdb, err := pdb.Open(ctx, wcfg, "poison-writer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wdb.Close)
	logs := &syncBuf{}
	w, err := New(Config{
		Settings: Settings{NodeID: "poison", OpsAddr: "127.0.0.1:0", Batch: 64, FirstPointThreshold: DefaultFirstPointThreshold,
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

	// the trigger's poison needs maxDeliveries deliveries with the retry
	// backoff (1 s, 5 s, 30 s, 2 min) before it is dead-lettered
	cons, err := js.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	deadline := started.Add(6 * time.Minute)
	sum := func(tenant uuid.UUID, route string) uint64 {
		var n int64
		err := super.QueryRow(ctx, `select coalesce(sum(count), 0)::bigint from api.metrics_minute where tenant_id = $1 and route = $2`, tenant, route).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return uint64(n)
	}
	goodApplied := false
	for {
		info, err := cons.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !goodApplied && sum(good.id, "/ok") == want && sum(poison.id, "/fine") == 2 {
			goodApplied = true
			t.Logf("other traffic applied after %s, %d messages still pending or unacked",
				time.Since(started).Round(time.Second), info.NumPending+uint64(info.NumAckPending))
		}
		if !goodApplied && time.Since(started) > time.Minute {
			// the poison must not hold up anyone else's messages: they are
			// applied in the first round, not after the poison is gone
			t.Fatalf("other traffic stalled behind the poison: good tenant has %d of %d requests after a minute, pending %d, ack pending %d",
				sum(good.id, "/ok"), want, info.NumPending, info.NumAckPending)
		}
		if info.NumPending == 0 && info.NumAckPending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue not drained: pending %d, ack pending %d; good tenant has %d of %d requests",
				info.NumPending, info.NumAckPending, sum(good.id, "/ok"), want)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got := sum(good.id, "/ok"); got != want {
		t.Fatalf("good tenant: %d requests stored, want %d", got, want)
	}
	if got := sum(poison.id, "/fine"); got != 2 {
		t.Fatalf("the poison tenant's own good message: %d requests stored, want 2", got)
	}
	var poisonRows int64
	if err := super.QueryRow(ctx, `select count(*) from api.metrics_minute where tenant_id = $1 and route <> '/fine'`, poison.id).Scan(&poisonRows); err != nil {
		t.Fatal(err)
	}
	if poisonRows != 0 {
		t.Fatalf("%d rows of poison messages stored", poisonRows)
	}

	// exactly the three poison messages are on API_DLQ, each with its reason
	dlq, err := js.OrderedConsumer(ctx, natsx.StreamAPIDLQ, jetstream.OrderedConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for {
		m, err := dlq.Next(jetstream.FetchMaxWait(2 * time.Second))
		if err != nil {
			break
		}
		if m.Subject() != natsx.Subject(natsx.SubjectAPIDLQ, poison.id.String()) {
			t.Errorf("dead letter on %s", m.Subject())
		}
		reasons = append(reasons, m.Headers().Get(natsx.HeaderDLQReason))
	}
	if len(reasons) != 3 {
		t.Fatalf("%d dead letters, want 3: %q", len(reasons), reasons)
	}
	for _, want := range []string{"not valid utf-8", "data error", "after 5 deliveries"} {
		found := false
		for _, r := range reasons {
			found = found || strings.Contains(r, want)
		}
		if !found {
			t.Errorf("no dead letter reason contains %q: %q", want, reasons)
		}
	}
}
