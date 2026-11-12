// Package natsx holds the nats topology (streams, buckets, consumers, users)
// and the helpers every service uses to talk to jetstream.
package natsx

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// stream names
const (
	StreamWebEvents  = "WEB_EVENTS"
	StreamAPIMetrics = "API_METRICS"
	StreamEdgeStats  = "EDGE_STATS"
	StreamWebDLQ     = "WEB_DLQ"
	StreamAPIDLQ     = "API_DLQ"
)

// subject prefixes. the last token is the site id, tenant id or node id.
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

// Spec is the full topology for one replica count.
type Spec struct {
	Streams   []jetstream.StreamConfig
	Buckets   []jetstream.KeyValueConfig
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
			Subjects:    []string{subject + ".*"},
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
	consumer := func(stream, name, desc string) ConsumerSpec {
		return ConsumerSpec{Stream: stream, Config: jetstream.ConsumerConfig{
			Durable:       name,
			Description:   desc,
			DeliverPolicy: jetstream.DeliverAllPolicy,
			AckPolicy:     jetstream.AckExplicitPolicy,
			AckWait:       AckWait,
			// unlimited, so transient failures retry until the stream's max
			// age. permanent failures are moved to a dlq stream explicitly,
			// because a work-queue stream keeps messages that hit max deliver.
			MaxDeliver: -1,
		}}
	}
	return Spec{
		Streams: []jetstream.StreamConfig{
			workQueue(StreamWebEvents, SubjectWebEvents, "accepted web events, subject token is the site id", 72*time.Hour),
			workQueue(StreamAPIMetrics, SubjectAPIMetrics, "filtered otlp metrics, subject token is the tenant id", 7*24*time.Hour),
			workQueue(StreamEdgeStats, SubjectEdgeStats, "per-site outcome counters, subject token is the node id", 7*24*time.Hour),
			dlq(StreamWebDLQ, SubjectWebDLQ, "web events rejected permanently downstream, subject token is the site id", 7*24*time.Hour),
			dlq(StreamAPIDLQ, SubjectAPIDLQ, "metric batches rejected permanently downstream, subject token is the tenant id", 30*24*time.Hour),
		},
		Buckets: []jetstream.KeyValueConfig{
			{Bucket: BucketRegistrySites, Description: "site id to tenant, status, origins and quarantine website", History: 1, Storage: jetstream.FileStorage, Replicas: replicas},
			{Bucket: BucketRegistryOTLP, Description: "sha-256 of otlp token to tenant, token id and status", History: 1, Storage: jetstream.FileStorage, Replicas: replicas},
			// memory only: the daily secrets must never touch disk
			{Bucket: BucketEdgeSecrets, Description: "daily visitor secret, ip-cluster secret and proof key", History: 1, Storage: jetstream.MemoryStorage, Replicas: replicas, LimitMarkerTTL: edgeSecretsMarkerTTL},
			{Bucket: BucketVisitState, Description: "replayer visit continuity and duplicate guard", History: 1, Storage: jetstream.MemoryStorage, Replicas: replicas, TTL: 24 * time.Hour},
			{Bucket: BucketLocks, Description: "leader locks for singleton workers", History: 1, Storage: jetstream.MemoryStorage, Replicas: replicas, TTL: LocksTTL},
		},
		Consumers: []ConsumerSpec{
			consumer(StreamWebEvents, ConsumerReplayer, "replayer delivers web events to umami"),
			consumer(StreamAPIMetrics, ConsumerMetricsWriter, "metricswriter stores metric batches"),
			consumer(StreamEdgeStats, ConsumerJobs, "jobs edgestats task drains outcome counters"),
		},
	}, nil
}

// User is one service's nats user with its subject allow lists. this stage
// issues the credentials and renders these into the server configuration
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

// Users returns the nats user of every service. only
// gateways subscribe to edge.visitor-token and edge.ipcluster, only the
// control plane sends visitor-token requests, and each service reaches only
// the streams, consumers and buckets it needs.
func Users() []User {
	return []User{
		user(ServiceGateway, true,
			subjects(SubjectIPCluster, SubjectVisitorToken),
			subjects(SubjectWebEvents+".*", SubjectEdgeStats+".*", SubjectIPCluster),
			kvWatch(BucketRegistrySites),
			kvWrite(BucketEdgeSecrets),
		),
		user(ServiceCollector, false, nil,
			subjects(SubjectAPIMetrics+".*"),
			kvWatch(BucketRegistryOTLP),
		),
		user(ServiceReplayer, false, nil,
			consume(StreamWebEvents, ConsumerReplayer),
			subjects(SubjectWebDLQ+".*", SubjectEdgeStats+".*"),
			kvWrite(BucketVisitState),
			kvWrite(BucketLocks),
		),
		user(ServiceMetricsWriter, false, nil,
			consume(StreamAPIMetrics, ConsumerMetricsWriter),
			subjects(SubjectAPIDLQ+".*"),
			kvWrite(BucketLocks),
		),
		user(ServiceJobs, false, nil,
			consume(StreamEdgeStats, ConsumerJobs),
			kvWrite(BucketLocks),
			// reconcile writes the registry, erasure follow-up reads visit
			// state and cleans the web dlq
			kvWrite(BucketRegistrySites),
			kvRead(BucketVisitState),
			dlqManage(StreamWebDLQ),
		),
		user(ServiceControlPlane, false, nil,
			subjects(SubjectVisitorToken),
			kvWrite(BucketRegistrySites),
			kvWrite(BucketRegistryOTLP),
			kvRead(BucketVisitState),
			dlqManage(StreamWebDLQ),
		),
		// platformctl manages the topology and needs the whole api
		user(ServicePlatformctl, false, nil,
			subjects(jsAPI+".>"),
		),
	}
}
