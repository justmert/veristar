// Package natsx holds the nats topology (streams, buckets, consumers, users)
// and the helpers every service uses to talk to jetstream.
package natsx

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/events"
)

// stream names
const (
	StreamWebEvents  = "WEB_EVENTS"
	StreamAPIMetrics = "API_METRICS"
	StreamEdgeStats  = "EDGE_STATS"
	StreamWebDLQ     = "WEB_DLQ"
	StreamAPIDLQ     = "API_DLQ"
)

// subject prefixes. the token after the prefix is the site id, tenant id or
// node id. WEB_EVENTS subjects carry the visitor's partition as one more
// token (events.WebEvent.Subject).
const (
	SubjectWebEvents  = "web.events"
	SubjectAPIMetrics = "api.metrics"
	SubjectEdgeStats  = "stats.web"
	SubjectWebDLQ     = "dlq.web"
	SubjectAPIDLQ     = "dlq.api"
)

// core nats subjects that never touch jetstream
const (
	// SubjectIPCluster carries new distinct visitor tokens between gateways.
	SubjectIPCluster = "edge.ipcluster"
	// SubjectVisitorToken is the request the control plane sends to the
	// gateways for visitor_today erasure.
	SubjectVisitorToken = "edge.visitor-token"
)

// bucket names
const (
	BucketRegistrySites = "registry_sites"
	BucketRegistryOTLP  = "registry_otlp"
	BucketEdgeSecrets   = "edge_secrets"
	BucketVisitState    = "visit_state"
	BucketLocks         = "locks"
)

// durable consumer names. they equal the service that drains the stream, so
// the subject permissions can name the one consumer each service may read.
const (
	ConsumerReplayer      = "replayer"
	ConsumerMetricsWriter = "metricswriter"
	ConsumerJobs          = "jobs"
)

// service names double as nats user names and connection names.
const (
	ServiceGateway       = "gateway"
	ServiceCollector     = "collector"
	ServiceReplayer      = "replayer"
	ServiceMetricsWriter = "metricswriter"
	ServiceJobs          = "jobs"
	ServiceControlPlane  = "controlplane"
	ServicePlatformctl   = "platformctl"
)

// Subject returns prefix.token, the subject for one site, tenant or node.
func Subject(prefix, token string) string { return prefix + "." + token }

// WEB_EVENTS subject filters. the partitioned subjects have four tokens and
// the legacy ones of earlier releases three, so the two never overlap.
const (
	// WebEventsPartitioned matches every partitioned web event subject.
	WebEventsPartitioned = SubjectWebEvents + ".*.*"
	// WebEventsLegacy matches the subjects earlier releases published to,
	// web.events.<site id>. the stream keeps it, and the replayer drains it,
	// until `platformctl nats retire-legacy-web` removes it after an upgrade.
	WebEventsLegacy = SubjectWebEvents + ".*"
)

// WebEventsPartition is the filter of one partition's consumer.
func WebEventsPartition(p int) string {
	return SubjectWebEvents + ".*." + events.PartitionToken(p)
}

// WebEventsSite are the subjects that hold a site's queued events: its
// legacy subject and its partitions.
func WebEventsSite(site string) []string {
	return []string{Subject(SubjectWebEvents, site), Subject(SubjectWebEvents, site) + ".*"}
}

// ConsumerPartition is the durable consumer of one WEB_EVENTS partition.
func ConsumerPartition(p int) string { return ConsumerReplayer + "-p" + events.PartitionToken(p) }

// LockPartition is the lock that makes one replayer the only owner of a
// partition.
func LockPartition(p int) string { return LockReplayer + "-p" + events.PartitionToken(p) }

// ReplayerConsumers lists the partition consumers, then the legacy one.
func ReplayerConsumers() []string {
	out := make([]string, 0, events.Partitions+1)
	for p := range events.Partitions {
		out = append(out, ConsumerPartition(p))
	}
	return append(out, ConsumerReplayer)
}

