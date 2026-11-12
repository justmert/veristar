package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/natsx"
)

// outcomes counted per site besides the rejected_<reason> family, whose
// names are the Reason constants in reasons.go.
const (
	StatAccepted    = "accepted"
	StatQuarantined = "quarantined"
)

// StatsInterval is how often counters are published (the design).
const StatsInterval = 10 * time.Second

// statsSeqUnit is the clock unit of the window sequence. it is finer than
// the flush interval on purpose: the shutdown flush can follow a regular
// flush within the same 10 s window, and a node restarted a moment later must
// still never reuse a sequence, or the stream's duplicate window and
// cp.stats_dedup would discard the restarted node's first real window.
const statsSeqUnit = time.Millisecond

// maxStatsEntries bounds one EDGE_STATS message. an entry is at most about
// 220 bytes of json, so 2000 stay well under the 1 MiB default max payload.
// entries beyond it stay in the counters for the next flush.
const maxStatsEntries = 2000

// statsPublishTimeout bounds one publish, so a flush never outlives its tick.
const statsPublishTimeout = 5 * time.Second

// outcomeRe mirrors the outcome rule of events.EdgeStat, so a bad outcome
// fails at the call site instead of poisoning a whole window at flush time.
var outcomeRe = regexp.MustCompile(`^(accepted|quarantined|rejected_[a-z0-9_]{1,40})$`)

// ErrBadOutcome is returned by Count for an outcome EDGE_STATS would refuse.
var ErrBadOutcome = errors.New("gateway: outcome is not accepted, quarantined or rejected_<reason>")

// ErrBadSiteID is returned by Count for a site id that is not a canonical
// uuid. checking it here keeps one bad id from making a whole window
// unencodable.
var ErrBadSiteID = errors.New("gateway: stats site id is not a canonical uuid")

type statKey struct {
	site    string
	outcome string
}

// window is one published or to-be-published EDGE_STATS message.
type window struct {
	seq   uint64
	stats []events.EdgeStat
}

// StatsConfig configures Stats.
type StatsConfig struct {
	// Node is this gateway's node id, a single subject token. it is the
	// subject token on EDGE_STATS and the first half of every message id.
	Node string
	JS   jetstream.JetStream
	Log  *slog.Logger
	// Registerer receives the flush metrics.
	Registerer prometheus.Registerer
	// Now is the clock. nil means time.Now.
	Now func() time.Time
}

// Stats counts request outcomes per site and publishes the deltas to
// EDGE_STATS, where the jobs edgestats task folds them into
// cp.web_ingest_hourly.
//
// callers pass only site ids taken from the registry snapshot: the jobs task
// needs the site's tenant, and counting arbitrary ids from requests would let
// a client grow the counter map without bound. outcomes of requests whose
// site is unknown belong in a prometheus counter, not here.
//
// delivery: a window keeps its message id until the stream acknowledged it,
// and is retried with the same id, so a lost acknowledgement never counts a
// window twice. while a window is pending, new counts keep accumulating and
// form the next window once the pending one is through.
type Stats struct {
	node string
	js   jetstream.JetStream
	log  *slog.Logger
	now  func() time.Time

	mu      sync.Mutex
	counts  map[statKey]int64
	pending *window
	lastSeq uint64

	// flushMu serialises flushes, so the ticker and the shutdown flush never
	// publish two windows at once
	flushMu sync.Mutex

	flushes *prometheus.CounterVec
	unsent  prometheus.GaugeFunc
	counted *prometheus.CounterVec
}

var nodeTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// NewStats returns a Stats that publishes as cfg.Node.
func NewStats(cfg StatsConfig) (*Stats, error) {
	if !nodeTokenRe.MatchString(cfg.Node) {
		return nil, fmt.Errorf("gateway: stats node id %q is not a subject token", cfg.Node)
	}
	if cfg.JS == nil || cfg.Log == nil || cfg.Registerer == nil {
		return nil, errors.New("gateway: StatsConfig needs JS, Log and Registerer")
	}
	s := &Stats{
		node:   cfg.Node,
		js:     cfg.JS,
		log:    cfg.Log,
		now:    cfg.Now,
		counts: map[statKey]int64{},
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.flushes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_stats_flushes_total",
		Help: "EDGE_STATS publishes by result (ok, failed).",
	}, []string{"result"})
	s.counted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_outcomes_total",
		Help: "request outcomes counted for EDGE_STATS, summed over sites.",
	}, []string{"outcome"})
	s.unsent = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "gateway_stats_unsent_entries",
		Help: "site and outcome counters not yet acknowledged by EDGE_STATS.",
	}, func() float64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		n := len(s.counts)
		if s.pending != nil {
			n += len(s.pending.stats)
		}
		return float64(n)
	})
	for _, c := range []prometheus.Collector{s.flushes, s.counted, s.unsent} {
		if err := cfg.Registerer.Register(c); err != nil {
			return nil, fmt.Errorf("gateway: register stats metrics: %w", err)
		}
	}
	return s, nil
}

