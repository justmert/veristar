package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// visTestClock is a clock the test moves by hand. every gateway instance in
// a test reads the same one, as nodes with synchronised clocks would.
type visTestClock struct{ ns atomic.Int64 }

func newVisTestClock(t time.Time) *visTestClock {
	c := &visTestClock{}
	c.Set(t)
	return c
}

func (c *visTestClock) Now() time.Time      { return time.Unix(0, c.ns.Load()).UTC() }
func (c *visTestClock) Set(t time.Time)     { c.ns.Store(t.UnixNano()) }
func (c *visTestClock) Add(d time.Duration) { c.ns.Add(int64(d)) }

func visTestConnect(t *testing.T, url string) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	nc, err := nats.Connect(url, nats.MaxReconnects(-1))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	return nc, js
}

func visTestSecrets(t *testing.T, js jetstream.JetStream) jetstream.KeyValue {
	t.Helper()
	kv, err := js.KeyValue(t.Context(), natsx.BucketEdgeSecrets)
	require.NoError(t, err)
	return kv
}

func visTestQuiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestUntilMidnight(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339Nano, s)
		require.NoError(t, err)
		return v
	}
	require.Equal(t, 24*time.Hour, UntilMidnight(at("2026-11-12T00:00:00Z")))
	require.Equal(t, 4*time.Second, UntilMidnight(at("2026-11-12T23:59:56Z")))
	require.Equal(t, 4*time.Second, UntilMidnight(at("2026-11-12T23:59:56.5Z")))
	require.Equal(t, time.Second, UntilMidnight(at("2026-11-12T23:59:59.999Z")))
	// the day is the utc day, whatever zone the clock reports in
	require.Equal(t, 4*time.Second, UntilMidnight(at("2026-11-13T02:59:56+03:00")))
	require.Equal(t, "visitor.20261112", SecretKey(secretKindVisitor, at("2026-11-13T02:59:56+03:00")))
}

// TestEdgeSecretsCluster runs the visitor and ip-cluster checks against one
// real 3-node cluster with the section 4.6 topology at three replicas. every
// gateway instance connects to a different node.
func TestEdgeSecretsCluster(t *testing.T) {
	cluster := natsctr.StartCluster(t)
	_, js := visTestConnect(t, cluster.URLs())
	spec, err := natsx.Topology(3)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), js, spec)
	require.NoError(t, err)

	t.Run("visitor token is stable within a utc day and rotates at midnight", func(t *testing.T) {
		testVisitorRotation(t, cluster)
	})
	t.Run("a survivor adopts the secrets of a bucket emptied by a restart", func(t *testing.T) {
		testVisitorVerify(t, cluster)
	})
	t.Run("visitor-token requests are answered from today's secret", func(t *testing.T) {
		testVisitorTokenRequests(t, cluster)
	})
	t.Run("quarantine starts at exactly the threshold across three gateways", func(t *testing.T) {
		testIPClusterThreeNodes(t, cluster)
	})
	t.Run("a node publishes at most threshold tokens per key and window", func(t *testing.T) {
		testIPClusterPublishBound(t, cluster)
	})
	t.Run("the key bound evicts the least recently used set", func(t *testing.T) {
		testIPClusterKeyBound(t, cluster)
	})
}

