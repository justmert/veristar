package metricswriter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
)

const (
	// fetchWait bounds one fetch, so a lost lock or a shutdown is noticed
	// within it even when the stream is idle.
	fetchWait = time.Second
	// fetchRetryWait is the pause after a failed fetch or batch.
	fetchRetryWait = time.Second
	// acquireRetry is how often a standby tries to take the lock.
	acquireRetry = time.Second
	// lagEvery is how often the consumer lag gauges are refreshed.
	lagEvery = 5 * time.Second
	// ackTimeout bounds the acks after a commit. it is detached from the
	// lead context, because a commit that happened must be acked even when
	// the lock is lost right after it; ingest_dedup covers a lost ack.
	ackTimeout = 10 * time.Second
	// confirmed acks in flight at once after a committed batch
	ackConcurrency = 64
	// bindRetry is how often binding the locks bucket is tried again while
	// it is missing; the bucket keeper recreates it meanwhile.
	bindRetry = time.Second
)

// errNotReady ends a hold because jetstream could not serve the consumer
// yet, as while a cluster recovers from a restart. Run contends again
// instead of stopping the process.
var errNotReady = errors.New("metricswriter: jetstream not ready")

// Config wires a Writer.
type Config struct {
	Settings Settings
	NC       *nats.Conn
	// Replicas is NATS_REPLICAS, the replica count the locks bucket is
	// recreated with when a restart lost it.
	Replicas int
	DB       *pdb.DB
	Log      *slog.Logger
	Registry *prometheus.Registry
	// Now is the clock for the point age checks; time.Now when nil.
	Now func() time.Time
}

// Writer is one metricswriter instance.
type Writer struct {
	set    Settings
	nc     *nats.Conn
	js     jetstream.JetStream
	db     *pdb.DB
	log    *slog.Logger
	reg    *prometheus.Registry
	m      *metrics
	now    func() time.Time
	holder string
	maxAge time.Duration
	// buckets keeps the memory-backed locks bucket in existence: a full nats
	// restart loses it, and the writer recovers without an operator
	buckets *natsx.BucketKeeper

	mu   sync.Mutex
	lock *natsx.Lock
	// lastCommit is the unix nanoseconds of the last committed batch
	lastCommit atomic.Int64
}

