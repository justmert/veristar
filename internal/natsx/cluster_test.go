package natsx_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func connect(t *testing.T, urls string) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	nc, err := nats.Connect(urls, nats.MaxReconnects(-1))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	return nc, js
}

// TestCluster runs everything that needs three replicas against one real
// 3-node cluster, because starting a cluster takes most of the test time.
func TestCluster(t *testing.T) {
	cluster := natsctr.StartCluster(t)
	_, js := connect(t, cluster.URLs())
	spec, err := natsx.Topology(3)
	require.NoError(t, err)

	t.Run("apply is idempotent and reads back equal", func(t *testing.T) {
		testApply(t, js, spec)
	})
	t.Run("per-key ttl removes the key on every node", func(t *testing.T) {
		testKeyTTL(t, cluster, js)
	})
	t.Run("dedup on every stream that declares it", func(t *testing.T) {
		testDedup(t, js)
	})
	t.Run("pull loop acks, retries and dead-letters", func(t *testing.T) {
		testPullLoop(t, js)
	})
	fdb := fenceDB(t)
	t.Run("lock compare-and-set and fencing", func(t *testing.T) {
		testLockCAS(t, cluster.URLs(), fdb)
	})
	t.Run("leader election and takeover", func(t *testing.T) {
		testElection(t, cluster.URLs())
	})
	// these two reset the locks bucket, so they run last
	t.Run("recreated locks bucket lets a new leader in", func(t *testing.T) {
		testFenceAfterReset(t, cluster.URLs(), fdb, natsx.LockJobs, func() {
			require.NoError(t, js.DeleteKeyValue(t.Context(), natsx.BucketLocks))
			_, err := natsx.Apply(t.Context(), js, spec)
			require.NoError(t, err)
		})
	})
	t.Run("full cluster restart lets a new leader in", func(t *testing.T) {
		testFenceAfterReset(t, cluster.URLs(), fdb, natsx.LockMetricsWriter, func() {
			cluster.Restart(t)
		})
	})
}

