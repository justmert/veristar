package natsx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// lock names of the singleton workers
const (
	LockReplayer      = "replayer"
	LockMetricsWriter = "metricswriter"
	LockJobs          = "jobs"
)

var (
	// ErrLockHeld means another holder has the lock.
	ErrLockHeld = errors.New("natsx: lock is held by another holder")
	// ErrLockLost means this holder no longer has the lock: a renewal or a
	// confirmation failed, or it never had it.
	ErrLockLost = errors.New("natsx: lock lost")
)

// Fence is the fencing token of one hold. Epoch is the server time, in unix
// nanoseconds, of the message that acquired the lock: it is fixed for the
// hold and newer for every later hold, because a successor can only acquire
// after the previous hold expired, at least LocksTTL after its last renewal.
// Revision is the kv revision, which grows with every renewal inside one
// epoch. revisions alone are not enough: the locks bucket is memory-backed,
// and after a full cluster restart its sequence starts again from 0 while
// the stream itself, and its creation time, survive.
type Fence struct {
	Epoch    int64
	Revision uint64
}

// Lock is one leader lock in the locks bucket. the bucket ttl removes a lock
// whose holder stopped renewing.
type Lock struct {
	kv     jetstream.KeyValue
	name   string
	holder []byte

	mu    sync.Mutex
	rev   uint64
	epoch int64
}

// NewLock returns the lock name for one holder. holder must be unique per
// process (for example node name plus a random suffix), because it is the
// value Confirm compares.
func NewLock(kv jetstream.KeyValue, name, holder string) (*Lock, error) {
	if name == "" || holder == "" {
		return nil, errors.New("natsx: lock needs a name and a holder")
	}
	return &Lock{kv: kv, name: name, holder: []byte(holder)}, nil
}

// Name returns the lock name.
func (l *Lock) Name() string { return l.name }

// Revision returns the kv revision of the current hold, or 0 when this
// holder does not have the lock.
func (l *Lock) Revision() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rev
}

// Fence returns the fencing token of the current hold; the zero Fence when
// this holder does not have the lock.
func (l *Lock) Fence() Fence {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rev == 0 {
		return Fence{}
	}
	return Fence{Epoch: l.epoch, Revision: l.rev}
}

// Acquire takes the lock if nobody holds it. Create is atomic, so of several
// contenders exactly one succeeds.
func (l *Lock) Acquire(ctx context.Context) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rev, l.epoch = 0, 0
	rev, err := l.kv.Create(ctx, l.name, l.holder)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return 0, ErrLockHeld
		}
		return 0, fmt.Errorf("natsx: acquire lock %s: %w", l.name, err)
	}
	// the epoch is the server's timestamp of the acquiring message, read back
	// by revision so it is exactly this hold's message
	e, err := l.readRevision(ctx, rev)
	if err != nil {
		// holding a lock without a fencing token is useless; give it back
		_ = l.kv.Delete(ctx, l.name, jetstream.LastRevision(rev))
		return 0, fmt.Errorf("natsx: read epoch of lock %s: %w", l.name, err)
	}
	l.rev, l.epoch = rev, e.Created().UnixNano()
	return rev, nil
}

// readRevision reads one revision of the lock. the bucket allows direct
// gets, which any replica answers, so a follower that has not applied the
// create yet answers "not found" for a moment; the read is retried briefly.
func (l *Lock) readRevision(ctx context.Context, rev uint64) (jetstream.KeyValueEntry, error) {
	const attempts, pause = 40, 25 * time.Millisecond
	var err error
	for range attempts {
		var e jetstream.KeyValueEntry
		e, err = l.kv.GetRevision(ctx, l.name, rev)
		if err == nil {
			return e, nil
		}
		if !errors.Is(err, jetstream.ErrKeyNotFound) || !sleep(ctx, pause) {
			return nil, err
		}
	}
	return nil, err
}

// Renew extends the hold with a compare-and-set against the last revision
// this holder saw. any failure means the lock is lost: either someone else
// took it after it expired, or this holder cannot prove it still has it.
func (l *Lock) Renew(ctx context.Context) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rev == 0 {
		return 0, ErrLockLost
	}
	rev, err := l.kv.Update(ctx, l.name, l.holder, l.rev)
	if err != nil {
		l.rev = 0
		return 0, fmt.Errorf("%w: renew %s: %w", ErrLockLost, l.name, err)
	}
	l.rev = rev
	return rev, nil
}

