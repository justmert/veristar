package replayer

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/natsx"
)

// the barrier keeps a visitor's order while an upgrade queues events on the
// legacy subject and on the partitions at once. a gateway of the earlier
// release and one of this release can each accept an event of the same
// visitor, and the two land in different consumers of one stream. stream
// sequences order every message of the stream by its publish, so an event
// is delivered only when no event of the other kind with a lower sequence
// is left in the stream: a work-queue stream drops a message when it is
// acked, so a lower one still there is not delivered yet. the message with
// the lowest sequence of all is never held back, so the two kinds cannot
// wait for each other for ever.
//
// both directions are needed: a partition owner waits for older legacy
// events, and the legacy owner waits for older partitioned ones. once the
// legacy subject is retired the partition side costs nothing.

const (
	// barrierPace is the least time between two reads of the stream by one
	// barrier; holds waiting at the same time share one read.
	barrierPace = 25 * time.Millisecond
	// retireCheckEvery is how often the legacy barrier checks whether the
	// legacy subject was retired.
	retireCheckEvery = 30 * time.Second
)

// errLegacyGone ends the legacy hold: the legacy consumer was deleted, the
// last step of `platformctl nats retire-legacy-web`.
var errLegacyGone = errors.New("replayer: the legacy web consumer is gone")

// seqBarrier tells the holds of one kind which of their messages no message
// of the other kind precedes.
type seqBarrier struct {
	js jetstream.JetStream
	// filter matches the other kind's subjects
	filter string
	// legacy is set on the barrier that watches the legacy subject, which
	// can be retired for good
	legacy bool

	// below: every message with a lower sequence is clear
	below atomic.Uint64
	// covered is the highest sequence the waiting kind has fetched. a read
	// that finds no message of the other kind clears everything up to it,
	// because a message published later gets a higher sequence.
	covered atomic.Uint64
	retired atomic.Bool

	mu        sync.Mutex
	last      time.Time
	lastCheck time.Time
	stream    jetstream.Stream
}

func newSeqBarrier(js jetstream.JetStream, filter string, legacy bool) *seqBarrier {
	return &seqBarrier{js: js, filter: filter, legacy: legacy}
}

// fetched records that a hold fetched seq; it is called before the hold
// waits for seq.
func (b *seqBarrier) fetched(seq uint64) {
	for {
		c := b.covered.Load()
		if seq <= c || b.covered.CompareAndSwap(c, seq) {
			return
		}
	}
}

func (b *seqBarrier) raise(to uint64) {
	for {
		c := b.below.Load()
		if to <= c || b.below.CompareAndSwap(c, to) {
			return
		}
	}
}

func (b *seqBarrier) clear(seq uint64) bool { return b.retired.Load() || seq < b.below.Load() }

// wait returns true once seq is clear, false when ctx ends or stop is
// closed first.
func (b *seqBarrier) wait(ctx context.Context, stop <-chan struct{}, seq uint64) bool {
	for !b.clear(seq) {
		select {
		case <-ctx.Done():
			return false
		case <-stop:
			return false
		default:
		}
		b.refresh(ctx, seq)
	}
	return true
}

// refresh reads the lowest sequence of the other kind. one read runs at a
// time and at most one per barrierPace; a waiter that a read finished while
// it queued returns without another.
func (b *seqBarrier) refresh(ctx context.Context, seq uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clear(seq) {
		return
	}
	if d := barrierPace - time.Since(b.last); d > 0 && !sleepCtx(ctx, d) {
		return
	}
	b.last = time.Now()
	if b.legacy && time.Since(b.lastCheck) >= retireCheckEvery {
		b.lastCheck = time.Now()
		if retired, err := legacyRetired(ctx, b.js); err == nil && retired {
			b.retired.Store(true)
			return
		}
	}
	covered := b.covered.Load()
	if b.stream == nil {
		s, err := b.js.Stream(ctx, natsx.StreamWebEvents)
		if err != nil {
			return
		}
		b.stream = s
	}
	m, err := b.stream.GetMsg(ctx, 1, jetstream.WithGetMsgSubject(b.filter))
	switch {
	case errors.Is(err, jetstream.ErrMsgNotFound):
		b.raise(covered + 1)
	case err == nil:
		b.raise(m.Sequence)
	}
}

// legacyRetired reports whether the legacy subject is gone for good: the
// stream no longer lists it and its consumer is deleted. retire-legacy-web
// deletes the consumer only after the stream held no legacy event with the
// subject removed, so no legacy event can arrive afterwards.
func legacyRetired(ctx context.Context, js jetstream.JetStream) (bool, error) {
	s, err := js.Stream(ctx, natsx.StreamWebEvents)
	if err != nil {
		return false, err
	}
	if slices.Contains(s.CachedInfo().Config.Subjects, natsx.WebEventsLegacy) {
		return false, nil
	}
	_, err = js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
		return true, nil
	case err != nil:
		return false, err
	}
	return false, nil
}