func testApply(t *testing.T, js jetstream.JetStream, spec natsx.Spec) {
	ctx := t.Context()
	first, err := natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	require.Len(t, first, len(spec.Streams)+len(spec.Buckets)+len(spec.Consumers))
	for _, c := range first {
		require.Equal(t, natsx.ActionCreate, c.Action, c.String())
	}

	second, err := natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	for _, c := range second {
		require.Equal(t, natsx.ActionUnchanged, c.Action, "second apply changed %s", c.String())
	}

	// read every setting back from the server, independent of the diff code
	for _, want := range spec.Streams {
		s, err := js.Stream(ctx, want.Name)
		require.NoError(t, err)
		info, err := s.Info(ctx)
		require.NoError(t, err)
		have := info.Config
		require.Equal(t, want.Subjects, have.Subjects, want.Name)
		require.Equal(t, want.Retention, have.Retention, want.Name)
		require.Equal(t, want.Storage, have.Storage, want.Name)
		require.Equal(t, 3, have.Replicas, want.Name)
		require.Equal(t, want.MaxAge, have.MaxAge, want.Name)
		require.Equal(t, want.Duplicates, have.Duplicates, want.Name)
		require.Equal(t, want.Description, have.Description, want.Name)
		require.NotNil(t, info.Cluster, want.Name)
		require.Len(t, info.Cluster.Replicas, 2, "%s must have a leader and two followers", want.Name)
	}
	expectStream := map[string]struct {
		retention jetstream.RetentionPolicy
		maxAge    time.Duration
	}{
		natsx.StreamWebEvents:  {jetstream.WorkQueuePolicy, 72 * time.Hour},
		natsx.StreamAPIMetrics: {jetstream.WorkQueuePolicy, 7 * 24 * time.Hour},
		natsx.StreamEdgeStats:  {jetstream.WorkQueuePolicy, 7 * 24 * time.Hour},
		natsx.StreamWebDLQ:     {jetstream.LimitsPolicy, 7 * 24 * time.Hour},
		natsx.StreamAPIDLQ:     {jetstream.LimitsPolicy, 30 * 24 * time.Hour},
	}
	require.Len(t, spec.Streams, len(expectStream))
	for _, s := range spec.Streams {
		e, ok := expectStream[s.Name]
		require.True(t, ok, s.Name)
		require.Equal(t, e.retention, s.Retention, s.Name)
		require.Equal(t, e.maxAge, s.MaxAge, s.Name)
		require.Equal(t, jetstream.FileStorage, s.Storage, s.Name)
	}

	expectBucket := map[string]struct {
		storage jetstream.StorageType
		ttl     time.Duration
		markers bool
	}{
		natsx.BucketRegistrySites: {jetstream.FileStorage, 0, false},
		natsx.BucketRegistryOTLP:  {jetstream.FileStorage, 0, false},
		natsx.BucketEdgeSecrets:   {jetstream.MemoryStorage, 0, true},
		natsx.BucketVisitState:    {jetstream.MemoryStorage, 24 * time.Hour, false},
		natsx.BucketLocks:         {jetstream.MemoryStorage, 15 * time.Second, false},
	}
	require.Len(t, spec.Buckets, len(expectBucket))
	for _, want := range spec.Buckets {
		e, ok := expectBucket[want.Bucket]
		require.True(t, ok, want.Bucket)
		kv, err := js.KeyValue(ctx, want.Bucket)
		require.NoError(t, err)
		st, err := kv.Status(ctx)
		require.NoError(t, err)
		have := st.Config()
		require.Equal(t, e.storage, have.Storage, want.Bucket)
		require.Equal(t, e.ttl, have.TTL, want.Bucket)
		require.Equal(t, 3, have.Replicas, want.Bucket)
		require.Equal(t, uint8(1), have.History, want.Bucket)
		require.Equal(t, e.markers, have.LimitMarkerTTL > 0, want.Bucket)
		require.Empty(t, natsx.BucketDiff(want, have), want.Bucket)
	}

	for _, want := range spec.Consumers {
		c, err := js.Consumer(ctx, want.Stream, want.Config.Durable)
		require.NoError(t, err)
		have := c.CachedInfo().Config
		require.Equal(t, 30*time.Second, have.AckWait)
		require.Equal(t, -1, have.MaxDeliver)
		require.Equal(t, jetstream.AckExplicitPolicy, have.AckPolicy)
		require.Empty(t, have.BackOff)
	}

	// drift made outside apply is found and repaired, and only the changed
	// field is reported
	drifted := spec.Streams[0]
	drifted.MaxAge = time.Hour
	_, err = js.UpdateStream(ctx, drifted)
	require.NoError(t, err)
	diff, err := natsx.Diff(ctx, js, spec)
	require.NoError(t, err)
	require.Equal(t, natsx.ActionUpdate, diff[0].Action)
	require.Equal(t, []string{"max_age"}, diff[0].Fields)
	_, err = natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	third, err := natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	for _, c := range third {
		require.Equal(t, natsx.ActionUnchanged, c.Action, c.String())
	}
}

// streamState is the part of /jsz this test reads: each server reports the
// state of its own replica.
type jsz struct {
	AccountDetails []struct {
		Streams []struct {
			Name  string `json:"name"`
			State struct {
				Msgs     uint64 `json:"messages"`
				FirstSeq uint64 `json:"first_seq"`
				LastSeq  uint64 `json:"last_seq"`
			} `json:"state"`
		} `json:"stream_detail"`
	} `json:"account_details"`
}

func replicaState(t *testing.T, monitorURL, stream string) (first, last uint64) {
	t.Helper()
	resp, err := http.Get(monitorURL + "/jsz?accounts=true&streams=true")
	require.NoError(t, err)
	defer resp.Body.Close()
	var z jsz
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&z))
	for _, a := range z.AccountDetails {
		for _, s := range a.Streams {
			if s.Name == stream {
				return s.State.FirstSeq, s.State.LastSeq
			}
		}
	}
	t.Fatalf("%s has no replica of %s", monitorURL, stream)
	return 0, 0
}

