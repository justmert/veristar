package scheduler_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/jobs/scheduler"
	"github.com/justmert/stellar-analytics/internal/natsx"
)

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestScheduler runs every lock scenario against one real nats server and
// one real postgres, because starting them is most of the test time.
func TestScheduler(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)

	t.Run("only one daemon runs the tasks and the other takes over", func(t *testing.T) {
		testOneLeader(t, srv.URL, d)
	})
	t.Run("a stale leader's transaction is refused by the fence", func(t *testing.T) {
		testStaleLeader(t, srv.URL, d)
	})
	t.Run("run once takes the lock and is refused while a daemon leads", func(t *testing.T) {
		testRunOnce(t, srv.URL, d)
	})
}

func lockFor(t *testing.T, url, holder string) (*nats.Conn, *natsx.Lock) {
	t.Helper()
	nc, js := jobstest.Connect(t, url)
	kv, err := js.KeyValue(t.Context(), natsx.BucketLocks)
	require.NoError(t, err)
	l, err := natsx.NewLock(kv, natsx.LockJobs, holder)
	require.NoError(t, err)
	return nc, l
}

// fencedWrite is a real write through Leader.Tx: one cp.stats_dedup row.
// it returns the error of the whole transaction.
func fencedWrite(ctx context.Context, l *scheduler.Leader, id string) error {
	return l.Tx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into cp.stats_dedup (message_id) values ($1)`, id)
		return err
	})
}

func dedupHas(t *testing.T, d *pdb.DB, id string) bool {
	t.Helper()
	for _, x := range jobstest.DedupIDs(t, d) {
		if x == id {
			return true
		}
	}
	return false
}

type daemon struct {
	name    string
	nc      *nats.Conn
	sched   *scheduler.Scheduler
	running atomic.Bool
	ticks   atomic.Int64
	writes  atomic.Int64
	stop    context.CancelFunc
	done    chan error
}

func testOneLeader(t *testing.T, url string, d *pdb.DB) {
	var concurrent, maxConcurrent atomic.Int32
	var writeErrs sync.Map
	start := func(name string) *daemon {
		nc, lock := lockFor(t, url, name)
		m, err := scheduler.NewMetrics(prometheus.NewRegistry())
		require.NoError(t, err)
		dm := &daemon{name: name, nc: nc, done: make(chan error, 1)}
		tasks := []scheduler.Task{
			{
				// continuous: holds the "running" flag for as long as it leads
				// and writes through the fence every 200 ms
				Name: "continuous",
				Run: func(ctx context.Context, l *scheduler.Leader) error {
					n := concurrent.Add(1)
					defer concurrent.Add(-1)
					for {
						m := maxConcurrent.Load()
						if n <= m || maxConcurrent.CompareAndSwap(m, n) {
							break
						}
					}
					dm.running.Store(true)
					defer dm.running.Store(false)
					i := 0
					for {
						select {
						case <-ctx.Done():
							return nil
						case <-time.After(200 * time.Millisecond):
						}
						i++
						id := name + "-" + time.Now().Format(time.RFC3339Nano)
						if err := fencedWrite(ctx, l, id); err != nil {
							if ctx.Err() == nil {
								writeErrs.Store(id, err)
							}
							continue
						}
						dm.writes.Add(1)
					}
				},
				Once: func(context.Context, *scheduler.Leader) error { return nil },
			},
			{
				Name:  "periodic",
				Every: time.Second,
				Run: func(context.Context, *scheduler.Leader) error {
					dm.ticks.Add(1)
					return nil
				},
				Once: func(context.Context, *scheduler.Leader) error { return nil },
			},
		}
		dm.sched = &scheduler.Scheduler{Lock: lock, DB: d, Tasks: tasks, Log: quietLog(), Metrics: m, RetryEvery: 250 * time.Millisecond}
		ctx, cancel := context.WithCancel(t.Context())
		dm.stop = cancel
		go func() { dm.done <- dm.sched.Run(ctx) }()
		return dm
	}
	a := start("daemon-a")
	b := start("daemon-b")
	t.Cleanup(func() { a.stop(); b.stop() })

	require.Eventually(t, func() bool { return a.running.Load() || b.running.Load() }, 10*time.Second, 50*time.Millisecond)
	// across more than one renewal: exactly one daemon runs the tasks
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		require.NotEqual(t, a.running.Load(), b.running.Load(), "exactly one daemon must run the tasks")
		time.Sleep(50 * time.Millisecond)
	}
	leader, follower := a, b
	if b.running.Load() {
		leader, follower = b, a
	}
	require.Positive(t, leader.writes.Load(), "the leader's fenced writes must commit")
	require.GreaterOrEqual(t, leader.ticks.Load(), int64(5), "the periodic task runs every second on the leader")
	require.Zero(t, follower.writes.Load())
	require.Zero(t, follower.ticks.Load(), "the follower runs nothing")

	// kill the leader's nats connection: it cannot renew or release
	killed := time.Now()
	leader.nc.Close()
	require.Eventually(t, func() bool { return follower.running.Load() }, natsx.LocksTTL+5*time.Second, 50*time.Millisecond,
		"the follower did not take over")
	t.Logf("takeover after %s", time.Since(killed).Round(10*time.Millisecond))
	require.Eventually(t, func() bool { return !leader.running.Load() }, natsx.LockRenewEvery+2*time.Second, 50*time.Millisecond,
		"the old leader must stop its tasks once its renewal fails")
	before := follower.writes.Load()
	require.Eventually(t, func() bool { return follower.writes.Load() > before+3 }, 10*time.Second, 50*time.Millisecond,
		"the new leader's fenced writes must commit")
	require.Equal(t, int32(1), maxConcurrent.Load(), "two daemons ran the task at the same time")
	writeErrs.Range(func(k, v any) bool {
		t.Errorf("fenced write %v failed while leading: %v", k, v)
		return true
	})

	follower.stop()
	select {
	case err := <-follower.done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

// testStaleLeader pauses a leader the way a stopped process or a long gc
// pause does: it keeps its token but stops renewing. after the lock expires
// and a new holder writes, the old holder's transaction must roll back.
func testStaleLeader(t *testing.T, url string, d *pdb.DB) {
	ctx := t.Context()
	_, lockA := lockFor(t, url, "stale-a")
	_, lockB := lockFor(t, url, "stale-b")
	_, err := lockA.Acquire(ctx)
	require.NoError(t, err)
	la, err := scheduler.NewLeader(lockA, d)
	require.NoError(t, err)
	lb, err := scheduler.NewLeader(lockB, d)
	require.NoError(t, err)
	require.NoError(t, fencedWrite(ctx, la, "stale-a-1"))

	// a no longer renews; b takes the lock as soon as it expires
	require.Eventually(t, func() bool {
		_, err := lockB.Acquire(ctx)
		return err == nil
	}, natsx.LocksTTL+5*time.Second, 200*time.Millisecond)
	require.NotZero(t, lockA.Revision(), "the paused holder still believes it holds the lock")
	require.NoError(t, fencedWrite(ctx, lb, "stale-b-1"))

	err = fencedWrite(ctx, la, "stale-a-2")
	require.ErrorIs(t, err, natsx.ErrFenced)
	require.False(t, dedupHas(t, d, "stale-a-2"), "the stale leader's write must not commit")
	require.True(t, dedupHas(t, d, "stale-a-1"))
	require.True(t, dedupHas(t, d, "stale-b-1"))

	// the new holder keeps writing
	require.NoError(t, fencedWrite(ctx, lb, "stale-b-2"))
	// a holder that knows it lost the lock does not even open a transaction
	_, err = lockA.Renew(ctx)
	require.ErrorIs(t, err, natsx.ErrLockLost)
	require.ErrorIs(t, fencedWrite(ctx, la, "stale-a-3"), natsx.ErrLockLost)
	require.NoError(t, lockB.Release(ctx))
}

func testRunOnce(t *testing.T, url string, d *pdb.DB) {
	ctx := t.Context()
	newSched := func(holder string, once func(context.Context, *scheduler.Leader) error) *scheduler.Scheduler {
		_, lock := lockFor(t, url, holder)
		m, err := scheduler.NewMetrics(prometheus.NewRegistry())
		require.NoError(t, err)
		return &scheduler.Scheduler{Lock: lock, DB: d, Log: quietLog(), Metrics: m, RetryEvery: 250 * time.Millisecond,
			Tasks: []scheduler.Task{{
				Name: "t",
				Run: func(ctx context.Context, _ *scheduler.Leader) error {
					<-ctx.Done()
					return nil
				},
				Once: once,
			}}}
	}
	var ran atomic.Int32
	once := func(ctx context.Context, l *scheduler.Leader) error {
		ran.Add(1)
		return fencedWrite(ctx, l, "once-"+time.Now().Format(time.RFC3339Nano))
	}
	s := newSched("once-1", once)
	require.NoError(t, s.RunOnce(ctx, "t"))
	require.Equal(t, int32(1), ran.Load())
	require.ErrorIs(t, s.RunOnce(ctx, "missing"), scheduler.ErrUnknownTask)
	// the lock was released, so a second one-off run gets it at once
	require.NoError(t, newSched("once-2", once).RunOnce(ctx, "t"))

	// a task error is reported
	boom := errors.New("boom")
	require.ErrorIs(t, newSched("once-3", func(context.Context, *scheduler.Leader) error { return boom }).RunOnce(ctx, "t"), boom)

	// while a daemon leads, a one-off run is refused and does not run
	dctx, stop := context.WithCancel(ctx)
	daemon := newSched("daemon", once)
	done := make(chan error, 1)
	go func() { done <- daemon.Run(dctx) }()
	kv := lockKV(t, url)
	require.Eventually(t, func() bool {
		e, err := kv.Get(ctx, natsx.LockJobs)
		return err == nil && string(e.Value()) == "daemon"
	}, 10*time.Second, 50*time.Millisecond)
	before := ran.Load()
	require.ErrorIs(t, newSched("once-4", once).RunOnce(ctx, "t"), natsx.ErrLockHeld)
	require.Equal(t, before, ran.Load())
	stop()
	require.ErrorIs(t, <-done, context.Canceled)
}

func lockKV(t *testing.T, url string) jetstream.KeyValue {
	_, js := jobstest.Connect(t, url)
	kv, err := js.KeyValue(t.Context(), natsx.BucketLocks)
	require.NoError(t, err)
	return kv
}
