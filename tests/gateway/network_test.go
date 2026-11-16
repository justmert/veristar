//go:build gatewayit

package gatewayit

import (
	"archive/tar"
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// addresses in long-stable allocations of the open data the image carries:
// 1.1.1.0/24 is cloudflare (as13335, a hosting network) and 80.128.0.0/24
// deutsche telekom (as3320, an access network). no packet is sent to them;
// the test only sets them as the client address caddy would report.
const (
	hostingNet = "1.1.1"
	accessNet  = "80.128.0"
)

// putScaledSite registers a site whose registry entry carries a scale, as
// the reconciler publishes it from the integrity task's baseline.
func (s *Stack) putScaledSite(t testing.TB, origin string, scale int) Site {
	t.Helper()
	p, err := registry.NewPublisher(context.Background(), s.JS)
	if err != nil {
		t.Fatal(err)
	}
	site := Site{ID: uuid.NewString(), Quarantine: uuid.NewString(), Tenant: uuid.NewString(), Origin: origin}
	site.Host = strings.TrimPrefix(origin, "https://")
	if err := p.PutSite(context.Background(), registry.Site{
		ID: site.ID, TenantID: site.Tenant, Status: registry.StatusActive, Origins: []string{origin},
		QuarantineWebsiteID: site.Quarantine, Scale: scale,
	}); err != nil {
		t.Fatal(err)
	}
	return site
}

// firstTorExit reads one address of the tor exit list baked into the image.
func firstTorExit(t testing.TB) string {
	t.Helper()
	name := "sa-gwit-tor-" + randHex(4)
	mustDocker(t, "create", "--name", name, gatewayImage)
	t.Cleanup(func() { _, _ = docker("rm", "-f", name) })
	// docker cp to stdout writes a tar stream of the one file
	out := mustDocker(t, "cp", name+":/geo/tor-exits.txt", "-")
	tr := tar.NewReader(strings.NewReader(out))
	if _, err := tr.Next(); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(tr)
	for sc.Scan() {
		if a, err := netip.ParseAddr(sc.Text()); err == nil && a.Is4() {
			return a.String()
		}
	}
	t.Fatal("no ipv4 tor exit address in the image")
	return ""
}

// TestNetworkClassAtTheEdge sends visitors from a hosting network, an access
// network and a tor exit through a real gateway container with the image's
// open network data. it checks the band each event gets under the
// class-aware thresholds, and the evidence counters and network sightings
// on EDGE_STATS, which carry a class and a keyed hash, never an address or
// an autonomous system number.
func TestNetworkClassAtTheEdge(t *testing.T) {
	s := newStack(t)
	small := s.PutSite(t, "", "https://small.sa-tenant.example", "active")
	// a site whose typical day has 8192 visitors: one access prefix may
	// carry 81 distinct visitors an hour before it is quarantined
	large := s.putScaledSite(t, "https://large.sa-tenant.example", 8192)
	g := s.AddGateway(t, "edge-net", nil)

	send := func(site Site, addr, agent, path string) *events.WebEvent {
		t.Helper()
		mark := lastSeq(t, s.JS)
		c := direct(g, site.Origin, agent, addr)
		p := proofFor(t, c, site.ID)
		status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, path, p), nil)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("%s from %s: status %d, %v", path, addr, status, err)
		}
		got := queuedAfter(t, s.JS, mark, 1)
		if len(got) != 1 {
			t.Fatalf("%s: %d events queued, want 1", path, len(got))
		}
		return got[0].Event
	}
	visitor := func(i int) string { return fmt.Sprintf("%s visitor-%03d", firefoxUA, i) }

	t.Run("a hosting prefix is quarantined at the fifth distinct visitor", func(t *testing.T) {
		hosting := integrity.PrefixThreshold(20, integrity.NetHosting, 0)
		for i := range hosting + 2 {
			ev := send(small, fmt.Sprintf("%s.%d", hostingNet, 10+i), visitor(i), fmt.Sprintf("/h/%d", i))
			want := events.BandPrimary
			if i+1 >= hosting {
				want = events.BandQuarantine
			}
			if ev.Band != want {
				t.Fatalf("hosting visitor %d: band %s, want %s", i+1, ev.Band, want)
			}
		}
	})

	t.Run("an access prefix of a small site keeps the fixed floor", func(t *testing.T) {
		for i := range 20 {
			ev := send(small, fmt.Sprintf("%s.%d", accessNet, 10+i), visitor(100+i), fmt.Sprintf("/a/%d", i))
			if (ev.Band == events.BandQuarantine) != (i+1 >= 20) {
				t.Fatalf("access visitor %d: band %s", i+1, ev.Band)
			}
		}
	})

	t.Run("an access prefix of a large site carries its carrier-grade nat", func(t *testing.T) {
		for i := range 40 {
			ev := send(large, fmt.Sprintf("%s.%d", accessNet, 100+i), visitor(200+i), fmt.Sprintf("/l/%d", i))
			if ev.Band != events.BandPrimary {
				t.Fatalf("visitor %d of a large site quarantined from an access prefix", i+1)
			}
		}
	})

	t.Run("a tor exit is an anonymiser", func(t *testing.T) {
		exit := firstTorExit(t)
		for i := range 5 {
			ev := send(large, exit, visitor(400+i), fmt.Sprintf("/t/%d", i))
			if ev.Band != events.BandPrimary {
				t.Fatalf("tor visitor %d quarantined below the hosting threshold of a large site", i+1)
			}
		}
	})

	t.Run("edge stats carry classes and network keys only", func(t *testing.T) {
		time.Sleep(12 * time.Second) // one stats interval and a margin
		stats := edgeStats(t, s.JS)
		get := func(site Site, outcome string) int64 { return stats[[2]string{site.ID, outcome}] }
		if n := get(small, integrity.EvidenceOutcome(integrity.NetHosting)); n != 7 {
			t.Errorf("small site hosting events %d, want 7", n)
		}
		if n := get(small, integrity.EvidenceOutcome(integrity.NetAccess)); n != 20 {
			t.Errorf("small site access events %d, want 20", n)
		}
		if n := get(large, integrity.EvidenceOutcome(integrity.NetAccess)); n != 40 {
			t.Errorf("large site access events %d, want 40", n)
		}
		if n := get(large, integrity.EvidenceOutcome(integrity.NetAnonymiser)); n != 5 {
			t.Errorf("large site anonymiser events %d, want 5", n)
		}
		networks := map[string]int{}
		for k, n := range stats {
			if !integrity.IsNetworkOutcome(k[1]) {
				continue
			}
			if n != 1 {
				t.Errorf("network %s reported %d times by one node, want once", k[1], n)
			}
			networks[k[0]]++
		}
		// small: cloudflare and deutsche telekom; large: deutsche telekom and
		// the tor exit's network
		// the tor exit's own network is whatever the open data says, so
		// only its lower bound is fixed
		if networks[small.ID] != 2 || networks[large.ID] < 1 || networks[large.ID] > 2 {
			t.Errorf("distinct networks small %d, large %d, want 2 and 1 or 2", networks[small.ID], networks[large.ID])
		}
		for k := range stats {
			for _, leak := range []string{hostingNet, accessNet, "13335", "3320"} {
				if strings.Contains(k[1], leak) {
					t.Errorf("outcome %q carries %q", k[1], leak)
				}
			}
		}
	})
}
