package metricswriter

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type metrics struct {
	leader      prometheus.Gauge
	pending     prometheus.Gauge
	ackPending  prometheus.Gauge
	lagSampled  prometheus.Gauge
	batches     *prometheus.CounterVec
	batchTime   prometheus.Histogram
	messages    *prometheus.CounterVec
	points      *prometheus.CounterVec
	requests    *prometheus.CounterVec
	rows        prometheus.Counter
	noPartition prometheus.Counter
	overflow    prometheus.Counter
	ackFailures prometheus.Counter
}

// results of metricswriter_batches_total
const (
	batchCommitted = "committed"
	batchFailed    = "failed"
	batchFenced    = "fenced"
)

// outcomes of metricswriter_messages_total
const (
	msgApplied   = "applied"
	msgDuplicate = "duplicate"
	msgDLQ       = "dlq"
	msgRefused   = "refused"
)

// pointRejected is the outcome label of a point the engine refused.
const pointRejected = "rejected"

func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		leader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "metricswriter_leader",
			Help: "1 while this instance holds the metricswriter lock and consumes, else 0.",
		}),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "metricswriter_consumer_pending_messages",
			Help: "consumer lag: API_METRICS messages not yet delivered to the metricswriter consumer.",
		}),
		ackPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "metricswriter_consumer_ack_pending_messages",
			Help: "API_METRICS messages delivered to the metricswriter consumer and not yet acknowledged.",
		}),
		lagSampled: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "metricswriter_consumer_info_timestamp_seconds",
			Help: "unix time of the last successful consumer info read behind the lag gauges.",
		}),
		batches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "metricswriter_batches_total",
			Help: "batch transactions by result (committed, failed, fenced).",
		}, []string{"result"}),
		batchTime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "metricswriter_batch_duration_seconds",
			Help:    "time from fetch to commit of one batch.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
		messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "metricswriter_messages_total",
			Help: "API_METRICS messages by outcome (applied, duplicate, dlq, refused: tenant removed or token revoked, acked without storing).",
		}, []string{"outcome"}),
		points: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "metricswriter_points_total",
			Help: "histogram points by what they contributed (delta, new, baseline, increment, reset, stale, old, rejected).",
		}, []string{"outcome"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "metricswriter_requests_total",
			Help: "requests added to minute rows, by source (metric, span).",
		}, []string{"source"}),
		rows: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "metricswriter_minute_upserts_total",
			Help: "minute rows inserted or added to.",
		}),
		noPartition: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "metricswriter_rows_without_partition_total",
			Help: "minute rows not stored because no partition covers their minute; their requests are lost and logged.",
		}),
		overflow: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "metricswriter_cardinality_overflow_points_total",
			Help: "points stored under {other} because a tenant's daily route or service cap was reached.",
		}),
		ackFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "metricswriter_ack_failures_total",
			Help: "acks the server did not confirm after a commit; the message comes back and ingest_dedup skips it.",
		}),
	}
	for _, c := range []prometheus.Collector{m.leader, m.pending, m.ackPending, m.lagSampled, m.batches, m.batchTime,
		m.messages, m.points, m.requests, m.rows, m.noPartition, m.overflow, m.ackFailures} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("metricswriter: register metrics: %w", err)
		}
	}
	// every labelled series exists from the start, so rates and alerts work
	// before the first event of each kind
	for _, r := range []string{batchCommitted, batchFailed, batchFenced} {
		m.batches.WithLabelValues(r)
	}
	for _, o := range []string{msgApplied, msgDuplicate, msgDLQ, msgRefused} {
		m.messages.WithLabelValues(o)
	}
	for _, o := range Outcomes() {
		m.points.WithLabelValues(o.String())
	}
	m.points.WithLabelValues(pointRejected)
	m.requests.WithLabelValues(SourceMetric)
	m.requests.WithLabelValues(SourceSpan)
	return m, nil
}