// New checks the configuration and registers the metrics. it neither takes
// the lock nor touches the database.
func New(cfg Config) (*Writer, error) {
	if cfg.NC == nil || cfg.DB == nil || cfg.Log == nil || cfg.Registry == nil {
		return nil, errors.New("metricswriter: Config needs NC, DB, Log and Registry")
	}
	if !cfg.Settings.valid() {
		return nil, errors.New("metricswriter: settings are invalid, load them with LoadSettings")
	}
	if _, err := cfg.DB.Pool(pdb.RoleMetricsWriter); err != nil {
		return nil, err
	}
	js, err := jetstream.New(cfg.NC)
	if err != nil {
		return nil, fmt.Errorf("metricswriter: jetstream: %w", err)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("metricswriter: holder id: %w", err)
	}
	maxAge, err := apiMetricsMaxAge()
	if err != nil {
		return nil, err
	}
	buckets, err := natsx.NewBucketKeeper(js, cfg.Replicas, cfg.Log, natsx.BucketLocks)
	if err != nil {
		return nil, err
	}
	w := &Writer{
		set: cfg.Settings, nc: cfg.NC, js: js, db: cfg.DB, log: cfg.Log, reg: cfg.Registry,
		now: cfg.Now, holder: cfg.Settings.NodeID + "-" + hex.EncodeToString(b), maxAge: maxAge,
		buckets: buckets,
	}
	if w.now == nil {
		w.now = time.Now
	}
	w.m, err = newMetrics(cfg.Registry)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// apiMetricsMaxAge reads the stream's max age from the declared topology:
// a point older than that cannot be a message the stream still holds.
func apiMetricsMaxAge() (time.Duration, error) {
	spec, err := natsx.Topology(1)
	if err != nil {
		return 0, err
	}
	for _, s := range spec.Streams {
		if s.Name == natsx.StreamAPIMetrics && s.MaxAge > 0 {
			return s.MaxAge, nil
		}
	}
	return 0, errors.New("metricswriter: topology declares no max age for API_METRICS")
}

// Ops serves /healthz, /readyz (nats connected, locks bucket not known
// missing, database reachable) and /metrics.
func (w *Writer) Ops() http.Handler {
	return obs.NewOpsHandler(w.reg, w.log,
		obs.Check{Name: "nats", Fn: func(context.Context) error {
			if s := w.nc.Status(); s != nats.CONNECTED {
				return fmt.Errorf("nats connection is %s", s)
			}
			return nil
		}},
		obs.Check{Name: "buckets", Fn: w.buckets.Ready},
		obs.Check{Name: "database", Fn: func(ctx context.Context) error {
			p, err := w.db.Pool(pdb.RoleMetricsWriter)
			if err != nil {
				return err
			}
			return p.Ping(ctx)
		}})
}

// bindLock binds the locks bucket and creates the lock, retrying while the
// bucket is missing after a nats restart.
func (w *Writer) bindLock(ctx context.Context) (*natsx.Lock, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lock != nil {
		return w.lock, nil
	}
	for {
		kv, err := w.js.KeyValue(ctx, natsx.BucketLocks)
		if err == nil {
			l, err := natsx.NewLock(kv, natsx.LockMetricsWriter, w.holder)
			if err != nil {
				return nil, err
			}
			w.lock = l
			return l, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		w.buckets.Kick(err)
		w.log.Warn("cannot bind the locks bucket, retrying", "error", err.Error())
		if !sleepCtx(ctx, bindRetry) {
			return nil, ctx.Err()
		}
	}
}

// Run contends for the lock and consumes while it holds it, until ctx ends.
func (w *Writer) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	bg, stopBg := context.WithCancel(context.WithoutCancel(ctx))
	wg.Go(func() { w.sampleLag(bg) })
	wg.Go(func() { w.buckets.Run(bg) })
	defer func() {
		stopBg()
		wg.Wait()
	}()
	// a bucket the restart lost is recreated before the first bind; a
	// failure here is retried by the keeper and the bind loop below
	if err := w.buckets.Ensure(ctx); err != nil && ctx.Err() == nil {
		w.log.Warn("locks bucket check failed, retrying", "error", err.Error())
	}
	lock, err := w.bindLock(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	el := &natsx.Elector{Lock: lock, RenewEvery: natsx.LockRenewEvery, RetryEvery: acquireRetry, Log: w.log, Buckets: w.buckets}
	for {
		err := el.Run(ctx, w.lead)
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case errors.Is(err, natsx.ErrFenced) || errors.Is(err, natsx.ErrLockLost):
			w.log.Warn("stopped consuming, the lock is lost or a newer holder exists", "error", err.Error())
			continue
		case errors.Is(err, errNotReady):
			w.log.Warn("stopped consuming, jetstream not ready", "error", err.Error())
			if !sleepCtx(ctx, acquireRetry) {
				return nil
			}
			continue
		case err == nil:
			// lead returns nil only when ctx ended
			return nil
		}
		return err
	}
}

// sampleLag refreshes the lag gauges from the consumer info.
func (w *Writer) sampleLag(ctx context.Context) {
	t := time.NewTicker(lagEvery)
	defer t.Stop()
	var cons jetstream.Consumer
	for {
		if cons == nil {
			c, err := w.js.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
			if err == nil {
				cons = c
			} else if ctx.Err() == nil {
				w.log.Warn("lag sampler cannot bind the consumer", "error", err.Error())
			}
		}
		if cons != nil {
			ictx, cancel := context.WithTimeout(ctx, lagEvery)
			info, err := cons.Info(ictx)
			cancel()
			if err == nil {
				w.m.pending.Set(float64(info.NumPending))
				w.m.ackPending.Set(float64(info.NumAckPending))
				w.m.lagSampled.Set(float64(time.Now().Unix()))
			} else if ctx.Err() == nil {
				w.log.Warn("consumer info failed, lag gauges are stale", "error", err.Error())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// lead consumes while the lock is held: fetch a batch, apply it in one
// fenced transaction, ack it. it returns nil when ctx ends (the elector
// cancels it when a renewal fails) and natsx.ErrFenced when a newer holder
// has written.
func (w *Writer) lead(ctx context.Context) error {
	w.m.leader.Set(1)
	defer w.m.leader.Set(0)
	cons, err := w.js.Consumer(ctx, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter)
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return nil
	case errors.Is(err, jetstream.ErrConsumerNotFound) || errors.Is(err, jetstream.ErrStreamNotFound):
		return fmt.Errorf("metricswriter: bind consumer %s on %s (run platformctl nats apply): %w", natsx.ConsumerMetricsWriter, natsx.StreamAPIMetrics, err)
	default:
		// a cluster that is still electing; the lock is released and Run
		// contends again
		return fmt.Errorf("%w: bind consumer: %w", errNotReady, err)
	}
	for ctx.Err() == nil {
		batch, err := cons.Fetch(w.set.Batch, jetstream.FetchMaxWait(fetchWait))
		if err != nil {
			if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) {
				return fmt.Errorf("metricswriter: consumer is gone, run platformctl nats apply: %w", err)
			}
			w.log.Warn("nats fetch failed", "error", err.Error())
			sleepCtx(ctx, fetchRetryWait)
			continue
		}
		var msgs []jetstream.Msg
		for m := range batch.Messages() {
			msgs = append(msgs, m)
		}
		if err := batch.Error(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, jetstream.ErrNoMessages) {
			w.log.Warn("nats fetch ended with an error", "error", err.Error())
			if len(msgs) == 0 {
				// a fetch that fails at once (no responders while the
				// stream has no leader) would otherwise spin
				sleepCtx(ctx, fetchRetryWait)
				continue
			}
		}
		if len(msgs) == 0 {
			continue
		}
		if err := w.process(ctx, msgs); err != nil {
			return err
		}
	}
	return nil
}

// process applies one fetched batch and acks or naks every message of it.
func (w *Writer) process(ctx context.Context, msgs []jetstream.Msg) error {
	start := time.Now()
	fence := w.currentLock().Fence()
	if fence.Revision == 0 {
		nakAll(msgs, 0)
		return natsx.ErrLockLost
	}
	res, err := w.apply(ctx, fence, msgs)
	switch {
	case err == nil:
		w.committed(ctx, msgs, res, start)
		return nil
	case errors.Is(err, natsx.ErrFenced):
		w.m.batches.WithLabelValues(batchFenced).Inc()
		nakAll(msgs, 0)
		return err
	}
	w.m.batches.WithLabelValues(batchFailed).Inc()
	if ctx.Err() == nil {
		w.log.Warn("batch not applied", "messages", len(msgs), "error", err.Error())
	}
	if refusedBySQL(err) {
		// the database is up and refused something in the batch. a retry
		// of the same batch would fail the same way and take every other
		// tenant's messages in it down again, so each message is applied
		// on its own: the good ones commit, the one that fails is retried
		// or dead-lettered alone
		return w.isolate(ctx, msgs)
	}
	nakAll(msgs, natsx.RetryDelay(delivered(msgs[0])))
	// a database that is down fails every batch; pause instead of
	// fetching the next messages at once
	sleepCtx(ctx, fetchRetryWait)
	return nil
}

// maxDeliveries bounds how often a message the database refuses for a
// reason that is not a data error is tried before it is dead-lettered. with
// the retry backoff (1 s, 5 s, 30 s, 2 min) that is about 2.5 minutes.
const maxDeliveries = 5

// poisonWindow is how recent another message's commit must be for a
// message that keeps failing alone to count as the culprit rather than the
// database.
const poisonWindow = 10 * time.Minute

// isolate applies the messages of a failed batch one transaction each, in
// queue order. a message the database refuses with a data error (sqlstate
// classes 22 and 23) is dead-lettered at once: it fails the same way every
// time. any other refusal is retried with the usual backoff, and after
// maxDeliveries deliveries it is dead-lettered too, provided other messages
// committed recently, so a poison message never stalls the stream while a
// database fault that fails everything is never mistaken for one.
func (w *Writer) isolate(ctx context.Context, msgs []jetstream.Msg) error {
	for i, m := range msgs {
		start := time.Now()
		fence := w.currentLock().Fence()
		if fence.Revision == 0 {
			nakAll(msgs[i:], 0)
			return natsx.ErrLockLost
		}
		one := []jetstream.Msg{m}
		res, err := w.apply(ctx, fence, one)
		switch {
		case err == nil:
			w.committed(ctx, one, res, start)
			continue
		case errors.Is(err, natsx.ErrFenced):
			w.m.batches.WithLabelValues(batchFenced).Inc()
			nakAll(msgs[i:], 0)
			return err
		case !refusedBySQL(err):
			// the database went away meanwhile: everything left waits
			nakAll(msgs[i:], natsx.RetryDelay(delivered(m)))
			sleepCtx(ctx, fetchRetryWait)
			return nil
		}
		w.m.batches.WithLabelValues(batchFailed).Inc()
		n := delivered(m)
		data := dataError(err)
		if !data && (n < maxDeliveries || !w.committedSince(time.Now().Add(-poisonWindow))) {
			w.log.Warn("message refused by the database, it is redelivered alone", "deliveries", n, "error", err.Error())
			_ = m.NakWithDelay(natsx.RetryDelay(n))
			continue
		}
		reason := fmt.Sprintf("refused by the database after %d deliveries: %v", n, err)
		if data {
			reason = fmt.Sprintf("refused by the database (data error): %v", err)
		}
		if err := w.deadLetter(ctx, m, reason); err != nil {
			w.log.Warn("dead letter failed, the message is redelivered", "error", err.Error())
			_ = m.NakWithDelay(natsx.RetryDelay(n))
			continue
		}
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackTimeout)
		if err := m.DoubleAck(actx); err != nil {
			w.m.ackFailures.Inc()
			w.log.Warn("ack of a dead-lettered message not confirmed, it comes back", "error", err.Error())
		}
		cancel()
		w.m.messages.WithLabelValues(msgDLQ).Inc()
	}
	return nil
}

// committed records a committed batch and acks its messages.
func (w *Writer) committed(ctx context.Context, msgs []jetstream.Msg, res *result, start time.Time) {
	w.lastCommit.Store(time.Now().UnixNano())
	w.m.batches.WithLabelValues(batchCommitted).Inc()
	w.m.batchTime.Observe(time.Since(start).Seconds())
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackTimeout)
	defer cancel()
	// the batch is committed, so the acks only tell the server what is done.
	// each confirmed ack is one round trip to the consumer's leader, which
	// can sit in another location (one after the other, this stage measured
	// about 150 messages a second, so about 6.7 ms each), so they go out
	// concurrently. an ack that fails brings its message back, and it is
	// skipped as already applied.
	var wg sync.WaitGroup
	sem := make(chan struct{}, ackConcurrency)
	for _, m := range msgs {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			if err := m.DoubleAck(actx); err != nil {
				w.m.ackFailures.Inc()
				w.log.Warn("ack not confirmed, the message comes back and is skipped as applied", "error", err.Error())
			}
		})
	}
	wg.Wait()
	res.record(w.m)
}

