//go:build gatewayit

package gatewayit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/gateway"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/tests/gateway/gwclient"
)

// clock is real time plus an adjustable offset, so ttls on the nats server
// (which runs on real time) line up with the gateway's injected day.
type clock struct{ offset atomic.Int64 }

func (c *clock) Now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

// setTo moves the clock so that it reads at now.
func (c *clock) setTo(at time.Time) { c.offset.Store(int64(time.Until(at))) }

// inProcess is a gateway.Node in this process, built exactly as cmd/gateway
// builds it but with an injected clock, served on a loopback test server.
type inProcess struct {
	URL  string
	Node *gateway.Node
}

// dbipFile copies the db-ip file out of the gateway image, so in-process
// nodes use the same month as the containers.
func dbipFile(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dbip.mmdb")
	id := mustDocker(t, "create", gatewayImage)
	defer func() { _, _ = docker("rm", id) }()
	mustDocker(t, "cp", id+":/geo/dbip-country-lite.mmdb", path)
	return path
}

func startInProcess(t testing.TB, s *Stack, node string, now func() time.Time) *inProcess {
	t.Helper()
	env := map[string]string{
		"GATEWAY_NODE_ID":     node,
		"DBIP_MMDB":           dbipFile(t),
		"TRACKER_RELEASE_DIR": filepath.Join(repoRoot(), "packages", "tracker", "releases"),
	}
	for k, v := range gatewayEnv {
		env[k] = v
	}
	cfg, err := config.LoadGateway(config.FromMap(env))
	if err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(s.NATSURL, nats.Name("gateway"), nats.MaxReconnects(-1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	log := obs.NewLogger(os.Stderr, slog.LevelWarn)
	n, err := gateway.NewNode(ctx, gateway.NodeConfig{Gateway: cfg, NC: nc, Log: log, Registry: obs.NewRegistry(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()
	srv := httptest.NewServer(n.Handler())
	ops := httptest.NewServer(n.Ops())
	t.Cleanup(func() {
		srv.Close()
		ops.Close()
		cancel()
		<-done
		nc.Close()
	})
	waitReady(t, ops.URL+"/readyz", 30*time.Second)
	return &inProcess{URL: srv.URL, Node: n}
}

func inProcessClient(p *inProcess, origin, agent, addr string) *gwclient.Client {
	return browserLike(p.URL, origin, agent, addr)
}

func midnightAfter(daysAhead int) time.Time {
	d := time.Now().UTC().AddDate(0, 0, daysAhead)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

func kvHas(t testing.TB, kv jetstream.KeyValue, key string) bool {
	t.Helper()
	_, err := kv.Get(context.Background(), key)
	switch {
	case err == nil:
		return true
	case errors.Is(err, jetstream.ErrKeyNotFound), errors.Is(err, jetstream.ErrKeyDeleted):
		return false
	}
	t.Fatal(err)
	return false
}

// TestClocks covers challenge expiry in server time, the previous day's
// proof key during the overlap, and the visitor token at utc midnight. a
// browser whose own clock is wrong is covered by tests/e2e/specs/clock.spec.ts
// in TestBrowserMatrix.
func TestClocks(t *testing.T) {
	s := newStack(t)
	site := s.PutSite(t, "", "https://clock.sa-tenant.example", "active")
	secrets, err := s.JS.KeyValue(context.Background(), natsx.BucketEdgeSecrets)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("challenge older than 6 hours of server time is refused", func(t *testing.T) {
		var clk clock
		clk.setTo(midnightAfter(30).Add(time.Hour))
		p := startInProcess(t, s, "clock-expiry", clk.Now)
		c := inProcessClient(p, site.Origin, firefoxUA, "198.51.100.60")
		proof := proofFor(t, c, site.ID)
		clk.setTo(time.Unix(proof.IAT, 0).Add(6*time.Hour - time.Minute))
		if status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/young", proof), nil); err != nil || status != http.StatusAccepted {
			t.Fatalf("5h59m old challenge: status %d, %v, want 202", status, err)
		}
		clk.setTo(time.Unix(proof.IAT, 0).Add(6*time.Hour + time.Minute))
		if status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/old", proof), nil); err != nil || status != http.StatusForbidden {
			t.Fatalf("6h01m old challenge: status %d, %v, want 403", status, err)
		}
	})

	t.Run("previous day's key verifies within the overlap", func(t *testing.T) {
		var clk clock
		midnight := midnightAfter(40)
		clk.setTo(midnight.Add(-30 * time.Second))
		p := startInProcess(t, s, "clock-overlap", clk.Now)
		c := inProcessClient(p, site.Origin, firefoxUA, "198.51.100.61")
		proof := proofFor(t, c, site.ID)
		clk.setTo(midnight.Add(3 * time.Hour))
		if status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/overlap", proof), nil); err != nil || status != http.StatusAccepted {
			t.Fatalf("yesterday's challenge 3h after midnight: status %d, %v, want 202", status, err)
		}
		// a challenge issued now is signed with the new day's key
		fresh := proofFor(t, c, site.ID)
		if status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/today", fresh), nil); err != nil || status != http.StatusAccepted {
			t.Fatalf("today's challenge: status %d, %v", status, err)
		}
		for _, k := range []string{gateway.ProofKeyName(midnight.Add(-time.Hour)), gateway.ProofKeyName(midnight)} {
			if !kvHas(t, secrets, k) {
				t.Errorf("edge_secrets lacks %s", k)
			}
		}
	})

	t.Run("visitor token changes at utc midnight and the old secret is gone", func(t *testing.T) {
		var clk clock
		midnight := midnightAfter(50)
		clk.setTo(midnight.Add(-4 * time.Second))
		p := startInProcess(t, s, "clock-midnight", clk.Now)
		c := inProcessClient(p, site.Origin, firefoxUA, "198.51.100.62")
		mark := lastSeq(t, s.JS)
		post := func(path string) {
			pr := proofFor(t, c, site.ID)
			if status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, path, pr), nil); err != nil || status != http.StatusAccepted {
				t.Fatalf("%s: status %d, %v", path, status, err)
			}
		}
		post("/before-1")
		post("/before-2")
		oldKey := gateway.SecretKey("visitor", midnight.Add(-time.Second))
		if !kvHas(t, secrets, oldKey) {
			t.Fatalf("edge_secrets lacks %s before midnight", oldKey)
		}
		for !clk.Now().After(midnight.Add(time.Second)) {
			time.Sleep(100 * time.Millisecond)
		}
		post("/after")
		got := queuedAfter(t, s.JS, mark, 3)
		if len(got) != 3 || got[0].Event.VT != got[1].Event.VT || got[1].Event.VT == got[2].Event.VT {
			t.Fatalf("tokens before, before, after midnight: %v", tokens(got))
		}
		if !p.Node.Visitor().Day().Equal(midnight) {
			t.Fatalf("loaded secrets are for %s, want %s", p.Node.Visitor().Day(), midnight)
		}
		// the key's ttl ended at midnight; the server removes it on its own
		deadline := time.Now().Add(10 * time.Second)
		for kvHas(t, secrets, oldKey) {
			if time.Now().After(deadline) {
				t.Fatalf("%s still in edge_secrets after midnight", oldKey)
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Logf("token before midnight %s..., after %s..., %s removed", got[0].Event.VT[:12], got[2].Event.VT[:12], oldKey)
	})
}

func tokens(q []queued) []string {
	var out []string
	for _, e := range q {
		out = append(out, e.Event.VT)
	}
	return out
}
