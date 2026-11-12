package replayer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// failure is a classified error of one delivery attempt.
type failure struct {
	err error
	// permanent: a retry cannot fix it, the message goes to the dlq
	permanent bool
	// unreachable: umami did not answer at all (or its proxy said so), so a
	// heartbeat that answers again is a reason to retry early
	unreachable bool
}

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// classify maps a send error to a failure. 4xx is permanent, except 408 and
// 429, which say "later" rather than "never". 5xx and transport errors are
// transient; 502, 503 and 504 come from something in front of umami and
// count as unreachable, like transport errors.
func classify(err error) (*failure, string) {
	var api *umami.APIError
	if errors.As(err, &api) {
		switch {
		case api.Status == http.StatusRequestTimeout || api.Status == http.StatusTooManyRequests:
			return &failure{err: err}, sendClientError
		case api.Status >= 400 && api.Status < 500:
			return &failure{err: err, permanent: true}, sendClientError
		case api.Status == http.StatusBadGateway || api.Status == http.StatusServiceUnavailable || api.Status == http.StatusGatewayTimeout:
			return &failure{err: err, unreachable: true}, sendServerError
		default:
			return &failure{err: err}, sendServerError
		}
	}
	return &failure{err: err, unreachable: true}, sendNetwork
}

// deliver handles one held message until it is acked or moved to the dlq
// (true), or until ctx ends (false, the message stays held). transient
// failures are retried in place with natsx.RetryDelay, which keeps the lane,
// and so every later event of the same visitor, behind this one. once the
// message is older than the stream's max age the server has dropped it, and
// what the lane still holds is moved to the dlq rather than lost.
func (lr *leadRun) deliver(ctx context.Context, h *held) bool {
	for attempt := uint64(1); ; attempt++ {
		if ctx.Err() != nil {
			return false
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
		} else if time.Since(h.published) >= lr.r.maxAge {
			if lr.deadLetter(ctx, h, fmt.Sprintf("not delivered within the %s max age of %s: %s", lr.r.maxAge, natsx.StreamWebEvents, f.err.Error())) {
				return true
			}
		}
		lr.r.m.retries.Inc()
		delay := natsx.RetryDelay(attempt)
		lr.r.log.Warn("delivery failed, retrying in place", "stream_seq", h.seq, "attempt", attempt,
			"retry_in", delay.String(), "error", f.err.Error())
		if !lr.gate.wait(ctx, delay, f.unreachable) {
			return false
		}
	}
}

// attempt runs the duplicate guard, the send and the bookkeeping once.
func (lr *leadRun) attempt(ctx context.Context, h *held) error {
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
		res, err := r.umami.Send(ctx, umami.SendRequest{
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
		// one lane owns this visitor, so reading again here sees the record
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
			r.m.bookkeeping.Inc()
			return err
		}
		h.visitSaved = true
	}
	if err := r.store.markSent(bctx, ev.ID); err != nil {
		r.m.bookkeeping.Inc()
		return err
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

// gate lets lanes wait for their retry delay, and wakes the ones that wait
// because umami was unreachable as soon as its heartbeat answers again.
type gate struct {
	mu      sync.Mutex
	waiters map[chan struct{}]bool
}

// wait sleeps for d. it returns false when ctx ended first.
func (g *gate) wait(ctx context.Context, d time.Duration, unreachable bool) bool {
	ch := make(chan struct{}, 1)
	g.mu.Lock()
	if g.waiters == nil {
		g.waiters = map[chan struct{}]bool{}
	}
	g.waiters[ch] = unreachable
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

func (g *gate) hasUnreachable() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, u := range g.waiters {
		if u {
			return true
		}
	}
	return false
}

func (g *gate) wakeUnreachable() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for ch, u := range g.waiters {
		if u {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}