func testVisitorRotation(t *testing.T, cluster *natsctr.Cluster) {
	ctx := t.Context()
	// four seconds before a utc midnight that the real clock has not
	// reached, so the keys are fresh and the per-key ttl is four seconds
	clock := newVisTestClock(time.Date(2031, 3, 9, 23, 59, 56, 0, time.UTC))
	_, js1 := visTestConnect(t, cluster.Servers[0].URL)
	_, js2 := visTestConnect(t, cluster.Servers[1].URL)
	kv := visTestSecrets(t, js1)
	v1, err := NewVisitor(kv, clock.Now)
	require.NoError(t, err)
	v2, err := NewVisitor(visTestSecrets(t, js2), clock.Now)
	require.NoError(t, err)

	site := "0b9f7f43-62a4-4c43-9a5e-2f6f3e1c9a10"
	ua := "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
	ip, err := ParseClientIP("198.51.100.23")
	require.NoError(t, err)

	// the two instances start at once and race on create-if-absent
	var wg sync.WaitGroup
	tokens := make([]string, 2)
	errs := make([]error, 2)
	for i, v := range []*Visitor{v1, v2} {
		wg.Go(func() { tokens[i], errs[i] = v.Token(ctx, site, ip, ua) })
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	day1 := tokens[0]
	require.Len(t, day1, 64)
	require.Equal(t, day1, tokens[1], "two gateways disagree on the token")

	oldKey := SecretKey(secretKindVisitor, clock.Now())
	oldCluster := SecretKey(secretKindIPCluster, clock.Now())
	e, err := kv.Get(ctx, oldKey)
	require.NoError(t, err)
	require.Len(t, e.Value(), secretLen)
	stored := e.Value()
	require.NotEqual(t, make([]byte, secretLen), stored)

	// the stored secret is the one the token was computed with, not a
	// second random value
	ref, err := NewVisitor(kv, clock.Now)
	require.NoError(t, err)
	tok, err := ref.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	require.Equal(t, day1, tok)

	// same visitor later the same day: same token. a different user agent,
	// address or site: different tokens.
	clock.Add(2 * time.Second)
	again, err := v2.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	require.Equal(t, day1, again)
	otherUA, err := v1.Token(ctx, site, ip, ua+" x")
	require.NoError(t, err)
	require.NotEqual(t, day1, otherUA)
	ip2, err := ParseClientIP("198.51.100.24")
	require.NoError(t, err)
	otherIP, err := v1.Token(ctx, site, ip2, ua)
	require.NoError(t, err)
	require.NotEqual(t, day1, otherIP)
	otherSite, err := v1.Token(ctx, "a8d2b1a4-7c55-4d7a-9a51-1c2d3e4f5a6b", ip, ua)
	require.NoError(t, err)
	require.NotEqual(t, day1, otherSite)

	// the same /24 gives the same prefix key on both nodes; another /24 does not
	p1, err := v1.PrefixKey(ctx, ip)
	require.NoError(t, err)
	p2, err := v2.PrefixKey(ctx, ip2)
	require.NoError(t, err)
	require.Equal(t, p1, p2)
	ip3, err := ParseClientIP("198.51.101.23")
	require.NoError(t, err)
	p3, err := v1.PrefixKey(ctx, ip3)
	require.NoError(t, err)
	require.NotEqual(t, p1, p3)

	// keep the slices the instance holds, to check they are zeroed
	v1.mu.Lock()
	heldVisitor, heldCluster := v1.visitor, v1.ipcluster
	v1.mu.Unlock()
	require.Equal(t, stored, heldVisitor)

	// the injected clock passes midnight
	clock.Set(time.Date(2031, 3, 10, 0, 0, 1, 0, time.UTC))
	require.NoError(t, v1.Rotate(ctx))
	require.Equal(t, make([]byte, secretLen), heldVisitor, "old visitor secret not zeroed")
	require.Equal(t, make([]byte, secretLen), heldCluster, "old ip-cluster secret not zeroed")
	require.Equal(t, time.Date(2031, 3, 10, 0, 0, 0, 0, time.UTC), v1.Day())

	day2, err := v1.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	require.NotEqual(t, day1, day2, "token did not change after midnight")
	day2b, err := v2.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	require.Equal(t, day2, day2b, "two gateways disagree after midnight")
	p1b, err := v1.PrefixKey(ctx, ip)
	require.NoError(t, err)
	require.NotEqual(t, p1, p1b, "prefix key did not change after midnight")

	// yesterday's keys leave the bucket through their per-key ttl
	require.Eventually(t, func() bool {
		_, e1 := kv.Get(ctx, oldKey)
		_, e2 := kv.Get(ctx, oldCluster)
		return errorsIsNotFound(e1) && errorsIsNotFound(e2)
	}, 15*time.Second, 200*time.Millisecond, "yesterday's secrets are still in edge_secrets")
	// and are absent on every node, not just the one answering
	for _, s := range cluster.Servers {
		_, jsn := visTestConnect(t, s.URL)
		_, err := visTestSecrets(t, jsn).Get(ctx, oldKey)
		require.ErrorIs(t, err, jetstream.ErrKeyNotFound, s.Name)
	}

	// a clock that goes back over midnight is refused rather than
	// recreating yesterday's secret
	clock.Set(time.Date(2031, 3, 9, 23, 59, 59, 0, time.UTC))
	_, err = v1.Token(ctx, site, ip, ua)
	require.ErrorContains(t, err, "clock moved back")
}

func errorsIsNotFound(err error) bool { return errors.Is(err, jetstream.ErrKeyNotFound) }

func testVisitorTokenRequests(t *testing.T, cluster *natsctr.Cluster) {
	ctx := t.Context()
	clock := newVisTestClock(time.Date(2031, 4, 2, 12, 0, 0, 0, time.UTC))
	var gws []*Visitor
	for i := range 3 {
		nc, js := visTestConnect(t, cluster.Servers[i].URL)
		v, err := NewVisitor(visTestSecrets(t, js), clock.Now)
		require.NoError(t, err)
		sub, err := v.ServeTokenRequests(nc, visTestQuiet())
		require.NoError(t, err)
		t.Cleanup(func() { _ = sub.Unsubscribe() })
		require.NoError(t, nc.Flush())
		gws = append(gws, v)
	}
	cp, _ := visTestConnect(t, cluster.Servers[2].URL)

	site := "0b9f7f43-62a4-4c43-9a5e-2f6f3e1c9a10"
	ua := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X)"
	ip, err := ParseClientIP("2001:db8:1234::5")
	require.NoError(t, err)
	want, err := gws[0].Token(ctx, site, ip, ua)
	require.NoError(t, err)

	ask := func(req any) VisitorTokenReply {
		t.Helper()
		b, err := json.Marshal(req)
		require.NoError(t, err)
		m, err := cp.Request(natsx.SubjectVisitorToken, b, 5*time.Second)
		require.NoError(t, err)
		require.NotContains(t, string(m.Data), "2001:db8", "reply echoes the address")
		var r VisitorTokenReply
		require.NoError(t, json.Unmarshal(m.Data, &r))
		return r
	}
	// several requests, so the queue group spreads them over the gateways;
	// whichever answers, the token is the same. the address arrives in
	// another spelling and still matches.
	for range 6 {
		r := ask(VisitorTokenRequest{Site: site, Address: "2001:0db8:1234:0::5", UserAgent: ua})
		require.Empty(t, r.Error)
		require.Equal(t, want, r.Token)
		require.Equal(t, "2031-04-02", r.Day)
	}

	r := ask(VisitorTokenRequest{Site: site, Address: "not an address", UserAgent: ua})
	require.Empty(t, r.Token)
	require.NotEmpty(t, r.Error)
	r = ask(VisitorTokenRequest{Site: "", Address: "203.0.113.1", UserAgent: ua})
	require.Empty(t, r.Token)
	require.NotEmpty(t, r.Error)
	m, err := cp.Request(natsx.SubjectVisitorToken, []byte("{"), 5*time.Second)
	require.NoError(t, err)
	require.Contains(t, string(m.Data), `"error"`)

	// after midnight the old token can no longer be produced
	clock.Set(time.Date(2031, 4, 3, 0, 0, 5, 0, time.UTC))
	r = ask(VisitorTokenRequest{Site: site, Address: "2001:db8:1234::5", UserAgent: ua})
	require.Empty(t, r.Error)
	require.NotEqual(t, want, r.Token)
	require.Equal(t, "2031-04-03", r.Day)
}

