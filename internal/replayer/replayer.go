// Package replayer delivers accepted web events from WEB_EVENTS to umami's
// /api/send.
//
// one instance runs on each core node; only the holder of the replayer lock
// consumes. each visitor (site id and visitor token) with held messages has
// its own queue, delivered one message at a time in stream order, so one
// visitor's events reach umami in stream order. a failing event is retried
// in place and holds back only that visitor's later events, never another
// visitor or site. REPLAYER_WORKERS bounds the sends in flight at once.
//
// sends are spread over several umami processes by a hash of site id and
// visitor token, so a visitor's events always go to one process.
// the processes share one database and one APP_SECRET, and umami derives the
// session id, the visit id and the cache token from those and the event alone
// (docs/reference/umami-contract.md item 14), so which process stores an
// event does not change what is stored.
//
// delivery is at least once with a narrow duplicate window: the replayer checks
// sent.<event id> in visit_state before sending and writes it after umami's
// 200, then acks. a crash, kill or lock loss between umami's 200 and that
// write stores the event twice; at most one event per delivery slot can be in that
// window at any moment.
//
// umami's database cannot be fenced, so the lock is confirmed with the
// server before every fetch and, at most a second apart, before sends. a
// former leader that was paused and resumes after losing the lock can still
// complete the sends it had already passed that check for: at most one per
// delivery slot. a new leader first waits until the previous holder's unacknowledged
// messages are redeliverable (at most natsx.AckWait), so they reach umami
// before anything newer of the same visitor.
//
// visits: visit_state keeps umami's session id,
// cache token and the last acceptance time per site, visitor token and umami
// website. the cache token is sent only while the gap to the previous event
// is at most 1800 s. without it umami derives the visit from the session and
// the utc hour (docs/reference/umami-contract.md item 7), so two events more
// than 30 minutes apart still form one visit when both fall in the utc hour
// in which the visit started; that is umami's own behaviour for timestamped
// events.
package replayer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/internal/umami"
)

const (
	// fetchBatch is the most messages fetched at once; the lock is confirmed
	// before every fetch.
	fetchBatch = 64
	// fetchWait bounds one fetch, so a lost lock or a shutdown is noticed
	// within it even when the stream is idle.
	fetchWait = 2 * time.Second
	// fetchRetryWait is the pause after a failed fetch, for example while the
	// client reconnects.
	fetchRetryWait = time.Second
	// keepaliveEvery resets the ack wait of every held message. a message
	// waits in its visitor's queue while an earlier event of the visitor
	// retries, which can take far longer than natsx.AckWait.
	keepaliveEvery = natsx.AckWait / 3
	// confirmMaxAge is how old the last lock confirmation may be when a visitor
	// sends. together with the confirmation before every fetch it bounds what
	// a paused former leader can still send to one in-flight send per delivery slot.
	confirmMaxAge = time.Second
	// probeEvery is how often umami's health is checked while visitors wait
	// because umami was unhealthy, so they resume as soon as it is back
	// instead of at the end of a long backoff step.
	probeEvery = 2 * time.Second
	// healthTTL is how long one health check result of an umami process is
	// reused. during an outage every waiting visitor fails at once, and one
	// check answers for all of them.
	healthTTL = time.Second
	// blockedBacklog caps the messages a visitor may have held while its
	// oldest event fails on a healthy umami. a burst beyond it goes to the
	// dlq instead of filling MaxInFlight, which would stop the fetch for
	// every site. no browser sends that many events within the ~36 s a
	// failing event is retried (the gateway's replay limit is 50 events per
	// proof and node).
	blockedBacklog = 64
	// bookkeepingTimeout bounds the visit state write, the duplicate guard
	// write, the dlq publish and the ack after umami answered. it is detached
	// from the lead context so a lock loss right after a 200 still records the
	// send, which keeps the duplicate window as narrow as possible.
	bookkeepingTimeout = 10 * time.Second
	// lagEvery is how often the consumer lag gauges are refreshed.
	lagEvery = 5 * time.Second
	// predecessorPoll is how often a new leader checks whether the previous
	// holder's unacknowledged messages are back.
	predecessorPoll = 500 * time.Millisecond
	// acquireRetry is how often a standby tries to take the lock.
	acquireRetry = time.Second
)

// errNotReady ends a hold because jetstream could not serve the consumer
// yet, as while a cluster recovers from a restart. Run contends again
// instead of stopping the process.
var errNotReady = errors.New("replayer: jetstream not ready")

