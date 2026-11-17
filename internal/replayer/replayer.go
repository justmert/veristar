// Package replayer delivers accepted web events from WEB_EVENTS to umami's
// /api/send.
//
// WEB_EVENTS is split into events.Partitions partitions by a hash of site id
// and visitor token, so all of a visitor's events are in one partition. each
// partition has its own durable consumer and its own lock. one instance runs
// on each core node, and the instances share the partitions: each holds the
// locks of the partitions assigned to it (partitions.go) and delivers their
// events to its own node's umami processes. a partition has at most one
// owner at a time, and a new owner first waits for its predecessor's
// unacknowledged messages, so a visitor's events reach umami in stream order
// across owner changes. within a partition each visitor (site id and visitor
// token) with held messages has its own queue, delivered one message at a
// time in stream order. a failing event is retried in place and holds back
// only that visitor's later events, never another visitor or site.
// REPLAYER_WORKERS bounds the sends in flight at once on the node.
//
// sends are spread over several umami processes by a hash of site id and
// visitor token, so a visitor's events always go to one process.
// the processes of every node share one database and one APP_SECRET, and
// umami derives the session id, the visit id and the cache token from those
// and the event alone (docs/reference/umami-contract.md item 14), so which
// process and which node stores an event does not change what is stored.
//
// delivery is at least once with a narrow duplicate window: the replayer checks
// sent.<event id> in visit_state before sending and writes it after umami's
// 200, then acks. a crash, kill or lock loss between umami's 200 and that
// write stores the event twice; at most one event per delivery slot can be in that
// window at any moment. a partition that moves to another node on purpose
// (a rebalance or a shutdown) finishes the sends in progress first.
//
// umami's database cannot be fenced, so a partition's lock is confirmed with
// the server before every fetch and, at most a second apart, before sends. a
// former owner that was paused and resumes after losing the lock can still
// complete the sends it had already passed that check for: at most one per
// delivery slot. a new owner first waits until the previous holder's
// unacknowledged messages are redeliverable (at most natsx.AckWait), so they
// reach umami before anything newer of the same visitor.
//
// upgrade: releases before the partitions published every event of a site
// on one subject, consumed by the single "replayer" consumer under the
// "replayer" lock. an instance also holds that lock while the consumer exists
// and drains it, so a replayer of the earlier release and this one never
// consume it at once. while legacy and partitioned events are queued
// together, an event is delivered only when no event of the other kind with
// a lower stream sequence is left (barrier.go), which keeps every visitor's
// order across the two.
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
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/umami"
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
	// a paused former owner can still send to one in-flight send per delivery slot.
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
	// dlq instead of filling the partition's share of MaxInFlight, which
	// would stop the fetch for every visitor of the partition. no browser
	// sends that many events within the ~36 s a failing event is retried
	// (the gateway's replay limit is 50 events per proof and node).
	blockedBacklog = 64
	// minPartitionInFlight is the least a partition may hold, so one
	// blocked visitor (blockedBacklog) never takes all of it.
	minPartitionInFlight = 2 * blockedBacklog
	// bookkeepingTimeout bounds the visit state write, the duplicate guard
	// write, the dlq publish and the ack after umami answered. it is detached
	// from the hold so a lock loss right after a 200 still records the
	// send, which keeps the duplicate window as narrow as possible.
	bookkeepingTimeout = 10 * time.Second
	// lagEvery is how often the consumer lag gauges are refreshed.
	lagEvery = 5 * time.Second
	// predecessorPoll is how often a new owner checks whether the previous
	// holder's unacknowledged messages are back.
	predecessorPoll = 500 * time.Millisecond
	// acquireRetry is how often an instance tries to take a lock it wants.
	acquireRetry = time.Second
	// yieldTimeout bounds how long a partition that is handed over on
	// purpose delivers what it holds before it stops and hands the rest
	// back. the core role gives the container 30 s to stop.
	yieldTimeout = 10 * time.Second
)

