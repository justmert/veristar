//go:build gatewayit

package gatewayit

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/justmert/stellar-analytics/tests/gateway/gwclient"
)

const (
	chromeUA  = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36"
	firefoxUA = "Mozilla/5.0 (X11; Linux x86_64; rv:157.0) Gecko/20100101 Firefox/157.0"
)

// fetch metadata a browser sends on the tracker's requests
var browserFetch = http.Header{
	"Sec-Fetch-Site": {"cross-site"},
	"Sec-Fetch-Mode": {"cors"},
	"Sec-Fetch-Dest": {"empty"},
}

func encodeStrkey(t testing.TB, v strkey.VersionByte, n int) string {
	t.Helper()
	b := make([]byte, n)
	_, _ = rand.Read(b)
	s, err := strkey.Encode(v, b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestEventChecks runs the documented refusals and acceptances against one
// gateway container, posting as caddy would (client address header set by
// the test). every outcome is checked in the response, in
// gateway_responses_total and, for registered sites, on EDGE_STATS.
func TestEventChecks(t *testing.T) {
	s := newStack(t)
	site := s.PutSite(t, "", "https://shop.sa-tenant.example", "active")
	suspended := s.PutSite(t, "", "https://old.sa-tenant.example", "suspended")
	g := s.AddGateway(t, "edge-a", nil)
	c := direct(g, site.Origin, firefoxUA, "198.51.100.10")
	mark := lastSeq(t, s.JS)

	ok := func(t *testing.T, e gwclient.Event, extra http.Header) {
		t.Helper()
		status, err := c.PostEvent(context.Background(), e, extra)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("status %d, %v, want 202", status, err)
		}
	}
	expect := func(t *testing.T, cl *gwclient.Client, body []byte, extra http.Header, want int) http.Header {
		t.Helper()
		status, h, err := cl.Post(context.Background(), body, extra)
		if err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("status %d, want %d", status, want)
		}
		// every response names the request origin, so a refusal is not a
		// cors error in the visitor's console
		if o := cl.Header.Get("Origin"); h.Get("Access-Control-Allow-Origin") != o {
			t.Fatalf("Access-Control-Allow-Origin %q, want %q", h.Get("Access-Control-Allow-Origin"), o)
		}
		return h
	}
	enc := func(e gwclient.Event) []byte {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("naive post without proof is 403", func(t *testing.T) {
		expect(t, c, enc(gwclient.Pageview(site.ID, site.Host, "/", nil)), nil, http.StatusForbidden)
	})
	t.Run("forged tag is 403", func(t *testing.T) {
		p := proofFor(t, c, site.ID)
		forged := *p
		forged.Tag = strings.Repeat("ab", 32)
		forged = gwclient.Solve(gwclient.Challenge{W: site.ID, IAT: forged.IAT, Salt: forged.Salt, Bits: forged.Bits, Tag: forged.Tag})
		expect(t, c, enc(gwclient.Pageview(site.ID, site.Host, "/", &forged)), nil, http.StatusForbidden)
	})
	t.Run("replay past the limit is 403", func(t *testing.T) {
		p := proofFor(t, c, site.ID)
		for i := range 50 {
			status, err := c.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/replay", p), nil)
			if err != nil || status != http.StatusAccepted {
				t.Fatalf("use %d: status %d, %v", i+1, status, err)
			}
		}
		expect(t, c, enc(gwclient.Pageview(site.ID, site.Host, "/replay", p)), nil, http.StatusForbidden)
	})
	t.Run("scripted client with forged headers is accepted", func(t *testing.T) {
		// the documented limitation: a go client with no browser solves the
		// challenge and forges Origin and Sec-Fetch-*; see
		// docs/reference/browser-proof.md, "what it shows and what it does not"
		sc := gwclient.New(g.URL, false)
		sc.Header.Set(clientIPHdr, "198.51.100.11")
		sc.Header.Set("User-Agent", chromeUA)
		sc.Header.Set("Origin", site.Origin)
		for k, v := range browserFetch {
			sc.Header[k] = v
		}
		p := proofFor(t, sc, site.ID)
		status, err := sc.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/scripted", p), nil)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("status %d, %v, want 202", status, err)
		}
		// without the forged headers, a chrome user agent is refused
		for k := range browserFetch {
			sc.Header.Del(k)
		}
		expect(t, sc, enc(gwclient.Pageview(site.ID, site.Host, "/scripted", p)), nil, http.StatusForbidden)
	})
	t.Run("wrong origin is 403", func(t *testing.T) {
		p := proofFor(t, c, site.ID)
		wrong := direct(g, "https://evil.example", firefoxUA, "198.51.100.10")
		expect(t, wrong, enc(gwclient.Pageview(site.ID, site.Host, "/", p)), nil, http.StatusForbidden)
	})
	t.Run("unknown site is 404", func(t *testing.T) {
		p := proofFor(t, c, site.ID)
		other := "11111111-2222-4333-8444-555555555555"
		expect(t, c, enc(gwclient.Pageview(other, site.Host, "/", p)), nil, http.StatusNotFound)
	})
	t.Run("suspended site is 404", func(t *testing.T) {
		p := proofFor(t, c, site.ID)
		sc := direct(g, suspended.Origin, firefoxUA, "198.51.100.10")
		expect(t, sc, enc(gwclient.Pageview(suspended.ID, suspended.Host, "/", p)), nil, http.StatusNotFound)
		// and it gets no challenge either
		if _, err := sc.Challenge(context.Background(), suspended.ID); err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("suspended site challenge: %v", err)
		}
	})
	t.Run("oversized body is 413", func(t *testing.T) {
		p := proofFor(t, c, site.ID)
		e := gwclient.Pageview(site.ID, site.Host, "/", p)
		e.Ti = strings.Repeat("x", 9000)
		expect(t, c, enc(e), nil, http.StatusRequestEntityTooLarge)
	})
	t.Run("Sec-GPC is 204 and queues nothing", func(t *testing.T) {
		p := proofFor(t, c, site.ID)
		expect(t, c, enc(gwclient.Pageview(site.ID, site.Host, "/gpc", p)), http.Header{"Sec-Gpc": {"1"}}, http.StatusNoContent)
	})

	t.Run("sanitization end to end", func(t *testing.T) {
		account := encodeStrkey(t, strkey.VersionByteAccountID, 32)
		contract := encodeStrkey(t, strkey.VersionByteContract, 32)
		seed := encodeStrkey(t, strkey.VersionByteSeed, 32)
		muxed := encodeStrkey(t, strkey.VersionByteMuxedAccount, 40)
		before := lastSeq(t, s.JS)
		p := proofFor(t, c, site.ID)
		e := gwclient.Event{
			V: 1, W: site.ID, T: "ev", H: site.Host,
			U:  "/account/" + account + "/contract/" + contract + "/import/" + seed + "?email=maria%40example.com&utm_source=" + account + "&q=hello",
			R:  "https://ref.example/u/" + account + "?token=abc",
			Ti: "Wallet " + muxed, S: "1920x1080", L: "de-DE",
			N: "send", D: map[string]any{"to": account, "amount": 5},
			TS: time.Now().UnixMilli(), P: p,
		}
		ok(t, e, nil)
		got := queuedAfter(t, s.JS, before, 1)
		if len(got) != 1 {
			t.Fatalf("%d events queued, want 1", len(got))
		}
		ev := got[0].Event
		raw := string(got[0].Raw)
		for _, secret := range []string{account, contract, seed, muxed, "maria", "example.com", "token=abc", "hello"} {
			if strings.Contains(raw, secret) {
				t.Errorf("queued event still contains %q: %s", secret, raw)
			}
		}
		// the site does not collect utm parameters (the default), so the
		// query is dropped whole; internal/gateway covers a site that does
		if ev.URL != "/account/{account}/contract/{contract}/import/{redacted-secret}" {
			t.Errorf("url %q", ev.URL)
		}
		if ev.Title != "Wallet {muxed}" || ev.Referrer != "https://ref.example" || ev.Language != "de" {
			t.Errorf("title %q referrer %q language %q", ev.Title, ev.Referrer, ev.Language)
		}
		if ev.Data["to"] != "{account}" {
			t.Errorf("event data %v", ev.Data)
		}
	})

	t.Run("country from db-ip ranges", func(t *testing.T) {
		addrs := countryAddrs(t, "DE", "JP")
		before := lastSeq(t, s.JS)
		for _, cc := range []string{"DE", "JP"} {
			cl := direct(g, site.Origin, firefoxUA, addrs[cc].String())
			p := proofFor(t, cl, site.ID)
			if status, err := cl.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/"+cc, p), nil); err != nil || status != http.StatusAccepted {
				t.Fatalf("%s: status %d, %v", cc, status, err)
			}
		}
		got := queuedAfter(t, s.JS, before, 2)
		if len(got) != 2 || got[0].Event.Country != "DE" || got[1].Event.Country != "JP" {
			t.Fatalf("countries %v", countries(got))
		}
		t.Logf("DE address %s and JP address %s gave %v", addrs["DE"], addrs["JP"], countries(got))
	})

	t.Run("visitor token is stable per address and agent", func(t *testing.T) {
		before := lastSeq(t, s.JS)
		send := func(addr, agent string) {
			cl := direct(g, site.Origin, agent, addr)
			p := proofFor(t, cl, site.ID)
			if status, err := cl.PostEvent(context.Background(), gwclient.Pageview(site.ID, site.Host, "/vt", p), nil); err != nil || status != http.StatusAccepted {
				t.Fatalf("status %d, %v", status, err)
			}
		}
		send("198.51.100.20", firefoxUA)
		send("198.51.100.20", firefoxUA)
		send("198.51.100.21", firefoxUA)
		send("198.51.100.20", firefoxUA+" x")
		got := queuedAfter(t, s.JS, before, 4)
		vt := func(i int) string { return got[i].Event.VT }
		if vt(0) != vt(1) || vt(0) == vt(2) || vt(0) == vt(3) || vt(2) == vt(3) {
			t.Fatalf("visitor tokens %s %s %s %s", vt(0), vt(1), vt(2), vt(3))
		}
	})

	// gateways flush counters every 10 s
	time.Sleep(11 * time.Second)
	stats := edgeStats(t, s.JS)
	prom := responses(t, g, "event")
	t.Logf("gateway_responses_total{route=event}: %v", prom)
	for outcome, want := range map[string]int64{
		"rejected_missing_proof": 1, "rejected_challenge_tag": 1, "rejected_replay": 1, "rejected_signals": 1,
		"rejected_origin": 1, "rejected_body_too_large": 1, "rejected_gpc": 1, "rejected_unknown_site": 1,
	} {
		if prom[outcome] != float64(want) {
			t.Errorf("gateway_responses_total %s = %v, want %d", outcome, prom[outcome], want)
		}
	}
	// counted per site on EDGE_STATS where the site is registered; an
	// unknown site and an unparsable body have no trusted site
	for outcome, want := range map[string]int64{
		"rejected_replay": 1, "rejected_signals": 1, "rejected_origin": 1, "rejected_gpc": 1,
		"rejected_challenge_tag": 1,
	} {
		if got := stats[[2]string{site.ID, outcome}]; got != want {
			t.Errorf("EDGE_STATS %s = %d, want %d", outcome, got, want)
		}
	}
	if got := stats[[2]string{suspended.ID, "rejected_inactive_site"}]; got != 1 {
		t.Errorf("EDGE_STATS rejected_inactive_site = %d, want 1", got)
	}
	accepted := stats[[2]string{site.ID, "accepted"}]
	queuedNow := queuedAfter(t, s.JS, mark, 0)
	if accepted != int64(len(queuedNow)) {
		t.Errorf("EDGE_STATS accepted %d, queued %d", accepted, len(queuedNow))
	}
	if prom["accepted"] != float64(accepted) {
		t.Errorf("prometheus accepted %v, EDGE_STATS %d", prom["accepted"], accepted)
	}
}

