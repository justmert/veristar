//go:build gatewayit

package gatewayit

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/jobs/edgestats"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/jobs/scheduler"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/tests/gateway/gwclient"
)

// TestStatsIntoPostgres sends known outcomes through a gateway node, lets it
// publish its window to EDGE_STATS, drains the window with the jobs
// edgestats task into cp.web_ingest_hourly, delivers the same message a
// second time (commit done, ack lost) and checks the table holds exactly
// the outcomes sent.
func TestStatsIntoPostgres(t *testing.T) {
	s := newStack(t)
	site := s.PutSite(t, "", "https://stats.sa-tenant.example", "active")
	pg := jobstest.Postgres(t)
	db := jobstest.JobsDB(t, pg)

	var clk clock
	p := startInProcess(t, s, "edge-stats", clk.Now)
	c := inProcessClient(p, site.Origin, firefoxUA, "198.51.100.70")
	post := func(cl *gwclient.Client, extra http.Header, want int) {
		t.Helper()
		pr := proofFor(t, c, site.ID)
		status, err := cl.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/s", pr), extra)
		if err != nil || status != want {
			t.Fatalf("status %d, %v, want %d", status, err, want)
		}
	}
	for range 3 {
		post(c, nil, http.StatusAccepted)
	}
	wrong := inProcessClient(p, "https://evil.example", firefoxUA, "198.51.100.70")
	for range 2 {
		post(wrong, nil, http.StatusForbidden)
	}
	post(c, http.Header{"Sec-Gpc": {"1"}}, http.StatusNoContent)
	if err := p.Node.Stats().Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	// the jobs side, as the daemon runs it: registry snapshot, jobs lock,
	// fenced transactions
	sites := registry.NewSiteWatcher(s.JS, slog.New(slog.DiscardHandler))
	wctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = sites.Run(wctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	for !sites.Loaded() {
		time.Sleep(20 * time.Millisecond)
	}
	kv, err := s.JS.KeyValue(context.Background(), natsx.BucketLocks)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := natsx.NewLock(kv, natsx.LockJobs, "gatewayit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	leader, err := scheduler.NewLeader(lock, db)
	if err != nil {
		t.Fatal(err)
	}
	m, err := edgestats.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	drainer, err := edgestats.New(s.JS, sites, slog.New(slog.DiscardHandler), m)
	if err != nil {
		t.Fatal(err)
	}

	cons, err := s.JS.Consumer(context.Background(), natsx.StreamEdgeStats, natsx.ConsumerJobs)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := drainer.Handle(context.Background(), leader.Tx, msg); err != nil {
		t.Fatal(err)
	}
	// commit done, ack lost: the server delivers the same message again
	if err := msg.Nak(); err != nil {
		t.Fatal(err)
	}
	again, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := again.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if meta.NumDelivered != 2 {
		t.Fatalf("second fetch is delivery %d, want the same message again", meta.NumDelivered)
	}
	if err := drainer.Handle(context.Background(), leader.Tx, again); err != nil {
		t.Fatal(err)
	}
	if err := again.DoubleAck(context.Background()); err != nil {
		t.Fatal(err)
	}
	// nothing else is left: the node sent one window
	if n, err := drainer.Drain(context.Background(), leader.Tx); err != nil || n != 0 {
		t.Fatalf("drain after the window: %d messages, %v", n, err)
	}

	got := jobstest.Totals(t, db)
	want := map[string]int64{
		site.ID + "/accepted":        3,
		site.ID + "/rejected_origin": 2,
		site.ID + "/rejected_gpc":    1,
	}
	if len(got) != len(want) {
		t.Fatalf("cp.web_ingest_hourly totals %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("cp.web_ingest_hourly totals %v, want %v", got, want)
		}
	}
	t.Logf("cp.web_ingest_hourly after a redelivered window: %v, dedup ids %v", got, jobstest.DedupIDs(t, db))
}
