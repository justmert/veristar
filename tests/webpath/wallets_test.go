//go:build webpath

package webpath

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/keypair"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// the this stage example of tests/e2e/sites.ts (WALLET_SITE)
var siteWallets = exampleSite{"wallets-kit-spa", "wk.sa-tenant.example", "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e05", "@veristar/example-wallets-kit-spa", 47105}

// TEST_WALLET_ADDRESS of tests/fixtures/wallets-kit-module, the account the
// test wallet hands the kit. it must reach no request body, no queued message
// and no umami table.
const testWalletAddress = "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN"

// TestWalletsKit is the end-to-end part of the design: the wallets kit
// example in chromium, in both capture modes (script tag and npm package),
// through caddy, the gateway, nats, the replayer and umami. run it alone with
// `go test -tags webpath -run TestWalletsKit ./tests/webpath/`.
func TestWalletsKit(t *testing.T) {
	s := newStack(t)
	fwdBin := buildTool(t, "tests/webpath/fwd")
	s.StartNATS(t)
	s.StartUmami(t)
	quarantine := uuid.NewString()
	s.AddWebsite(t, siteWallets.ID, siteWallets.Key, siteWallets.Host)
	s.AddWebsite(t, quarantine, siteWallets.Key+" quarantine", siteWallets.Host)
	s.PutSite(t, registry.Site{ID: siteWallets.ID, TenantID: uuid.NewString(), Status: registry.StatusActive,
		Origins: []string{"https://" + siteWallets.Host}, QuarantineWebsiteID: quarantine})
	s.StartEdge(t)
	s.AddCountry(t, "DE", countrySubnets(t, "DE")["DE"])
	s.StartForwarder(t, fwdBin, "DE")
	waitChallenge(t, s.FwdAddr, siteWallets)
	b := startBrowsersWith(t, s.FwdAddr, []siteRun{{Site: siteWallets, Version: "1.1.0", Build: "build:test"}})

	// a fresh secret seed per run, so a hit can only come from this run
	kp, err := keypair.Random()
	if err != nil {
		t.Fatal(err)
	}
	seed := kp.Seed()

	// no replayer yet: every accepted event stays in WEB_EVENTS, so the
	// queue scan sees all of them
	mark := s.lastSeq(t)
	b.MustRun(t, []string{"wallets"}, "@wallets-script|@wallets-module", "WEBPATH_SEED="+seed)
	logged := b.NewEvents(t)
	all202(t, logged)

	t.Run("address in no browser request", func(t *testing.T) {
		scanBrowserRequests(t, filepath.Join(b.OutDir, "wallet-requests.jsonl"), seed)
	})

	t.Run("limits enforced by the gateway", func(t *testing.T) {
		gatewayLimits(t, s)
	})

	want := map[string]int{"pageview": 4, "wallet_selected": 2, "wallet_connected": 2, "wallet_disconnected": 2,
		"signup-click": 2, "manual-call": 2, "seed-check": 2, "limit-16-keys": 1}

	t.Run("queued messages", func(t *testing.T) {
		raw := queuedRaw(t, s, mark)
		names := map[string]int{}
		for _, m := range raw {
			if strings.Contains(string(m), testWalletAddress) {
				t.Errorf("queued message carries the test address: %s", m)
			}
			if strings.Contains(string(m), seed) {
				t.Errorf("queued message carries the secret seed: %s", m)
			}
			ev, err := events.DecodeWebEvent(m)
			if err != nil {
				t.Fatal(err)
			}
			if ev.SiteID != siteWallets.ID {
				continue
			}
			if ev.Name == "" {
				names["pageview"]++
			} else {
				names[ev.Name]++
			}
			if ev.Name == "seed-check" && ev.Data["note"] != "key {redacted-secret}" {
				t.Errorf("seed-check data in the queue: %v", ev.Data)
			}
		}
		t.Logf("%d queued messages, none with the address or the seed; by name: %s", len(raw), joinKeys(names))
		expectCounts(t, "queue", names, want)
	})

	s.AddReplayer(t, "core-a")
	s.Drained(t, 5*time.Minute)
	s.WaitStored(t, siteWallets.ID, 17)

	t.Run("stored in umami", func(t *testing.T) {
		evs := s.Events(t, siteWallets.ID, today())
		names := countBy(evs, func(e UmamiEvent) string {
			if e.EventType == umamiPageview {
				return "pageview"
			}
			return e.Name()
		})
		t.Logf("umami events of the site: %s", joinKeys(names))
		expectCounts(t, "umami", names, want)
		if q := s.Events(t, quarantine, today()); len(q) != 0 {
			t.Errorf("%d events in the quarantine website", len(q))
		}

		rows := s.psql(t, `select e.event_name || ' ' || d.data_key || '=' || coalesce(d.string_value, d.number_value::text, '')
			from event_data d join website_event e on e.event_id = d.website_event_id
			where e.website_id = '`+siteWallets.ID+`' and e.event_name <> 'limit-16-keys' order by 1`)
		got := strings.Split(rows, "\n")
		wantRows := []string{
			"manual-call source=wallets-kit-spa", "manual-call source=wallets-kit-spa",
			"seed-check note=key {redacted-secret}", "seed-check note=key {redacted-secret}",
			"signup-click plan=free", "signup-click plan=free",
			"wallet_connected network=testnet",
			"wallet_selected wallet=sa-test-wallet", "wallet_selected wallet=sa-test-wallet",
		}
		sort.Strings(got)
		t.Logf("umami event_data rows:\n%s", strings.Join(got, "\n"))
		if strings.Join(got, "\n") != strings.Join(wantRows, "\n") {
			t.Errorf("event data rows differ, want:\n%s", strings.Join(wantRows, "\n"))
		}
		n := s.psql(t, `select count(*) from event_data d join website_event e on e.event_id = d.website_event_id
			where e.website_id = '`+siteWallets.ID+`' and e.event_name = 'limit-16-keys'`)
		t.Logf("limit-16-keys: %s event_data rows", n)
		if n != "16" {
			t.Errorf("limit-16-keys has %s data rows in umami, want 16", n)
		}
	})

	t.Run("address in no umami table", func(t *testing.T) {
		dump := mustDocker(t, "exec", s.Postgres, "pg_dump", "-U", "postgres", "-d", "umami", "--data-only", "--no-owner")
		tables := strings.Count(dump, "\nCOPY public.")
		hits := strings.Count(dump, testWalletAddress)
		seedHits := strings.Count(dump, seed)
		redacted := strings.Count(dump, "{redacted-secret}")
		t.Logf("pg_dump of umami: %d bytes, %d tables with data; address %d hits, seed %d hits, {redacted-secret} %d",
			len(dump), tables, hits, seedHits, redacted)
		if tables == 0 || !strings.Contains(dump, "wallet_connected") {
			t.Fatal("the dump does not hold the run's events")
		}
		if hits != 0 || seedHits != 0 {
			t.Error("the address or the seed reached the umami database")
		}
	})

	t.Run("dashboard of a view-only member", func(t *testing.T) {
		ctx := context.Background()
		pw := secret()
		u, err := s.Admin.CreateUser(ctx, umami.NewUser{Username: "gh-wallets-" + s.ID, Password: pw, Role: "view-only"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Admin.AddTeamUser(ctx, s.TeamID, u.ID, "team-view-only"); err != nil {
			t.Fatal(err)
		}
		sess, err := s.Client.Login(ctx, u.Username, pw)
		if err != nil {
			t.Fatal(err)
		}
		b.MustRun(t, []string{"wallets-ui"}, "@wallets-ui",
			"WEBPATH_UMAMI_URL="+s.UmamiURL.String(), "WEBPATH_UMAMI_TOKEN="+sess.Token.Reveal(),
			"WEBPATH_WEBSITE="+siteWallets.ID)
		if keep := os.Getenv("WEBPATH_LOGS"); keep != "" {
			for _, f := range []string{"ui-wallet-events.png", "ui-wallet-events.txt"} {
				if data, err := os.ReadFile(filepath.Join(b.OutDir, f)); err == nil {
					_ = os.WriteFile(filepath.Join(keep, f), data, 0o644)
				}
			}
		}
	})
}

func expectCounts(t *testing.T, where string, got, want map[string]int) {
	t.Helper()
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s: %d %s, want %d", where, got[k], k, n)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: unexpected %d %s", where, got[k], k)
		}
	}
}