// errNotReady ends a hold because jetstream could not serve the consumer
// yet, as while a cluster recovers from a restart. the owner contends again
// instead of stopping the process.
var errNotReady = errors.New("replayer: jetstream not ready")

// errYield ends a hold on purpose: the partition is handed to another
// instance or the process shuts down. what the hold fetched is delivered
// first, so the next owner finds nothing unacknowledged and starts at once.
var errYield = errors.New("replayer: partition handed over")

// legacyLead is the leads key of the legacy consumer's hold.
const legacyLead = -1

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
	locksKV jetstream.KeyValue
	holder  string
	buckets *natsx.BucketKeeper
	stats   *stats
	m       *metrics
	maxAge  time.Duration
	pool    *pool
	members *membership
	// legacyBar clears partitioned messages against the legacy subject,
	// partBar legacy messages against the partitions
	legacyBar *seqBarrier
	partBar   *seqBarrier

	mu    sync.Mutex
	leads map[int]*leadRun
}

// pool is what the holds of one instance share: the delivery slots and the
// health and write record of each umami process, and the visitors waiting
// for one of them.
type pool struct {
	// slots holds one semaphore per umami process; together they allow
	// REPLAYER_WORKERS sends at once
	slots []chan struct{}
	// health caches one health check per umami process
	health []targetHealth
	// writes is the recent send record per umami process, which shows a
	// write fault the health check cannot see (writefault.go)
	writes []writeWatch
	gate   *gate
}

// New recreates the memory buckets when a restart lost them, binds them and
// registers the metrics. it takes no lock.
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
	maxAge, err := webEventsMaxAge()
	if err != nil {
		return nil, err
	}
	n := len(cfg.Umami)
	p := &pool{slots: make([]chan struct{}, n), health: make([]targetHealth, n), writes: make([]writeWatch, n), gate: &gate{}}
	for i := range p.slots {
		// the workers are shared evenly; LoadSettings keeps the process
		// count at most the worker count, so every process gets a slot
		p.slots[i] = make(chan struct{}, slotsPerTarget(cfg.Settings.Workers, n, i))
	}
	r := &Replayer{
		set:     cfg.Settings,
		nc:      cfg.NC,
		js:      js,
		umami:   cfg.Umami,
		log:     cfg.Log,
		reg:     cfg.Registry,
		store:   visitStore{kv: visitKV},
		locksKV: locksKV,
		holder:  holder,
		buckets: buckets,
		maxAge:  maxAge,
		pool:    p,
		leads:   map[int]*leadRun{},
	}
	r.members = newMembership(locksKV, cfg.Settings.NodeID, holder, cfg.Log)
	r.legacyBar = newSeqBarrier(js, natsx.WebEventsLegacy, true)
	r.partBar = newSeqBarrier(js, natsx.WebEventsPartitioned, false)
	r.m, err = newMetrics(cfg.Registry, n, r.inFlight, r.oldestAge)
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

// Run takes part in the partition assignment and consumes the partitions it
// owns, and the legacy subject while it holds that lock, until ctx ends. on
// the way out every hold hands back what it has not delivered and releases
// its lock, so the other instances take over at once. the stats flusher, the
// lag sampler and the health probe run for the whole process. an error means
// the topology is missing something only an operator can fix.
func (r *Replayer) Run(ctx context.Context) error {
	var bgWG sync.WaitGroup
	bg, stopBg := context.WithCancel(context.WithoutCancel(ctx))
	bgWG.Go(func() { r.stats.run(bg) })
	bgWG.Go(func() { r.sampleLag(bg) })
	bgWG.Go(func() { r.buckets.Run(bg) })
	bgWG.Go(func() { r.probe(bg) })
	defer func() {
		stopBg()
		bgWG.Wait()
	}()

	run, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	var wg sync.WaitGroup
	wg.Go(func() { r.members.run(run, r.buckets) })
	for p := range events.Partitions {
		wg.Go(func() {
			if err := r.own(run, p); err != nil {
				stop(err)
			}
		})
	}
	wg.Go(func() {
		if err := r.own(run, legacyLead); err != nil {
			stop(err)
		}
	})
	wg.Wait()
	// the member key goes last, after every partition was handed back, so
	// the others do not count this instance while it still delivers
	r.members.leave()
	if ctx.Err() != nil {
		return nil
	}
	return context.Cause(run)
}