// TestGatewayEdgePermissions runs the visitor and ip-cluster code as the
// gateway nats user from the topology, with the control plane user sending
// the erasure request, so a missing permission shows up here and not in
// production.
func TestGatewayEdgePermissions(t *testing.T) {
	users := natsx.Users()
	creds := map[string]string{}
	for _, u := range users {
		creds[u.Name] = fmt.Sprintf("pw-%s-%d", u.Name, time.Now().UnixNano())
	}
	auth, err := natsx.RenderAuthorization(users, creds)
	require.NoError(t, err)
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))

	var mu sync.Mutex
	var violations []string
	conn := func(service string) *nats.Conn {
		t.Helper()
		cfg, err := config.LoadNATS(config.FromMap(map[string]string{
			"NATS_URL": srv.URL, "NATS_REPLICAS": "1",
			"NATS_USER": service, "NATS_PASSWORD": creds[service],
		}))
		require.NoError(t, err)
		opts, err := natsx.Options(cfg, service, visTestQuiet())
		require.NoError(t, err)
		opts = append(opts, nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			mu.Lock()
			violations = append(violations, service+": "+err.Error())
			mu.Unlock()
		}))
		nc, err := nats.Connect(srv.URL, opts...)
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		return nc
	}

	admin := conn(natsx.ServicePlatformctl)
	ajs, err := jetstream.New(admin)
	require.NoError(t, err)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), ajs, spec)
	require.NoError(t, err)

	gw := conn(natsx.ServiceGateway)
	gjs, err := jetstream.New(gw)
	require.NoError(t, err)
	kv, err := gjs.KeyValue(t.Context(), natsx.BucketEdgeSecrets)
	require.NoError(t, err)
	clock := newVisTestClock(time.Now())
	v, err := NewVisitor(kv, clock.Now)
	require.NoError(t, err)
	require.NoError(t, v.Ensure(t.Context()))
	ip, err := ParseClientIP("203.0.113.50")
	require.NoError(t, err)
	vt, err := v.Token(t.Context(), "site", ip, "agent")
	require.NoError(t, err)
	pk, err := v.PrefixKey(t.Context(), ip)
	require.NoError(t, err)
	sub, err := v.ServeTokenRequests(gw, visTestQuiet())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	ipc, err := NewIPCluster(gw, "g1", 2, 100, clock.Now)
	require.NoError(t, err)
	require.NoError(t, ipc.Start(t.Context(), visTestQuiet()))
	require.NoError(t, gw.Flush())
	_, err = ipc.Observe("site", pk, vt)
	require.NoError(t, err)
	require.NoError(t, gw.Flush())

	cp := conn(natsx.ServiceControlPlane)
	b, err := json.Marshal(VisitorTokenRequest{Site: "site", Address: "203.0.113.50", UserAgent: "agent"})
	require.NoError(t, err)
	m, err := cp.Request(natsx.SubjectVisitorToken, b, 5*time.Second)
	require.NoError(t, err)
	var r VisitorTokenReply
	require.NoError(t, json.Unmarshal(m.Data, &r))
	require.Equal(t, vt, r.Token)

	// give async permission errors time to arrive before asserting none did
	require.NoError(t, gw.Flush())
	require.NoError(t, cp.Flush())
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, violations)
}

