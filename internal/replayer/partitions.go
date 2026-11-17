package replayer

import (
	"cmp"
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
)

// partition assignment. every instance announces itself with a member key in
// the locks bucket, which the bucket's ttl removes 15 s after the instance
// stops renewing it. from the same member list every instance computes the
// same assignment (assign) and contends only for the partitions assigned to
// it. the locks, not the assignment, decide who consumes: two instances that
// briefly disagree on the members still never own one partition together.
// an instance gives a partition back when it is assigned elsewhere, after
// finishing the sends in progress, and the assigned instance takes it over.
// a partition nobody holds for orphanGrace is taken by any instance, so a
// view that is off for a while never leaves one unconsumed.

const (
	// memberPrefix starts the member keys, memberPrefix + node id.
	memberPrefix = natsx.LockReplayer + "-member."
	// memberEvery is how often an instance renews its member key and reads
	// the member list. a third of natsx.LocksTTL and more, so an instance
	// that stops is out of every list 15 s after its last renewal, and one
	// that starts is in every list within this time.
	memberEvery = 2 * time.Second
	// memberSettle is how long a new member must be seen before the others
	// hand partitions to it, so an instance that restarts in a loop does
	// not move partitions back and forth.
	memberSettle = 10 * time.Second
	// minHold is the least time an instance keeps a partition before it
	// hands it over, for the same reason.
	minHold = 15 * time.Second
	// orphanGrace is how long a partition's lock must be free before an
	// instance it is not assigned to takes it.
	orphanGrace = 10 * time.Second
	// legacyRecheck is how often an instance looks for the legacy consumer
	// again after it was missing.
	legacyRecheck = time.Minute
)

// assign returns the owning node of every partition: each partition goes to
// the node with the highest rendezvous score that still has room. a node
// has room for floor(parts / nodes) partitions, and the first
// parts mod nodes nodes to fill up get one more. the result depends only on
// the node set, so every instance computes the same one, the nodes differ
// by one partition at most, and a node that joins or leaves moves few
// partitions besides its own.
func assign(nodes []string, parts int) []string {
	out := make([]string, parts)
	if len(nodes) == 0 {
		return out
	}
	nodes = slices.Clone(nodes)
	slices.Sort(nodes)
	nodes = slices.Compact(nodes)
	base, extra := parts/len(nodes), parts%len(nodes)
	load := make(map[string]int, len(nodes))
	order := slices.Clone(nodes)
	for p := range parts {
		slices.SortFunc(order, func(a, b string) int {
			if c := cmp.Compare(assignScore(p, b), assignScore(p, a)); c != 0 {
				return c
			}
			return strings.Compare(a, b)
		})
		for _, n := range order {
			if load[n] < base || (load[n] == base && extra > 0) {
				if load[n] == base {
					extra--
				}
				out[p] = n
				load[n]++
				break
			}
		}
	}
	return out
}

func assignScore(p int, node string) uint64 {
	f := fnv.New64a()
	_, _ = f.Write([]byte("assign\x00"))
	_, _ = f.Write([]byte(strconv.Itoa(p)))
	_, _ = f.Write([]byte{0})
	_, _ = f.Write([]byte(node))
	return f.Sum64()
}

// membership keeps this instance's member key alive and the current
// assignment.
type membership struct {
	kv     jetstream.KeyValue
	node   string
	holder string
	log    *slog.Logger

	mu        sync.Mutex
	listed    bool
	firstSeen map[string]time.Time
	owners    []string
}

func newMembership(kv jetstream.KeyValue, node, holder string, log *slog.Logger) *membership {
	m := &membership{kv: kv, node: node, holder: holder, log: log, firstSeen: map[string]time.Time{}}
	m.owners = assign([]string{node}, events.Partitions)
	return m
}

// owner returns the node assigned partition p.
func (m *membership) owner(p int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.owners[p]
}

// run renews the member key and refreshes the assignment until ctx ends.
func (m *membership) run(ctx context.Context, buckets *natsx.BucketKeeper) {
	t := time.NewTicker(memberEvery)
	defer t.Stop()
	for {
		if err := m.refresh(ctx); err != nil && ctx.Err() == nil {
			m.log.Warn("partition membership not refreshed, keeping the last assignment", "error", err.Error())
			buckets.Kick(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *membership) refresh(ctx context.Context) error {
	rctx, cancel := context.WithTimeout(ctx, memberEvery)
	defer cancel()
	if _, err := m.kv.Put(rctx, memberPrefix+m.node, []byte(m.holder)); err != nil {
		return err
	}
	lister, err := m.kv.ListKeysFiltered(rctx, memberPrefix+"*")
	if err != nil {
		return err
	}
	var listed []string
	for k := range lister.Keys() {
		listed = append(listed, strings.TrimPrefix(k, memberPrefix))
	}
	// the lister ends its own subscription once it delivered every key, so
	// stopping it afterwards finds nothing to stop
	if err := lister.Stop(); err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) && !errors.Is(err, nats.ErrBadSubscription) {
		return err
	}
	m.update(listed, time.Now())
	return nil
}

// update applies one member list. members of the first list count as
// settled at once: they were there before this instance.
func (m *membership) update(listed []string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	for _, n := range listed {
		seen[n] = true
		if _, ok := m.firstSeen[n]; !ok {
			at := now
			if !m.listed {
				at = now.Add(-memberSettle)
			}
			m.firstSeen[n] = at
		}
	}
	for n := range m.firstSeen {
		if !seen[n] {
			delete(m.firstSeen, n)
		}
	}
	m.listed = true
	nodes := []string{m.node}
	for n, at := range m.firstSeen {
		if now.Sub(at) >= memberSettle {
			nodes = append(nodes, n)
		}
	}
	owners := assign(nodes, events.Partitions)
	if !slices.Equal(owners, m.owners) {
		mine := 0
		for _, o := range owners {
			if o == m.node {
				mine++
			}
		}
		slices.Sort(nodes)
		m.log.Info("partition assignment changed", "members", strings.Join(slices.Compact(nodes), ","), "assigned_here", mine)
	}
	m.owners = owners
}

// leave deletes the member key, so the others reassign at once instead of
// after the ttl. it runs after ctx ended, so it detaches from it.
func (m *membership) leave(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), natsx.LockRenewEvery)
	defer cancel()
	if err := m.kv.Delete(ctx, memberPrefix+m.node); err != nil {
		m.log.Warn("member key not deleted, it expires with the bucket ttl", "error", err.Error())
	}
}