const (
	// LocksTTL is the bucket ttl of locks. a holder that stops renewing loses
	// the lock at most this long after its last renewal.
	LocksTTL = 15 * time.Second
	// LockRenewEvery is a third of LocksTTL, so two renewals can fail in a
	// row on a slow network before the lock could expire.
	LockRenewEvery = 5 * time.Second

	// dedupWindow is the server default, stated explicitly so the read-back
	// comparison is exact. producers retry a publish within seconds, and
	// EDGE_STATS duplicates outside the window are caught by cp.stats_dedup.
	dedupWindow = 2 * time.Minute

	// edgeSecretsMarkerTTL is how long a ttl removal leaves a delete marker.
	// per-key ttl needs markers enabled. a minute is enough for a watcher to
	// see the removal and short enough that markers do not pile up.
	edgeSecretsMarkerTTL = time.Minute

	// AckWait is how long a consumer may hold a message before redelivery.
	AckWait = 30 * time.Second
)

// RetryBackoff is the delay before a transient failure is retried: 1 s, 5 s,
// 30 s, 2 min, then 10 min for every later attempt until the stream's max
// age. it is applied by PullLoop with a delayed nak, not with the consumer's
// BackOff field: at nats.go v1.55.0 BackOff overrides AckWait and applies only
// to ack timeouts, not to naks, so it would cut the 30 s processing time of
// the first delivery to 1 s.
var RetryBackoff = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// VisitStateMaxBytes caps visit_state, which grows with every delivered
// event (a sent.<event id> key kept 24 hours) and shares each server's memory
// store (edge_nats_max_memory_store, 512 MiB) with edge_secrets. without a
// cap a busy day filled the store, and the gateways could no longer create
// the next day's secrets, which stops all ingestion. with it a full bucket
// refuses replayer writes only; the replayer skips them and keeps delivering,
// ingestion goes on, and docs/runbooks/capacity.md says when to
// raise both numbers.
const VisitStateMaxBytes = 384 << 20

// Spec is the full topology for one replica count.
type Spec struct {
	Streams   []jetstream.StreamConfig
	Buckets   []jetstream.KeyValueConfig
	Consumers []ConsumerSpec
	// Legacy are objects of earlier releases that Apply keeps while they
	// exist and never creates: an upgrade drains them, a new installation
	// never has them.
	Legacy Legacy
}

// Legacy is the part of an earlier release's topology an upgrade drains.
type Legacy struct {
	// Subjects maps a stream to subjects it keeps, ahead of its own, while
	// the live stream still has them.
	Subjects map[string][]string
	// Consumers are reconciled to these settings while they exist. Apply
	// handles them before Spec.Consumers, because a work-queue stream
	// refuses a filtered consumer next to an unfiltered one.
	Consumers []ConsumerSpec
}

// ConsumerSpec is a durable consumer on a stream.
type ConsumerSpec struct {
	Stream string
	Config jetstream.ConsumerConfig
}

