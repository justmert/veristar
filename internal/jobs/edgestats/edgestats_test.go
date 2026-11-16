package edgestats_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/jobs/edgestats"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/jobs/scheduler"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
)

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

type env struct {
	js      jetstream.JetStream
	db      *pdb.DB
	leader  *scheduler.Leader
	drainer *edgestats.Drainer
	reg     *prometheus.Registry
	site    uuid.UUID
	tenant  uuid.UUID
}

// setup starts nats and postgres, registers one site and holds the jobs
// lock, as the leading daemon does.
func setup(t *testing.T) *env {
	t.Helper()
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	e := &env{db: jobstest.JobsDB(t, pg), site: uuid.New(), tenant: uuid.New(), reg: prometheus.NewRegistry()}
	_, e.js = jobstest.Connect(t, srv.URL)
	jobstest.PutSite(t, e.js, e.site, e.tenant)

	sites := registry.NewSiteWatcher(e.js, quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = sites.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, func() bool { _, ok := sites.Get(e.site.String()); return ok }, 10*time.Second, 20*time.Millisecond)

	kv, err := e.js.KeyValue(t.Context(), natsx.BucketLocks)
	require.NoError(t, err)
	lock, err := natsx.NewLock(kv, natsx.LockJobs, "edgestats-test")
	require.NoError(t, err)
	_, err = lock.Acquire(t.Context())
	require.NoError(t, err)
	e.leader, err = scheduler.NewLeader(lock, e.db)
	require.NoError(t, err)
	m, err := edgestats.NewMetrics(e.reg)
	require.NoError(t, err)
	e.drainer, err = edgestats.New(e.js, sites, quietLog(), m)
	require.NoError(t, err)
	return e
}

// publish sends one window the way the gateway's stats publisher does.
func (e *env) publish(t *testing.T, node string, seq uint64, counts map[string]int64) *jetstream.PubAck {
	t.Helper()
	var stats []events.EdgeStat
	for outcome, n := range counts {
		stats = append(stats, events.EdgeStat{Node: node, WindowSeq: seq, SiteID: e.site.String(), Outcome: outcome, Count: n})
	}
	subject, id, data, err := events.EncodeEdgeStats(stats)
	require.NoError(t, err)
	ack, err := natsx.PublishDedup(t.Context(), e.js, natsx.StreamEdgeStats, subject, id, data, nil)
	require.NoError(t, err)
	return ack
}

func (e *env) fetchOne(t *testing.T) jetstream.Msg {
	t.Helper()
	cons, err := e.js.Consumer(t.Context(), natsx.StreamEdgeStats, natsx.ConsumerJobs)
	require.NoError(t, err)
	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	require.NoError(t, err)
	var got jetstream.Msg
	for m := range batch.Messages() {
		got = m
	}
	require.NotNil(t, got, "no message delivered")
	return got
}

func (e *env) total(t *testing.T, outcome string) int64 {
	return jobstest.Totals(t, e.db)[e.site.String()+"/"+outcome]
}

