package replayer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/umami"
)

// failure is a classified error of one delivery attempt.
type failure struct {
	err error
	// permanent: a retry cannot fix it, the message goes to the dlq
	permanent bool
	// send: the error came from the umami send (an answer, a bad answer or
	// no answer), not from nats bookkeeping, so umami's health decides how
	// it is retried
	send bool
}

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// classify maps a send error to a failure and a metric label. 4xx is
// permanent, except 408 and 429, which say "later" rather than "never".
// every other error is retried; whether without limit or a bounded number
// of times is decided by umami's health (deliver).
func classify(err error) (*failure, string) {
	var api *umami.APIError
	if errors.As(err, &api) {
		switch {
		case api.Status == http.StatusRequestTimeout || api.Status == http.StatusTooManyRequests:
			return &failure{err: err, send: true}, sendClientError
		case api.Status >= 400 && api.Status < 500:
			return &failure{err: err, permanent: true, send: true}, sendClientError
		default:
			return &failure{err: err, send: true}, sendServerError
		}
	}
	return &failure{err: err, send: true}, sendNetwork
}

// maxHealthyFailures is how many sends of one event may fail while umami's
// health checks pass before the event goes to the dlq. umami 3.5.0 inserts
// the website_event row before the event data, so a failing data row leaves
// one stored custom event per attempt; the bound keeps that small, where the
// in-place retry until the stream's max age stored one about every 10
// minutes for 72 hours.
const maxHealthyFailures = 4

// healthyRetryDelay is the pause after the n-th failure on a healthy umami:
// 1 s, 5 s, 30 s, so the event is retried for about 36 s in all. a short
// spell of contention on one row can pass in that time; a value umami cannot
// store never does.
func healthyRetryDelay(n int) time.Duration {
	return natsx.RetryDelay(uint64(n)) //nolint:gosec // n is a small positive count
}

// deliver handles one held message until it is acked or moved to the dlq
// (true), or until ctx ends (false, the message stays held). only this
// visitor's later events wait while it is retried (runVisitor), so a failing
// event never holds back another visitor or site.
//
// a failed send is followed by a health check of the umami process
// (heartbeat and collection probe). when umami is unhealthy, umami as a
// whole cannot store events: the event is retried without limit, up to the
// stream's max age, and is woken early when the process is healthy again.
// when umami is healthy but failing sends of many visitors show it cannot
// write (writefault.go), the same holds, and the event is woken by the next
// send the process stores. otherwise the event itself is the problem: after
// maxHealthyFailures such failures it goes to the dlq. nats errors in the
// bookkeeping are retried without limit, as they say nothing about the
// event.
func (lr *leadRun) deliver(ctx context.Context, q *visitorQueue, h *held) bool {
	for attempt := uint64(1); ; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		if !h.delivered && time.Since(h.published) >= lr.r.maxAge {
			// the server has already dropped it from the stream, and an
			// erasure run that waited for the stream may have finished, so
			// it is not sent any more
			if lr.deadLetter(ctx, h, fmt.Sprintf("not delivered within the %s max age of %s", lr.r.maxAge, natsx.StreamWebEvents)) {
				return true
			}
			if !lr.gate.wait(ctx, natsx.RetryDelay(attempt), noTarget) {
				return false
			}
			continue
		}
		err := lr.attempt(ctx, h)
		if err == nil {
			return true
		}
		if errors.Is(err, natsx.ErrLockLost) {
			lr.cancel(err)
			return false
		}
		if ctx.Err() != nil {
			return false
		}
		var f *failure
		if !errors.As(err, &f) {
			f = &failure{err: err}
		}
		if f.permanent {
			if lr.deadLetter(ctx, h, f.err.Error()) {
				return true
			}
			// the dlq publish failed; retry the whole step later
		}
		delay := natsx.RetryDelay(attempt)
		waitOn := noTarget
		if f.send && !f.permanent {
			healthy := lr.healthy(ctx, h.target)
			if healthy {
				lr.writes[h.target].fail(h.key, time.Now())
			}
			switch {
			case healthy && lr.writes[h.target].faulted(time.Now()):
				// the process cannot write (writefault.go): the failure says
				// nothing about this event, so it is not counted and the
				// visitor waits for the process's next stored send
				h.healthyFailures = 0
				lr.setBlocked(q, false)
				delay = writeFaultDelay(attempt)
				waitOn = writeFaultWaiter(h.target)
				lr.r.m.writeFaultRetries.Inc()
			case healthy:
				h.healthyFailures++
				if h.healthyFailures >= maxHealthyFailures {
					reason := fmt.Sprintf("umami failed this event %d times while its health checks passed: %s", h.healthyFailures, f.err.Error())
					if lr.deadLetter(ctx, h, reason) {
						return true
					}
				}
				lr.setBlocked(q, true)
				delay = healthyRetryDelay(h.healthyFailures)
			default:
				lr.setBlocked(q, false)
				waitOn = h.target
			}
		}
		lr.r.m.retries.Inc()
		lr.r.log.Warn("delivery failed, retrying", "stream_seq", h.seq, "attempt", attempt,
			"healthy_failures", h.healthyFailures, "retry_in", delay.String(), "error", f.err.Error())
		if !lr.gate.wait(ctx, delay, waitOn) {
			return false
		}
	}
}