// Config wires a Replayer.
type Config struct {
	Settings Settings
	NC       *nats.Conn
	// Replicas is NATS_REPLICAS, the replica count visit_state and locks are
	// recreated with when a restart lost them.
	Replicas int
	// Umami holds one client per umami process (Settings.UmamiURLs), in the
	// same order; a visitor's events go to Umami[visitorHash % len(Umami)].
	Umami    []*umami.Client
	Log      *slog.Logger
	Registry *prometheus.Registry
}

// Replayer is one replayer instance.
type Replayer struct {
	set     Settings
	nc      *nats.Conn
	js      jetstream.JetStream
	umami   []*umami.Client
	log     *slog.Logger
	reg     *prometheus.Registry
	store   visitStore
	lock    *natsx.Lock
	buckets *natsx.BucketKeeper
	stats   *stats
	m       *metrics
	maxAge  time.Duration

	// current is the lead in progress, nil on a standby
	current atomic.Pointer[leadRun]
}

// New recreates the memory buckets when a restart lost them, binds them and
// registers the metrics. it does not take the lock.
func New(ctx context.Context, cfg Config) (*Replayer, error) {
	if cfg.NC == nil || len(cfg.Umami) == 0 || slices.Contains(cfg.Umami, nil) || cfg.Log == nil || cfg.Registry == nil {
		return nil, errors.New("replayer: Config needs NC, at least one Umami client, Log and Registry")
	}
	if cfg.Settings.Workers < 1 || cfg.Settings.MaxInFlight < cfg.Settings.Workers || !nodeIDRe.MatchString(cfg.Settings.NodeID) {
		return nil, errors.New("replayer: settings are invalid, load them with LoadSettings")
	}
	js, err := jetstream.New(cfg.NC)
	if err != nil {
		return nil, fmt.Errorf("replayer: jetstream: %w", err)
	}
	// both buckets are memory-backed; the replayer recreates them itself, so
	// it recovers from a nats restart without an operator
	buckets, err := natsx.NewBucketKeeper(js, cfg.Replicas, cfg.Log, natsx.BucketVisitState, natsx.BucketLocks)
	if err != nil {
		return nil, err
	}
	if err := buckets.Ensure(ctx); err != nil {
		return nil, fmt.Errorf("replayer: %w", err)
	}
	visitKV, err := js.KeyValue(ctx, natsx.BucketVisitState)
	if err != nil {
		return nil, fmt.Errorf("replayer: bind %s (run platformctl nats apply): %w", natsx.BucketVisitState, err)
	}
	locksKV, err := js.KeyValue(ctx, natsx.BucketLocks)
	if err != nil {
		return nil, fmt.Errorf("replayer: bind %s (run platformctl nats apply): %w", natsx.BucketLocks, err)
	}
	holder, err := holderID(cfg.Settings.NodeID)
	if err != nil {
		return nil, err
	}
	lock, err := natsx.NewLock(locksKV, natsx.LockReplayer, holder)
	if err != nil {
		return nil, err
	}
	maxAge, err := webEventsMaxAge()
	if err != nil {
		return nil, err
	}
	r := &Replayer{
		set:     cfg.Settings,
		nc:      cfg.NC,
		js:      js,
		umami:   cfg.Umami,
		log:     cfg.Log,
		reg:     cfg.Registry,
		store:   visitStore{kv: visitKV},
		lock:    lock,
		buckets: buckets,
		maxAge:  maxAge,
	}
	r.m, err = newMetrics(cfg.Registry, len(cfg.Umami), r.inFlight, r.oldestAge)
	if err != nil {
		return nil, err
	}
	r.stats, err = newStats(StatsNode(cfg.Settings.NodeID), js, cfg.Log, cfg.Registry)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// holderID is the lock value: node id plus a random suffix, so two processes
// started with the same node id by mistake still never confirm each other's
// hold.
func holderID(node string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("replayer: holder id: %w", err)
	}
	return node + "-" + hex.EncodeToString(b), nil
}

// webEventsMaxAge reads the stream's max age from the declared topology, so
// the retry limit follows section 4.6 instead of a second constant.
func webEventsMaxAge() (time.Duration, error) {
	spec, err := natsx.Topology(1)
	if err != nil {
		return 0, err
	}
	for _, s := range spec.Streams {
		if s.Name == natsx.StreamWebEvents && s.MaxAge > 0 {
			return s.MaxAge, nil
		}
	}
	return 0, errors.New("replayer: topology declares no max age for WEB_EVENTS")
}