// testVisitorVerify empties edge_secrets the way a full cluster restart does
// while a gateway keeps running. a gateway that starts afterwards creates new
// secrets, and the survivor must switch to them instead of keeping its copy.
func testVisitorVerify(t *testing.T, cluster *natsctr.Cluster) {
	ctx := t.Context()
	clock := newVisTestClock(time.Date(2031, 5, 4, 12, 0, 0, 0, time.UTC))
	_, js1 := visTestConnect(t, cluster.Servers[0].URL)
	_, js2 := visTestConnect(t, cluster.Servers[2].URL)
	kv := visTestSecrets(t, js1)
	survivor, err := NewVisitor(kv, clock.Now)
	require.NoError(t, err)
	site := "5c1f0d8e-2b1a-4d5e-9f1e-7a1b2c3d4e5f"
	ua := "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
	ip, err := ParseClientIP("198.51.100.42")
	require.NoError(t, err)

	before, err := survivor.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	replaced, err := survivor.Verify(ctx)
	require.NoError(t, err)
	require.False(t, replaced, "the bucket still holds the loaded secrets")

	// the restart empties the bucket; the survivor's copy is all that is left
	for _, kind := range []string{secretKindVisitor, secretKindIPCluster} {
		require.NoError(t, kv.Purge(ctx, SecretKey(kind, clock.Now())))
	}
	fresh, err := NewVisitor(visTestSecrets(t, js2), clock.Now)
	require.NoError(t, err)
	after, err := fresh.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	require.NotEqual(t, before, after, "the new gateway created new secrets")
	still, err := survivor.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	require.Equal(t, before, still, "without a check the survivor keeps its copy")

	replaced, err = survivor.Verify(ctx)
	require.NoError(t, err)
	require.True(t, replaced)
	got, err := survivor.Token(ctx, site, ip, ua)
	require.NoError(t, err)
	require.Equal(t, after, got, "the survivor must use the bucket's secrets")
	replaced, err = survivor.Verify(ctx)
	require.NoError(t, err)
	require.False(t, replaced)
}
