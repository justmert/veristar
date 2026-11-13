package registry

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/natsx"
)

// rewatchWait is the pause before a failed or ended watch is opened again.
const rewatchWait = time.Second

// reconnectPoll is how often the watcher checks whether the connection
// reconnected. after a reconnect it rebuilds the snapshot from a fresh watch
// instead of trusting that the old ordered consumer resumed without a gap.
const reconnectPoll = time.Second

// Watcher keeps a full in-memory snapshot of one registry bucket current
// from a kv watch. while nats is unreachable it keeps serving the last
// snapshot it built; it never empties itself because of a lost connection.
type Watcher[T any] struct {
	js     jetstream.JetStream
	bucket string
	decode func(key string, value []byte) (T, error)
	log    *slog.Logger

	mu     sync.RWMutex
	snap   map[string]T
	loaded atomic.Bool
	synced atomic.Int64 // unix nanoseconds of the last complete initial load
}

// NewSiteWatcher watches registry_sites.
func NewSiteWatcher(js jetstream.JetStream, log *slog.Logger) *Watcher[Site] {
	return &Watcher[Site]{js: js, bucket: natsx.BucketRegistrySites, decode: DecodeSite, log: log}
}

// NewOTLPWatcher watches registry_otlp.
func NewOTLPWatcher(js jetstream.JetStream, log *slog.Logger) *Watcher[OTLPToken] {
	return &Watcher[OTLPToken]{js: js, bucket: natsx.BucketRegistryOTLP, decode: DecodeOTLPToken, log: log}
}

// Loaded reports whether a complete snapshot was built at least once.
// services report not ready, and fail closed, until it is true.
func (w *Watcher[T]) Loaded() bool { return w.loaded.Load() }

// LastSync returns when the snapshot was last rebuilt from a full watch.
func (w *Watcher[T]) LastSync() time.Time { return time.Unix(0, w.synced.Load()) }

// Get returns the entry for key from the snapshot.
func (w *Watcher[T]) Get(key string) (T, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	v, ok := w.snap[key]
	return v, ok
}

// Snapshot returns a copy of every entry, keyed as in the bucket, for
// callers that walk the whole registry (the jobs task aggregate-web).
func (w *Watcher[T]) Snapshot() map[string]T {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return maps.Clone(w.snap)
}

// Len returns the number of entries in the snapshot.
func (w *Watcher[T]) Len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.snap)
}

// Run keeps the snapshot current until ctx ends.
func (w *Watcher[T]) Run(ctx context.Context) error {
	for {
		err := w.watchOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			w.log.Warn("registry watch interrupted, serving last snapshot", "bucket", w.bucket, "error", err.Error())
		}
		t := time.NewTimer(rewatchWait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

var errReconnected = errors.New("connection reconnected, rebuilding snapshot")

// watchOnce opens one watch, builds a complete new snapshot from its initial
// values, swaps it in, then applies updates until the watch ends.
func (w *Watcher[T]) watchOnce(ctx context.Context) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	kv, err := w.js.KeyValue(wctx, w.bucket)
	if err != nil {
		return err
	}
	reconnects := w.js.Conn().Stats().Reconnects
	watch, err := kv.WatchAll(wctx)
	if err != nil {
		return err
	}
	defer func() { _ = watch.Stop() }()

	next := map[string]T{}
	initial := true
	poll := time.NewTicker(reconnectPoll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
			if w.js.Conn().Stats().Reconnects != reconnects {
				return errReconnected
			}
		case e, ok := <-watch.Updates():
			if !ok {
				return errors.New("watch closed")
			}
			if e == nil {
				// end of the initial values: the new snapshot is complete
				if initial {
					w.mu.Lock()
					w.snap = next
					w.mu.Unlock()
					w.synced.Store(time.Now().UnixNano())
					w.loaded.Store(true)
					initial = false
					w.log.Info("registry snapshot loaded", "bucket", w.bucket, "entries", len(next))
				}
				continue
			}
			if initial {
				w.applyTo(next, e)
				continue
			}
			// copy on write, so readers holding the old map are unaffected
			w.mu.Lock()
			cp := maps.Clone(w.snap)
			w.applyTo(cp, e)
			w.snap = cp
			w.mu.Unlock()
		}
	}
}

func (w *Watcher[T]) applyTo(m map[string]T, e jetstream.KeyValueEntry) {
	if e.Operation() != jetstream.KeyValuePut {
		delete(m, e.Key())
		return
	}
	v, err := w.decode(e.Key(), e.Value())
	if err != nil {
		// fail closed: an entry that cannot be read is treated as absent
		// rather than keeping an older value that may grant access
		w.log.Error("registry entry rejected", "bucket", w.bucket, "error", err.Error())
		delete(m, e.Key())
		return
	}
	m[e.Key()] = v
}