// Ops serves /healthz, /readyz (nats connected, no bucket known missing)
// and /metrics.
func (r *Replayer) Ops() http.Handler {
	return obs.NewOpsHandler(r.reg, r.log, obs.Check{Name: "nats", Fn: func(context.Context) error {
		if s := r.nc.Status(); s != nats.CONNECTED {
			return fmt.Errorf("nats connection is %s", s)
		}
		return nil
	}}, obs.Check{Name: "buckets", Fn: r.buckets.Ready})
}

// Run contends for the lock and consumes while it holds it, until ctx ends.
// the stats flusher and the lag sampler run for the whole process, so a
// standby reports lag too and the last counts are flushed on shutdown.
func (r *Replayer) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	bg, stopBg := context.WithCancel(context.WithoutCancel(ctx))
	wg.Go(func() { r.stats.run(bg) })
	wg.Go(func() { r.sampleLag(bg) })
	wg.Go(func() { r.buckets.Run(bg) })
	defer func() {
		stopBg()
		wg.Wait()
	}()

	el := &natsx.Elector{Lock: r.lock, RenewEvery: natsx.LockRenewEvery, RetryEvery: acquireRetry, Log: r.log, Buckets: r.buckets}
	for {
		err := el.Run(ctx, r.lead)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, natsx.ErrLockLost) {
			// a visitor or the fetch loop could not confirm the hold; contend again
			r.log.Warn("stopped consuming, lock not confirmed", "error", err.Error())
			continue
		}
		if errors.Is(err, errNotReady) {
			r.log.Warn("stopped consuming, jetstream not ready", "error", err.Error())
			if !sleepCtx(ctx, acquireRetry) {
				return nil
			}
			continue
		}
		return err
	}
}

func (r *Replayer) inFlight() float64 {
	if lr := r.current.Load(); lr != nil {
		return float64(lr.count())
	}
	return 0
}

func (r *Replayer) oldestAge() float64 {
	if lr := r.current.Load(); lr != nil {
		if t := lr.oldest(); !t.IsZero() {
			return time.Since(t).Seconds()
		}
	}
	return 0
}

