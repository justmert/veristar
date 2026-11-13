// Package edgestats is the jobs task that drains EDGE_STATS into
// cp.web_ingest_hourly. each message is applied in one fenced transaction
// that also records its message id in cp.stats_dedup, so a message that is
// delivered again (a crash between commit and ack, or a producer retry after
// the stream's duplicate window) adds nothing the second time.
package edgestats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
)

// TaskName is the name of the task in the scheduler and in `jobs run`.
const TaskName = "edgestats"

// TxFunc runs fn in one fenced jobs transaction (scheduler.Leader.Tx).
type TxFunc func(ctx context.Context, fn func(pgx.Tx) error) error

// Sites is the registry snapshot the task reads tenant ids from. the jobs
// role cannot read cp.sites, and the registry is the same source the
// gateways counted against.
type Sites interface {
	Get(id string) (registry.Site, bool)
	Loaded() bool
}

const (
	// fetchBatch is the most messages fetched at once. each one is its own
	// transaction, so the batch size only saves round trips.
	fetchBatch = 64
	// fetchWait bounds one fetch, so a lost lock or a shutdown is noticed
	// within it.
	fetchWait = 2 * time.Second
	// unknownSiteRetries is how many deliveries a message whose site is not
	// in the snapshot gets before its unattributable counters are dropped.
	// with natsx.RetryBackoff the last try comes about 36 s after the first,
	// far longer than a registry watch lags behind a newly registered site.
	unknownSiteRetries = 4
	// loadPoll is how often Run checks whether the registry snapshot loaded.
	loadPoll = 250 * time.Millisecond
)

// Metrics are the task's prometheus metrics.
type Metrics struct {
	applied      prometheus.Counter
	duplicates   prometheus.Counter
	discarded    prometheus.Counter
	dropped      prometheus.Counter
	unattributed *prometheus.CounterVec
}

// NewMetrics registers the task metrics on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		applied: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_edgestats_applied_total",
			Help: "EDGE_STATS messages added to cp.web_ingest_hourly.",
		}),
		duplicates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_edgestats_duplicates_total",
			Help: "EDGE_STATS messages skipped because cp.stats_dedup already held their id.",
		}),
		discarded: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_edgestats_discarded_total",
			Help: "EDGE_STATS messages acked without applying because they can never be applied (malformed). each is logged.",
		}),
		// unlabelled, so the series exists at 0 from startup and an alert
		// on it needs no outcome label to have appeared first
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_edgestats_dropped_total",
			Help: "site counts dropped because their site was still missing from the registry after every retry (about 36 s). each drop is logged.",
		}),
		unattributed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_edgestats_unattributed_total",
			Help: "counts dropped because their site was not in the registry after every retry, by outcome. each drop is logged.",
		}, []string{"outcome"}),
	}
	for _, c := range []prometheus.Collector{m.applied, m.duplicates, m.discarded, m.dropped, m.unattributed} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("edgestats: register metrics: %w", err)
		}
	}
	return m, nil
}

// Drainer applies EDGE_STATS messages.
type Drainer struct {
	js      jetstream.JetStream
	sites   Sites
	log     *slog.Logger
	metrics *Metrics
}

// New returns a Drainer.
func New(js jetstream.JetStream, sites Sites, log *slog.Logger, m *Metrics) (*Drainer, error) {
	if js == nil || sites == nil || log == nil || m == nil {
		return nil, errors.New("edgestats: needs jetstream, sites, log and metrics")
	}
	return &Drainer{js: js, sites: sites, log: log, metrics: m}, nil
}

func (d *Drainer) consumer(ctx context.Context) (jetstream.Consumer, error) {
	c, err := d.js.Consumer(ctx, natsx.StreamEdgeStats, natsx.ConsumerJobs)
	if err != nil {
		return nil, fmt.Errorf("edgestats: bind consumer %s on %s (run platformctl nats apply): %w", natsx.ConsumerJobs, natsx.StreamEdgeStats, err)
	}
	return c, nil
}

