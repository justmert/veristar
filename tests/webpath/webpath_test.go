//go:build webpath

package webpath

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
)

// suite is the state the ordered checks of TestWebPath share.
type suite struct {
	s          *Stack
	b          *Browsers
	quarantine map[string]string // site id to its quarantine website
	sniffBin   string
	// end of the first static visit, the start of the gap before the second
	s1End time.Time
}

func (su *suite) sites(ss ...exampleSite) map[string][]string {
	out := map[string][]string{}
	for _, x := range ss {
		out[x.ID] = []string{x.ID, su.quarantine[x.ID]}
	}
	return out
}

// TestWebPath runs every verification item of the design that needs the
// whole chain, in one stack, in a fixed order. the second static visit has
// to come more than 30 minutes and at least one utc hour after the first, so
// the other checks run in between.
func TestWebPath(t *testing.T) {
	s := newStack(t)
	su := &suite{s: s, quarantine: map[string]string{}}
	fwdBin := buildTool(t, "tests/webpath/fwd")
	su.sniffBin = buildTool(t, "tests/webpath/sniff")

	s.StartNATS(t)
	s.StartUmami(t)
	boot := s.StartSniffer(t, su.sniffBin, "boot")
	for _, x := range allSites {
		q := uuid.NewString()
		su.quarantine[x.ID] = q
		s.AddWebsite(t, x.ID, x.Key, x.Host)
		s.AddWebsite(t, q, x.Key+" quarantine", x.Host)
		s.PutSite(t, registry.Site{ID: x.ID, TenantID: uuid.NewString(), Status: registry.StatusActive,
			Origins: []string{"https://" + x.Host}, QuarantineWebsiteID: q})
	}
	s.StartEdge(t)
	subnets := countrySubnets(t, "DE", "JP")
	de := s.AddCountry(t, "DE", subnets["DE"])
	jp := s.AddCountry(t, "JP", subnets["JP"])
	t.Logf("client addresses: DE %s (network %s), JP %s (network %s)", de.Client, de.Subnet, jp.Client, jp.Subnet)
	s.StartForwarder(t, fwdBin, "DE")
	s.AddReplayer(t, "core-a")
	s.AddReplayer(t, "core-b")
	for _, x := range allSites {
		waitChallenge(t, s.FwdAddr, x)
	}
	su.b = startBrowsers(t, s.FwdAddr)

	t.Run("custom event rerun x20", su.customEventRerun)
	s.ResetSites(t, su.sites(allSites...))
	t.Run("pageviews visitors visits bounce, first visit", su.firstVisit)
	t.Run("session duration", su.sessionDuration)
	t.Run("referrers", su.referrers)
	t.Run("country", su.country)
	t.Run("device browser os", su.devices)
	t.Run("bots", su.bots)
	t.Run("time ranges", su.timeRanges)
	t.Run("visit rule", su.visitRule)
	bootLines := s.StopSniffer(t, boot)
	t.Run("egress from boot to here", func(t *testing.T) { reportEgress(t, classify(bootLines, s.Subnet)) })
	t.Run("outage umami stopped", func(t *testing.T) { su.outage(t, s.Umami, "outu") })
	t.Run("outage postgres stopped", func(t *testing.T) { su.outage(t, s.Postgres, "outp") })
	t.Run("replayer kill", su.replayerKill)
	t.Run("quarantine", su.quarantineBand)
	t.Run("no outbound calls during a ui session", su.uiEgress)
	t.Run("pageviews visitors visits bounce, second visit", su.secondVisit)
	t.Run("privacy dump", func(t *testing.T) { su.privacy(t, de, jp) })
	t.Run("every browser event answered 202", func(t *testing.T) {
		all := su.b.NewEvents(t)
		all202(t, all)
		t.Logf("%d further browser events, all checked", len(all))
	})
}

// waitChallenge asks for a challenge through the forwarder and caddy until
// the gateway's registry snapshot has the site.
func waitChallenge(t *testing.T, fwd string, x exampleSite) {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{ServerName: gatewayHost, InsecureSkipVerify: true},
	}}
	deadline := time.Now().Add(time.Minute)
	for {
		req, _ := http.NewRequest(http.MethodGet, "https://"+fwd+"/v1/c?w="+x.ID, nil)
		req.Host = gatewayHost
		req.Header.Set("Origin", "https://"+x.Host)
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no challenge for %s through the forwarder: %v", x.Key, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

var playwrightCounts = regexp.MustCompile(`(?m)^\s+(\d+) (passed|failed|flaky|skipped)`)

func playwrightTotals(out string) map[string]int {
	m := map[string]int{}
	for _, x := range playwrightCounts.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(x[1])
		m[x[2]] += n
	}
	return m
}