// Count adds one outcome for a site. outcome is StatAccepted, StatQuarantined
// or the string of a Reason.
func (s *Stats) Count(siteID, outcome string) error {
	if !outcomeRe.MatchString(outcome) {
		return fmt.Errorf("%w: %q", ErrBadOutcome, outcome)
	}
	if u, err := uuid.Parse(siteID); err != nil || u.String() != siteID {
		return ErrBadSiteID
	}
	s.mu.Lock()
	s.counts[statKey{siteID, outcome}]++
	s.mu.Unlock()
	s.counted.WithLabelValues(outcome).Inc()
	return nil
}

// Accepted counts an accepted event.
func (s *Stats) Accepted(siteID string) error { return s.Count(siteID, StatAccepted) }

// Quarantined counts an event accepted into the quarantine band.
func (s *Stats) Quarantined(siteID string) error { return s.Count(siteID, StatQuarantined) }

// Rejected counts a refusal under its reason (reasons.go).
func (s *Stats) Rejected(siteID string, r Reason) error {
	return s.Count(siteID, string(r))
}

// Run flushes every StatsInterval until ctx ends, then flushes once more so
// a clean shutdown loses nothing that nats can still take.
func (s *Stats) Run(ctx context.Context) error {
	t := time.NewTicker(StatsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statsPublishTimeout)
			err := s.Flush(fctx)
			cancel()
			if err != nil {
				s.log.Error("final stats flush failed, unsent counters are lost with this process", "error", err.Error())
			}
			return ctx.Err()
		case <-t.C:
			if err := s.Flush(ctx); err != nil {
				s.log.Warn("stats flush failed, retrying at the next tick", "error", err.Error())
			}
		}
	}
}

// Flush publishes the pending window, if any, and then the counts gathered
// so far as a new window. it returns the first publish error; counts are
// never dropped on failure.
func (s *Stats) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	for {
		w := s.nextWindow()
		if w == nil {
			return nil
		}
		if err := s.publish(ctx, w); err != nil {
			s.flushes.WithLabelValues("failed").Inc()
			return err
		}
		s.flushes.WithLabelValues("ok").Inc()
		s.mu.Lock()
		s.pending = nil
		more := len(s.counts) > 0
		s.mu.Unlock()
		if !more {
			return nil
		}
	}
}

// nextWindow returns the pending window, or moves up to maxStatsEntries
// counters into a new pending window. nil means nothing to send.
func (s *Stats) nextWindow() *window {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		return s.pending
	}
	if len(s.counts) == 0 {
		return nil
	}
	seq := max(events.WindowSeq(s.now(), statsSeqUnit), s.lastSeq+1)
	s.lastSeq = seq
	w := &window{seq: seq}
	for k, n := range s.counts {
		if len(w.stats) == maxStatsEntries {
			break
		}
		w.stats = append(w.stats, events.EdgeStat{Node: s.node, WindowSeq: seq, SiteID: k.site, Outcome: k.outcome, Count: n})
		delete(s.counts, k)
	}
	s.pending = w
	return w
}

func (s *Stats) publish(ctx context.Context, w *window) error {
	subject, msgID, data, err := events.EncodeEdgeStats(w.stats)
	if err != nil {
		// a window the encoder refuses can never be sent; keeping it would
		// block every later window, so it is dropped loudly
		s.mu.Lock()
		s.pending = nil
		s.mu.Unlock()
		s.log.Error("stats window refused by the encoder and dropped", "entries", len(w.stats), "error", err.Error())
		return fmt.Errorf("gateway: encode stats window: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, statsPublishTimeout)
	defer cancel()
	if _, err := natsx.PublishDedup(pctx, s.js, natsx.StreamEdgeStats, subject, msgID, data, nil); err != nil {
		return err
	}
	return nil
}
