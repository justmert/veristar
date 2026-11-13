//go:build gatewayit

package gatewayit

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// TestThreeInstances: same-ip skepticism across three gateways, each getting
// a third of the traffic from one /24, and the same visitor token on every
// instance.
func TestThreeInstances(t *testing.T) {
	s := newStack(t)
	site := s.PutSite(t, "", "https://app.sa-tenant.example", "active")
	gws := []*Gateway{s.AddGateway(t, "edge-1", nil), s.AddGateway(t, "edge-2", nil), s.AddGateway(t, "edge-3", nil)}
	const threshold = 20

	mark := lastSeq(t, s.JS)
	send := func(g *Gateway, addr, agent, path string) {
		t.Helper()
		c := direct(g, site.Origin, agent, addr)
		p := proofFor(t, c, site.ID)
		status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, path, p), nil)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("%s %s: status %d, %v", g.Node, path, status, err)
		}
		// gossip on edge.ipcluster is asynchronous; a pause lets the token
		// reach the peers before the next request lands on one of them
		time.Sleep(100 * time.Millisecond)
	}
	const total = threshold + 5
	for i := range total {
		// 203.0.113.0/24, one distinct user agent (and address) per visitor
		send(gws[i%3], fmt.Sprintf("203.0.113.%d", 10+i), fmt.Sprintf("%s visitor-%02d", firefoxUA, i+1), fmt.Sprintf("/q/%d", i+1))
	}
	// a visitor seen before the threshold is quarantined too for the rest of
	// the window
	send(gws[1], "203.0.113.10", firefoxUA+" visitor-01", "/q/again")

	got := queuedAfter(t, s.JS, mark, total+1)
	if len(got) != total+1 {
		t.Fatalf("%d events queued, want %d", len(got), total+1)
	}
	firstQ := 0
	for i, q := range got {
		quarantined := q.Event.Band == events.BandQuarantine
		if quarantined && firstQ == 0 {
			firstQ = i + 1
		}
		wantQ := i+1 >= threshold
		if quarantined != wantQ {
			t.Errorf("event %d (%s): band %s", i+1, q.Event.URL, q.Event.Band)
		}
		if quarantined && q.Event.WebsiteID != site.Quarantine {
			t.Errorf("event %d: quarantine event goes to %s, want %s", i+1, q.Event.WebsiteID, site.Quarantine)
		}
	}
	t.Logf("quarantine started at distinct visitor %d of the /24, threshold %d, spread over 3 instances", firstQ, threshold)

	// same address and user agent on two instances gives one token
	mark = lastSeq(t, s.JS)
	send(gws[0], "192.0.2.50", chromeUA+" same", "/same")
	send(gws[2], "192.0.2.50", chromeUA+" same", "/same")
	same := queuedAfter(t, s.JS, mark, 2)
	if len(same) != 2 || same[0].Event.VT != same[1].Event.VT {
		t.Fatalf("visitor tokens across instances differ: %v", same)
	}
}

// TestQueueOutage: with nats stopped the gateway answers 503 and /readyz
// fails within 10 s; after nats returns both recover.
func TestQueueOutage(t *testing.T) {
	s := newStack(t)
	site := s.PutSite(t, "", "https://app.sa-tenant.example", "active")
	g := s.AddGateway(t, "edge-1", nil)
	c := direct(g, site.Origin, firefoxUA, "198.51.100.40")
	p := proofFor(t, c, site.ID)
	if status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/before", p), nil); err != nil || status != http.StatusAccepted {
		t.Fatalf("before outage: status %d, %v", status, err)
	}

	stopped := time.Now()
	mustDocker(t, "stop", "-t", "10", s.NATS)
	t.Logf("nats stopped %s after the stop was issued", time.Since(stopped).Round(time.Millisecond))

	start := time.Now()
	status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/during", p), nil)
	if err != nil || status != http.StatusServiceUnavailable {
		t.Fatalf("during outage: status %d, %v, want 503", status, err)
	}
	t.Logf("event during outage answered 503 in %s", time.Since(start).Round(time.Millisecond))

	var notReady time.Duration
	for {
		resp, err := http.Get(g.OpsURL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusServiceUnavailable {
			notReady = time.Since(stopped)
			break
		}
		if time.Since(stopped) > 15*time.Second {
			t.Fatal("/readyz still 200 15 s after nats stopped")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if notReady > 10*time.Second {
		t.Fatalf("/readyz failed %s after the stop was issued, want at most 10 s", notReady)
	}
	t.Logf("/readyz failed %s after the stop was issued", notReady.Round(time.Millisecond))

	mustDocker(t, "start", s.NATS)
	// a restarted single server keeps its file streams but not the memory
	// buckets (edge_secrets, visit_state, locks). the gateway recreates its own
	// bucket from the topology (natsx.BucketKeeper), so it must come back
	// without anyone running `platformctl nats apply`.
	restarted := time.Now()
	waitReady(t, g.OpsURL+"/readyz", 30*time.Second)
	t.Logf("/readyz ready again %s after nats restarted, with no manual apply", time.Since(restarted).Round(time.Millisecond))
	// the memory bucket lost the day's proof key with the restart, so a new
	// challenge is needed
	p = proofFor(t, c, site.ID)
	if status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/after", p), nil); err != nil || status != http.StatusAccepted {
		t.Fatalf("after outage: status %d, %v", status, err)
	}
}