// committedSince reports whether a batch committed after t.
func (w *Writer) committedSince(t time.Time) bool {
	return w.lastCommit.Load() > t.UnixNano()
}

func delivered(m jetstream.Msg) uint64 {
	if meta, err := m.Metadata(); err == nil {
		return meta.NumDelivered
	}
	return 0
}

// refusedBySQL reports whether the database answered with an error that a
// retry of the same transaction would get again: an sql error outside the
// classes that describe the connection, a conflict with another
// transaction, missing resources or the server itself (08, 40, 53, 55, 57,
// 58, XX). those are left to the plain retry of the whole batch.
func refusedBySQL(err error) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || len(pe.Code) != 5 {
		return false
	}
	switch pe.Code[:2] {
	case "08", "40", "53", "55", "57", "58", "XX":
		return false
	}
	return true
}

// dataError reports a data exception (22, for example invalid utf-8) or an
// integrity violation (23, for example a check constraint): properties of
// the values the message carries.
func dataError(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (strings.HasPrefix(pe.Code, "22") || strings.HasPrefix(pe.Code, "23"))
}

func (w *Writer) currentLock() *natsx.Lock {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lock
}

func nakAll(msgs []jetstream.Msg, delay time.Duration) {
	for _, m := range msgs {
		if delay > 0 {
			_ = m.NakWithDelay(delay)
		} else {
			_ = m.Nak()
		}
	}
}

// tenantOf returns the tenant id of an API_METRICS subject.
func tenantOf(subject string) (uuid.UUID, error) {
	tok, ok := strings.CutPrefix(subject, natsx.SubjectAPIMetrics+".")
	if !ok || strings.Contains(tok, ".") {
		return uuid.Nil, fmt.Errorf("subject %q is not %s.<tenant>", subject, natsx.SubjectAPIMetrics)
	}
	id, err := uuid.Parse(tok)
	if err != nil {
		return uuid.Nil, fmt.Errorf("subject %q: tenant: %w", subject, err)
	}
	return id, nil
}

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