func testKeyTTL(t *testing.T, cluster *natsctr.Cluster, js jetstream.JetStream) {
	ctx := t.Context()
	kv, err := js.KeyValue(ctx, natsx.BucketEdgeSecrets)
	require.NoError(t, err)
	key := fmt.Sprintf("ttl-test-%d", time.Now().UnixNano())
	rev, err := kv.Create(ctx, key, []byte("secret"), jetstream.KeyTTL(3*time.Second))
	require.NoError(t, err)

	// every replica holds the key before it expires
	stream := "KV_" + natsx.BucketEdgeSecrets
	for _, s := range cluster.Servers {
		require.Eventually(t, func() bool {
			_, last := replicaState(t, s.MonitorURL, stream)
			return last >= rev
		}, 2*time.Second, 50*time.Millisecond, "%s never received the key", s.Name)
	}

	time.Sleep(5 * time.Second)
	_, err = kv.Get(ctx, key)
	require.ErrorIs(t, err, jetstream.ErrKeyNotFound)
	for _, s := range cluster.Servers {
		first, _ := replicaState(t, s.MonitorURL, stream)
		require.Greater(t, first, rev, "%s still stores the expired key", s.Name)
	}

	rev2, err := kv.Create(ctx, key, []byte("next"), jetstream.KeyTTL(3*time.Second))
	require.NoError(t, err, "create after expiry must succeed")
	require.Greater(t, rev2, rev)
}

func testDedup(t *testing.T, js jetstream.JetStream) {
	ctx := t.Context()
	for _, c := range []struct{ stream, prefix string }{
		{natsx.StreamWebEvents, natsx.SubjectWebEvents},
		{natsx.StreamAPIMetrics, natsx.SubjectAPIMetrics},
		{natsx.StreamEdgeStats, natsx.SubjectEdgeStats},
	} {
		subject := natsx.Subject(c.prefix, "dedup-test")
		id := fmt.Sprintf("dedup-%d", time.Now().UnixNano())
		a1, err := natsx.PublishDedup(ctx, js, c.stream, subject, id, []byte("one"), nil)
		require.NoError(t, err, c.stream)
		require.False(t, a1.Duplicate, c.stream)
		a2, err := natsx.PublishDedup(ctx, js, c.stream, subject, id, []byte("one"), nil)
		require.NoError(t, err, c.stream)
		require.True(t, a2.Duplicate, c.stream)
		require.Equal(t, a1.Sequence, a2.Sequence, c.stream)

		s, err := js.Stream(ctx, c.stream)
		require.NoError(t, err)
		info, err := s.Info(ctx, jetstream.WithSubjectFilter(subject))
		require.NoError(t, err)
		require.Equal(t, uint64(1), info.State.Subjects[subject], "%s stored the duplicate", c.stream)
		require.NoError(t, s.Purge(ctx, jetstream.WithPurgeSubject(subject)))
	}

	// the expected-stream check refuses a subject owned by another stream
	_, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, natsx.Subject(natsx.SubjectAPIMetrics, "x"), "wrong-stream", nil, nil)
	require.Error(t, err)
	_, err = natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, natsx.Subject(natsx.SubjectWebEvents, "x"), "", nil, nil)
	require.ErrorIs(t, err, natsx.ErrNoMsgID)
}

