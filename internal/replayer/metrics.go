package replayer

import (
	"fmt"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/events"
)

// outcome labels of replayer_outcomes_total
const (
	outcomeDelivered = "delivered"
	outcomeBot       = "bot"
	outcomeDLQ       = "dlq"
	outcomeDuplicate = "duplicate_skipped"
)

// result labels of replayer_send_duration_seconds
const (
	sendOK          = "ok"
	sendBot         = "bot"
	sendClientError = "client_error"
	sendServerError = "server_error"
	sendNetwork     = "network"
)

type metrics struct {
	leader              prometheus.Gauge
	owned               prometheus.Gauge
	partitions          prometheus.Gauge
	legacyLeader        prometheus.Gauge
	partitionPending    *prometheus.GaugeVec
	partitionAckPending *prometheus.GaugeVec
	pending             prometheus.Gauge
	ackPending          prometheus.Gauge
	lagSampled          prometheus.Gauge
	send                *prometheus.HistogramVec
	perProcess          *prometheus.CounterVec
	outcomes            *prometheus.CounterVec
	retries             prometheus.Counter
	writeFaultRetries   prometheus.Counter
	visitStateSkipped   *prometheus.CounterVec
	visitStateBytes     prometheus.Gauge
	visitStateMax       prometheus.Gauge
	undecodable         prometheus.Counter
	bookkeeping         prometheus.Counter
	inFlight            prometheus.GaugeFunc
	oldestHeld          prometheus.GaugeFunc
	ackFailures         prometheus.Counter
	lockConfirms        *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer, processes int, inFlight, oldestAge func() float64) (*metrics, error) {
	m := &metrics{
		leader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_leader",
			Help: "1 while this instance owns at least one WEB_EVENTS partition or the legacy subject and consumes, else 0.",
		}),
		owned: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_partitions_owned",
			Help: "WEB_EVENTS partitions this instance owns and consumes. the sum over the instances equals replayer_partitions while every partition has an owner.",
		}),
		partitions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_partitions",
			Help: "the fixed number of WEB_EVENTS partitions.",
		}),
		legacyLeader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_legacy_leader",
			Help: "1 while this instance drains the single web subject of releases before the partitions, else 0.",
		}),
		partitionPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "replayer_partition_pending_messages",
			Help: "consumer lag of one partition this instance owns (partition label 00 to 31, or legacy): messages not yet delivered to its consumer. reported by the owner only.",
		}, []string{"partition"}),
		partitionAckPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "replayer_partition_ack_pending_messages",
			Help: "messages of one partition this instance owns that its consumer delivered and that are not yet acknowledged. reported by the owner only.",
		}, []string{"partition"}),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_consumer_pending_messages",
			Help: "consumer lag: WEB_EVENTS messages not yet delivered to the consumers this instance owns, summed. the sum over the instances is the whole lag.",
		}),
		ackPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_consumer_ack_pending_messages",
			Help: "WEB_EVENTS messages delivered to the consumers this instance owns and not yet acknowledged, summed.",
		}),
		lagSampled: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_consumer_info_timestamp_seconds",
			Help: "unix time of the last round in which every owned consumer's info was read for the lag gauges.",
		}),
		send: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "replayer_send_duration_seconds",
			Help:    "latency of umami /api/send calls by result (ok, bot, client_error, server_error, network).",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15},
		}, []string{"result"}),
		perProcess: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "replayer_umami_sends_total",
			Help: "umami /api/send calls answered with 200, by umami process (its index in UMAMI_URL), so an uneven spread or an idle process shows.",
		}, []string{"process"}),
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "replayer_outcomes_total",
			Help: "final outcome per WEB_EVENTS message (delivered, bot, dlq, duplicate_skipped).",
		}, []string{"outcome"}),
		retries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "replayer_retries_total",
			Help: "transient failures retried in place (umami 5xx, 408, 429, network, nats).",
		}),
		writeFaultRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "replayer_write_fault_retries_total",
			Help: "failed sends retried without counting against the event because umami looked unable to write (failures of several visitors on a process whose health checks passed).",
		}),
		visitStateSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "replayer_visit_state_writes_skipped_total",
			Help: "visit_state writes skipped because the bucket is full, by kind: visit (continuity) or sent (duplicate guard). the events were delivered.",
		}, []string{"kind"}),
		visitStateBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_visit_state_bytes",
			Help: "bytes held by the visit_state bucket, sampled with the lag gauges.",
		}),
		visitStateMax: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "replayer_visit_state_max_bytes",
			Help: "byte limit of the visit_state bucket; writes beyond it are refused and skipped.",
		}),
		undecodable: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "replayer_undecodable_total",
			Help: "messages moved to WEB_DLQ because they are not a valid web event.",
		}),
		bookkeeping: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "replayer_bookkeeping_retries_total",
			Help: "retries of the visit state and duplicate guard writes after umami already stored the event.",
		}),
		ackFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "replayer_ack_failures_total",
			Help: "acks the server did not confirm; the message comes back and the duplicate guard skips it.",
		}),
		lockConfirms: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "replayer_lock_confirms_total",
			Help: "lock confirmations before sends by result (ok, lost).",
		}, []string{"result"}),
		inFlight: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "replayer_in_flight_messages",
			Help: "messages held between fetch and ack by this instance, over every partition it owns.",
		}, inFlight),
		oldestHeld: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "replayer_oldest_pending_age_seconds",
			Help: "age, from its stream publish time, of the oldest message this instance holds unacknowledged, over every partition it owns. 0 when it owns none or holds nothing.",
		}, oldestAge),
	}
	for _, c := range []prometheus.Collector{m.leader, m.owned, m.partitions, m.legacyLeader, m.partitionPending, m.partitionAckPending, m.pending, m.ackPending, m.lagSampled, m.send, m.perProcess, m.outcomes,
		m.retries, m.writeFaultRetries, m.visitStateSkipped, m.visitStateBytes, m.visitStateMax, m.undecodable, m.bookkeeping, m.ackFailures, m.lockConfirms, m.inFlight, m.oldestHeld} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("replayer: register metrics: %w", err)
		}
	}
	m.partitions.Set(float64(events.Partitions))
	// create every labelled series at 0, so alerts and rates work before the
	// first event of each kind
	for _, o := range []string{outcomeDelivered, outcomeBot, outcomeDLQ, outcomeDuplicate} {
		m.outcomes.WithLabelValues(o)
	}
	for _, r := range []string{sendOK, sendBot, sendClientError, sendServerError, sendNetwork} {
		m.send.WithLabelValues(r)
	}
	for i := range processes {
		m.perProcess.WithLabelValues(strconv.Itoa(i))
	}
	m.visitStateSkipped.WithLabelValues("visit")
	m.visitStateSkipped.WithLabelValues("sent")
	m.lockConfirms.WithLabelValues("ok")
	m.lockConfirms.WithLabelValues("lost")
	return m, nil
}