// sampleLag refreshes the lag gauges from the consumer info.
func (r *Replayer) sampleLag(ctx context.Context) {
	t := time.NewTicker(lagEvery)
	defer t.Stop()
	var cons jetstream.Consumer
	for {
		if cons == nil {
			c, err := r.js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
			if err == nil {
				cons = c
			} else if ctx.Err() == nil {
				r.log.Warn("lag sampler cannot bind the consumer", "error", err.Error())
			}
		}
		if cons != nil {
			ictx, cancel := context.WithTimeout(ctx, lagEvery)
			info, err := cons.Info(ictx)
			cancel()
			if err == nil {
				r.m.pending.Set(float64(info.NumPending))
				r.m.ackPending.Set(float64(info.NumAckPending))
				r.m.lagSampled.Set(float64(time.Now().Unix()))
			} else if ctx.Err() == nil {
				r.log.Warn("consumer info failed, lag gauges are stale", "error", err.Error())
			}
		}
		r.sampleVisitState(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sampleVisitState refreshes the visit_state usage gauges, which the
// ReplayerVisitStateNearlyFull alert reads.
func (r *Replayer) sampleVisitState(ctx context.Context) {
	ictx, cancel := context.WithTimeout(ctx, lagEvery)
	defer cancel()
	st, err := r.store.kv.Status(ictx)
	if err != nil {
		if ctx.Err() == nil {
			r.log.Warn("visit_state status failed, usage gauges are stale", "error", err.Error())
		}
		return
	}
	r.m.visitStateBytes.Set(float64(st.Bytes()))
	if kv, ok := st.(*jetstream.KeyValueBucketStatus); ok {
		r.m.visitStateMax.Set(float64(kv.StreamInfo().Config.MaxBytes))
	}
}

// lead consumes while the lock is held. it returns nil when ctx ends (the
// elector cancels it when a renewal fails) and an error wrapping
// natsx.ErrLockLost when a confirmation failed.
func (r *Replayer) lead(parent context.Context) error {
	cons, err := r.js.Consumer(parent, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	switch {
	case err == nil:
	case parent.Err() != nil:
		return nil
	case errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound):
		// both are file-backed and survive restarts; missing means the
		// topology was never applied, which only an operator can fix
		return fmt.Errorf("replayer: bind consumer %s on %s (run platformctl nats apply): %w", natsx.ConsumerReplayer, natsx.StreamWebEvents, err)
	default:
		// a timeout or a leaderless consumer while a cluster recovers from a
		// restart: give the lock back and contend again
		return fmt.Errorf("%w: bind consumer %s on %s: %w", errNotReady, natsx.ConsumerReplayer, natsx.StreamWebEvents, err)
	}
	if err := r.awaitPredecessor(parent, cons); err != nil {
		return nil //nolint:nilerr // only a cancelled context ends the wait, which is a normal end of the hold
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	lr := &leadRun{
		r:        r,
		cons:     cons,
		cancel:   cancel,
		guard:    &lockGuard{lock: r.lock, m: r.m},
		slots:    make([]chan struct{}, len(r.umami)),
		health:   make([]targetHealth, len(r.umami)),
		writes:   make([]writeWatch, len(r.umami)),
		held:     map[uint64]*held{},
		visitors: map[string]*visitorQueue{},
		freed:    make(chan struct{}, 1),
		gate:     &gate{},
	}
	for i := range lr.slots {
		// the workers are shared evenly; LoadSettings keeps the process
		// count at most the worker count, so every process gets a slot
		lr.slots[i] = make(chan struct{}, slotsPerTarget(r.set.Workers, len(r.umami), i))
	}
	r.current.Store(lr)
	r.m.leader.Set(1)
	defer func() {
		r.current.Store(nil)
		r.m.leader.Set(0)
	}()
	r.log.Info("consuming", "workers", r.set.Workers, "max_in_flight", r.set.MaxInFlight, "umami_processes", len(r.umami))
	return lr.run(ctx, parent)
}

// awaitPredecessor waits until the previous holder's unacknowledged messages
// can be redelivered before the first fetch. without it the new leader would
// fetch newer messages first and a visitor's held-back events would reach
// umami after later ones. a clean predecessor naks everything on the way out,
// so the wait ends at once; a killed one leaves messages pending until their
// ack wait runs out, at most natsx.AckWait after this point because the
// predecessor stopped refreshing them before its lock expired.
func (r *Replayer) awaitPredecessor(ctx context.Context, cons jetstream.Consumer) error {
	deadline := time.Now().Add(natsx.AckWait)
	logged := false
	for {
		info, err := cons.Info(ctx)
		if err == nil && info.NumAckPending == 0 {
			return nil
		}
		if err != nil && ctx.Err() == nil {
			r.log.Warn("consumer info failed while waiting for the previous holder", "error", err.Error())
		}
		if err == nil && !logged {
			r.log.Info("waiting for the previous holder's messages to become redeliverable", "ack_pending", info.NumAckPending)
			logged = true
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		t := time.NewTimer(predecessorPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// lockGuard confirms the hold before every fetch and, at most confirmMaxAge
// apart, before sends.
type lockGuard struct {
	lock *natsx.Lock
	m    *metrics
	mu   sync.Mutex
	at   time.Time
}

func (g *lockGuard) confirm(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.confirmLocked(ctx)
}

func (g *lockGuard) confirmLocked(ctx context.Context) error {
	if _, err := g.lock.Confirm(ctx); err != nil {
		g.m.lockConfirms.WithLabelValues("lost").Inc()
		return err
	}
	g.at = time.Now()
	g.m.lockConfirms.WithLabelValues("ok").Inc()
	return nil
}

func (g *lockGuard) check(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.at) < confirmMaxAge {
		return nil
	}
	return g.confirmLocked(ctx)
}

// held is one message between fetch and ack.
type held struct {
	seq       uint64
	published time.Time
	ev        *events.WebEvent
	// target is the index of the umami process this visitor's events go to
	target int
	// key is the visitor: site id and visitor token
	key string
	// healthyFailures counts failed sends while umami's health checks
	// passed; only the visitor's queue goroutine touches it
	healthyFailures int

	mu  sync.Mutex
	msg jetstream.Msg
	// delivered is set once umami answered 200, so a retry after a failed
	// bookkeeping write never sends the event again
	delivered  bool
	bot        bool
	sessionID  string
	cache      string
	visitSaved bool
}

func (h *held) current() jetstream.Msg {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.msg
}

// replace keeps the newest delivery of a message that came back while it was
// still held (a keepalive that did not reach the server), so the ack goes to
// the delivery the server is waiting for.
func (h *held) replace(m jetstream.Msg) {
	h.mu.Lock()
	h.msg = m
	h.mu.Unlock()
}

// leadRun is the state of one hold of the lock.
type leadRun struct {
	r      *Replayer
	cons   jetstream.Consumer
	cancel context.CancelCauseFunc
	guard  *lockGuard
	gate   *gate
	// slots holds one semaphore per umami process; together they allow
	// REPLAYER_WORKERS sends at once
	slots []chan struct{}
	// health caches one health check per umami process
	health []targetHealth
	// writes is the recent send record per umami process, which shows a
	// write fault the health check cannot see (writefault.go)
	writes []writeWatch
	// visitorsWG tracks the visitor goroutines, which are started by the
	// fetch loop
	visitorsWG sync.WaitGroup

	mu       sync.Mutex
	held     map[uint64]*held
	visitors map[string]*visitorQueue
	freed    chan struct{}
}

// visitorQueue is one visitor's held messages in stream order. one
// goroutine delivers them, oldest first.
type visitorQueue struct {
	key string
	// msgs and blocked are guarded by leadRun.mu
	msgs []*held
	// blocked is set while the oldest message fails on a healthy umami
	blocked bool
}

// targetHealth is the last health check of one umami process. mu is held
// during a check, so concurrent callers wait for it and reuse its result.
type targetHealth struct {
	mu sync.Mutex
	at time.Time
	ok bool
}

func (lr *leadRun) count() int {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return len(lr.held)
}

func (lr *leadRun) oldest() time.Time {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	var t time.Time
	for _, h := range lr.held {
		if t.IsZero() || h.published.Before(t) {
			t = h.published
		}
	}
	return t
}

func (lr *leadRun) run(ctx, parent context.Context) error {
	var wg sync.WaitGroup
	wg.Go(func() { lr.keepalive(ctx) })
	wg.Go(func() { lr.probe(ctx) })
	err := lr.fetchLoop(ctx)
	lr.cancel(err)
	wg.Wait()
	// the fetch loop has returned, so no visitor goroutine starts any more
	lr.visitorsWG.Wait()
	lr.release()
	if parent.Err() != nil {
		return nil
	}
	if cause := context.Cause(ctx); errors.Is(cause, natsx.ErrLockLost) {
		return cause
	}
	return err
}

// fetchLoop confirms the hold, fetches up to the free capacity and hands
// each message to its visitor's queue, until ctx ends or a confirmation fails.
func (lr *leadRun) fetchLoop(ctx context.Context) error {
	maxInFlight := lr.r.set.MaxInFlight
	for {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		free := maxInFlight - lr.count()
		if free <= 0 {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-lr.freed:
			}
			continue
		}
		if err := lr.guard.confirm(ctx); err != nil {
			return err
		}
		batch, err := lr.cons.Fetch(min(free, fetchBatch), jetstream.FetchMaxWait(fetchWait))
		if err != nil {
			if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) {
				return fmt.Errorf("replayer: consumer is gone, run platformctl nats apply: %w", err)
			}
			lr.r.log.Warn("nats fetch failed", "error", err.Error())
			t := time.NewTimer(fetchRetryWait)
			select {
			case <-ctx.Done():
				t.Stop()
			case <-t.C:
			}
			continue
		}
		for msg := range batch.Messages() {
			lr.accept(ctx, msg)
		}
		if err := batch.Error(); err != nil && !errors.Is(err, context.Canceled) {
			if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) {
				return fmt.Errorf("replayer: consumer is gone, run platformctl nats apply: %w", err)
			}
			lr.r.log.Warn("nats fetch ended with an error", "error", err.Error())
		}
	}
}

// visitorHash is fnv-1a over site id and visitor token: stable across
// processes and restarts, and cheap; the token is already a uniform hmac, so
// the spread is even.
func visitorHash(siteID, vt string) uint64 {
	f := fnv.New64a()
	_, _ = f.Write([]byte(siteID))
	_, _ = f.Write([]byte{0})
	_, _ = f.Write([]byte(vt))
	return f.Sum64()
}

// targetOf picks the umami process of a visitor among n, so every event of
// one visitor goes to one process.
func targetOf(siteID, vt string, n int) int {
	return int(visitorHash(siteID, vt) % uint64(n)) //nolint:gosec // n is a positive process count
}

func (lr *leadRun) accept(ctx context.Context, msg jetstream.Msg) {
	meta, err := msg.Metadata()
	if err != nil {
		lr.r.log.Error("message without jetstream metadata", "subject", msg.Subject(), "error", err.Error())
		return
	}
	lr.mu.Lock()
	if h, ok := lr.held[meta.Sequence.Stream]; ok {
		lr.mu.Unlock()
		h.replace(msg)
		return
	}
	lr.mu.Unlock()

	ev, reason := decode(msg)
	if reason != "" {
		lr.r.m.undecodable.Inc()
		lr.deadLetterUnkeyed(ctx, msg, meta, reason)
		return
	}
	h := &held{seq: meta.Sequence.Stream, published: meta.Timestamp, ev: ev, msg: msg,
		target: targetOf(ev.SiteID, ev.VT, len(lr.r.umami)), key: ev.SiteID + "\x00" + ev.VT}
	lr.mu.Lock()
	q := lr.visitors[h.key]
	if q != nil && q.blocked && len(q.msgs) >= blockedBacklog {
		lr.mu.Unlock()
		reason := fmt.Sprintf("visitor already has %d events held behind one that fails on a healthy umami", blockedBacklog)
		if lr.deadLetter(ctx, h, reason) {
			return
		}
		// the dlq publish failed: hold it after all, which is what happened
		// before the cap existed
		lr.mu.Lock()
		q = lr.visitors[h.key]
	}
	lr.held[h.seq] = h
	fresh := q == nil
	if fresh {
		q = &visitorQueue{key: h.key}
		lr.visitors[h.key] = q
	}
	q.msgs = append(q.msgs, h)
	lr.mu.Unlock()
	if fresh {
		lr.visitorsWG.Go(func() { lr.runVisitor(ctx, q) })
	}
}

// runVisitor delivers one visitor's messages in stream order until its queue
// is empty or ctx ends. messages left held when ctx ends are naked by
// release, in stream order.
func (lr *leadRun) runVisitor(ctx context.Context, q *visitorQueue) {
	for {
		lr.mu.Lock()
		if len(q.msgs) == 0 {
			delete(lr.visitors, q.key)
			lr.mu.Unlock()
			return
		}
		h := q.msgs[0]
		lr.mu.Unlock()
		if !lr.deliver(ctx, q, h) {
			return
		}
		lr.mu.Lock()
		q.msgs[0] = nil
		q.msgs = q.msgs[1:]
		q.blocked = false
		lr.mu.Unlock()
		lr.done(h)
	}
}

// setBlocked records whether the visitor's oldest message is failing on a
// healthy umami, which caps how many more of its messages are held.
func (lr *leadRun) setBlocked(q *visitorQueue, blocked bool) {
	lr.mu.Lock()
	q.blocked = blocked
	lr.mu.Unlock()
}

// slotsPerTarget shares workers delivery slots over n umami processes as
// evenly as possible, at least one each.
func slotsPerTarget(workers, n, i int) int {
	s := workers / n
	if i < workers%n {
		s++
	}
	return max(s, 1)
}

// acquire takes a delivery slot of an umami process; release gives it back.
func (lr *leadRun) acquire(ctx context.Context, target int) (func(), error) {
	select {
	case lr.slots[target] <- struct{}{}:
		return func() { <-lr.slots[target] }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// healthy reports whether an umami process passes its heartbeat and the
// collection probe, which reaches its database (umami.Client.Healthy). a
// result is reused for healthTTL.
func (lr *leadRun) healthy(ctx context.Context, target int) bool {
	th := &lr.health[target]
	th.mu.Lock()
	defer th.mu.Unlock()
	if time.Since(th.at) < healthTTL {
		return th.ok
	}
	pctx, cancel := context.WithTimeout(ctx, probeEvery)
	err := lr.r.umami[target].Healthy(pctx)
	cancel()
	if ctx.Err() != nil {
		// a shutdown is no verdict on umami
		return false
	}
	th.at, th.ok = time.Now(), err == nil
	if err != nil {
		lr.r.log.Warn("umami health check failed", "umami_process", target, "error", err.Error())
	}
	return th.ok
}

// decode returns the event or the reason it can never be delivered.
func decode(msg jetstream.Msg) (*events.WebEvent, string) {
	ev, err := events.DecodeWebEvent(msg.Data())
	if err != nil {
		return nil, "not a valid web event: " + err.Error()
	}
	if msg.Subject() != ev.Subject() {
		return nil, "subject does not match the event's site id"
	}
	if ev.UA == "" {
		// umami needs the original user agent for its bot check and device
		// detection, and go's default would be classified as a bot
		return nil, "event has no user agent"
	}
	if ev.AcceptedAt < 1000 {
		return nil, "accepted_at is not a unix millisecond time"
	}
	return ev, ""
}

// deadLetterUnkeyed moves a message without a usable visitor key to the dlq
// at once. it has no visitor, so ordering does not apply to it.
func (lr *leadRun) deadLetterUnkeyed(ctx context.Context, msg jetstream.Msg, meta *jetstream.MsgMetadata, reason string) {
	site := siteFromSubject(msg.Subject())
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepingTimeout)
	defer cancel()
	if err := natsx.DeadLetter(bctx, lr.r.js, natsx.StreamWebDLQ, natsx.Subject(natsx.SubjectWebDLQ, dlqToken(site)), msg, reason); err != nil {
		lr.r.log.Error("dead letter failed, retrying later", "stream_seq", meta.Sequence.Stream, "error", err.Error())
		_ = msg.NakWithDelay(natsx.RetryDelay(meta.NumDelivered))
		return
	}
	lr.r.log.Warn("message moved to the dlq", "stream_seq", meta.Sequence.Stream, "reason", reason)
	lr.r.stats.count(site, StatDLQ)
	lr.r.m.outcomes.WithLabelValues(outcomeDLQ).Inc()
	if err := msg.DoubleAck(bctx); err != nil {
		lr.r.m.ackFailures.Inc()
		lr.r.log.Warn("ack not confirmed, message may come back", "stream_seq", meta.Sequence.Stream, "error", err.Error())
	}
}

func siteFromSubject(subject string) string {
	const prefix = natsx.SubjectWebEvents + "."
	if len(subject) > len(prefix) && subject[:len(prefix)] == prefix {
		return subject[len(prefix):]
	}
	return ""
}

// dlqToken is the dlq subject token: the site id, or "unknown" for a
// subject that carries none, so the publish still matches dlq.web.*.
func dlqToken(site string) string {
	if site == "" {
		return "unknown"
	}
	return site
}

// done removes a finished message and wakes the fetch loop.
func (lr *leadRun) done(h *held) {
	lr.mu.Lock()
	delete(lr.held, h.seq)
	lr.mu.Unlock()
	select {
	case lr.freed <- struct{}{}:
	default:
	}
}

// keepalive resets the ack wait of every held message.
func (lr *leadRun) keepalive(ctx context.Context) {
	t := time.NewTicker(keepaliveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		lr.mu.Lock()
		hs := make([]*held, 0, len(lr.held))
		for _, h := range lr.held {
			hs = append(hs, h)
		}
		lr.mu.Unlock()
		failed := 0
		for _, h := range hs {
			if err := h.current().InProgress(); err != nil {
				failed++
			}
		}
		if failed > 0 {
			lr.r.log.Warn("in-progress signals failed, those messages may be redelivered while held", "failed", failed, "held", len(hs))
		}
	}
}

// probe wakes visitors that wait because their umami process was unhealthy
// as soon as that process passes its health check again. each process is
// checked on its own, so one that is back does not wake visitors of one
// still down.
func (lr *leadRun) probe(ctx context.Context) {
	t := time.NewTicker(probeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, target := range lr.gate.unreachableTargets() {
			if lr.healthy(ctx, target) {
				lr.gate.wakeUnreachable(target)
			}
		}
	}
}

// release naks every message still held, in stream order, so the next
// leader gets them back in the order they were published.
func (lr *leadRun) release() {
	lr.mu.Lock()
	hs := make([]*held, 0, len(lr.held))
	for _, h := range lr.held {
		hs = append(hs, h)
	}
	lr.held = map[uint64]*held{}
	lr.mu.Unlock()
	slices.SortFunc(hs, func(a, b *held) int {
		switch {
		case a.seq < b.seq:
			return -1
		case a.seq > b.seq:
			return 1
		}
		return 0
	})
	for _, h := range hs {
		_ = h.current().Nak()
	}
	if len(hs) > 0 {
		lr.r.log.Info("handed back held messages", "count", len(hs))
	}
}

// sleepCtx waits d and reports false when ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