func (d *Drainer) waitLoaded(ctx context.Context) error {
	t := time.NewTicker(loadPoll)
	defer t.Stop()
	for !d.sites.Loaded() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// Run drains continuously until ctx ends. it is the daemon's entry point.
func (d *Drainer) Run(ctx context.Context, tx TxFunc) error {
	if err := d.waitLoaded(ctx); err != nil {
		return err
	}
	cons, err := d.consumer(ctx)
	if err != nil {
		return err
	}
	return natsx.PullLoop(ctx, cons, natsx.PullConfig{
		Batch:      fetchBatch,
		MaxWait:    fetchWait,
		DeadLetter: d.discard,
		Log:        d.log,
	}, func(ctx context.Context, msg jetstream.Msg) error { return d.Handle(ctx, tx, msg) })
}

// Drain applies every queued message and returns how many it took, for
// `jobs run edgestats`. it stops at the first message that fails, leaving it
// queued for a retry, and reports the error.
func (d *Drainer) Drain(ctx context.Context, tx TxFunc) (int, error) {
	if err := d.waitLoaded(ctx); err != nil {
		return 0, err
	}
	cons, err := d.consumer(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for {
		batch, err := cons.Fetch(fetchBatch, jetstream.FetchMaxWait(fetchWait))
		if err != nil {
			return n, fmt.Errorf("edgestats: fetch: %w", err)
		}
		got := 0
		for msg := range batch.Messages() {
			got++
			herr := d.Handle(ctx, tx, msg)
			if natsx.IsPermanent(herr) {
				herr = d.discard(ctx, msg, herr.Error())
			}
			if herr != nil {
				_ = msg.Nak()
				// hand back the rest of the batch at once
				for rest := range batch.Messages() {
					_ = rest.Nak()
				}
				return n, herr
			}
			if err := msg.DoubleAck(ctx); err != nil {
				return n, fmt.Errorf("edgestats: ack: %w", err)
			}
			n++
		}
		if err := batch.Error(); err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
			return n, fmt.Errorf("edgestats: fetch: %w", err)
		}
		if got == 0 {
			return n, nil
		}
	}
}

// discard is the dead-letter path. EDGE_STATS has no dlq stream (plan
// section 4.6), and a message that cannot be decoded never will be, so it is
// acked and logged at error level with its id; the metric makes it visible.
func (d *Drainer) discard(_ context.Context, msg jetstream.Msg, reason string) error {
	d.metrics.discarded.Inc()
	d.log.Error("edge stats message discarded", "subject", msg.Subject(),
		"message_id", msg.Headers().Get(jetstream.MsgIDHeader), "reason", reason)
	return nil
}

type rowKey struct {
	site    uuid.UUID
	outcome string
}

// Handle applies one message in one fenced transaction. it returns an error
// wrapped with natsx.Permanent for a message that can never be applied, and
// a plain error for one that may succeed later.
func (d *Drainer) Handle(ctx context.Context, tx TxFunc, msg jetstream.Msg) error {
	meta, err := msg.Metadata()
	if err != nil {
		return natsx.Permanent(fmt.Errorf("edgestats: metadata: %w", err))
	}
	stats, err := events.DecodeEdgeStats(msg.Data())
	if err != nil {
		return natsx.Permanent(err)
	}
	id := msg.Headers().Get(jetstream.MsgIDHeader)
	if want := events.EdgeStatMsgID(stats[0].Node, stats[0].WindowSeq); id != want {
		// the id is the dedup key; a message whose id does not match its own
		// window could be counted under someone else's id
		return natsx.Permanent(fmt.Errorf("edgestats: message id %q does not match its window %q", id, want))
	}

	// the hour is the stream's receive time: a window is published within
	// one flush interval of its events, so it lands in their hour except
	// across an hour boundary or after a nats outage
	hour := meta.Timestamp.UTC().Truncate(time.Hour)

	sums := map[rowKey]int64{}
	tenants := map[uuid.UUID]uuid.UUID{}
	var missing []events.EdgeStat
	for _, s := range stats {
		site, ok := d.sites.Get(s.SiteID)
		if !ok {
			missing = append(missing, s)
			continue
		}
		siteID, err := uuid.Parse(s.SiteID)
		if err != nil {
			return natsx.Permanent(fmt.Errorf("edgestats: site id: %w", err))
		}
		tenantID, err := uuid.Parse(site.TenantID)
		if err != nil {
			return natsx.Permanent(fmt.Errorf("edgestats: tenant id of site %s: %w", s.SiteID, err))
		}
		tenants[siteID] = tenantID
		k := rowKey{siteID, s.Outcome}
		sums[k] += s.Count
	}
	if len(missing) > 0 && meta.NumDelivered < unknownSiteRetries {
		return fmt.Errorf("edgestats: %d counters name sites missing from the registry snapshot, retrying (delivery %d of %d)",
			len(missing), meta.NumDelivered, unknownSiteRetries)
	}

	p := jobsq.AddIngestCountsParams{Hour: hour}
	for k, n := range sums {
		p.SiteIds = append(p.SiteIds, k.site)
		p.TenantIds = append(p.TenantIds, tenants[k.site])
		p.Outcomes = append(p.Outcomes, k.outcome)
		p.Counts = append(p.Counts, n)
	}
	var duplicate bool
	err = tx(ctx, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		n, err := q.RecordStatsMessage(ctx, id)
		if err != nil {
			return fmt.Errorf("edgestats: record message id: %w", err)
		}
		if n == 0 {
			duplicate = true
			return nil
		}
		if len(p.SiteIds) == 0 {
			return nil
		}
		if err := q.AddIngestCounts(ctx, p); err != nil {
			return fmt.Errorf("edgestats: add counts: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if duplicate {
		d.metrics.duplicates.Inc()
		d.log.Info("edge stats message already applied", "message_id", id)
		return nil
	}
	d.metrics.applied.Inc()
	for _, s := range missing {
		d.metrics.dropped.Add(float64(s.Count))
		d.metrics.unattributed.WithLabelValues(s.Outcome).Add(float64(s.Count))
		d.log.Error("edge stats counter dropped, site not in the registry", "message_id", id,
			"site_id", s.SiteID, "outcome", s.Outcome, "count", s.Count)
	}
	return nil
}