func countries(q []queued) []string {
	var out []string
	for _, e := range q {
		out = append(out, e.Event.Country)
	}
	return out
}

// countryAddrs picks one ipv4 address per country from the db-ip file baked
// into the gateway image, inside a network at least a /24 wide so the
// truncated lookup gives the same answer.
func countryAddrs(t testing.TB, ccs ...string) map[string]netip.Addr {
	t.Helper()
	dir := t.TempDir()
	id := mustDocker(t, "create", gatewayImage)
	defer func() { _, _ = docker("rm", id) }()
	path := filepath.Join(dir, "dbip.mmdb")
	mustDocker(t, "cp", id+":/geo/dbip-country-lite.mmdb", path)
	r, err := maxminddb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = os.Remove(path) }()
	want := map[string]bool{}
	for _, cc := range ccs {
		want[cc] = true
	}
	out := map[string]netip.Addr{}
	for res := range r.Networks() {
		var rec struct {
			Country struct {
				ISOCode string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
		}
		if err := res.Decode(&rec); err != nil {
			t.Fatal(err)
		}
		p := res.Prefix()
		if !p.Addr().Is4() || p.Bits() > 24 || !want[rec.Country.ISOCode] {
			continue
		}
		if _, done := out[rec.Country.ISOCode]; !done {
			out[rec.Country.ISOCode] = p.Addr().Next()
		}
		if len(out) == len(ccs) {
			return out
		}
	}
	t.Fatalf("db-ip file has no /24 or wider ipv4 network for all of %v", ccs)
	return nil
}