// attempt runs the duplicate guard, the send and the bookkeeping once.
func (lr *leadRun) attempt(ctx context.Context, h *held) error {
	release, err := lr.acquire(ctx, h.target)
	if err != nil {
		return err
	}
	defer release()
	r := lr.r
	ev := h.ev
	if !h.delivered {
		sent, err := r.store.sent(ctx, ev.ID)
		if err != nil {
			return err
		}
		if sent {
			// a redelivery of an event umami already stored: a crash or lock
			// loss after the guard write and before the ack
			r.m.outcomes.WithLabelValues(outcomeDuplicate).Inc()
			lr.ack(ctx, h)
			return nil
		}
		rec, err := r.store.load(ctx, ev.SiteID, ev.VT)
		if errors.Is(err, errBadVisitRecord) {
			r.log.Warn("visit state unreadable, sending without continuity", "stream_seq", h.seq, "error", err.Error())
			rec, err = nil, nil
		}
		if err != nil {
			return err
		}
		ts := ev.AcceptedAt / 1000
		cache := rec.cacheFor(ev.WebsiteID, ts)
		if err := lr.guard.check(ctx); err != nil {
			return err
		}
		start := time.Now()
		res, err := r.umami[h.target].Send(ctx, umami.SendRequest{
			Event: umami.Event{
				Website:   ev.WebsiteID,
				Hostname:  ev.Hostname,
				URL:       ev.URL,
				Referrer:  ev.Referrer,
				Title:     ev.Title,
				Screen:    ev.Screen,
				Language:  ev.Language,
				Name:      ev.Name,
				Data:      ev.Data,
				Timestamp: ts,
				Salt:      ev.VT,
			},
			UserAgent: ev.UA,
			Country:   ev.Country,
			Cache:     cache,
		})
		elapsed := time.Since(start).Seconds()
		if err != nil {
			f, label := classify(err)
			r.m.send.WithLabelValues(label).Observe(elapsed)
			return f
		}
		if res.Bot {
			r.m.send.WithLabelValues(sendBot).Observe(elapsed)
		} else {
			r.m.send.WithLabelValues(sendOK).Observe(elapsed)
		}
		r.m.perProcess.WithLabelValues(strconv.Itoa(h.target)).Inc()
		if lr.writes[h.target].succeed(h.key, time.Now()) {
			// visitors waiting out a write fault of this process retry now
			lr.gate.wake(writeFaultWaiter(h.target))
		}
		h.delivered, h.bot = true, res.Bot
		h.sessionID, h.cache = res.SessionID, res.Cache
	}
	return lr.bookkeep(ctx, h)
}