func (r *Replayer) activeLeads() []*leadRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*leadRun, 0, len(r.leads))
	for _, lr := range r.leads {
		out = append(out, lr)
	}
	return out
}

func (r *Replayer) inFlight() float64 {
	n := 0
	for _, lr := range r.activeLeads() {
		n += lr.count()
	}
	return float64(n)
}

func (r *Replayer) oldestAge() float64 {
	var t time.Time
	for _, lr := range r.activeLeads() {
		if o := lr.oldest(); !o.IsZero() && (t.IsZero() || o.Before(t)) {
			t = o
		}
	}
	if t.IsZero() {
		return 0
	}
	return time.Since(t).Seconds()
}

// partitionCap is how many messages one hold may have between fetch and
// ack: an even share of REPLAYER_MAX_IN_FLIGHT over the holds of this
// instance, at least minPartitionInFlight, at most the whole bound (which
// stays under the server's max ack pending of a consumer).
func (r *Replayer) partitionCap() int {
	r.mu.Lock()
	n := max(len(r.leads), 1)
	r.mu.Unlock()
	share := (r.set.MaxInFlight + n - 1) / n
	return min(max(share, minPartitionInFlight), r.set.MaxInFlight)
}

// sampleLag refreshes the lag gauges from the info of the consumers this
// instance holds. a partition that has no owner here reports nothing here,
// so summing the gauges over the instances counts each consumer once.
func (r *Replayer) sampleLag(ctx context.Context) {
	t := time.NewTicker(lagEvery)
	defer t.Stop()
	for {
		var pending, ackPending uint64
		ok := true
		for _, lr := range r.activeLeads() {
			ictx, cancel := context.WithTimeout(ctx, lagEvery)
			info, err := lr.cons.Info(ictx)
			cancel()
			if err != nil {
				ok = false
				if ctx.Err() == nil {
					r.log.Warn("consumer info failed, lag gauges are stale", "consumer", lr.name, "error", err.Error())
				}
				continue
			}
			pending += info.NumPending
			ackPending += uint64(info.NumAckPending) //nolint:gosec // a count
			r.m.partitionPending.WithLabelValues(lr.label).Set(float64(info.NumPending))
			r.m.partitionAckPending.WithLabelValues(lr.label).Set(float64(info.NumAckPending))
		}
		if ok {
			r.m.pending.Set(float64(pending))
			r.m.ackPending.Set(float64(ackPending))
			r.m.lagSampled.Set(float64(time.Now().Unix()))
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

// lead consumes one partition, or the legacy subject, while its lock is
// held. it returns nil when ctx ends, errYield after a hand-over, an error
// wrapping natsx.ErrLockLost when a confirmation failed, errNotReady when
// jetstream could not serve the consumer, and errLegacyGone when the legacy
// consumer no longer exists.
func (r *Replayer) lead(parent context.Context, part int, lock *natsx.Lock, yield <-chan struct{}) error {
	name, bar, label := natsx.ConsumerReplayer, r.partBar, "legacy"
	if part != legacyLead {
		name, bar, label = natsx.ConsumerPartition(part), r.legacyBar, events.PartitionToken(part)
	}
	cons, err := r.js.Consumer(parent, natsx.StreamWebEvents, name)
	switch {
	case err == nil:
	case parent.Err() != nil:
		return nil
	case part == legacyLead && errors.Is(err, jetstream.ErrConsumerNotFound):
		return errLegacyGone
	case errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound):
		// both are file-backed and survive restarts; missing means the
		// topology was never applied, which only an operator can fix
		return fmt.Errorf("replayer: bind consumer %s on %s (run platformctl nats apply): %w", name, natsx.StreamWebEvents, err)
	default:
		// a timeout or a leaderless consumer while a cluster recovers from a
		// restart: give the lock back and contend again
		return fmt.Errorf("%w: bind consumer %s on %s: %w", errNotReady, name, natsx.StreamWebEvents, err)
	}
	if err := r.awaitPredecessor(parent, yield, cons, name); err != nil {
		return nil //nolint:nilerr // only a cancelled context or a hand-over ends the wait, both a normal end of the hold
	}
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	lr := &leadRun{
		r:        r,
		part:     part,
		name:     name,
		label:    label,
		cons:     cons,
		cancel:   cancel,
		guard:    &lockGuard{lock: lock, m: r.m},
		bar:      bar,
		yield:    yield,
		held:     map[uint64]*held{},
		visitors: map[string]*visitorQueue{},
		freed:    make(chan struct{}, 1),
	}
	r.mu.Lock()
	r.leads[part] = lr
	r.mu.Unlock()
	r.setOwned()
	defer func() {
		r.mu.Lock()
		delete(r.leads, part)
		r.mu.Unlock()
		r.setOwned()
		r.m.partitionPending.DeleteLabelValues(label)
		r.m.partitionAckPending.DeleteLabelValues(label)
	}()
	r.log.Info("consuming", "consumer", name, "workers", r.set.Workers, "max_in_flight", r.set.MaxInFlight, "umami_processes", len(r.umami))
	return lr.run(ctx, parent)
}

// setOwned refreshes the gauges of what this instance holds.
func (r *Replayer) setOwned() {
	r.mu.Lock()
	parts, legacy := 0, false
	for k := range r.leads {
		if k == legacyLead {
			legacy = true
		} else {
			parts++
		}
	}
	r.mu.Unlock()
	r.m.owned.Set(float64(parts))
	r.m.legacyLeader.Set(b2f(legacy))
	r.m.leader.Set(b2f(parts > 0 || legacy))
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// awaitPredecessor waits until the previous holder's unacknowledged messages
// can be redelivered before the first fetch. without it the new owner would
// fetch newer messages first and a visitor's held-back events would reach
// umami after later ones. a clean predecessor naks everything on the way out,
// so the wait ends at once; a killed one leaves messages pending until their
// ack wait runs out, at most natsx.AckWait after this point because the
// predecessor stopped refreshing them before its lock expired.
func (r *Replayer) awaitPredecessor(ctx context.Context, yield <-chan struct{}, cons jetstream.Consumer, name string) error {
	deadline := time.Now().Add(natsx.AckWait)
	logged := false
	for {
		info, err := cons.Info(ctx)
		if err == nil && info.NumAckPending == 0 {
			return nil
		}
		if err != nil && ctx.Err() == nil {
			r.log.Warn("consumer info failed while waiting for the previous holder", "consumer", name, "error", err.Error())
		}
		if err == nil && !logged {
			r.log.Info("waiting for the previous holder's messages to become redeliverable", "consumer", name, "ack_pending", info.NumAckPending)
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
		case <-yield:
			t.Stop()
			return errYield
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

// leadRun is the state of one hold of a partition's lock, or of the legacy
// lock.
type leadRun struct {
	r *Replayer
	// part is the partition, or legacyLead
	part int
	// name is the consumer, label the partition token or "legacy"
	name, label string
	cons        jetstream.Consumer
	cancel      context.CancelCauseFunc
	guard       *lockGuard
	// bar clears this hold's messages against the other kind of subject
	bar *seqBarrier
	// yield is closed when the hold is to end after the sends in progress
	yield <-chan struct{}
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

// yielding reports whether the hold is being handed over.
func (lr *leadRun) yielding() bool {
	select {
	case <-lr.yield:
		return true
	default:
		return false
	}
}

func (lr *leadRun) run(ctx, parent context.Context) error {
	var wg sync.WaitGroup
	wg.Go(func() { lr.keepalive(ctx) })
	err := lr.fetchLoop(ctx)
	if errors.Is(err, errYield) {
		// a hand-over: nothing more is fetched and the visitors deliver what
		// they hold, so every message is acked when the lock goes and the
		// next owner's wait for its predecessor ends at once. a nak would
		// not do: the server counts a nacked message as unacknowledged
		// until it is delivered again.
		done := make(chan struct{})
		go func() {
			lr.visitorsWG.Wait()
			close(done)
		}()
		t := time.NewTimer(yieldTimeout)
		select {
		case <-done:
		case <-t.C:
			lr.r.log.Warn("messages still held at the end of the hand-over, handing them back", "consumer", lr.name)
		case <-ctx.Done():
		}
		t.Stop()
	}
	lr.cancel(err)
	wg.Wait()
	// the fetch loop has returned, so no visitor goroutine starts any more
	lr.visitorsWG.Wait()
	lr.release()
	if cause := context.Cause(ctx); errors.Is(cause, natsx.ErrLockLost) {
		return cause
	}
	if parent.Err() != nil {
		return nil
	}
	return err
}

// fetchLoop confirms the hold, fetches up to the hold's free share and hands
// each message to its visitor's queue, until ctx ends, the hold yields or a
// confirmation fails.
func (lr *leadRun) fetchLoop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if lr.yielding() {
			return errYield
		}
		free := lr.r.partitionCap() - lr.count()
		if free <= 0 {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-lr.yield:
				return errYield
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
				return lr.consumerGone(err)
			}
			lr.r.log.Warn("nats fetch failed", "consumer", lr.name, "error", err.Error())
			t := time.NewTimer(fetchRetryWait)
			select {
			case <-ctx.Done():
				t.Stop()
			case <-t.C:
			}
			continue
		}
		// once a message waits for the barrier and the hold ends, the rest
		// of the batch is only held, so release hands it back in order
		parked := false
		for msg := range batch.Messages() {
			h := lr.accept(ctx, msg)
			if h == nil {
				continue
			}
			if !parked && !lr.bar.wait(ctx, lr.yield, h.seq) {
				parked = true
			}
			if !parked {
				lr.enqueue(ctx, h)
			}
		}
		if err := batch.Error(); err != nil && !errors.Is(err, context.Canceled) {
			if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) {
				return lr.consumerGone(err)
			}
			lr.r.log.Warn("nats fetch ended with an error", "consumer", lr.name, "error", err.Error())
		}
	}
}

// consumerGone is the end of a hold whose consumer was deleted: the normal
// end of the legacy subject after retire-legacy-web, a missing topology for
// a partition.
func (lr *leadRun) consumerGone(err error) error {
	if lr.part == legacyLead {
		return errLegacyGone
	}
	return fmt.Errorf("replayer: consumer %s is gone, run platformctl nats apply: %w", lr.name, err)
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

// accept takes a fetched message into the hold. it returns nil for a
// message that is handled already: a second delivery of a held message, or
// one that cannot be decoded and went to the dlq.
func (lr *leadRun) accept(ctx context.Context, msg jetstream.Msg) *held {
	meta, err := msg.Metadata()
	if err != nil {
		lr.r.log.Error("message without jetstream metadata", "subject", msg.Subject(), "error", err.Error())
		return nil
	}
	lr.bar.fetched(meta.Sequence.Stream)
	lr.mu.Lock()
	if h, ok := lr.held[meta.Sequence.Stream]; ok {
		lr.mu.Unlock()
		h.replace(msg)
		return nil
	}
	lr.mu.Unlock()

	ev, reason := decode(msg, lr.part == legacyLead)
	if reason != "" {
		lr.r.m.undecodable.Inc()
		lr.deadLetterUnkeyed(ctx, msg, meta, reason)
		return nil
	}
	h := &held{seq: meta.Sequence.Stream, published: meta.Timestamp, ev: ev, msg: msg,
		target: targetOf(ev.SiteID, ev.VT, len(lr.r.umami)), key: ev.SiteID + "\x00" + ev.VT}
	lr.mu.Lock()
	lr.held[h.seq] = h
	lr.mu.Unlock()
	return h
}

// enqueue hands a held message to its visitor's queue, or to the dlq when
// the visitor is blocked with blockedBacklog messages already.
func (lr *leadRun) enqueue(ctx context.Context, h *held) {
	lr.mu.Lock()
	q := lr.visitors[h.key]
	if q != nil && q.blocked && len(q.msgs) >= blockedBacklog {
		lr.mu.Unlock()
		reason := fmt.Sprintf("visitor already has %d events held behind one that fails on a healthy umami", blockedBacklog)
		if lr.deadLetter(ctx, h, reason) {
			lr.done(h)
			return
		}
		// the dlq publish failed: queue it after all, which is what happened
		// before the cap existed
		lr.mu.Lock()
		q = lr.visitors[h.key]
	}
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
// is empty or ctx ends, also during a hand-over. messages left held are
// naked by release, in stream order.
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
			lr.mu.Lock()
			delete(lr.visitors, q.key)
			lr.mu.Unlock()
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
	slots := lr.r.pool.slots[target]
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// healthy reports whether an umami process passes its heartbeat and the
// collection probe, which reaches its database (umami.Client.Healthy). a
// result is reused for healthTTL.
func (r *Replayer) healthy(ctx context.Context, target int) bool {
	th := &r.pool.health[target]
	th.mu.Lock()
	defer th.mu.Unlock()
	if time.Since(th.at) < healthTTL {
		return th.ok
	}
	pctx, cancel := context.WithTimeout(ctx, probeEvery)
	err := r.umami[target].Healthy(pctx)
	cancel()
	if ctx.Err() != nil {
		// a shutdown is no verdict on umami
		return false
	}
	th.at, th.ok = time.Now(), err == nil
	if err != nil {
		r.log.Warn("umami health check failed", "umami_process", target, "error", err.Error())
	}
	return th.ok
}

// decode returns the event or the reason it can never be delivered. a
// partitioned message must be on its event's partition subject, a legacy one
// on its site's legacy subject.
func decode(msg jetstream.Msg, legacy bool) (*events.WebEvent, string) {
	ev, err := events.DecodeWebEvent(msg.Data())
	if err != nil {
		return nil, "not a valid web event: " + err.Error()
	}
	want := ev.Subject()
	if legacy {
		want = ev.LegacySubject()
	}
	if msg.Subject() != want {
		return nil, "subject does not match the event's site id and partition"
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

// siteFromSubject is the site token of web.events.<site> and
// web.events.<site>.<partition>.
func siteFromSubject(subject string) string {
	const prefix = natsx.SubjectWebEvents + "."
	if len(subject) <= len(prefix) || subject[:len(prefix)] != prefix {
		return ""
	}
	rest := subject[len(prefix):]
	for i := range len(rest) {
		if rest[i] == '.' {
			return rest[:i]
		}
	}
	return rest
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
			lr.r.log.Warn("in-progress signals failed, those messages may be redelivered while held", "consumer", lr.name, "failed", failed, "held", len(hs))
		}
	}
}

// probe wakes visitors that wait because their umami process was unhealthy
// as soon as that process passes its health check again. each process is
// checked on its own, so one that is back does not wake visitors of one
// still down.
func (r *Replayer) probe(ctx context.Context) {
	t := time.NewTicker(probeEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, target := range r.pool.gate.unreachableTargets() {
			if r.healthy(ctx, target) {
				r.pool.gate.wakeUnreachable(target)
			}
		}
	}
}

// release naks every message still held, in stream order, so the next
// owner gets them back in the order they were published.
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
		lr.r.log.Info("handed back held messages", "consumer", lr.name, "count", len(hs))
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