// Topology returns every stream, bucket and consumer of section 4.6 with the
// given replica count.
func Topology(replicas int) (Spec, error) {
	if replicas < 1 || replicas > 5 || replicas%2 == 0 {
		return Spec{}, fmt.Errorf("natsx: replica count %d is not 1, 3 or 5", replicas)
	}
	workQueue := func(name, subject, desc string, maxAge time.Duration) jetstream.StreamConfig {
		return jetstream.StreamConfig{
			Name:        name,
			Description: desc,
			Subjects:    []string{subject},
			Retention:   jetstream.WorkQueuePolicy,
			Storage:     jetstream.FileStorage,
			Replicas:    replicas,
			MaxAge:      maxAge,
			Duplicates:  dedupWindow,
			Discard:     jetstream.DiscardOld,
			MaxMsgs:     -1,
			MaxBytes:    -1,
			MaxMsgSize:  -1,
		}
	}
	dlq := func(name, subject, desc string, maxAge time.Duration) jetstream.StreamConfig {
		return jetstream.StreamConfig{
			Name:        name,
			Description: desc,
			Subjects:    []string{subject + ".*"},
			Retention:   jetstream.LimitsPolicy,
			Storage:     jetstream.FileStorage,
			Replicas:    replicas,
			MaxAge:      maxAge,
			Duplicates:  dedupWindow,
			Discard:     jetstream.DiscardOld,
			MaxMsgs:     -1,
			MaxBytes:    -1,
			MaxMsgSize:  -1,
		}
	}
	consumer := func(stream, name, filter, desc string) ConsumerSpec {
		return ConsumerSpec{Stream: stream, Config: jetstream.ConsumerConfig{
			Durable:       name,
			Description:   desc,
			FilterSubject: filter,
			DeliverPolicy: jetstream.DeliverAllPolicy,
			AckPolicy:     jetstream.AckExplicitPolicy,
			AckWait:       AckWait,
			// unlimited, so transient failures retry until the stream's max
			// age. permanent failures are moved to a dlq stream explicitly,
			// because a work-queue stream keeps messages that hit max deliver.
			MaxDeliver: -1,
		}}
	}
	consumers := make([]ConsumerSpec, 0, events.Partitions+2)
	for p := range events.Partitions {
		consumers = append(consumers, consumer(StreamWebEvents, ConsumerPartition(p), WebEventsPartition(p),
			"replayer delivers the web events of partition "+events.PartitionToken(p)+" to umami"))
	}
	consumers = append(consumers,
		consumer(StreamAPIMetrics, ConsumerMetricsWriter, "", "metricswriter stores metric batches"),
		consumer(StreamEdgeStats, ConsumerJobs, "", "jobs edgestats task drains outcome counters"),
	)
	return Spec{
		Streams: []jetstream.StreamConfig{
			workQueue(StreamWebEvents, WebEventsPartitioned, "accepted web events, subject tokens are the site id and the visitor's partition", 72*time.Hour),
			workQueue(StreamAPIMetrics, SubjectAPIMetrics+".*", "filtered otlp metrics, subject token is the tenant id", 7*24*time.Hour),
			workQueue(StreamEdgeStats, SubjectEdgeStats+".*", "per-site outcome counters, subject token is the node id", 7*24*time.Hour),
			dlq(StreamWebDLQ, SubjectWebDLQ, "web events rejected permanently downstream, subject token is the site id", 7*24*time.Hour),
			dlq(StreamAPIDLQ, SubjectAPIDLQ, "metric batches rejected permanently downstream, subject token is the tenant id", 30*24*time.Hour),
		},
		Buckets: []jetstream.KeyValueConfig{
			{Bucket: BucketRegistrySites, Description: "site id to tenant, status, origins and quarantine website", History: 1, Storage: jetstream.FileStorage, Replicas: replicas},
			{Bucket: BucketRegistryOTLP, Description: "sha-256 of otlp token to tenant, token id and status", History: 1, Storage: jetstream.FileStorage, Replicas: replicas},
			// memory only: the daily secrets must never touch disk
			{Bucket: BucketEdgeSecrets, Description: "daily visitor secret, ip-cluster secret and proof key", History: 1, Storage: jetstream.MemoryStorage, Replicas: replicas, LimitMarkerTTL: edgeSecretsMarkerTTL},
			{Bucket: BucketVisitState, Description: "replayer visit continuity and duplicate guard", History: 1, Storage: jetstream.MemoryStorage, Replicas: replicas, TTL: 24 * time.Hour, MaxBytes: VisitStateMaxBytes},
			{Bucket: BucketLocks, Description: "leader locks for singleton workers", History: 1, Storage: jetstream.MemoryStorage, Replicas: replicas, TTL: LocksTTL},
		},
		Consumers: consumers,
		// the single subject and consumer of earlier releases, kept until
		// their last events are delivered. the consumer gets a filter, so it
		// no longer overlaps the partition consumers on the work queue.
		Legacy: Legacy{
			Subjects: map[string][]string{StreamWebEvents: {WebEventsLegacy}},
			Consumers: []ConsumerSpec{
				consumer(StreamWebEvents, ConsumerReplayer, WebEventsLegacy, "replayer drains the web events of releases before the partitions"),
			},
		},
	}, nil
}