// customEventRerun re-runs the this stage chromium custom event case
// (specs/events.spec.ts, one data-umami-event click and one umami.track()
// call per site) 20 times through the real gateway, then checks that umami
// stored every event.
func (su *suite) customEventRerun(t *testing.T) {
	s, b := su.s, su.b
	// only the sites this suite serves and registers: events.spec.ts covers
	// every site of tests/e2e/sites.ts, and a site without a server here
	// (vue, sveltekit, next.js) waits out the 5-minute test timeout 20 times
	keys := make([]string, 0, len(allSites))
	for _, x := range allSites {
		keys = append(keys, regexp.QuoteMeta(x.Key))
	}
	grep := `(` + strings.Join(keys, "|") + `): data-umami-event and umami\.track\(\)`
	out, err := b.Run(t, []string{"chromium"}, grep, []string{"--repeat-each", "20"})
	tot := playwrightTotals(out)
	t.Logf("events.spec.ts x20 in chromium through the real gateway: %v", tot)
	if want := 20 * len(allSites); tot["passed"]+tot["failed"]+tot["flaky"] != want {
		t.Errorf("playwright ran %v, want %d runs (%d sites x20)", tot, want, len(allSites))
	}
	if err != nil {
		var failed []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "✘") {
				failed = append(failed, strings.TrimSpace(l))
			}
		}
		t.Errorf("playwright reported failures (browser-side misses):\n%s", strings.Join(failed, "\n"))
	}
	logged := b.NewEvents(t)
	all202(t, logged)
	s.Drained(t, 5*time.Minute)
	for _, x := range allSites {
		accepted := 0
		for _, e := range logged {
			if e.Body.W == x.ID {
				accepted++
			}
		}
		s.WaitStored(t, x.ID, accepted)
		evs := s.Events(t, x.ID, today())
		n := countBy(evs, func(e UmamiEvent) string {
			if e.EventType == umamiPageview {
				return "pageview"
			}
			return e.Name()
		})
		sent := countBy(logged, func(e LoggedEvent) string {
			if e.Body.W != x.ID {
				return ""
			}
			if e.Body.T == "pv" {
				return "pageview"
			}
			return e.Body.N
		})
		t.Logf("%s: browsers sent pageview=%d signup-click=%d manual-call=%d; umami stored %s",
			x.Key, sent["pageview"], sent["signup-click"], sent["manual-call"], joinKeys(n))
		for _, name := range []string{"signup-click", "manual-call"} {
			if n[name] != 20 {
				t.Errorf("%s: umami has %d %s events, want 20 (one per run)", x.Key, n[name], name)
			}
		}
	}
}

func (su *suite) firstVisit(t *testing.T) {
	s, b := su.s, su.b
	b.MustRun(t, []string{"chromium"}, "@s1-visit1")
	b.MustRun(t, []string{"firefox"}, "@s1-single")
	logged := b.NewEvents(t)
	all202(t, logged)
	su.s1End = time.Now().UTC()
	s.Drained(t, 2*time.Minute)
	s.WaitStored(t, siteStatic.ID, 4)
	st := s.stats(t, siteStatic.ID, today())
	t.Logf("static after the first visits: %+v", st)
	want := umami.StatsValues{Pageviews: 4, Visitors: 2, Visits: 2, Bounces: 1}
	st.Totaltime = 0
	if st != want {
		t.Errorf("stats %+v, want %+v", st, want)
	}
}

func (su *suite) sessionDuration(t *testing.T) {
	s, b := su.s, su.b
	s.ResetSites(t, su.sites(siteSPA))
	b.MustRun(t, []string{"chromium"}, "@duration")
	logged := b.NewEvents(t)
	all202(t, logged)
	var ts []int64
	for _, e := range logged {
		if e.Body.W == siteSPA.ID && e.Body.T == "pv" {
			ts = append(ts, e.Body.TS)
		}
	}
	if len(ts) != 3 {
		t.Fatalf("%d pageviews logged, want 3", len(ts))
	}
	expected := float64(ts[2]-ts[0]) / 1000
	s.Drained(t, 2*time.Minute)
	s.WaitStored(t, siteSPA.ID, 3)
	st := s.stats(t, siteSPA.ID, today())
	t.Logf("spa: %+v; expected visit duration %.3f s (browser clock, first to last pageview)", st, expected)
	if st.Visits != 1 || st.Pageviews != 3 || st.Bounces != 0 {
		t.Errorf("stats %+v, want one visit of three pageviews and no bounce", st)
	}
	if d := math.Abs(float64(st.Totaltime) - expected); d > 2 {
		t.Errorf("totaltime %d s differs from %.3f s by %.3f s, more than 2 s", st.Totaltime, expected, d)
	}
}

