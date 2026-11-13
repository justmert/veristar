package gateway_test

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/jobs/edgestats"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/jobs/scheduler"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
)

func statsLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestStatsCountValidation(t *testing.T) {
	srv := jobstest.NATS(t)
	_, js := jobstest.Connect(t, srv.URL)
	_, err := gateway.NewStats(gateway.StatsConfig{Node: "edge.1", JS: js, Log: statsLog(), Registerer: prometheus.NewRegistry()})
	require.Error(t, err, "a node id with a dot is not one subject token")
	s, err := gateway.NewStats(gateway.StatsConfig{Node: "edge-1", JS: js, Log: statsLog(), Registerer: prometheus.NewRegistry()})
	require.NoError(t, err)
	site := uuid.NewString()
	require.ErrorIs(t, s.Count(site, "dropped"), gateway.ErrBadOutcome)
	require.ErrorIs(t, s.Count(site, "rejected_"), gateway.ErrBadOutcome)
	require.ErrorIs(t, s.Count(strings.ToUpper(site), gateway.StatAccepted), gateway.ErrBadSiteID)
	require.ErrorIs(t, s.Count("not-a-site", gateway.StatAccepted), gateway.ErrBadSiteID)
	for _, r := range gateway.Reasons() {
		require.NoError(t, s.Rejected(site, r), "every reason must be a valid outcome: %s", r)
	}
	require.NoError(t, s.Flush(t.Context()))
}

// TestStatsReachHourlyRows sends outcomes through the real publisher and the
// real jobs task, across a nats outage, and compares cp.web_ingest_hourly
// with what was counted.
func TestStatsReachHourlyRows(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	_, js := jobstest.Connect(t, srv.URL)
	tenant := uuid.New()
	sites := []uuid.UUID{uuid.New(), uuid.New()}
	for _, s := range sites {
		jobstest.PutSite(t, js, s, tenant)
	}

	nodes := map[string]*gateway.Stats{}
	for _, n := range []string{"edge-1", "edge-2"} {
		_, njs := jobstest.Connect(t, srv.URL)
		s, err := gateway.NewStats(gateway.StatsConfig{Node: n, JS: njs, Log: statsLog(), Registerer: prometheus.NewRegistry()})
		require.NoError(t, err)
		nodes[n] = s
	}
	outcomes := []string{gateway.StatAccepted, gateway.StatQuarantined, string(gateway.ReasonOrigin), string(gateway.ReasonRateLimited)}
	want := map[string]int64{}
	rng := rand.New(rand.NewPCG(1, 2))
	countSome := func() {
		for range 500 {
			site := sites[rng.IntN(len(sites))].String()
			outcome := outcomes[rng.IntN(len(outcomes))]
			for _, s := range nodes {
				require.NoError(t, s.Count(site, outcome))
				want[site+"/"+outcome]++
			}
		}
	}
	flushAll := func() error {
		for _, s := range nodes {
			if err := s.Flush(t.Context()); err != nil {
				return err
			}
		}
		return nil
	}

	countSome()
	require.NoError(t, flushAll())
	countSome()
	require.NoError(t, flushAll())

	// nats goes away: the flush fails and nothing is lost
	countSome()
	srv.Stop(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	require.Error(t, nodes["edge-1"].Flush(ctx))
	cancel()
	countSome() // counted while a window is pending
	srv.Start(t)
	// a single server keeps file streams across a restart but not memory
	// buckets such as locks, so the topology is applied again
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), js, spec)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return flushAll() == nil }, 30*time.Second, 200*time.Millisecond)

	// the shutdown flush of Run sends what is left
	countSome()
	rctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- nodes["edge-2"].Run(rctx) }()
	stop()
	require.ErrorIs(t, <-done, context.Canceled)
	require.NoError(t, nodes["edge-1"].Flush(t.Context()))

	// every message id is node plus window sequence, and unique
	info, err := js.Stream(t.Context(), natsx.StreamEdgeStats)
	require.NoError(t, err)
	si, err := info.Info(t.Context())
	require.NoError(t, err)
	queued := si.State.Msgs
	require.GreaterOrEqual(t, queued, uint64(8))

	drain(t, js, d)
	got := jobstest.Totals(t, d)
	require.Equal(t, want, got, "cp.web_ingest_hourly must equal the outcomes counted")
	ids := jobstest.DedupIDs(t, d)
	require.Len(t, ids, int(queued))
	for _, id := range ids {
		node, _, ok := strings.Cut(id, ".")
		require.True(t, ok, id)
		require.Contains(t, nodes, node, id)
	}
}

// drain runs the jobs edgestats task once, as `jobs run edgestats` does.
func drain(t *testing.T, js jetstream.JetStream, d *pdb.DB) {
	t.Helper()
	sites := registry.NewSiteWatcher(js, statsLog())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { _ = sites.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	kv, err := js.KeyValue(t.Context(), natsx.BucketLocks)
	require.NoError(t, err)
	lock, err := natsx.NewLock(kv, natsx.LockJobs, "stats-test")
	require.NoError(t, err)
	_, err = lock.Acquire(t.Context())
	require.NoError(t, err)
	defer func() { _ = lock.Release(context.WithoutCancel(t.Context())) }()
	leader, err := scheduler.NewLeader(lock, d)
	require.NoError(t, err)
	m, err := edgestats.NewMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	dr, err := edgestats.New(js, sites, statsLog(), m)
	require.NoError(t, err)
	_, err = dr.Drain(t.Context(), leader.Tx)
	require.NoError(t, err)
}
