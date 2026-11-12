package replayer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/natsx"
)

// outcomes the replayer reports on EDGE_STATS. gateway.Stats only accepts
// the gateway's own outcomes, so the replayer publishes the shared
// events.EdgeStat contract itself.
const (
	StatBot = "bot"
	StatDLQ = "dlq"
)

const (
	// statsInterval matches the gateway's flush interval.
	statsInterval = 10 * time.Second
	// statsSeqUnit is finer than the interval so a shutdown flush right after
	// a regular one, or a quick restart, never reuses a window sequence.
	statsSeqUnit = time.Millisecond
	// maxStatsEntries keeps one message well under the 1 MiB payload limit.
	maxStatsEntries = 2000
	// statsPublishTimeout bounds one publish.
	statsPublishTimeout = 5 * time.Second
)

// StatsNode returns the EDGE_STATS node token of a replayer instance. the
// prefix keeps it apart from gateway node ids, because the message id is
// node plus window sequence.
func StatsNode(nodeID string) string { return "replayer-" + nodeID }

type statKey struct{ site, outcome string }

// stats counts bot and dlq outcomes per site and publishes the deltas to
// EDGE_STATS. a window keeps its message id until the stream acknowledged
// it, so a retried window is stored once.
type stats struct {
	node string
	js   jetstream.JetStream
	log  *slog.Logger

	mu      sync.Mutex
	counts  map[statKey]int64
	pending []events.EdgeStat
	lastSeq uint64

	flushMu sync.Mutex
	flushes *prometheus.CounterVec
}

func newStats(node string, js jetstream.JetStream, log *slog.Logger, reg prometheus.Registerer) (*stats, error) {
	s := &stats{node: node, js: js, log: log, counts: map[statKey]int64{}}
	s.flushes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "replayer_stats_flushes_total",
		Help: "EDGE_STATS publishes by result (ok, failed).",
	}, []string{"result"})
	if err := reg.Register(s.flushes); err != nil {
		return nil, fmt.Errorf("replayer: register stats metrics: %w", err)
	}
	return s, nil
}

// count adds one outcome for a site. ids that are not canonical uuids are
// refused here, so one bad id cannot make a whole window unencodable.
func (s *stats) count(siteID, outcome string) {
	if u, err := uuid.Parse(siteID); err != nil || u.String() != siteID {
		return
	}
	s.mu.Lock()
	s.counts[statKey{siteID, outcome}]++
	s.mu.Unlock()
}

// run flushes every statsInterval until ctx ends, then once more.
func (s *stats) run(ctx context.Context) {
	t := time.NewTicker(statsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), statsPublishTimeout)
			if err := s.flush(fctx); err != nil {
				s.log.Error("final stats flush failed, unsent counters are lost with this process", "error", err.Error())
			}
			cancel()
			return
		case <-t.C:
			if err := s.flush(ctx); err != nil {
				s.log.Warn("stats flush failed, retrying at the next tick", "error", err.Error())
			}
		}
	}
}

func (s *stats) flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	for {
		w := s.next()
		if w == nil {
			return nil
		}
		subject, msgID, data, err := events.EncodeEdgeStats(w)
		if err != nil {
			// a window the encoder refuses can never be sent; keeping it would
			// block every later window
			s.mu.Lock()
			s.pending = nil
			s.mu.Unlock()
			s.flushes.WithLabelValues("failed").Inc()
			return fmt.Errorf("replayer: encode stats window: %w", err)
		}
		pctx, cancel := context.WithTimeout(ctx, statsPublishTimeout)
		_, err = natsx.PublishDedup(pctx, s.js, natsx.StreamEdgeStats, subject, msgID, data, nil)
		cancel()
		if err != nil {
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

func (s *stats) next() []events.EdgeStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		return s.pending
	}
	if len(s.counts) == 0 {
		return nil
	}
	seq := max(events.WindowSeq(time.Now(), statsSeqUnit), s.lastSeq+1)
	s.lastSeq = seq
	var w []events.EdgeStat
	for k, n := range s.counts {
		if len(w) == maxStatsEntries {
			break
		}
		w = append(w, events.EdgeStat{Node: s.node, WindowSeq: seq, SiteID: k.site, Outcome: k.outcome, Count: n})
		delete(s.counts, k)
	}
	s.pending = w
	return w
}