// own contends for one partition, or the legacy subject, and consumes it
// while it holds the lock, until ctx ends. it returns an error only when the
// topology misses the partition's consumer.
func (r *Replayer) own(ctx context.Context, part int) error {
	name := natsx.LockReplayer
	if part != legacyLead {
		name = natsx.LockPartition(part)
	}
	lock, err := natsx.NewLock(r.locksKV, name, r.holder)
	if err != nil {
		return err
	}
	var freeSince, legacyAgain time.Time
	for {
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case part == legacyLead && time.Now().Before(legacyAgain):
			sleepCtx(ctx, acquireRetry)
			continue
		case part != legacyLead && !r.wants(ctx, part, &freeSince):
			sleepCtx(ctx, acquireRetry)
			continue
		}
		if _, err := lock.Acquire(ctx); err != nil {
			if !errors.Is(err, natsx.ErrLockHeld) && ctx.Err() == nil {
				r.log.Warn("lock acquire failed", "lock", name, "error", err.Error())
				r.buckets.Kick(err)
			}
			sleepCtx(ctx, acquireRetry)
			continue
		}
		freeSince = time.Time{}
		r.log.Info("lock acquired", "lock", name, "revision", lock.Revision())
		err := r.hold(ctx, part, lock)
		switch {
		case err == nil || errors.Is(err, errYield):
		case errors.Is(err, errLegacyGone):
			legacyAgain = time.Now().Add(legacyRecheck)
		case errors.Is(err, natsx.ErrLockLost):
			r.log.Warn("stopped consuming, lock not confirmed", "lock", name, "error", err.Error())
		case errors.Is(err, errNotReady):
			r.log.Warn("stopped consuming, jetstream not ready", "lock", name, "error", err.Error())
			sleepCtx(ctx, acquireRetry)
		default:
			return err
		}
	}
}

// wants reports whether this instance should contend for partition p: it is
// assigned here, or its lock has been free for orphanGrace.
func (r *Replayer) wants(ctx context.Context, p int, freeSince *time.Time) bool {
	if r.members.owner(p) == r.set.NodeID {
		return true
	}
	_, err := r.locksKV.Get(ctx, natsx.LockPartition(p))
	if !errors.Is(err, jetstream.ErrKeyNotFound) {
		*freeSince = time.Time{}
		return false
	}
	if freeSince.IsZero() {
		*freeSince = time.Now()
	}
	return time.Since(*freeSince) >= orphanGrace
}

// hold runs one hold of a lock: it renews the lock, consumes, hands the
// partition over when it is assigned elsewhere or the process stops, and
// releases the lock at the end.
func (r *Replayer) hold(ctx context.Context, part int, lock *natsx.Lock) error {
	// the hold outlives ctx: a shutdown is a hand-over, which finishes the
	// sends in progress before the lock goes
	lctx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancel(nil)
	yield := make(chan struct{})
	var once sync.Once
	handOver := func() { once.Do(func() { close(yield) }) }
	start := time.Now()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		renew := time.NewTicker(natsx.LockRenewEvery)
		defer renew.Stop()
		check := time.NewTicker(acquireRetry)
		defer check.Stop()
		var hard <-chan time.Time
		stopping := ctx.Done()
		for {
			select {
			case <-done:
				return
			case <-hard:
				// the hand-over took too long; stop what is left
				cancel(errYield)
				return
			case <-stopping:
				handOver()
				stopping = nil
				t := time.NewTimer(yieldTimeout + 5*time.Second)
				defer t.Stop()
				hard = t.C
			case <-check.C:
				if part != legacyLead && time.Since(start) >= minHold && r.members.owner(part) != r.set.NodeID {
					handOver()
				}
			case <-renew.C:
				rctx, rcancel := context.WithTimeout(lctx, natsx.LockRenewEvery)
				_, err := lock.Renew(rctx)
				rcancel()
				if err != nil {
					r.log.Warn("lock renewal failed", "lock", lock.Name(), "error", err.Error())
					r.buckets.Kick(err)
					cancel(err)
					return
				}
			}
		}
	})
	err := r.lead(lctx, part, lock, yield)
	close(done)
	wg.Wait()
	if errors.Is(err, errYield) || (err == nil && ctx.Err() != nil) {
		r.log.Info("handed the partition over", "lock", lock.Name())
	}
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), natsx.LockRenewEvery)
	defer rcancel()
	if rerr := lock.Release(rctx); rerr != nil {
		r.log.Warn("lock release failed, it expires with the bucket ttl", "lock", lock.Name(), "error", rerr.Error())
	}
	if cause := context.Cause(lctx); err == nil && cause != nil && errors.Is(cause, natsx.ErrLockLost) {
		return cause
	}
	return err
}