// bookkeep records a send umami answered with 200: the visit state, then
// the duplicate guard, then the ack. it runs on a context detached from the
// hold, so a lock loss right after the 200 still records the send; a failed
// write is retried by deliver without sending again.
func (lr *leadRun) bookkeep(ctx context.Context, h *held) error {
	r := lr.r
	ev := h.ev
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()
	if !h.bot && !h.visitSaved {
		// one goroutine owns this visitor, so reading again here sees the record
		// the send was based on
		rec, err := r.store.load(bctx, ev.SiteID, ev.VT)
		if errors.Is(err, errBadVisitRecord) {
			rec, err = nil, nil
		}
		if err != nil {
			r.m.bookkeeping.Inc()
			return err
		}
		if rec == nil {
			rec = &VisitRecord{}
		}
		if rec.Websites == nil {
			rec.Websites = map[string]VisitEntry{}
		}
		ts := ev.AcceptedAt / 1000
		prev := rec.Websites[ev.WebsiteID]
		rec.Websites[ev.WebsiteID] = VisitEntry{SessionID: h.sessionID, Cache: h.cache, LastEventTS: max(prev.LastEventTS, ts)}
		if err := r.store.save(bctx, ev.SiteID, ev.VT, rec); err != nil {
			if !bucketFull(err) {
				r.m.bookkeeping.Inc()
				return err
			}
			// a full visit_state must not stop delivery: the
			// event is stored in umami already, and only this visitor's
			// continuity is lost (its next event starts a visit of its own
			// hour). ReplayerVisitStateFull alerts on the counter.
			r.m.visitStateSkipped.WithLabelValues("visit").Inc()
			r.log.Warn("visit_state is full, visit continuity not recorded", "stream_seq", h.seq)
		}
		h.visitSaved = true
	}
	if err := r.store.markSent(bctx, ev.ID); err != nil {
		if !bucketFull(err) {
			r.m.bookkeeping.Inc()
			return err
		}
		// without the guard a crash between here and the ack can store this
		// event once more; refusing the ack instead held every event until
		// keys expired, up to 24 hours
		r.m.visitStateSkipped.WithLabelValues("sent").Inc()
		r.log.Warn("visit_state is full, duplicate guard not recorded", "stream_seq", h.seq)
	}
	if h.bot {
		r.stats.count(ev.SiteID, StatBot)
		r.m.outcomes.WithLabelValues(outcomeBot).Inc()
	} else {
		r.m.outcomes.WithLabelValues(outcomeDelivered).Inc()
	}
	lr.ack(bctx, h)
	return nil
}

// ack confirms the message with the server. an unconfirmed ack is only
// counted: the message comes back and the duplicate guard skips it.
func (lr *leadRun) ack(ctx context.Context, h *held) {
	if err := h.current().DoubleAck(ctx); err != nil {
		lr.r.m.ackFailures.Inc()
		lr.r.log.Warn("ack not confirmed, message may come back", "stream_seq", h.seq, "error", err.Error())
	}
}

// deadLetter moves a held message to WEB_DLQ with the reason, counts it and
// acks it. false means the dlq publish failed and the message stays held.
func (lr *leadRun) deadLetter(ctx context.Context, h *held, reason string) bool {
	r := lr.r
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()
	msg := h.current()
	if err := natsx.DeadLetter(bctx, r.js, natsx.StreamWebDLQ, natsx.Subject(natsx.SubjectWebDLQ, h.ev.SiteID), msg, reason); err != nil {
		r.log.Error("dead letter failed, retrying in place", "stream_seq", h.seq, "error", err.Error())
		return false
	}
	r.log.Warn("event moved to the dlq", "stream_seq", h.seq, "reason", reason)
	r.stats.count(h.ev.SiteID, StatDLQ)
	r.m.outcomes.WithLabelValues(outcomeDLQ).Inc()
	lr.ack(bctx, h)
	return true
}

// noTarget marks a waiter that waits for its delay only, not for an umami
// process to come back.
const noTarget = -1

// gate lets visitors wait for their retry delay, and wakes the ones that wait
// because their umami process was unreachable as soon as its heartbeat
// answers again.
type gate struct {
	mu sync.Mutex
	// waiters maps each waiting visitor to the umami process it waits for, or
	// noTarget
	waiters map[chan struct{}]int
}

// wait sleeps for d, or until target answers again when target is not
// noTarget. it returns false when ctx ended first.
func (g *gate) wait(ctx context.Context, d time.Duration, target int) bool {
	ch := make(chan struct{}, 1)
	g.mu.Lock()
	if g.waiters == nil {
		g.waiters = map[chan struct{}]int{}
	}
	g.waiters[ch] = target
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.waiters, ch)
		g.mu.Unlock()
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	case <-ch:
		return true
	}
}

// unreachableTargets lists the umami processes some visitor waits for, sorted.
func (g *gate) unreachableTargets() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []int
	for _, t := range g.waiters {
		if t >= 0 && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return out
}

func (g *gate) wakeUnreachable(target int) { g.wake(target) }

// wake wakes every visitor waiting under key: an umami process index, or a
// writeFaultWaiter key.
func (g *gate) wake(key int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for ch, t := range g.waiters {
		if t == key {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}
