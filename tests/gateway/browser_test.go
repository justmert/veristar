//go:build gatewayit

package gatewayit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/justmert/stellar-analytics/internal/events"
)

// the four example sites of tests/e2e/sites.ts, with the same fixed ids
var exampleSites = []struct{ id, origin string }{
	{"0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e01", "https://static.sa-tenant.example"},
	{"0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e02", "https://spa.sa-tenant.example"},
	{"0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e03", "https://rr.sa-tenant.example"},
	{"0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e04", "https://astro.sa-tenant.example"},
}

// TestBrowserMatrix runs the whole this stage browser matrix (chromium, firefox,
// webkit, firefox with gpc; four example sites) against the real gateway
// image behind the edge caddy and real nats, then checks that every event
// the gateway answered with 202 is exactly one WebEvent on WEB_EVENTS with
// the fields the browser sent, and that none was refused, rate limited or
// quarantined (the acceptance criterion of the starting limits).
func TestBrowserMatrix(t *testing.T) {
	if out, err := exec.Command("node", "--version").Output(); err != nil || !strings.HasPrefix(string(out), "v26.") {
		t.Fatalf("node 26 is required on PATH (source ~/.nvm/nvm.sh && nvm use 26.13.1): %q %v", out, err)
	}
	s := newStack(t)
	for _, es := range exampleSites {
		s.PutSite(t, es.id, es.origin, "active")
	}
	g := s.AddGateway(t, "edge-1", nil)
	caddyAddr, _ := s.AddCaddy(t, "edge-1:8080")
	for _, es := range exampleSites {
		waitSite(t, direct(g, es.origin, firefoxUA, "192.0.2.1"), es.id)
	}
	mark := lastSeq(t, s.JS)

	eventLog := filepath.Join(t.TempDir(), "events.jsonl")
	cmd := exec.Command("pnpm", "--filter", "@stellar-analytics/e2e", "exec", "playwright", "test")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "SA_E2E_GATEWAY_UPSTREAM="+caddyAddr, "SA_E2E_EVENT_LOG="+eventLog)
	out, err := cmd.CombinedOutput()
	summary := string(out)
	if i := strings.LastIndex(summary, "Running "); i >= 0 {
		summary = summary[i:]
	}
	t.Logf("playwright against the real gateway:\n%s", summary)
	if err != nil {
		t.Fatalf("playwright failed: %v", err)
	}

	type logged struct {
		Status int `json:"status"`
		Body   struct {
			W  string `json:"w"`
			T  string `json:"t"`
			H  string `json:"h"`
			U  string `json:"u"`
			Ti string `json:"ti"`
			N  string `json:"n"`
		} `json:"body"`
	}
	f, err := os.Open(eventLog)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var want []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l logged
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Status != 202 {
			t.Errorf("browser event %s %s on %s answered %d", l.Body.T, l.Body.U, l.Body.W, l.Status)
			continue
		}
		want = append(want, fmt.Sprintf("%s %s %s %s %q %q", l.Body.W, l.Body.T, l.Body.H, l.Body.U, l.Body.Ti, l.Body.N))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	queuedEvents := queuedAfter(t, s.JS, mark, len(want))
	var got []string
	for _, q := range queuedEvents {
		e := q.Event
		if e.Band != events.BandPrimary {
			t.Errorf("event %s %s quarantined", e.SiteID, e.URL)
		}
		if e.UA == "" || len(e.VT) != 64 || e.AcceptedAt == 0 {
			t.Errorf("event %s lacks user agent, token or acceptance time", e.ID)
		}
		got = append(got, fmt.Sprintf("%s %s %s %s %q %q", e.SiteID, e.Type, e.Hostname, e.URL, e.Title, e.Name))
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Fatalf("queued events differ from the events the browsers sent\nsent:\n%s\nqueued:\n%s",
			strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
	t.Logf("%d browser events answered 202, %d WebEvents queued, all primary", len(want), len(got))
}