// User is one service's nats user with its subject allow lists. the
// deployment issues the credentials and renders these into the server configuration
// with RenderAuthorization.
type User struct {
	Name      string
	Publish   []string
	Subscribe []string
	// AllowResponses lets the user publish to the reply subject of a request
	// it received, without a publish permission for that subject.
	AllowResponses bool
}

// InboxPrefix is the reply subject prefix a service's connection uses. a
// prefix per service lets the subscribe allow list name only that service's
// replies, so no user can read another user's jetstream responses.
func InboxPrefix(service string) string { return "_INBOX_" + service }

const jsAPI = "$JS.API"

// kvRead covers binding a bucket and reading single keys.
func kvRead(bucket string) []string {
	s := "KV_" + bucket
	return []string{
		jsAPI + ".STREAM.INFO." + s,
		jsAPI + ".DIRECT.GET." + s,
		jsAPI + ".DIRECT.GET." + s + ".>",
		jsAPI + ".STREAM.MSG.GET." + s,
	}
}

// kvWatch adds the ordered consumer a key watch creates, and the flow
// control replies of that push consumer.
func kvWatch(bucket string) []string {
	s := "KV_" + bucket
	return append(kvRead(bucket),
		jsAPI+".CONSUMER.CREATE."+s,
		jsAPI+".CONSUMER.CREATE."+s+".>",
		jsAPI+".CONSUMER.DELETE."+s+".>",
		jsAPI+".CONSUMER.INFO."+s+".>",
		"$JS.FC."+s+".>",
	)
}

// kvWrite adds put, create, update and delete on keys.
func kvWrite(bucket string) []string {
	return append(kvWatch(bucket), "$KV."+bucket+".>")
}

// kvRecreate lets a service recreate one of its memory buckets after a
// restart lost it (EnsureBuckets). it names the bucket's own stream only, so
// the service cannot create any other stream; stream info, which the
// existence check needs, is already in kvRead.
func kvRecreate(bucket string) []string {
	return []string{jsAPI + ".STREAM.CREATE.KV_" + bucket}
}

// consume covers binding one durable pull consumer, fetching and acking.
func consume(stream, consumer string) []string {
	return []string{
		jsAPI + ".CONSUMER.INFO." + stream + "." + consumer,
		jsAPI + ".CONSUMER.MSG.NEXT." + stream + "." + consumer,
		"$JS.ACK." + stream + "." + consumer + ".>",
	}
}

// dlqManage covers inspecting a dlq stream and deleting messages from it,
// which erasure needs (purge by site subject, delete single messages after
// scanning with an ordered consumer).
func dlqManage(stream string) []string {
	return []string{
		jsAPI + ".STREAM.INFO." + stream,
		jsAPI + ".STREAM.PURGE." + stream,
		jsAPI + ".STREAM.MSG.GET." + stream,
		jsAPI + ".STREAM.MSG.DELETE." + stream,
		jsAPI + ".CONSUMER.CREATE." + stream,
		jsAPI + ".CONSUMER.CREATE." + stream + ".>",
		jsAPI + ".CONSUMER.MSG.NEXT." + stream + ".>",
		jsAPI + ".CONSUMER.DELETE." + stream + ".>",
		jsAPI + ".CONSUMER.INFO." + stream + ".>",
	}
}

// streamRead covers stream info and reading single messages by sequence or
// by the next sequence on a subject, without a consumer.
func streamRead(stream string) []string {
	return []string{
		jsAPI + ".STREAM.INFO." + stream,
		jsAPI + ".STREAM.MSG.GET." + stream,
	}
}