// scanBrowserRequests reads every request the pages made (url and body) and
// fails on the test address or a non-empty log.
func scanBrowserRequests(t *testing.T, path, seed string) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n, bodies, seedBodies := 0, 0, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		n++
		line := sc.Text()
		var r struct{ Mode, Method, URL, Body string }
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if r.Body != "" {
			bodies++
		}
		if strings.Contains(r.Body, seed) {
			seedBodies++
		}
		if strings.Contains(line, testWalletAddress) {
			t.Errorf("browser request carries the test address: %s", line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d browser requests (%d with a body), none with the address; the seed was in %d bodies as the page sent it", n, bodies, seedBodies)
	if n == 0 || bodies == 0 {
		t.Fatal("no browser requests were recorded")
	}
}

// queuedRaw returns the raw data of every WEB_EVENTS message after mark.
func queuedRaw(t *testing.T, s *Stack, mark uint64) [][]byte {
	t.Helper()
	st, err := s.JS.Stream(context.Background(), natsx.StreamWebEvents)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for seq := mark + 1; seq <= s.lastSeq(t); seq++ {
		m, err := st.GetMsg(context.Background(), seq)
		if err != nil {
			t.Fatalf("message %d: %v", seq, err)
		}
		out = append(out, m.Data)
	}
	return out
}

// gatewayLimits posts events with a valid proof through the forwarder and
// caddy, as a scripted client would: event data with 17 keys and with a
// nested object is refused with 400 and nothing is queued for it.
func gatewayLimits(t *testing.T, s *Stack) {
	c := gwclient.New("https://"+gatewayHost, true)
	tr := c.HTTP.Transport.(*http.Transport)
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, s.FwdAddr)
	}
	// a browser's headers: umami's isbot check drops go's default user
	// agent, and the gateway expects fetch metadata from a known browser
	c.Header.Set("Origin", "https://"+siteWallets.Host)
	c.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36")
	c.Header.Set("Sec-Fetch-Site", "cross-site")
	c.Header.Set("Sec-Fetch-Mode", "cors")
	c.Header.Set("Sec-Fetch-Dest", "empty")
	ctx := context.Background()
	ch, err := c.Challenge(ctx, siteWallets.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := gwclient.Solve(ch)
	before := s.lastSeq(t)

	many := map[string]any{}
	for i := 1; i <= 17; i++ {
		many["k"+string(rune('a'+i-1))] = i
	}
	sixteen := map[string]any{}
	for i := 1; i <= 16; i++ {
		sixteen["k"+string(rune('a'+i-1))] = i
	}
	cases := []struct {
		name string
		data map[string]any
		want int
	}{
		{"limit-16-keys", sixteen, http.StatusAccepted},
		{"limit-17-keys", many, http.StatusBadRequest},
		{"limit-nested", map[string]any{"outer": map[string]any{"inner": 1}}, http.StatusBadRequest},
		{"limit-array", map[string]any{"list": []int{1, 2}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		e := gwclient.Pageview(siteWallets.ID, siteWallets.Host, "/limits", &p)
		e.T, e.N, e.D = "ev", tc.name, tc.data
		status, err := c.PostEvent(ctx, e, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: gateway answered %d", tc.name, status)
		if status != tc.want {
			t.Errorf("%s: status %d, want %d", tc.name, status, tc.want)
		}
	}
	// only the 16-key event was queued
	evs, missing := s.queuedAfter(t, before)
	if missing != 0 || len(evs) != 1 || evs[0].Name != "limit-16-keys" || len(evs[0].Data) != 16 {
		t.Fatalf("queued after the limit posts: %d missing, %d events %v", missing, len(evs), evs)
	}
}
