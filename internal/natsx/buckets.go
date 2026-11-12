package natsx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// KeeperEvery is how often a BucketKeeper checks that its buckets exist. a
// memory bucket lost in a restart is back within about this long after the
// server answers again, without anyone running platformctl.
const KeeperEvery = time.Second

// ErrBucketMissing means a memory bucket does not exist and could not be
// created.
var ErrBucketMissing = errors.New("natsx: memory bucket missing")

// MemoryBucket returns the topology config of a memory-backed bucket. file
// buckets are refused: losing one is data loss, which an operator restores,
// so only platformctl creates them.
func MemoryBucket(replicas int, name string) (jetstream.KeyValueConfig, error) {
	spec, err := Topology(replicas)
	if err != nil {
		return jetstream.KeyValueConfig{}, err
	}
	for _, b := range spec.Buckets {
		if b.Bucket != name {
			continue
		}
		if b.Storage != jetstream.MemoryStorage {
			return jetstream.KeyValueConfig{}, fmt.Errorf("natsx: bucket %s is file-backed; only platformctl nats apply creates it", name)
		}
		return b, nil
	}
	return jetstream.KeyValueConfig{}, fmt.Errorf("natsx: bucket %s is not in the topology", name)
}

// EnsureBuckets creates every named memory bucket that does not exist, with
// exactly its topology config, and returns the names it created. a memory
// bucket is gone after a restart of a single server, and the services that
// use one recreate it themselves so recovery needs no operator. a bucket that
// exists is left alone, even when its settings drifted: reconciling settings
// is platformctl's job, and a service must not fight it. two services that
// create the same bucket at once both succeed, because the server accepts a
// create with an identical config.
func EnsureBuckets(ctx context.Context, js jetstream.JetStream, replicas int, names ...string) ([]string, error) {
	cfgs := make([]jetstream.KeyValueConfig, 0, len(names))
	for _, name := range names {
		cfg, err := MemoryBucket(replicas, name)
		if err != nil {
			return nil, err
		}
		cfgs = append(cfgs, cfg)
	}
	var created []string
	for _, cfg := range cfgs {
		_, err := js.KeyValue(ctx, cfg.Bucket)
		switch {
		case err == nil:
			continue
		case !errors.Is(err, jetstream.ErrBucketNotFound):
			return created, fmt.Errorf("natsx: read bucket %s: %w", cfg.Bucket, err)
		}
		if _, err := js.CreateKeyValue(ctx, cfg); err != nil {
			return created, fmt.Errorf("%w: recreate %s: %w", ErrBucketMissing, cfg.Bucket, err)
		}
		created = append(created, cfg.Bucket)
	}
	return created, nil
}

// MissingStream reports whether err is what a kv operation returns when its
// bucket's stream does not exist: a get finds no responder and a write gets
// no stream ack. a leaderless stream during an election looks the same, so
// this only asks for a check, it is no proof.
func MissingStream(err error) bool {
	return errors.Is(err, nats.ErrNoResponders) ||
		errors.Is(err, jetstream.ErrNoStreamResponse) ||
		errors.Is(err, jetstream.ErrBucketNotFound) ||
		errors.Is(err, jetstream.ErrStreamNotFound)
}

// BucketKeeper keeps one service's memory buckets in existence. it checks at
// startup, every KeeperEvery, and at once when an operation failed in a way
// that may mean a missing bucket.
type BucketKeeper struct {
	js       jetstream.JetStream
	replicas int
	names    []string
	log      *slog.Logger
	kick     chan struct{}

	mu sync.Mutex
	// missing is the error of the last check that found a bucket absent
	// and could not create it; nil otherwise
	missing error
}

// NewBucketKeeper returns a keeper of the named memory buckets. it refuses a
// name that is not a memory bucket of the topology, so a typo fails at
// startup instead of at the first restart.
func NewBucketKeeper(js jetstream.JetStream, replicas int, log *slog.Logger, names ...string) (*BucketKeeper, error) {
	if js == nil || log == nil || len(names) == 0 {
		return nil, errors.New("natsx: BucketKeeper needs jetstream, a logger and at least one bucket")
	}
	for _, name := range names {
		if _, err := MemoryBucket(replicas, name); err != nil {
			return nil, err
		}
	}
	return &BucketKeeper{
		js: js, replicas: replicas, names: slices.Clone(names), log: log,
		kick: make(chan struct{}, 1),
	}, nil
}

// Ensure checks every bucket once and creates the missing ones.
func (k *BucketKeeper) Ensure(ctx context.Context) error {
	created, err := EnsureBuckets(ctx, k.js, k.replicas, k.names...)
	for _, name := range created {
		// the bucket's content is gone; whoever uses it starts again empty
		k.log.Warn("memory bucket was missing and has been recreated", "bucket", name)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	switch {
	case err == nil:
		k.missing = nil
	case errors.Is(err, ErrBucketMissing):
		k.missing = err
	}
	// any other error (no connection, an election) proves nothing about the
	// buckets, so it leaves the readiness state as it was
	return err
}

// Ready fails while the last check found a bucket missing and could not
// create it. /readyz uses it.
func (k *BucketKeeper) Ready(context.Context) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.missing
}

// Kick asks for a check now when err may mean a missing bucket. it never
// blocks: a pending request already covers this one.
func (k *BucketKeeper) Kick(err error) {
	if err == nil || !MissingStream(err) {
		return
	}
	select {
	case k.kick <- struct{}{}:
	default:
	}
}

// Run checks every KeeperEvery and on every Kick until ctx ends.
func (k *BucketKeeper) Run(ctx context.Context) {
	t := time.NewTicker(KeeperEvery)
	defer t.Stop()
	var failing bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-k.kick:
		}
		cctx, cancel := context.WithTimeout(ctx, KeeperEvery)
		err := k.Ensure(cctx)
		cancel()
		switch {
		case err != nil && ctx.Err() == nil && !failing:
			failing = true
			k.log.Warn("memory bucket check failed", "buckets", k.names, "error", err.Error())
		case err == nil && failing:
			failing = false
			k.log.Info("memory bucket check succeeded again", "buckets", k.names)
		}
	}
}