func testPullLoop(t *testing.T, js jetstream.JetStream) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	site := fmt.Sprintf("pull-%d", time.Now().UnixNano())
	subject := natsx.Subject(natsx.SubjectWebEvents, site)
	for _, body := range []string{"ok", "flaky", "bad"} {
		_, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, subject, site+body, []byte(body), nil)
		require.NoError(t, err)
	}
	cons, err := js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	require.NoError(t, err)

	var mu sync.Mutex
	seen := map[string][]uint64{}
	done := errors.New("all handled")
	cfg := natsx.PullConfig{
		Batch:   10,
		MaxWait: 500 * time.Millisecond,
		Log:     quietLog(),
		BeforeBatch: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			if len(seen["ok"]) == 1 && len(seen["flaky"]) == 2 && len(seen["bad"]) == 1 {
				return done
			}
			return nil
		},
		DeadLetter: func(ctx context.Context, msg jetstream.Msg, reason string) error {
			return natsx.DeadLetter(ctx, js, natsx.StreamWebDLQ, natsx.Subject(natsx.SubjectWebDLQ, site), msg, reason)
		},
	}
	start := time.Now()
	var flakyRetryAt time.Duration
	err = natsx.PullLoop(ctx, cons, cfg, func(_ context.Context, msg jetstream.Msg) error {
		meta, err := msg.Metadata()
		require.NoError(t, err)
		body := string(msg.Data())
		mu.Lock()
		seen[body] = append(seen[body], meta.NumDelivered)
		mu.Unlock()
		switch {
		case body == "flaky" && meta.NumDelivered == 1:
			return errors.New("umami answered 503")
		case body == "flaky":
			flakyRetryAt = time.Since(start)
			return nil
		case body == "bad":
			return natsx.Permanent(errors.New("umami answered 400"))
		}
		return nil
	})
	require.ErrorIs(t, err, done)
	require.Equal(t, []uint64{1, 2}, seen["flaky"])
	// the first retry waits RetryBackoff[0], not the 30 s ack wait
	require.GreaterOrEqual(t, flakyRetryAt, natsx.RetryBackoff[0])
	require.Less(t, flakyRetryAt, 10*time.Second)

	// acked and dead-lettered messages left the work queue
	s, err := js.Stream(ctx, natsx.StreamWebEvents)
	require.NoError(t, err)
	info, err := s.Info(ctx, jetstream.WithSubjectFilter(subject))
	require.NoError(t, err)
	require.Zero(t, info.State.Subjects[subject])

	dlq, err := js.Stream(ctx, natsx.StreamWebDLQ)
	require.NoError(t, err)
	m, err := dlq.GetLastMsgForSubject(ctx, natsx.Subject(natsx.SubjectWebDLQ, site))
	require.NoError(t, err)
	require.Equal(t, "bad", string(m.Data))
	require.Equal(t, "umami answered 400", m.Header.Get(natsx.HeaderDLQReason))
	require.Equal(t, subject, m.Header.Get(natsx.HeaderDLQSubject))
	require.Equal(t, natsx.StreamWebEvents, m.Header.Get(natsx.HeaderDLQStream))
}

func lockKV(t *testing.T, urls string) (*nats.Conn, jetstream.KeyValue) {
	t.Helper()
	nc, js := connect(t, urls)
	kv, err := js.KeyValue(t.Context(), natsx.BucketLocks)
	require.NoError(t, err)
	return nc, kv
}