func (su *suite) referrers(t *testing.T) {
	s, b := su.s, su.b
	s.ResetSites(t, su.sites(siteRR))
	b.MustRun(t, []string{"chromium", "firefox", "webkit"}, "@referrer")
	logged := b.NewEvents(t)
	all202(t, logged)
	for _, e := range logged {
		t.Logf("sent referrer %q from origin %s", e.Body.R, e.Origin)
	}
	s.Drained(t, 2*time.Minute)
	s.WaitStored(t, siteRR.ID, 3)
	m := s.metrics(t, siteRR.ID, "referrer", today())
	t.Logf("rr /metrics?type=referrer: %v", m)
	if m["news.sa-referrer.example"] != 3 {
		t.Errorf("referrer news.sa-referrer.example = %d, want 3 (one per engine)", m["news.sa-referrer.example"])
		dumpEvents(t, s, siteRR.ID)
	}
}

func (su *suite) country(t *testing.T) {
	s, b := su.s, su.b
	s.ResetSites(t, su.sites(siteRR))
	b.MustRun(t, []string{"chromium"}, "@one-page", "WEBPATH_SITE="+siteRR.Key)
	s.Route(t, "JP")
	b.MustRun(t, []string{"chromium"}, "@one-page", "WEBPATH_SITE="+siteRR.Key)
	s.Route(t, "DE")
	all202(t, b.NewEvents(t))
	s.Drained(t, 2*time.Minute)
	s.WaitStored(t, siteRR.ID, 2)
	m := s.metrics(t, siteRR.ID, "country", today())
	t.Logf("rr /metrics?type=country: %v", m)
	if len(m) != 2 || m["DE"] != 1 || m["JP"] != 1 {
		t.Errorf("countries %v, want DE 1 and JP 1", m)
	}
	n := s.psql(t, "select count(*) from session where region is not null or city is not null")
	t.Logf("session rows with region or city: %s", n)
	if n != "0" {
		t.Errorf("%s session rows carry a region or city", n)
	}
}

func (su *suite) devices(t *testing.T) {
	s, b := su.s, su.b
	s.ResetSites(t, su.sites(siteAstro))
	b.MustRun(t, []string{"chromium", "firefox", "webkit", "chromium-mobile", "webkit-mobile"}, "@one-page", "WEBPATH_SITE="+siteAstro.Key)
	all202(t, b.NewEvents(t))
	s.Drained(t, 2*time.Minute)
	s.WaitStored(t, siteAstro.ID, 5)
	// umami's own classification of the playwright device profiles
	// (src/lib/detect.ts: detect-browser names, getDevice with screen width).
	// playwright's firefox reaches the gateway with its real linux user agent
	// rather than the windows one of the "Desktop Firefox" descriptor
	// (observed in the umami session row), so firefox counts as Linux.
	want := map[string]map[string]int64{
		"device":  {"laptop": 3, "mobile": 2},
		"browser": {"chrome": 2, "firefox": 1, "safari": 1, "ios": 1},
		"os":      {"Windows 10": 1, "Linux": 1, "Mac OS": 1, "Android OS": 1, "iOS": 1},
	}
	for _, typ := range []string{"device", "browser", "os"} {
		m := s.metrics(t, siteAstro.ID, typ, today())
		t.Logf("astro /metrics?type=%s: %v", typ, m)
		if fmt.Sprint(m) != fmt.Sprint(want[typ]) {
			t.Errorf("%s: %v, want %v", typ, m, want[typ])
		}
	}
	dumpEvents(t, s, siteAstro.ID)
}