// Confirm checks with the server that this holder still has the lock at its
// current revision. the replayer calls it before every send batch, because
// it cannot fence umami's database.
func (l *Lock) Confirm(ctx context.Context) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rev == 0 {
		return 0, ErrLockLost
	}
	e, err := l.kv.Get(ctx, l.name)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			l.rev = 0
			return 0, fmt.Errorf("%w: %s expired", ErrLockLost, l.name)
		}
		// unreachable server: the hold is unproven, so the caller must stop,
		// but the lock is kept for the next renewal to settle
		return 0, fmt.Errorf("%w: confirm %s: %w", ErrLockLost, l.name, err)
	}
	if e.Revision() != l.rev || !bytes.Equal(e.Value(), l.holder) {
		held := l.rev
		l.rev = 0
		return 0, fmt.Errorf("%w: %s is at revision %d, held at %d", ErrLockLost, l.name, e.Revision(), held)
	}
	return l.rev, nil
}

// Release deletes the lock if this holder still has it, so a successor does
// not wait for the ttl after a clean shutdown.
func (l *Lock) Release(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rev == 0 {
		return nil
	}
	rev := l.rev
	l.rev = 0
	if err := l.kv.Delete(ctx, l.name, jetstream.LastRevision(rev)); err != nil {
		return fmt.Errorf("natsx: release lock %s: %w", l.name, err)
	}
	return nil
}

// Elector runs a function only while it holds a lock.
type Elector struct {
	Lock *Lock
	// RenewEvery is LockRenewEvery in production. tests shorten nothing:
	// the ttl is a bucket setting, so the timing under test is the real one.
	RenewEvery time.Duration
	// RetryEvery is how often a follower tries to acquire.
	RetryEvery time.Duration
	Log        *slog.Logger
}

// Run contends for the lock until ctx ends. while it holds the lock it runs
// lead with a context that is cancelled, with cause ErrLockLost, as soon as a
// renewal fails; lead must then return promptly, and Run contends again. when
// lead returns on its own, Run releases the lock and returns lead's error.
func (e *Elector) Run(ctx context.Context, lead func(ctx context.Context) error) error {
	if e.Lock == nil || e.RenewEvery <= 0 || e.RetryEvery <= 0 || e.Log == nil {
		return errors.New("natsx: Elector needs Lock, RenewEvery, RetryEvery and Log")
	}
	for {
		rev, err := e.Lock.Acquire(ctx)
		if err != nil {
			if !errors.Is(err, ErrLockHeld) && ctx.Err() == nil {
				e.Log.Warn("lock acquire failed", "lock", e.Lock.Name(), "error", err.Error())
			}
			if !sleep(ctx, e.RetryEvery) {
				return ctx.Err()
			}
			continue
		}
		e.Log.Info("lock acquired", "lock", e.Lock.Name(), "revision", rev)
		lost, err := e.lead(ctx, lead)
		if lost {
			e.Log.Warn("lock lost", "lock", e.Lock.Name())
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		// a fresh context, because ctx may be the reason lead returned
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.RenewEvery)
		if rerr := e.Lock.Release(rctx); rerr != nil {
			e.Log.Warn("lock release failed, it expires with the bucket ttl", "lock", e.Lock.Name(), "error", rerr.Error())
		}
		cancel()
		if err == nil {
			// lead returned because ctx ended
			return ctx.Err()
		}
		return err
	}
}

// lead runs fn while renewing. lost reports that a renewal failed.
func (e *Elector) lead(ctx context.Context, fn func(context.Context) error) (lost bool, err error) {
	lctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(e.RenewEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-lctx.Done():
				return
			case <-t.C:
				rctx, rcancel := context.WithTimeout(lctx, e.RenewEvery)
				_, rerr := e.Lock.Renew(rctx)
				rcancel()
				if rerr != nil {
					e.Log.Warn("lock renewal failed", "lock", e.Lock.Name(), "error", rerr.Error())
					cancel(ErrLockLost)
					return
				}
			}
		}
	}()
	err = fn(lctx)
	close(done)
	wg.Wait()
	return errors.Is(context.Cause(lctx), ErrLockLost), err
}