func testLockCAS(t *testing.T, urls string, fdb *pdb.DB) {
	ctx := t.Context()
	name := fmt.Sprintf("cas-%d", time.Now().UnixNano())
	_, kvA := lockKV(t, urls)
	_, kvB := lockKV(t, urls)
	a, err := natsx.NewLock(kvA, name, "holder-a")
	require.NoError(t, err)
	b, err := natsx.NewLock(kvB, name, "holder-b")
	require.NoError(t, err)

	revA, err := a.Acquire(ctx)
	require.NoError(t, err)
	_, err = b.Acquire(ctx)
	require.ErrorIs(t, err, natsx.ErrLockHeld)

	// a renews twice, each renewal raises the fencing token
	r1, err := a.Renew(ctx)
	require.NoError(t, err)
	require.Greater(t, r1, revA)
	r2, err := a.Confirm(ctx)
	require.NoError(t, err)
	require.Equal(t, r1, r2)
	lastA := r1
	fenceA := a.Fence()
	require.Equal(t, lastA, fenceA.Revision)
	require.Positive(t, fenceA.Epoch)

	// a pauses: no renewals. b takes over only after the bucket ttl.
	paused := time.Now()
	var revB uint64
	require.Eventually(t, func() bool {
		revB, err = b.Acquire(ctx)
		return err == nil
	}, natsx.LocksTTL+5*time.Second, 200*time.Millisecond, "b never acquired: %v", err)
	waited := time.Since(paused)
	require.Greater(t, waited, natsx.LocksTTL-2*time.Second, "lock expired before its ttl")
	require.Greater(t, revB, lastA, "fencing token must grow across holders")
	fenceB1 := b.Fence()
	require.Greater(t, fenceB1.Epoch, fenceA.Epoch, "a later hold has a later epoch")

	// the resumed former holder fails its compare-and-set renewal
	_, err = a.Renew(ctx)
	require.ErrorIs(t, err, natsx.ErrLockLost)
	require.ErrorIs(t, err, jetstream.ErrKeyRevisionMismatch)
	require.Zero(t, a.Revision())
	_, err = a.Confirm(ctx)
	require.ErrorIs(t, err, natsx.ErrLockLost)

	// the new holder keeps renewing
	revB2, err := b.Renew(ctx)
	require.NoError(t, err)
	require.Greater(t, revB2, revB)

	fenceB2 := b.Fence()
	require.Equal(t, fenceB1.Epoch, fenceB2.Epoch, "renewal keeps the epoch")
	require.Equal(t, revB2, fenceB2.Revision)

	// a transaction carrying the former holder's token is refused
	check := func(f natsx.Fence) error {
		return withTx(ctx, fdb, func(tx txT) error { return natsx.CheckFence(ctx, tx, natsx.LockMetricsWriter, f) })
	}
	require.NoError(t, check(fenceB2))
	require.ErrorIs(t, check(fenceA), natsx.ErrFenced)
	// same epoch, stale revision: still refused
	require.ErrorIs(t, check(fenceB1), natsx.ErrFenced)
	// the same token again is accepted: a leader writes many transactions
	require.NoError(t, check(fenceB2))
	err = withTx(ctx, fdb, func(tx txT) error { return natsx.CheckFence(ctx, tx, "unseeded", fenceB2) })
	require.ErrorContains(t, err, "must seed it")
	require.Error(t, check(natsx.Fence{}), "the zero token of a lost lock is never written")

	require.NoError(t, b.Release(ctx))
	_, err = a.Acquire(ctx)
	require.NoError(t, err, "a released lock is free at once")
	require.NoError(t, a.Release(ctx))
}