func (su *suite) bots(t *testing.T) {
	s, b := su.s, su.b
	s.ResetSites(t, su.sites(siteAstro))
	// replayers and gateway flush EDGE_STATS every 10 s; start from empty
	time.Sleep(11 * time.Second)
	_ = s.edgeStats(t)
	before := s.replayerSum(t, `replayer_outcomes_total{outcome="bot"}`)
	b.MustRun(t, []string{"chromium"}, "@bot")
	logged := b.NewEvents(t)
	all202(t, logged)
	s.Drained(t, 2*time.Minute)
	time.Sleep(11 * time.Second)
	stats := s.edgeStats(t)
	after := s.replayerSum(t, `replayer_outcomes_total{outcome="bot"}`)
	evs := s.Events(t, siteAstro.ID, today())
	st := s.stats(t, siteAstro.ID, today())
	t.Logf("crawler: %d events answered 202; replayer bot outcomes +%.0f; EDGE_STATS bot=%d accepted=%d; umami events %d, stats %+v",
		len(logged), after-before, stats[[2]string{siteAstro.ID, "bot"}], stats[[2]string{siteAstro.ID, "accepted"}], len(evs), st)
	if len(logged) != 2 || after-before != 2 || stats[[2]string{siteAstro.ID, "bot"}] != 2 {
		t.Errorf("want 2 events accepted, counted 2 times as bot by the replayer and on EDGE_STATS")
	}
	if len(evs) != 0 || st.Pageviews != 0 || st.Visitors != 0 {
		t.Errorf("crawler events reached umami: %d events, %+v", len(evs), st)
	}
}

// crafted events stand in for the gateway: an acceptance time in the past
// cannot come from a browser. everything else matches what the gateway
// publishes.
func crafted(website, vt string, at time.Time, path string) events.WebEvent {
	return events.WebEvent{
		SiteID: website, WebsiteID: website, Band: events.BandPrimary, VT: vt,
		UA:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36",
		Country: "DE", Type: events.TypePageview, Hostname: "crafted.sa-tenant.example", URL: path,
		Title: "crafted", Screen: "1920x1080", Language: "en-US", AcceptedAt: at.UnixMilli(),
	}
}

func vtOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (su *suite) timeRanges(t *testing.T) {
	s := su.s
	website := uuid.NewString()
	s.AddWebsite(t, website, "time ranges", "crafted.sa-tenant.example")
	day0 := time.Now().UTC().Truncate(24 * time.Hour)
	days := []time.Time{day0.AddDate(0, 0, -3), day0.AddDate(0, 0, -2), day0.AddDate(0, 0, -1)}
	vt := vtOf("time ranges " + website)
	for i, d := range days {
		// i+2 pageviews at 12:00, 12:01, ... of that day
		for k := range i + 2 {
			s.Publish(t, crafted(website, vt, d.Add(12*time.Hour+time.Duration(k)*time.Minute), fmt.Sprintf("/day%d/%d", i, k)))
		}
	}
	s.Drained(t, 2*time.Minute)
	for i, d := range days {
		st := s.stats(t, website, dayRange(d))
		t.Logf("%s: %+v", d.Format("2006-01-02"), st)
		if st.Pageviews != int64(i+2) || st.Visitors != 1 || st.Visits != 1 {
			t.Errorf("%s: %+v, want %d pageviews, 1 visitor, 1 visit", d.Format("2006-01-02"), st, i+2)
		}
	}
	all := s.stats(t, website, umami.Range{Start: days[0], End: days[2].Add(24*time.Hour - time.Millisecond)})
	t.Logf("three days: %+v (one visitor token on three days counts as three visitors: daily salt)", all)
	if all.Pageviews != 9 || all.Visitors != 3 {
		t.Errorf("three days: %+v, want 9 pageviews and 3 visitors", all)
	}
	// the days around the three hold nothing
	for _, d := range []time.Time{day0.AddDate(0, 0, -4), day0} {
		if st := s.stats(t, website, dayRange(d)); st.Pageviews != 0 {
			t.Errorf("%s: %+v, want nothing", d.Format("2006-01-02"), st)
		}
	}
}