func TestEdgeStats(t *testing.T) {
	e := setup(t)

	t.Run("hourly rows equal the outcomes sent", func(t *testing.T) {
		e.publish(t, "edge-1", 100, map[string]int64{"accepted": 7, "rejected_origin": 2})
		e.publish(t, "edge-2", 100, map[string]int64{"accepted": 5, "quarantined": 1})
		e.publish(t, "edge-1", 101, map[string]int64{"accepted": 1, "rejected_origin": 3})
		n, err := e.drainer.Drain(t.Context(), e.leader.Tx)
		require.NoError(t, err)
		require.Equal(t, 3, n)
		rows := jobstest.Rows(t, e.db)
		for _, r := range rows {
			require.Equal(t, e.tenant, r.Tenant, "the tenant comes from the registry")
			require.Equal(t, r.Hour, r.Hour.Truncate(time.Hour))
			require.WithinDuration(t, time.Now(), r.Hour, time.Hour)
		}
		require.Equal(t, map[string]int64{
			e.site.String() + "/accepted":        13,
			e.site.String() + "/quarantined":     1,
			e.site.String() + "/rejected_origin": 5,
		}, jobstest.Totals(t, e.db))
		require.ElementsMatch(t, []string{"edge-1.100", "edge-2.100", "edge-1.101"}, jobstest.DedupIDs(t, e.db))
	})

	t.Run("evidence goes to its own daily tables, never into the outcomes", func(t *testing.T) {
		before := jobstest.Totals(t, e.db)
		e.publish(t, "edge-1", 150, map[string]int64{
			"accepted": 4, "evidence_net_access": 3, "evidence_net_hosting": 1,
			"network_00000000000000aa": 1, "network_00000000000000bb": 1,
		})
		// a second node reports one of the same networks and another event
		e.publish(t, "edge-2", 150, map[string]int64{
			"accepted": 1, "evidence_net_access": 1, "network_00000000000000aa": 1,
		})
		n, err := e.drainer.Drain(t.Context(), e.leader.Tx)
		require.NoError(t, err)
		require.Equal(t, 2, n)
		after := jobstest.Totals(t, e.db)
		require.Equal(t, before[e.site.String()+"/accepted"]+5, after[e.site.String()+"/accepted"])
		for k := range after {
			require.NotContains(t, k, "evidence_", "evidence must not become a request outcome")
			require.NotContains(t, k, "network_", "a network must not become a request outcome")
		}
		var access, hosting, networks int64
		err = e.db.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			if err := tx.QueryRow(t.Context(), `select
			    coalesce(sum(count) filter (where class = 'access'), 0), coalesce(sum(count) filter (where class = 'hosting'), 0)
			  from cp.web_evidence_daily where site_id = $1`, e.site).Scan(&access, &hosting); err != nil {
				return err
			}
			return tx.QueryRow(t.Context(), `select count(*) from cp.web_networks_daily where site_id = $1 and tenant_id = $2`,
				e.site, e.tenant).Scan(&networks)
		})
		require.NoError(t, err)
		require.Equal(t, int64(4), access)
		require.Equal(t, int64(1), hosting)
		require.Equal(t, int64(2), networks, "a network two nodes report counts once")
	})

	t.Run("page signals and coverage counters go to their daily tables", func(t *testing.T) {
		before := jobstest.Totals(t, e.db)
		e.publish(t, "edge-1", 160, map[string]int64{
			"accepted": 3, "signal_views": 3, "signal_framed": 1, "signal_never_visible": 2,
			"script_requests": 10, "script_gpc": 2,
		})
		// the gpc part can arrive in a later window than its requests
		e.publish(t, "edge-2", 160, map[string]int64{"script_gpc": 1})
		e.publish(t, "edge-2", 161, map[string]int64{"script_requests": 5})
		n, err := e.drainer.Drain(t.Context(), e.leader.Tx)
		require.NoError(t, err)
		require.Equal(t, 3, n)
		after := jobstest.Totals(t, e.db)
		require.Equal(t, before[e.site.String()+"/accepted"]+3, after[e.site.String()+"/accepted"])
		for k := range after {
			require.NotContains(t, k, "signal_", "a page signal must not become a request outcome")
			require.NotContains(t, k, "script_", "a script counter must not become a request outcome")
		}
		var views, framed, nv, requests, gpc int64
		var tenant uuid.UUID
		err = e.db.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			if err := tx.QueryRow(t.Context(), `select
			    coalesce(sum(count) filter (where signal = 'signal_views'), 0),
			    coalesce(sum(count) filter (where signal = 'signal_framed'), 0),
			    coalesce(sum(count) filter (where signal = 'signal_never_visible'), 0)
			  from cp.web_signals_daily where site_id = $1`, e.site).Scan(&views, &framed, &nv); err != nil {
				return err
			}
			return tx.QueryRow(t.Context(), `select tenant_id, script_requests, gpc_requests
			  from agg.web_coverage_daily where site_id = $1`, e.site).Scan(&tenant, &requests, &gpc)
		})
		require.NoError(t, err)
		require.Equal(t, []int64{3, 1, 2}, []int64{views, framed, nv})
		require.Equal(t, e.tenant, tenant)
		require.Equal(t, int64(15), requests)
		require.Equal(t, int64(3), gpc)
	})

	t.Run("an unknown page signal or script counter is refused", func(t *testing.T) {
		for i, outcome := range []string{"signal_hovered", "script_dnt"} {
			e.publish(t, "edge-4", 170+uint64(i), map[string]int64{outcome: 1})
			msg := e.fetchOne(t)
			err := e.drainer.Handle(t.Context(), e.leader.Tx, msg)
			require.True(t, natsx.IsPermanent(err), "%s: %v", outcome, err)
			require.NoError(t, msg.Ack())
		}
	})

	t.Run("a redelivered message is not counted twice", func(t *testing.T) {
		before := e.total(t, "accepted")
		e.publish(t, "edge-1", 200, map[string]int64{"accepted": 4})
		msg := e.fetchOne(t)
		require.NoError(t, e.drainer.Handle(t.Context(), e.leader.Tx, msg))
		require.Equal(t, before+4, e.total(t, "accepted"))
		// the commit happened but the ack did not: the server delivers it again
		require.NoError(t, msg.Nak())
		again := e.fetchOne(t)
		meta, err := again.Metadata()
		require.NoError(t, err)
		require.Equal(t, uint64(2), meta.NumDelivered, "this must be the same message, delivered again")
		require.NoError(t, e.drainer.Handle(t.Context(), e.leader.Tx, again))
		require.NoError(t, again.DoubleAck(t.Context()))
		require.Equal(t, before+4, e.total(t, "accepted"))
		require.InDelta(t, 1, metric(t, e.reg, "jobs_edgestats_duplicates_total", ""), 0)
	})

	t.Run("a re-sent window is not counted twice after the stream forgot its id", func(t *testing.T) {
		before := e.total(t, "accepted")
		// inside the duplicate window the stream itself drops the repeat
		require.False(t, e.publish(t, "edge-3", 300, map[string]int64{"accepted": 6}).Duplicate)
		require.True(t, e.publish(t, "edge-3", 300, map[string]int64{"accepted": 6}).Duplicate)
		_, err := e.drainer.Drain(t.Context(), e.leader.Tx)
		require.NoError(t, err)
		require.Equal(t, before+6, e.total(t, "accepted"))
		// recreating the stream drops its duplicate state, as the end of the
		// two-minute window does; cp.stats_dedup still refuses the window
		require.NoError(t, e.js.DeleteStream(t.Context(), natsx.StreamEdgeStats))
		spec, err := natsx.Topology(1)
		require.NoError(t, err)
		_, err = natsx.Apply(t.Context(), e.js, spec)
		require.NoError(t, err)
		require.False(t, e.publish(t, "edge-3", 300, map[string]int64{"accepted": 6}).Duplicate, "the stream must store it again")
		n, err := e.drainer.Drain(t.Context(), e.leader.Tx)
		require.NoError(t, err)
		require.Equal(t, 1, n)
		require.Equal(t, before+6, e.total(t, "accepted"))
	})

	t.Run("a malformed message is discarded loudly", func(t *testing.T) {
		before := jobstest.Totals(t, e.db)
		_, err := natsx.PublishDedup(t.Context(), e.js, natsx.StreamEdgeStats, "stats.web.edge-9", "edge-9.1", []byte(`[{"node":"edge-9"}]`), nil)
		require.NoError(t, err)
		// a message whose id does not match its window
		var stats []events.EdgeStat
		stats = append(stats, events.EdgeStat{Node: "edge-9", WindowSeq: 2, SiteID: e.site.String(), Outcome: "accepted", Count: 1})
		subject, _, data, err := events.EncodeEdgeStats(stats)
		require.NoError(t, err)
		_, err = natsx.PublishDedup(t.Context(), e.js, natsx.StreamEdgeStats, subject, "edge-9.999", data, nil)
		require.NoError(t, err)
		n, err := e.drainer.Drain(t.Context(), e.leader.Tx)
		require.NoError(t, err)
		require.Equal(t, 2, n)
		require.InDelta(t, 2, metric(t, e.reg, "jobs_edgestats_discarded_total", ""), 0)
		require.Equal(t, before, jobstest.Totals(t, e.db))
	})

	t.Run("an unknown site is retried, then only its counters are dropped", func(t *testing.T) {
		before := e.total(t, "accepted")
		unknown := uuid.New().String()
		stats := []events.EdgeStat{
			{Node: "edge-4", WindowSeq: 1, SiteID: e.site.String(), Outcome: "accepted", Count: 3},
			{Node: "edge-4", WindowSeq: 1, SiteID: unknown, Outcome: "accepted", Count: 8},
		}
		subject, id, data, err := events.EncodeEdgeStats(stats)
		require.NoError(t, err)
		_, err = natsx.PublishDedup(t.Context(), e.js, natsx.StreamEdgeStats, subject, id, data, nil)
		require.NoError(t, err)
		for attempt := 1; attempt < 4; attempt++ {
			msg := e.fetchOne(t)
			err := e.drainer.Handle(t.Context(), e.leader.Tx, msg)
			require.Error(t, err, "delivery %d must be retried", attempt)
			require.False(t, natsx.IsPermanent(err))
			require.NoError(t, msg.Nak())
			require.Equal(t, before, e.total(t, "accepted"), "nothing is applied while retrying")
		}
		msg := e.fetchOne(t)
		require.NoError(t, e.drainer.Handle(t.Context(), e.leader.Tx, msg))
		require.NoError(t, msg.DoubleAck(t.Context()))
		require.Equal(t, before+3, e.total(t, "accepted"))
		_, found := jobstest.Totals(t, e.db)[unknown+"/accepted"]
		require.False(t, found)
		require.InDelta(t, 8, metric(t, e.reg, "jobs_edgestats_unattributed_total", "accepted"), 0)
		require.InDelta(t, 8, metric(t, e.reg, "jobs_edgestats_dropped_total", ""), 0)
	})

	t.Run("a fenced transaction applies nothing", func(t *testing.T) {
		before := e.total(t, "accepted")
		e.publish(t, "edge-5", 1, map[string]int64{"accepted": 9})
		msg := e.fetchOne(t)
		// a newer holder's token is stored, as after a takeover
		require.NoError(t, e.db.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `update cp.leader_fence set epoch = epoch + 1 where name = 'jobs'`)
			return err
		}))
		err := e.drainer.Handle(t.Context(), e.leader.Tx, msg)
		require.ErrorIs(t, err, natsx.ErrFenced)
		require.NoError(t, msg.Nak())
		require.Equal(t, before, e.total(t, "accepted"))
		require.NotContains(t, jobstest.DedupIDs(t, e.db), "edge-5.1")
	})
}

// metric reads one counter back from the registry, so the test checks what
// /metrics would show. label selects the series of a vector; empty means
// the counter has no labels.
func metric(t *testing.T, reg *prometheus.Registry, name, label string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if label == "" || (len(m.GetLabel()) == 1 && m.GetLabel()[0].GetValue() == label) {
				return m.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("metric %s{%s} not found", name, label)
	return 0
}