func testElection(t *testing.T, urls string) {
	name := fmt.Sprintf("elect-%d", time.Now().UnixNano())
	type contender struct {
		nc      *nats.Conn
		lock    *natsx.Lock
		leading atomic.Bool
		lostAt  atomic.Int64
		revs    []uint64
		mu      sync.Mutex
		stop    context.CancelFunc
		done    chan error
	}
	var holders atomic.Int32
	var maxHolders atomic.Int32
	newContender := func(id string) *contender {
		nc, kv := lockKV(t, urls)
		l, err := natsx.NewLock(kv, name, id)
		require.NoError(t, err)
		c := &contender{nc: nc, lock: l, done: make(chan error, 1)}
		ctx, cancel := context.WithCancel(t.Context())
		c.stop = cancel
		e := &natsx.Elector{Lock: l, RenewEvery: natsx.LockRenewEvery, RetryEvery: 250 * time.Millisecond, Log: quietLog()}
		go func() {
			c.done <- e.Run(ctx, func(lctx context.Context) error {
				n := holders.Add(1)
				for {
					m := maxHolders.Load()
					if n <= m || maxHolders.CompareAndSwap(m, n) {
						break
					}
				}
				c.leading.Store(true)
				c.mu.Lock()
				c.revs = append(c.revs, l.Revision())
				c.mu.Unlock()
				<-lctx.Done()
				c.leading.Store(false)
				holders.Add(-1)
				if errors.Is(context.Cause(lctx), natsx.ErrLockLost) {
					c.lostAt.Store(time.Now().UnixNano())
				}
				return nil
			})
		}()
		return c
	}
	a := newContender("a")
	b := newContender("b")
	t.Cleanup(func() { a.stop(); b.stop() })

	require.Eventually(t, func() bool { return a.leading.Load() || b.leading.Load() }, 10*time.Second, 50*time.Millisecond)
	// watch across several renewals: never two holders, never zero
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		require.NotEqual(t, a.leading.Load(), b.leading.Load(), "exactly one contender must lead")
		time.Sleep(50 * time.Millisecond)
	}
	require.Equal(t, int32(1), maxHolders.Load())

	leader, follower := a, b
	if b.leading.Load() {
		leader, follower = b, a
	}
	leaderRev := leader.lock.Revision()
	require.NotZero(t, leaderRev)

	// kill the leader: its connection drops without releasing the lock
	killed := time.Now()
	leader.nc.Close()
	require.Eventually(t, func() bool { return follower.leading.Load() }, natsx.LocksTTL+3*time.Second, 50*time.Millisecond,
		"follower did not take over within the ttl")
	took := time.Since(killed)
	// the lock outlives the holder by ttl minus the time since its last
	// renewal, so takeover cannot happen before ttl minus one renew period
	require.Greater(t, took, natsx.LocksTTL-natsx.LockRenewEvery-time.Second)
	t.Logf("takeover after %s", took.Round(10*time.Millisecond))

	// the killed leader noticed the loss at its next renewal
	require.Eventually(t, func() bool { return leader.lostAt.Load() != 0 }, natsx.LockRenewEvery+2*time.Second, 50*time.Millisecond)
	require.Greater(t, follower.lock.Revision(), leaderRev, "fencing revision must increase")
	require.Equal(t, int32(1), maxHolders.Load())

	// a clean stop releases the lock and returns
	follower.stop()
	select {
	case err := <-follower.done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("elector did not stop")
	}
}

// testFenceAfterReset covers the risk of a memory-backed locks bucket: after
// the bucket is recreated, or the whole cluster restarts, lock revisions
// start again near 1. the epoch lets the new leader write, and the holder
// from before the reset stays fenced off.
func testFenceAfterReset(t *testing.T, urls string, fdb *pdb.DB, name string, reset func()) {
	ctx := t.Context()
	check := func(f natsx.Fence) error {
		return withTx(ctx, fdb, func(tx txT) error { return natsx.CheckFence(ctx, tx, name, f) })
	}
	_, kvOld := lockKV(t, urls)
	old, err := natsx.NewLock(kvOld, name, "before-reset")
	require.NoError(t, err)
	_, err = old.Acquire(ctx)
	require.NoError(t, err)
	for range 3 {
		_, err = old.Renew(ctx)
		require.NoError(t, err)
	}
	before := old.Fence()
	require.NoError(t, check(before))

	reset()

	_, js := connect(t, urls)
	var kvNew jetstream.KeyValue
	require.Eventually(t, func() bool {
		kvNew, err = js.KeyValue(ctx, natsx.BucketLocks)
		return err == nil
	}, 30*time.Second, 200*time.Millisecond)
	next, err := natsx.NewLock(kvNew, name, "after-reset")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err = next.Acquire(ctx)
		return err == nil
	}, 30*time.Second, 200*time.Millisecond, "no lock after the reset: %v", err)
	after := next.Fence()
	require.Less(t, after.Revision, before.Revision, "the reset must restart the revisions, or this test proves nothing")
	require.Greater(t, after.Epoch, before.Epoch)

	require.NoError(t, check(after), "the new leader must be able to write")
	require.ErrorIs(t, check(before), natsx.ErrFenced, "the holder from before the reset must stay fenced off")
	stale := after
	_, err = next.Renew(ctx)
	require.NoError(t, err)
	require.NoError(t, check(next.Fence()))
	require.ErrorIs(t, check(stale), natsx.ErrFenced, "a stale revision in the new epoch is refused")
	_, err = old.Renew(ctx)
	require.ErrorIs(t, err, natsx.ErrLockLost)
	require.NoError(t, next.Release(ctx))
}