func (su *suite) visitRule(t *testing.T) {
	s := su.s
	website := uuid.NewString()
	s.AddWebsite(t, website, "visit rule", "crafted.sa-tenant.example")
	y := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	at := func(h, m int) time.Time { return y.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	cases := []struct {
		name       string
		a, b       time.Time
		wantVisits int
	}{
		{"31 min apart across an hour", at(10, 50), at(11, 21), 2},
		{"31 min apart in one hour", at(13, 5), at(13, 36), 1},
		{"25 min apart across an hour", at(15, 50), at(16, 15), 1},
	}
	vts := map[string]string{}
	for _, c := range cases {
		vt := vtOf(c.name + website)
		vts[c.name] = vt
		s.Publish(t, crafted(website, vt, c.a, "/first"))
		s.Publish(t, crafted(website, vt, c.b, "/second"))
	}
	s.Drained(t, 2*time.Minute)
	evs := s.Events(t, website, dayRange(y))
	bySession := map[string]map[string]bool{}
	firstAt := map[string]time.Time{}
	for _, e := range evs {
		if bySession[e.SessionID] == nil {
			bySession[e.SessionID] = map[string]bool{}
		}
		bySession[e.SessionID][e.VisitID] = true
		if f, ok := firstAt[e.SessionID]; !ok || e.CreatedAt.Before(f) {
			firstAt[e.SessionID] = e.CreatedAt
		}
	}
	for _, c := range cases {
		var got int
		found := false
		for sid, f := range firstAt {
			if f.Equal(c.a) {
				got, found = len(bySession[sid]), true
			}
		}
		t.Logf("%s (%s and %s): %d visit(s)", c.name, c.a.Format("15:04"), c.b.Format("15:04"), got)
		if !found || got != c.wantVisits {
			t.Errorf("%s: %d visits (found %v), want %d", c.name, got, found, c.wantVisits)
		}
	}
	st := s.stats(t, website, dayRange(y))
	t.Logf("visit rule website: %+v", st)
	if st.Visitors != 3 || st.Visits != 4 {
		t.Errorf("stats %+v, want 3 visitors and 4 visits", st)
	}
}

// dumpEvents logs every umami event of a website today, for diagnosis.
func dumpEvents(t *testing.T, s *Stack, website string) {
	t.Helper()
	str := func(p *string) string {
		if p == nil {
			return "<null>"
		}
		return *p
	}
	for _, e := range s.Events(t, website, today()) {
		t.Logf("  event %s type %d %s %q session %s visit %s referrer %s browser %s os %s device %s country %s",
			e.CreatedAt.Format(time.RFC3339), e.EventType, e.URLPath, e.Name(), e.SessionID[:8], e.VisitID[:8],
			str(e.ReferrerDomain), str(e.Browser), str(e.OS), str(e.Device), str(e.Country))
	}
}

func reportEgress(t *testing.T, e Egress) {
	t.Logf("capture: %d outgoing packets, %d local connection attempts, local names %v", e.Packets, e.LocalFlows, e.LocalNames)
	if e.Packets == 0 {
		t.Errorf("the capture saw no outgoing packet at all, so it proves nothing")
	}
	for _, f := range e.Flows {
		t.Errorf("connection attempt outside the private network: %s %s -> %s:%d at %s", f.Proto, f.Src, f.Dst, f.DPort, f.At)
	}
	for _, n := range e.Names {
		t.Errorf("dns question for an external name: %s at %s", n.Name, n.At)
	}
}

// burst sends pages x perPage browser events per project through the
// browsers and returns what the fixture logged.
func (su *suite) burst(t *testing.T, projects []string, site exampleSite, tag string, pages, perPage int) []LoggedEvent {
	t.Helper()
	su.b.MustRun(t, projects, "@burst", "WEBPATH_SITE="+site.Key, "WEBPATH_TAG="+tag,
		"WEBPATH_PAGES="+strconv.Itoa(pages), "WEBPATH_PER_PAGE="+strconv.Itoa(perPage), "WEBPATH_PACE_MS=70")
	logged := su.b.NewEvents(t)
	all202(t, logged)
	misses := su.b.Misses(t)
	n := 0
	for _, m := range misses {
		n += len(m.Missing)
		t.Errorf("browser miss: %s page %d: %d umami.track() events never reached the gateway: %v; browser reported: %v",
			m.Project, m.Page, len(m.Missing), m.Missing, m.Browser)
	}
	if len(logged)+n != len(projects)*pages*perPage {
		t.Errorf("%d events logged and %d missed, want %d", len(logged), n, len(projects)*pages*perPage)
	}
	return logged
}

var burstProjects = []string{"chromium", "firefox", "webkit", "chromium-mobile"}

// outage stops a container (umami or its postgresql), sends 500 browser
// events, starts it again and checks that every event reaches umami with its
// original acceptance time, and that the consumer lag returns to zero.
func (su *suite) outage(t *testing.T, container, tag string) {
	s := su.s
	s.ResetSites(t, su.sites(siteAstro))
	mark := s.lastSeq(t)
	stoppedAt := time.Now()
	s.stop(t, container)
	// a failed check must not leave the service down for the checks after it
	restored := false
	defer func() {
		if !restored {
			s.start(t, container)
			if container == s.Postgres {
				s.waitPostgres(t, 2*time.Minute)
			}
			waitHTTP(t, s.UmamiURL.String()+"/api/heartbeat", 5*time.Minute)
		}
	}()
	logged := su.burst(t, burstProjects, siteAstro, tag, 5, 25)
	queued, missing := s.queuedAfter(t, mark)
	t.Logf("%d browser events answered 202 while %s was stopped; %d on WEB_EVENTS, %d already gone", len(logged), container, len(queued), missing)
	if len(queued) != len(logged) || missing != 0 {
		t.Fatalf("want all %d accepted events held on WEB_EVENTS, got %d and %d gone", len(logged), len(queued), missing)
	}
	held := s.consumerInfo(t)
	retries := s.replayerSum(t, "replayer_retries_total")
	t.Logf("before restart: consumer pending %d, ack pending %d; replayer retries so far %.0f", held.NumPending, held.NumAckPending, retries)
	s.start(t, container)
	restored = true
	if container == s.Postgres {
		s.waitPostgres(t, 2*time.Minute)
	}
	waitHTTP(t, s.UmamiURL.String()+"/api/heartbeat", 5*time.Minute)
	restarted := time.Now()
	drain := s.Drained(t, 20*time.Minute)
	t.Logf("%s down for %s; lag reached zero %s after it was started again", container, restarted.Sub(stoppedAt).Round(time.Second), drain.Round(time.Second))
	// the replayer's own lag gauge refreshes every 5 s
	deadline := time.Now().Add(20 * time.Second)
	for s.replayerSum(t, "replayer_consumer_pending_messages")+s.replayerSum(t, "replayer_consumer_ack_pending_messages") != 0 {
		if time.Now().After(deadline) {
			t.Errorf("replayer lag gauges did not return to zero")
			break
		}
		time.Sleep(time.Second)
	}
	dlq := s.replayerSum(t, `replayer_outcomes_total{outcome="dlq"}`)
	if dlq != 0 {
		t.Errorf("%.0f events went to the dlq", dlq)
	}

	s.WaitStored(t, siteAstro.ID, len(queued))
	evs := s.Events(t, siteAstro.ID, today())
	want := map[string]int64{} // custom event name to acceptance second
	var pvWant, pvGot []int64
	for _, q := range queued {
		if q.Type == events.TypeEvent {
			want[q.Name] = q.AcceptedAt / 1000
		} else {
			pvWant = append(pvWant, q.AcceptedAt/1000)
		}
	}
	got := map[string]int{}
	minuteMismatch, secondMismatch := 0, 0
	for _, e := range evs {
		if e.EventType == umamiPageview {
			pvGot = append(pvGot, e.CreatedAt.Unix())
			continue
		}
		got[e.Name()]++
		w, ok := want[e.Name()]
		if !ok {
			t.Errorf("umami has unexpected event %q", e.Name())
			continue
		}
		if e.CreatedAt.Unix() != w {
			secondMismatch++
		}
		if e.CreatedAt.Unix()/60 != w/60 {
			minuteMismatch++
		}
	}
	lost, dup := 0, 0
	for name := range want {
		switch got[name] {
		case 0:
			lost++
		case 1:
		default:
			dup += got[name] - 1
		}
	}
	sort.Slice(pvWant, func(i, j int) bool { return pvWant[i] < pvWant[j] })
	sort.Slice(pvGot, func(i, j int) bool { return pvGot[i] < pvGot[j] })
	pvSame := fmt.Sprint(pvWant) == fmt.Sprint(pvGot)
	minutes := map[int64]int{}
	for _, q := range queued {
		minutes[q.AcceptedAt/60000]++
	}
	t.Logf("umami stored %d events: %d of %d custom events (lost %d, duplicated %d), pageviews %d of %d with identical seconds %v; "+
		"custom events off by a second %d, off by a minute bucket %d; acceptance minutes %v",
		len(evs), len(got), len(want), lost, dup, len(pvGot), len(pvWant), pvSame, secondMismatch, minuteMismatch, minutes)
	if len(evs) != len(queued) || lost != 0 || dup != 0 || !pvSame || minuteMismatch != 0 || secondMismatch != 0 {
		t.Errorf("want all %d accepted events once, each at its acceptance second", len(queued))
	}
}

// replayerKill stops both replayers, queues 400 browser events, starts one
// replayer and kills it with SIGKILL while it delivers. the standby takes
// over; nothing may be lost and events stored twice may not exceed what was
// in flight at the kill.
func (su *suite) replayerKill(t *testing.T) {
	s := su.s
	s.ResetSites(t, su.sites(siteAstro))
	a, b := s.Replayers["core-a"], s.Replayers["core-b"]
	s.stop(t, a.Name)
	s.stop(t, b.Name)
	defer func() {
		// docker start of a running container is a no-op
		s.start(t, a.Name)
		s.start(t, b.Name)
		waitHTTP(t, a.OpsURL+"/healthz", time.Minute)
		waitHTTP(t, b.OpsURL+"/healthz", time.Minute)
	}()
	logged := su.burst(t, burstProjects, siteAstro, "kill", 4, 25)
	t.Logf("%d browser events queued with both replayers stopped", len(logged))
	s.start(t, a.Name)
	waitHTTP(t, a.OpsURL+"/healthz", time.Minute)
	// wait for a to lead before b starts, so b is the standby
	deadline := time.Now().Add(2 * time.Minute)
	for mustScrape(t, a.OpsURL)["replayer_leader"] != 1 {
		if time.Now().After(deadline) {
			t.Fatal("core-a never took the lock")
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.start(t, b.Name)
	waitHTTP(t, b.OpsURL+"/healthz", time.Minute)
	var last map[string]float64
	for {
		m, err := scrape(a.OpsURL)
		if err != nil {
			t.Fatal(err)
		}
		last = m
		if m[`replayer_outcomes_total{outcome="delivered"}`] >= 100 {
			break
		}
		if time.Now().After(deadline.Add(3 * time.Minute)) {
			t.Fatalf("core-a delivered only %.0f events", m[`replayer_outcomes_total{outcome="delivered"}`])
		}
		time.Sleep(10 * time.Millisecond)
	}
	mustDocker(t, "kill", "-s", "KILL", a.Name)
	info := s.consumerInfo(t)
	delivered := last[`replayer_outcomes_total{outcome="delivered"}`]
	t.Logf("killed core-a after %.0f deliveries (last scrape: in flight %.0f); consumer right after the kill: ack pending %d, pending %d",
		delivered, last["replayer_in_flight_messages"], info.NumAckPending, info.NumPending)
	if delivered >= float64(len(logged)) {
		t.Fatalf("core-a finished before the kill; the check needs a kill mid-run")
	}
	inFlight := info.NumAckPending
	drain := s.Drained(t, 10*time.Minute)
	bm := mustScrape(t, b.OpsURL)
	t.Logf("core-b led %v after the kill and drained the rest %s after it: delivered %.0f, duplicate_skipped %.0f",
		bm["replayer_leader"] == 1, drain.Round(time.Second), bm[`replayer_outcomes_total{outcome="delivered"}`], bm[`replayer_outcomes_total{outcome="duplicate_skipped"}`])

	s.WaitStored(t, siteAstro.ID, len(logged))
	evs := s.Events(t, siteAstro.ID, today())
	names := map[string]int{}
	pv := 0
	for _, e := range evs {
		if e.EventType == umamiPageview {
			pv++
		} else {
			names[e.Name()]++
		}
	}
	lost, dup := 0, 0
	for _, e := range logged {
		if e.Body.T == "ev" && names[e.Body.N] == 0 {
			lost++
		}
	}
	for _, n := range names {
		if n > 1 {
			dup += n - 1
		}
	}
	if pv < 16 {
		lost += 16 - pv
	} else {
		dup += pv - 16
	}
	t.Logf("umami: %d rows for %d accepted events, %d distinct custom events, %d pageviews of 16; lost %d, stored twice %d, in flight at the kill %d",
		len(evs), len(logged), len(names), pv, lost, dup, inFlight)
	if lost != 0 {
		t.Errorf("%d events lost", lost)
	}
	if dup > inFlight {
		t.Errorf("%d duplicates exceed the %d events in flight at the kill", dup, inFlight)
	}
}

func (su *suite) quarantineBand(t *testing.T) {
	s, b := su.s, su.b
	s.ResetSites(t, su.sites(siteSPA))
	s.Route(t, "JP")
	b.MustRun(t, []string{"chromium"}, "@quarantine")
	s.Route(t, "DE")
	logged := b.NewEvents(t)
	all202(t, logged)
	s.Drained(t, 2*time.Minute)
	s.WaitStored(t, siteSPA.ID, 38)
	s.WaitStored(t, su.quarantine[siteSPA.ID], 6)
	names := func(id string) map[string]int {
		return countBy(s.Events(t, id, today()), func(e UmamiEvent) string {
			if e.EventType == umamiPageview {
				return "pageview"
			}
			return e.Name()
		})
	}
	primary, quar := names(siteSPA.ID), names(su.quarantine[siteSPA.ID])
	t.Logf("%d events from 22 profiles on one address; primary website: %s; quarantine website: %s", len(logged), joinKeys(primary), joinKeys(quar))
	for n := 1; n <= 22; n++ {
		name := "q-" + strconv.Itoa(n)
		inQ := n >= 20
		if inQ && (quar[name] != 1 || primary[name] != 0) {
			t.Errorf("%s: primary %d, quarantine %d, want only the quarantine website", name, primary[name], quar[name])
		}
		if !inQ && (primary[name] != 1 || quar[name] != 0) {
			t.Errorf("%s: primary %d, quarantine %d, want only the primary website", name, primary[name], quar[name])
		}
	}
	if primary["pageview"] != 19 || quar["pageview"] != 3 {
		t.Errorf("pageviews primary %d quarantine %d, want 19 and 3", primary["pageview"], quar["pageview"])
	}
}

func (su *suite) uiEgress(t *testing.T) {
	s, b := su.s, su.b
	ctx := context.Background()
	pw := secret()
	u, err := s.Admin.CreateUser(ctx, umami.NewUser{Username: "gh-webpath-" + s.ID, Password: pw, Role: "view-only"})
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
	sniff := s.StartSniffer(t, su.sniffBin, "ui")
	out, runErr := b.Run(t, []string{"umami-ui"}, "@ui", nil,
		"WEBPATH_UMAMI_URL="+s.UmamiURL.String(), "WEBPATH_UMAMI_TOKEN="+sess.Token.Reveal(),
		"WEBPATH_WEBSITE="+siteRR.ID, "WEBPATH_TEAM="+s.TeamID)
	lines := s.StopSniffer(t, sniff)
	t.Logf("ui session:\n%s", lastLines(out, 6))
	if runErr != nil {
		t.Errorf("ui session failed: %v\n%s", runErr, out)
	}
	if ui, err := os.ReadFile(filepath.Join(b.OutDir, "ui.json")); err == nil {
		t.Logf("browser side: %s", strings.Join(strings.Fields(string(ui)), " "))
	}
	reportEgress(t, classify(lines, s.Subnet))
}

func (su *suite) secondVisit(t *testing.T) {
	s, b := su.s, su.b
	// the gap must exceed 30 minutes (the replayer drops the cache token)
	// and end in another utc hour (without it umami joins events of the hour
	// the visit started in)
	target := su.s1End.Add(31 * time.Minute)
	if h := su.s1End.Truncate(time.Hour).Add(time.Hour + 5*time.Second); h.After(target) {
		target = h
	}
	if d := time.Until(target); d > 0 {
		t.Logf("waiting %s until %s for the second visit", d.Round(time.Second), target.Format(time.TimeOnly))
		time.Sleep(d)
	}
	b.MustRun(t, []string{"chromium"}, "@s1-visit2")
	all202(t, b.NewEvents(t))
	s.Drained(t, 2*time.Minute)
	s.WaitStored(t, siteStatic.ID, 6)
	st := s.stats(t, siteStatic.ID, today())
	t.Logf("static after the second visit (%s after the first): %+v", time.Since(su.s1End).Round(time.Second), st)
	want := umami.StatsValues{Pageviews: 6, Visitors: 2, Visits: 3, Bounces: 1}
	st.Totaltime = 0
	if st != want {
		t.Errorf("stats %+v, want %+v", st, want)
	}
}

func (su *suite) privacy(t *testing.T, cs ...*CountryNet) {
	s := su.s
	dump := mustDocker(t, "exec", s.Postgres, "pg_dump", "-U", "postgres", "-d", "umami", "--data-only", "--no-owner")
	tables := strings.Count(dump, "\nCOPY public.")
	t.Logf("pg_dump of database umami: %d bytes, %d tables with COPY data", len(dump), tables)
	if tables == 0 {
		t.Fatal("the dump holds no table data")
	}
	hits := 0
	for _, c := range cs {
		for _, needle := range []string{c.Client.String(), c.Subnet.Masked().Addr().String()} {
			if n := strings.Count(dump, needle); n > 0 {
				hits += n
				t.Errorf("%s client address material %s appears %d times", c.Code, needle, n)
			}
		}
		b := c.Client.As4()
		prefix := fmt.Sprintf("%d.%d.%d.", b[0], b[1], b[2])
		t.Logf("%s client %s: %d hits; /24 prefix %s: %d hits", c.Code, c.Client, strings.Count(dump, c.Client.String()), prefix, strings.Count(dump, prefix))
	}
	t.Logf("constant replayer address %s: %d hits", umami.EdgeClientIP, strings.Count(dump, umami.EdgeClientIP))
	if hits != 0 {
		t.Errorf("%d hits of test client addresses in the umami database", hits)
	}
	n := s.psql(t, "select count(*) from session where region is not null or city is not null")
	total := s.psql(t, "select count(*) from session")
	t.Logf("sessions: %s, with region or city: %s", total, n)
	if n != "0" {
		t.Errorf("%s session rows carry a region or city", n)
	}
}
