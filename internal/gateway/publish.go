package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/natsx"
)

const (
	// PublishAckWait bounds the wait for jetstream's acknowledgement of one
	// event. the browser is waiting on a keepalive fetch, and a queue that
	// cannot acknowledge within it is treated as unavailable (503).
	PublishAckWait = 2 * time.Second
	// CanaryEvery is how often the readiness canary is published.
	CanaryEvery = time.Second
	// CanaryMaxAge is how recent the last acknowledged publish must be for
	// /readyz. the plan asks for an acknowledgement within the last 10 s and
	// for /readyz to fail within 10 s of a queue outage; the last ack can be
	// up to CanaryEvery older than the outage, and a probe takes up to the
	// ops handler's 2 s timeout, so 7 s keeps both inside 10 s.
	CanaryMaxAge = 7 * time.Second
	// canaryTTL removes each canary key on its own. it outlives a few
	// rounds so a slow replica never sees a gap.
	canaryTTL = 30 * time.Second
)

// ErrInvalidEvent means the event failed events.WebEvent validation after
// sanitization, so it is never queued.
var ErrInvalidEvent = errors.New("gateway: event failed validation")

// Publisher queues accepted events in WEB_EVENTS. the gateway answers 202
// only after Publish returned nil, which means jetstream stored the event.
type Publisher struct {
	js  jetstream.JetStream
	now func() time.Time
	// lastAck is the unix nanoseconds of the last acknowledged publish,
	// event or canary
	lastAck atomic.Int64
}

// NewPublisher returns a publisher on js. now is the clock of the readiness
// age check.
func NewPublisher(js jetstream.JetStream, now func() time.Time) (*Publisher, error) {
	if js == nil || now == nil {
		return nil, errors.New("gateway: NewPublisher needs jetstream and a clock")
	}
	return &Publisher{js: js, now: now}, nil
}

// Publish validates, encodes and publishes one event with its id as
// Nats-Msg-Id and waits at most PublishAckWait for the acknowledgement. a
// retry of the same event inside the stream's duplicate window is stored
// once.
func (p *Publisher) Publish(ctx context.Context, ev *events.WebEvent) error {
	data, err := ev.Marshal()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEvent, err)
	}
	ctx, cancel := context.WithTimeout(ctx, PublishAckWait)
	defer cancel()
	if _, err := natsx.PublishDedup(ctx, p.js, natsx.StreamWebEvents, ev.Subject(), ev.ID, data, nil); err != nil {
		return err
	}
	p.markAck()
	return nil
}

func (p *Publisher) markAck() { p.lastAck.Store(p.now().UnixNano()) }

// LastAck is when jetstream last acknowledged a publish of this node, zero
// before the first one.
func (p *Publisher) LastAck() time.Time {
	n := p.lastAck.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// Ready fails when no publish was acknowledged within CanaryMaxAge.
func (p *Publisher) Ready(context.Context) error {
	last := p.LastAck()
	if last.IsZero() {
		return errors.New("gateway: no jetstream acknowledgement yet")
	}
	if age := p.now().Sub(last); age > CanaryMaxAge {
		return fmt.Errorf("gateway: last jetstream acknowledgement %s ago", age.Round(time.Millisecond))
	}
	return nil
}

// RunCanary publishes a small canary every CanaryEvery until ctx ends, so
// /readyz sees a dead queue within CanaryMaxAge even when no events arrive.
//
// the canary is a create of a short lived key in edge_secrets, not a message
// on WEB_EVENTS: a work-queue message would be delivered to the replayer as
// an event, and the gateway user may publish only to its own streams and the
// edge_secrets bucket. a bucket create is a jetstream publish acknowledged
// by the bucket's stream leader after storing, on the same nats cluster, and
// the key removes itself after canaryTTL. an outage of the WEB_EVENTS stream
// alone does not show here; event publishes then fail with 503 directly.
func (p *Publisher) RunCanary(ctx context.Context, kv jetstream.KeyValue, node string, log *slog.Logger) {
	t := time.NewTicker(CanaryEvery)
	defer t.Stop()
	var failing bool
	for {
		err := p.canary(ctx, kv, node)
		switch {
		case err != nil && ctx.Err() == nil && !failing:
			failing = true
			log.Warn("jetstream canary not acknowledged", "error", err.Error())
		case err == nil && failing:
			failing = false
			log.Info("jetstream canary acknowledged again")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Publisher) canary(ctx context.Context, kv jetstream.KeyValue, node string) error {
	// one round must end before the next tick
	ctx, cancel := context.WithTimeout(ctx, CanaryEvery)
	defer cancel()
	key := "canary." + node + "." + strconv.FormatInt(p.now().UnixNano(), 10)
	if _, err := kv.Create(ctx, key, []byte{1}, jetstream.KeyTTL(canaryTTL)); err != nil {
		return err
	}
	p.markAck()
	return nil
}
