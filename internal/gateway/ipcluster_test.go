package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// the starting value from the this stage research list
const ipcTestThreshold = 20

type ipcTestNode struct {
	nc *nats.Conn
	v  *Visitor
	c  *IPCluster
}

func testIPClusterThreeNodes(t *testing.T, cluster *natsctr.Cluster) {
	ctx := t.Context()
	clock := newVisTestClock(time.Date(2031, 5, 6, 10, 0, 0, 0, time.UTC))
	var nodes []ipcTestNode
	for i := range 3 {
		nc, js := visTestConnect(t, cluster.Servers[i].URL)
		v, err := NewVisitor(visTestSecrets(t, js), clock.Now)
		require.NoError(t, err)
		c, err := NewIPCluster(nc, fmt.Sprintf("g%d", i+1), ipcTestThreshold, 10000, clock.Now)
		require.NoError(t, err)
		require.NoError(t, c.Start(ctx, visTestQuiet()))
		require.NoError(t, nc.Flush())
		nodes = append(nodes, ipcTestNode{nc, v, c})
	}

	site := "5f0c1d2e-3a4b-4c5d-8e6f-7a8b9c0d1e2f"
	// every distinct visitor comes from a different address in one /24 and
	// has its own user agent; traffic is spread round-robin, so each node
	// sees a third of it
	visitor := func(i int) (ClientIP, string) {
		ip, err := ParseClientIP(fmt.Sprintf("198.51.100.%d", 10+i))
		require.NoError(t, err)
		return ip, fmt.Sprintf("Mozilla/5.0 (test visitor %d) Firefox/140.0", i)
	}
	event := func(n ipcTestNode, i int) (bool, string) {
		t.Helper()
		ip, ua := visitor(i)
		vt, err := n.v.Token(ctx, site, ip, ua)
		require.NoError(t, err)
		pk, err := n.v.PrefixKey(ctx, ip)
		require.NoError(t, err)
		q, err := n.c.Observe(site, pk, vt)
		require.NoError(t, err)
		return q, pk
	}

	var prefix string
	for i := range ipcTestThreshold {
		n := nodes[i%3]
		q, pk := event(n, i)
		if prefix == "" {
			prefix = pk
		}
		require.Equal(t, prefix, pk, "nodes disagree on the prefix key")
		if i+1 < ipcTestThreshold {
			require.False(t, q, "visitor %d of %d quarantined early on node %d", i+1, ipcTestThreshold, i%3+1)
		} else {
			require.True(t, q, "visitor %d (the threshold) not quarantined on node %d", i+1, i%3+1)
		}
		// the next event goes to another node, which must already know
		// this token, or the threshold would be reached late there
		require.NoError(t, n.nc.Flush())
		for j, other := range nodes {
			require.Eventually(t, func() bool { return other.c.Distinct(site, prefix) == i+1 },
				5*time.Second, 5*time.Millisecond, "node %d holds %d tokens after visitor %d", j+1, other.c.Distinct(site, prefix), i+1)
		}
		// before the threshold no node quarantines, after it every node does
		for j, other := range nodes {
			require.Equal(t, i+1 >= ipcTestThreshold, other.c.Quarantined(site, prefix), "node %d after visitor %d", j+1, i+1)
		}
	}

	// a returning early visitor is quarantined too for the rest of the
	// window, on any node, and so is a new one
	for j, n := range nodes {
		q, _ := event(n, j)
		require.True(t, q, "early visitor on node %d", j+1)
	}
	q, _ := event(nodes[1], ipcTestThreshold+5)
	require.True(t, q)

	// another /24 and another site are unaffected
	other, err := ParseClientIP("198.51.101.10")
	require.NoError(t, err)
	opk, err := nodes[0].v.PrefixKey(ctx, other)
	require.NoError(t, err)
	vt, err := nodes[0].v.Token(ctx, site, other, "x")
	require.NoError(t, err)
	q, err = nodes[0].c.Observe(site, opk, vt)
	require.NoError(t, err)
	require.False(t, q)
	require.False(t, nodes[2].c.Quarantined("e1d2c3b4-a5f6-4789-8abc-def012345678", prefix))

	// one window after the threshold was reached the quarantine ends and
	// the counts are back to zero
	clock.Add(IPClusterWindow + time.Second)
	for j, n := range nodes {
		require.False(t, n.c.Quarantined(site, prefix), "node %d still quarantined after the window", j+1)
		require.Equal(t, 0, n.c.Distinct(site, prefix), "node %d", j+1)
		n.c.Prune()
	}
	q, _ = event(nodes[0], 0)
	require.False(t, q)
}

func ipcTestHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func testIPClusterPublishBound(t *testing.T, cluster *natsctr.Cluster) {
	ctx := t.Context()
	clock := newVisTestClock(time.Date(2031, 5, 7, 10, 0, 0, 0, time.UTC))
	nc, _ := visTestConnect(t, cluster.Servers[0].URL)
	watch, _ := visTestConnect(t, cluster.Servers[1].URL)
	const threshold = 3
	c, err := NewIPCluster(nc, "bound", threshold, 100, clock.Now)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx, visTestQuiet()))

	site, prefix := "bound-site", ipcTestHex("bound-prefix")
	var got atomic.Int64
	sub, err := watch.Subscribe(natsx.SubjectIPCluster, func(m *nats.Msg) {
		if strings.Contains(string(m.Data), site) && strings.Contains(string(m.Data), prefix) {
			got.Add(1)
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	require.NoError(t, watch.Flush())

	// ten distinct tokens and repeats of each: only the first threshold
	// distinct ones are published
	for i := range 10 {
		for range 3 {
			_, err := c.Observe(site, prefix, ipcTestHex(fmt.Sprint("tok", i)))
			require.NoError(t, err)
		}
	}
	require.NoError(t, nc.Flush())
	require.Eventually(t, func() bool { return got.Load() == threshold }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int64(threshold), got.Load(), "more than threshold publishes in one window")

	// in the next window the node may publish again
	clock.Add(IPClusterWindow + time.Second)
	_, err = c.Observe(site, prefix, ipcTestHex("tok-next"))
	require.NoError(t, err)
	require.NoError(t, nc.Flush())
	require.Eventually(t, func() bool { return got.Load() == threshold+1 }, 5*time.Second, 10*time.Millisecond)

	// malformed messages from the subject are refused and counted
	require.NoError(t, watch.Publish(natsx.SubjectIPCluster, []byte(`{"n":"x","s":"s","k":"short","t":"short"}`)))
	require.NoError(t, watch.Publish(natsx.SubjectIPCluster, []byte(`not json`)))
	require.NoError(t, watch.Flush())
	require.Eventually(t, func() bool { return c.Rejected() == 2 }, 5*time.Second, 10*time.Millisecond)

	// malformed observations are refused
	_, err = c.Observe("", prefix, ipcTestHex("a"))
	require.Error(t, err)
	_, err = c.Observe(site, "198.51.100.0/24", ipcTestHex("a"))
	require.Error(t, err)
}

func testIPClusterKeyBound(t *testing.T, cluster *natsctr.Cluster) {
	clock := newVisTestClock(time.Date(2031, 5, 8, 10, 0, 0, 0, time.UTC))
	nc, _ := visTestConnect(t, cluster.Servers[2].URL)
	c, err := NewIPCluster(nc, "keys", 1, 2, clock.Now)
	require.NoError(t, err)

	tok := ipcTestHex("t")
	// threshold 1: every key is quarantined by its first token
	for _, p := range []string{"a", "b"} {
		q, err := c.Observe("s", ipcTestHex(p), tok)
		require.NoError(t, err)
		require.True(t, q)
	}
	// touch a, so b is the least recently used
	require.True(t, c.Quarantined("s", ipcTestHex("a")))
	_, err = c.Observe("s", ipcTestHex("a"), tok)
	require.NoError(t, err)
	_, err = c.Observe("s", ipcTestHex("c"), tok)
	require.NoError(t, err)
	require.Equal(t, 2, c.Keys())
	require.Equal(t, uint64(1), c.Evicted())
	require.True(t, c.Quarantined("s", ipcTestHex("a")))
	require.False(t, c.Quarantined("s", ipcTestHex("b")), "evicted key still held")

	// prune removes sets whose tokens and quarantine have run out
	clock.Add(IPClusterWindow + time.Second)
	c.Prune()
	require.Equal(t, 0, c.Keys())

	_, err = NewIPCluster(nc, "x", 0, 1, clock.Now)
	require.Error(t, err)
	_, err = NewIPCluster(nc, "", 1, 1, clock.Now)
	require.Error(t, err)
}
