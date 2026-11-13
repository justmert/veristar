package natsx_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// svcConn is one service's connection with the async errors it received.
type svcConn struct {
	nc   *nats.Conn
	js   jetstream.JetStream
	mu   sync.Mutex
	errs []string
}

func (s *svcConn) violations() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.errs, "\n")
}

// denied waits until the server reported a permissions violation naming
// subject for this connection.
func (s *svcConn) denied(t *testing.T, kind, subject string) {
	t.Helper()
	want := strings.ToLower(fmt.Sprintf("permissions violation for %s to %q", kind, subject))
	require.Eventually(t, func() bool {
		return strings.Contains(strings.ToLower(s.violations()), want)
	}, 5*time.Second, 20*time.Millisecond, "no %q, got: %s", want, s.violations())
}

func randomPassword(t *testing.T) string {
	b := make([]byte, 24)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

func TestPermissions(t *testing.T) {
	users := natsx.Users()
	creds := map[string]string{}
	for _, u := range users {
		creds[u.Name] = randomPassword(t)
	}
	auth, err := natsx.RenderAuthorization(users, creds)
	require.NoError(t, err)
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))

	conn := func(service string) *svcConn {
		t.Helper()
		s := &svcConn{}
		// credentials go through the config loader, as a service reads them
		cfg, err := config.LoadNATS(config.FromMap(map[string]string{
			"NATS_URL": srv.URL, "NATS_REPLICAS": "1",
			"NATS_USER": service, "NATS_PASSWORD": creds[service],
		}))
		require.NoError(t, err)
		opts, err := natsx.Options(cfg, service, quietLog())
		require.NoError(t, err)
		opts = append(opts, nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			s.mu.Lock()
			s.errs = append(s.errs, err.Error())
			s.mu.Unlock()
		}))
		s.nc, err = nats.Connect(strings.Join(cfg.URLs, ","), opts...)
		require.NoError(t, err, service)
		t.Cleanup(s.nc.Close)
		s.js, err = jetstream.New(s.nc)
		require.NoError(t, err)
		return s
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	// a wrong password is refused
	_, err = nats.Connect(srv.URL, nats.UserInfo(natsx.ServiceGateway, "wrong"))
	require.Error(t, err)
	_, err = nats.Connect(srv.URL)
	require.Error(t, err, "anonymous connections must be refused")

	// platformctl can apply the whole topology
	admin := conn(natsx.ServicePlatformctl)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, admin.js, spec)
	require.NoError(t, err)

	gateway := conn(natsx.ServiceGateway)
	collector := conn(natsx.ServiceCollector)
	replayer := conn(natsx.ServiceReplayer)
	metricswriter := conn(natsx.ServiceMetricsWriter)
	jobs := conn(natsx.ServiceJobs)
	controlplane := conn(natsx.ServiceControlPlane)

	t.Run("each service can do its own work", func(t *testing.T) {
		// control plane writes the registries, gateway and collector watch them
		cpSites, err := controlplane.js.KeyValue(ctx, natsx.BucketRegistrySites)
		require.NoError(t, err)
		_, err = cpSites.Put(ctx, "site-1", []byte(`{"status":"active"}`))
		require.NoError(t, err)
		gwSites, err := gateway.js.KeyValue(ctx, natsx.BucketRegistrySites)
		require.NoError(t, err)
		w, err := gwSites.WatchAll(ctx)
		require.NoError(t, err)
		e := <-w.Updates()
		require.NotNil(t, e)
		require.Equal(t, "site-1", e.Key())
		require.NoError(t, w.Stop())

		cpOTLP, err := controlplane.js.KeyValue(ctx, natsx.BucketRegistryOTLP)
		require.NoError(t, err)
		_, err = cpOTLP.Put(ctx, "abc", []byte(`{"status":"active"}`))
		require.NoError(t, err)
		colOTLP, err := collector.js.KeyValue(ctx, natsx.BucketRegistryOTLP)
		require.NoError(t, err)
		got, err := colOTLP.Get(ctx, "abc")
		require.NoError(t, err)
		require.JSONEq(t, `{"status":"active"}`, string(got.Value()))

		// gateway creates the daily secret if absent
		secrets, err := gateway.js.KeyValue(ctx, natsx.BucketEdgeSecrets)
		require.NoError(t, err)
		_, err = secrets.Create(ctx, "visitor.2026-11-12", []byte("x"), jetstream.KeyTTL(time.Hour))
		require.NoError(t, err)

		// producers publish to their streams
		_, err = natsx.PublishDedup(ctx, gateway.js, natsx.StreamWebEvents, natsx.Subject(natsx.SubjectWebEvents, "site-1"), "e1", []byte("e"), nil)
		require.NoError(t, err)
		_, err = natsx.PublishDedup(ctx, gateway.js, natsx.StreamEdgeStats, natsx.Subject(natsx.SubjectEdgeStats, "node-1"), "node-1.1", []byte("s"), nil)
		require.NoError(t, err)
		_, err = natsx.PublishDedup(ctx, collector.js, natsx.StreamAPIMetrics, natsx.Subject(natsx.SubjectAPIMetrics, "tenant-1"), "m1", []byte("m"), nil)
		require.NoError(t, err)

		// consumers drain their own durable consumer and ack
		drain := func(s *svcConn, stream, consumer, want string) {
			c, err := s.js.Consumer(ctx, stream, consumer)
			require.NoError(t, err, consumer)
			b, err := c.Fetch(1, jetstream.FetchMaxWait(3*time.Second))
			require.NoError(t, err, consumer)
			var n int
			for m := range b.Messages() {
				require.Equal(t, want, string(m.Data()))
				require.NoError(t, m.DoubleAck(ctx), consumer)
				n++
			}
			require.NoError(t, b.Error())
			require.Equal(t, 1, n, consumer)
		}
		drain(replayer, natsx.StreamWebEvents, natsx.ConsumerReplayer, "e")
		drain(metricswriter, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter, "m")
		drain(jobs, natsx.StreamEdgeStats, natsx.ConsumerJobs, "s")

		// singleton workers take their locks
		for _, s := range []*svcConn{replayer, metricswriter, jobs} {
			kv, err := s.js.KeyValue(ctx, natsx.BucketLocks)
			require.NoError(t, err)
			l, err := natsx.NewLock(kv, "perm-"+s.nc.Opts.Name, "h")
			require.NoError(t, err)
			_, err = l.Acquire(ctx)
			require.NoError(t, err)
			_, err = l.Renew(ctx)
			require.NoError(t, err)
			_, err = l.Confirm(ctx)
			require.NoError(t, err)
			require.NoError(t, l.Release(ctx))
		}

		// replayer keeps visit state and dead-letters to WEB_DLQ
		vs, err := replayer.js.KeyValue(ctx, natsx.BucketVisitState)
		require.NoError(t, err)
		_, err = vs.Put(ctx, "sent.e1", []byte("1"))
		require.NoError(t, err)
		_, err = natsx.PublishDedup(ctx, replayer.js, natsx.StreamWebDLQ, natsx.Subject(natsx.SubjectWebDLQ, "site-1"), "d1", []byte("d"), nil)
		require.NoError(t, err)

		// control plane reads visit state and purges the dlq for erasure
		cpVS, err := controlplane.js.KeyValue(ctx, natsx.BucketVisitState)
		require.NoError(t, err)
		_, err = cpVS.Get(ctx, "sent.e1")
		require.NoError(t, err)
		dlq, err := controlplane.js.Stream(ctx, natsx.StreamWebDLQ)
		require.NoError(t, err)
		require.NoError(t, dlq.Purge(ctx, jetstream.WithPurgeSubject(natsx.Subject(natsx.SubjectWebDLQ, "site-1"))))

		// gateways gossip on edge.ipcluster
		got2 := make(chan *nats.Msg, 1)
		sub, err := gateway.nc.ChanSubscribe(natsx.SubjectIPCluster, got2)
		require.NoError(t, err)
		require.NoError(t, gateway.nc.Flush())
		require.NoError(t, gateway.nc.Publish(natsx.SubjectIPCluster, []byte("vt")))
		select {
		case <-got2:
		case <-time.After(3 * time.Second):
			t.Fatal("ipcluster gossip not received")
		}
		require.NoError(t, sub.Unsubscribe())

		// the control plane's visitor-token request is answered by a gateway
		vsub, err := gateway.nc.Subscribe(natsx.SubjectVisitorToken, func(m *nats.Msg) {
			_ = m.Respond([]byte("token"))
		})
		require.NoError(t, err)
		require.NoError(t, gateway.nc.Flush())
		resp, err := controlplane.nc.Request(natsx.SubjectVisitorToken, []byte("q"), 3*time.Second)
		require.NoError(t, err)
		require.Equal(t, "token", string(resp.Data))
		require.NoError(t, vsub.Unsubscribe())

		for _, s := range []*svcConn{admin, gateway, collector, replayer, metricswriter, jobs, controlplane} {
			require.Empty(t, s.violations(), "%s hit a permission it needs", s.nc.Opts.Name)
		}
	})

	t.Run("gateway cannot publish api metrics", func(t *testing.T) {
		subject := natsx.Subject(natsx.SubjectAPIMetrics, "tenant-1")
		// the server drops a denied publish, so the ack never comes
		pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
		defer pcancel()
		_, err := natsx.PublishDedup(pctx, gateway.js, natsx.StreamAPIMetrics, subject, "forged", []byte("x"), nil)
		require.Error(t, err)
		gateway.denied(t, "publish", subject)
		s, err := admin.js.Stream(ctx, natsx.StreamAPIMetrics)
		require.NoError(t, err)
		info, err := s.Info(ctx)
		require.NoError(t, err)
		require.Zero(t, info.State.Msgs, "the forged metric was stored")
	})

	t.Run("collector cannot read WEB_EVENTS", func(t *testing.T) {
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
		defer rcancel()
		_, err := collector.js.Consumer(rctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
		require.Error(t, err)
		collector.denied(t, "publish", "$JS.API.CONSUMER.INFO.WEB_EVENTS.replayer")

		_, err = collector.js.OrderedConsumer(rctx, natsx.StreamWebEvents, jetstream.OrderedConsumerConfig{})
		require.Error(t, err)

		rctx2, rcancel2 := context.WithTimeout(ctx, 2*time.Second)
		defer rcancel2()
		_, err = collector.js.Stream(rctx2, natsx.StreamWebEvents)
		require.Error(t, err)
		collector.denied(t, "publish", "$JS.API.STREAM.INFO.WEB_EVENTS")

		_, err = collector.nc.Subscribe(natsx.SubjectWebEvents+".>", func(*nats.Msg) {})
		require.NoError(t, err)
		require.NoError(t, collector.nc.Flush())
		collector.denied(t, "subscription", natsx.SubjectWebEvents+".>")
	})

	t.Run("only the control plane sends visitor-token requests", func(t *testing.T) {
		answered := make(chan struct{}, 10)
		vsub, err := gateway.nc.Subscribe(natsx.SubjectVisitorToken, func(m *nats.Msg) {
			answered <- struct{}{}
			_ = m.Respond([]byte("token"))
		})
		require.NoError(t, err)
		require.NoError(t, gateway.nc.Flush())
		defer func() { _ = vsub.Unsubscribe() }()
		for _, s := range []*svcConn{gateway, collector, replayer, metricswriter, jobs} {
			_, err := s.nc.Request(natsx.SubjectVisitorToken, []byte("q"), time.Second)
			require.Error(t, err, s.nc.Opts.Name)
			s.denied(t, "publish", natsx.SubjectVisitorToken)
		}
		require.Empty(t, answered, "a request from another service reached a gateway")
	})

	t.Run("only gateways subscribe to edge subjects", func(t *testing.T) {
		for _, s := range []*svcConn{controlplane, replayer} {
			for _, subj := range []string{natsx.SubjectVisitorToken, natsx.SubjectIPCluster} {
				_, err := s.nc.Subscribe(subj, func(*nats.Msg) {})
				require.NoError(t, err)
				require.NoError(t, s.nc.Flush())
				s.denied(t, "subscription", subj)
			}
		}
	})

	// these delete buckets, so they run last
	t.Run("services recreate only their own memory buckets", func(t *testing.T) {
		for _, b := range []string{natsx.BucketEdgeSecrets, natsx.BucketVisitState, natsx.BucketLocks} {
			require.NoError(t, admin.js.DeleteKeyValue(ctx, b))
		}
		// denied: a jetstream api request that the server drops never gets
		// an answer, so each attempt ends with its own short timeout
		refused := func(s *svcConn, stream string, try func(context.Context) error) {
			t.Helper()
			dctx, dcancel := context.WithTimeout(ctx, 2*time.Second)
			defer dcancel()
			require.Error(t, try(dctx), "%s created %s", s.nc.Opts.Name, stream)
			s.denied(t, "publish", "$JS.API.STREAM.CREATE."+stream)
		}
		// the create itself, with the topology config; EnsureBuckets would
		// stop earlier at the stream info a service may lack
		ensure := func(s *svcConn, name string) func(context.Context) error {
			return func(c context.Context) error {
				cfg, err := natsx.MemoryBucket(1, name)
				require.NoError(t, err)
				_, err = s.js.CreateKeyValue(c, cfg)
				return err
			}
		}
		createStream := func(s *svcConn, name string) func(context.Context) error {
			return func(c context.Context) error {
				_, err := s.js.CreateStream(c, jetstream.StreamConfig{Name: name, Subjects: []string{"evil." + name}, Storage: jetstream.MemoryStorage})
				return err
			}
		}

		// each service may not create a bucket of another service, any
		// other stream, or a file bucket it writes to
		refused(gateway, "KV_"+natsx.BucketLocks, ensure(gateway, natsx.BucketLocks))
		refused(gateway, "KV_"+natsx.BucketVisitState, ensure(gateway, natsx.BucketVisitState))
		refused(gateway, "EVIL", createStream(gateway, "EVIL"))
		refused(gateway, natsx.StreamWebEvents, createStream(gateway, natsx.StreamWebEvents))
		refused(jobs, "KV_"+natsx.BucketVisitState, ensure(jobs, natsx.BucketVisitState))
		refused(jobs, "KV_"+natsx.BucketEdgeSecrets, ensure(jobs, natsx.BucketEdgeSecrets))
		refused(jobs, "EVIL", createStream(jobs, "EVIL"))
		refused(jobs, "KV_"+natsx.BucketRegistrySites, func(c context.Context) error {
			_, err := jobs.js.CreateKeyValue(c, jetstream.KeyValueConfig{Bucket: natsx.BucketRegistrySites})
			return err
		})
		refused(replayer, "KV_"+natsx.BucketEdgeSecrets, ensure(replayer, natsx.BucketEdgeSecrets))
		refused(replayer, "EVIL", createStream(replayer, "EVIL"))
		refused(metricswriter, "KV_"+natsx.BucketVisitState, ensure(metricswriter, natsx.BucketVisitState))
		refused(metricswriter, "KV_"+natsx.BucketEdgeSecrets, ensure(metricswriter, natsx.BucketEdgeSecrets))
		refused(metricswriter, "EVIL", createStream(metricswriter, "EVIL"))
		for _, s := range []*svcConn{collector, controlplane} {
			refused(s, "KV_"+natsx.BucketLocks, ensure(s, natsx.BucketLocks))
			refused(s, "EVIL", createStream(s, "EVIL"))
		}
		// a file bucket is never recreated by a service, whatever its rights
		_, err := natsx.EnsureBuckets(ctx, admin.js, 1, natsx.BucketRegistrySites)
		require.ErrorContains(t, err, "file-backed")

		// each service recreates its own buckets with exactly the topology
		// config; a second service finding one already there is a no-op
		seen := map[*svcConn]string{}
		for _, s := range []*svcConn{gateway, replayer, jobs, metricswriter} {
			seen[s] = s.violations()
		}
		created, err := natsx.EnsureBuckets(ctx, gateway.js, 1, natsx.BucketEdgeSecrets)
		require.NoError(t, err)
		require.Equal(t, []string{natsx.BucketEdgeSecrets}, created)
		// the metricswriter recreates locks like jobs and the replayer; the
		// next one finds it there
		created, err = natsx.EnsureBuckets(ctx, metricswriter.js, 1, natsx.BucketLocks)
		require.NoError(t, err)
		require.Equal(t, []string{natsx.BucketLocks}, created)
		created, err = natsx.EnsureBuckets(ctx, jobs.js, 1, natsx.BucketLocks)
		require.NoError(t, err)
		require.Empty(t, created)
		created, err = natsx.EnsureBuckets(ctx, replayer.js, 1, natsx.BucketVisitState, natsx.BucketLocks)
		require.NoError(t, err)
		require.Equal(t, []string{natsx.BucketVisitState}, created)
		changes, err := natsx.Diff(ctx, admin.js, spec)
		require.NoError(t, err)
		for _, c := range changes {
			require.Equal(t, natsx.ActionUnchanged, c.Action, c.String())
		}
		for s, before := range seen {
			require.Equal(t, before, s.violations(), "%s was refused part of recreating its own buckets", s.nc.Opts.Name)
		}
		_, err = gateway.js.KeyValue(ctx, natsx.BucketEdgeSecrets)
		require.NoError(t, err)
	})
}

func TestRenderAuthorizationRequiresEveryCredential(t *testing.T) {
	users := natsx.Users()
	creds := map[string]string{}
	for _, u := range users[1:] {
		creds[u.Name] = "x"
	}
	_, err := natsx.RenderAuthorization(users, creds)
	require.ErrorContains(t, err, users[0].Name)
	creds[users[0].Name] = "x"
	creds["stranger"] = "x"
	_, err = natsx.RenderAuthorization(users, creds)
	require.ErrorContains(t, err, "stranger")
}
