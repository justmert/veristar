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
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
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
	// bindRetry is how often the locks bucket is bound again while it is
	// missing. the metricswriter user may not recreate it: the
	// replayer and jobs on the same core nodes do.
	bindRetry = time.Second
)

// Config wires a Writer.
type Config struct {
	Settings Settings
	NC       *nats.Conn
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

	mu   sync.Mutex
	lock *natsx.Lock
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
	w := &Writer{
		set: cfg.Settings, nc: cfg.NC, js: js, db: cfg.DB, log: cfg.Log, reg: cfg.Registry,
		now: cfg.Now, holder: cfg.Settings.NodeID + "-" + hex.EncodeToString(b), maxAge: maxAge,
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

// Ops serves /healthz, /readyz (nats connected, database reachable) and
// /metrics.
func (w *Writer) Ops() http.Handler {
	return obs.NewOpsHandler(w.reg, w.log,
		obs.Check{Name: "nats", Fn: func(context.Context) error {
			if s := w.nc.Status(); s != nats.CONNECTED {
				return fmt.Errorf("nats connection is %s", s)
			}
			return nil
		}},
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
	defer func() {
		stopBg()
		wg.Wait()
	}()
	lock, err := w.bindLock(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	el := &natsx.Elector{Lock: lock, RenewEvery: natsx.LockRenewEvery, RetryEvery: acquireRetry, Log: w.log}
	for {
		err := el.Run(ctx, w.lead)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, natsx.ErrFenced) || errors.Is(err, natsx.ErrLockLost) {
			w.log.Warn("stopped consuming, a newer lock holder exists", "error", err.Error())
			continue
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
		// a cluster that is still electing; the elector releases and retries
		w.log.Warn("cannot bind the consumer yet", "error", err.Error())
		sleepCtx(ctx, fetchRetryWait)
		return nil
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
	lock := w.currentLock()
	fence := lock.Fence()
	if fence.Revision == 0 {
		nakAll(msgs, 0)
		return natsx.ErrLockLost
	}
	res, err := w.apply(ctx, fence, msgs)
	switch {
	case err == nil:
	case errors.Is(err, natsx.ErrFenced):
		w.m.batches.WithLabelValues(batchFenced).Inc()
		nakAll(msgs, 0)
		return err
	default:
		w.m.batches.WithLabelValues(batchFailed).Inc()
		if ctx.Err() == nil {
			w.log.Warn("batch not applied, it is redelivered", "messages", len(msgs), "error", err.Error())
		}
		var delivered uint64
		if meta, merr := msgs[0].Metadata(); merr == nil {
			delivered = meta.NumDelivered
		}
		nakAll(msgs, natsx.RetryDelay(delivered))
		// a database that is down fails every batch; pause instead of
		// fetching the next messages at once
		sleepCtx(ctx, fetchRetryWait)
		return nil
	}
	w.m.batches.WithLabelValues(batchCommitted).Inc()
	w.m.batchTime.Observe(time.Since(start).Seconds())
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackTimeout)
	defer cancel()
	for _, m := range msgs {
		if err := m.DoubleAck(actx); err != nil {
			w.m.ackFailures.Inc()
			w.log.Warn("ack not confirmed, the message comes back and is skipped as applied", "error", err.Error())
		}
	}
	res.record(w.m)
	return nil
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