func user(name string, allowResponses bool, sub []string, pub ...[]string) User {
	// every service may ask for account info, which the jetstream client
	// uses for feature checks, and reads replies only on its own inbox
	p := []string{jsAPI + ".INFO"}
	for _, list := range pub {
		p = append(p, list...)
	}
	return User{
		Name:           name,
		Publish:        p,
		Subscribe:      append([]string{InboxPrefix(name) + ".>"}, sub...),
		AllowResponses: allowResponses,
	}
}

func subjects(s ...string) []string { return s }

// consumeReplayer covers every WEB_EVENTS consumer of the replayer: the
// partitions and the legacy one.
func consumeReplayer() []string {
	var out []string
	for _, c := range ReplayerConsumers() {
		out = append(out, consume(StreamWebEvents, c)...)
	}
	return out
}

// Users returns the nats user of every service. only
// gateways subscribe to edge.visitor-token and edge.ipcluster, only the
// control plane sends visitor-token requests, and each service reaches only
// the streams, consumers and buckets it needs.
func Users() []User {
	return []User{
		// the gateway keeps the legacy web subject while an upgrade runs, so
		// a gateway of the earlier release still publishes on a server that
		// already has the new permissions
		user(ServiceGateway, true,
			subjects(SubjectIPCluster, SubjectVisitorToken),
			subjects(WebEventsPartitioned, WebEventsLegacy, SubjectEdgeStats+".*", SubjectIPCluster),
			kvWatch(BucketRegistrySites),
			kvWrite(BucketEdgeSecrets),
			kvRecreate(BucketEdgeSecrets),
		),
		user(ServiceCollector, false, nil,
			subjects(SubjectAPIMetrics+".*"),
			kvWatch(BucketRegistryOTLP),
		),
		// stream reads let the replayer find the oldest legacy or
		// partitioned event, which keeps a visitor's order while both kinds
		// are queued during an upgrade
		user(ServiceReplayer, false, nil,
			consumeReplayer(),
			streamRead(StreamWebEvents),
			subjects(SubjectWebDLQ+".*", SubjectEdgeStats+".*"),
			kvWrite(BucketVisitState),
			kvWrite(BucketLocks),
			kvRecreate(BucketVisitState),
			kvRecreate(BucketLocks),
		),
		user(ServiceMetricsWriter, false, nil,
			consume(StreamAPIMetrics, ConsumerMetricsWriter),
			subjects(SubjectAPIDLQ+".*"),
			kvWrite(BucketLocks),
			kvRecreate(BucketLocks),
		),
		user(ServiceJobs, false, nil,
			consume(StreamEdgeStats, ConsumerJobs),
			kvWrite(BucketLocks),
			kvRecreate(BucketLocks),
			// reconcile writes both registries
			kvWrite(BucketRegistrySites),
			kvWrite(BucketRegistryOTLP),
		),
		// erasure runs in the control plane: it asks the
		// gateways for a visitor token, waits until the replayer has taken
		// a site's queued events (stream info and per-subject reads of
		// WEB_EVENTS), finds and removes visit records and deletes matching
		// web dlq messages
		user(ServiceControlPlane, false, nil,
			subjects(SubjectVisitorToken),
			kvWrite(BucketRegistrySites),
			kvWrite(BucketRegistryOTLP),
			kvWrite(BucketVisitState),
			streamRead(StreamWebEvents),
			dlqManage(StreamWebDLQ),
		),
		// platformctl manages the topology and needs the whole api. it also
		// publishes to the two work-queue subjects, because `platformctl dlq
		// replay` puts dead-lettered messages back where they came from.
		// `platformctl queue forward` (handover cutover) acks on the three
		// work-queue consumers of the stopped services in the old
		// environment and publishes their messages, edge stats included, in
		// the new one
		user(ServicePlatformctl, false, nil,
			subjects(jsAPI+".>"),
			subjects(WebEventsPartitioned, WebEventsLegacy, SubjectAPIMetrics+".*", SubjectEdgeStats+".*"),
			consumeReplayer(),
			consume(StreamAPIMetrics, ConsumerMetricsWriter),
			consume(StreamEdgeStats, ConsumerJobs),
		),
	}
}
