//go:build webpath

package webpath

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/coverage"
	"github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/registry"
)

// TestPageSignals is the browser check, on the current tracker release, of
// what 1.3.0 introduced: the counting rules
// (background tab, back/forward cache, history navigation, prerendering),
// the frame flag and the never-visible notice in chromium, firefox and
// webkit through caddy, the gateway, nats, the replayer and umami. it checks
// what the gateway counted on EDGE_STATS, including the tracker file
// counters of the coverage estimate, and that umami still derives browser,
// os and device for events without screen and language. run it alone with
// `go test -tags webpath -run TestPageSignals ./tests/webpath/`.
func TestPageSignals(t *testing.T) {
	s := newStack(t)
	fwdBin := buildTool(t, "tests/webpath/fwd")
	s.StartNATS(t)
	s.StartUmami(t)
	sites := []exampleSite{siteStatic, siteSPA}
	for _, x := range sites {
		quarantine := uuid.NewString()
		s.AddWebsite(t, x.ID, x.Key, x.Host)
		s.AddWebsite(t, quarantine, x.Key+" quarantine", x.Host)
		s.PutSite(t, registry.Site{ID: x.ID, TenantID: uuid.NewString(), Status: registry.StatusActive,
			Origins: []string{"https://" + x.Host}, QuarantineWebsiteID: quarantine})
	}
	s.StartEdge(t)
	s.AddCountry(t, "DE", countrySubnets(t, "DE")["DE"])
	s.StartForwarder(t, fwdBin, "DE")
	for _, x := range sites {
		waitChallenge(t, s.FwdAddr, x)
	}
	b := startBrowsersWith(t, s.FwdAddr, []siteRun{
		{Site: siteStatic, Version: currentTracker(t), Build: "build"},
		{Site: siteSPA, Version: currentTracker(t), Build: "build"},
	})
	// whatever the warm-up counted is not part of the run
	s.edgeStats(t)

	projects := []string{"signals-chromium", "signals-firefox", "signals-webkit"}
	b.MustRun(t, projects, "@signals$|@signals ")
	b.MustRun(t, []string{"signals-chromium"}, "@signals-prerender")
	logged := b.NewEvents(t)
	all202(t, logged)

	byType := map[string]int{}
	framed := 0
	for _, e := range logged {
		byType[e.Body.T]++
		if e.Body.Fr {
			framed++
		}
	}
	t.Logf("events the browsers sent: %v, framed %d", byType, framed)

	// the gateway reports its counters every 10 s
	time.Sleep(12 * time.Second)
	st := s.edgeStats(t)
	count := func(x exampleSite, outcome string) int64 { return st[[2]string{x.ID, outcome}] }
	for _, x := range sites {
		t.Logf("%s: accepted %d, signal_views %d, signal_framed %d, signal_never_visible %d, script_requests %d, script_gpc %d",
			x.Key, count(x, "accepted"), count(x, integrity.SignalViews), count(x, integrity.SignalFramed),
			count(x, integrity.SignalNeverVisible), count(x, coverage.ScriptRequests), count(x, coverage.ScriptGPC))
	}

	t.Run("never-visible notices are counted, not queued", func(t *testing.T) {
		// one per engine
		if got := count(siteStatic, integrity.SignalNeverVisible); got != 3 {
			t.Errorf("static never-visible notices %d, want 3", got)
		}
		if byType["nv"] != 3 {
			t.Errorf("the browsers sent %d notices, want 3", byType["nv"])
		}
	})

	t.Run("framed page views are counted", func(t *testing.T) {
		if got := count(siteSPA, integrity.SignalFramed); got != 3 {
			t.Errorf("spa framed views %d, want 3 (one per engine)", got)
		}
		if framed != 3 {
			t.Errorf("the browsers sent %d framed events, want 3", framed)
		}
		if count(siteStatic, integrity.SignalFramed) != 0 {
			t.Error("a top-level page was counted as framed")
		}
	})

	t.Run("every page view is a signal view", func(t *testing.T) {
		for _, x := range sites {
			// the sites send only page views: accepted equals signal views
			if count(x, integrity.SignalViews) != count(x, "accepted") {
				t.Errorf("%s: signal views %d, accepted %d", x.Key, count(x, integrity.SignalViews), count(x, "accepted"))
			}
		}
		// static: background tab 1, cache restore 3 in chromium and
		// firefox, 2 in webkit: 3 + 3 + 2 + 3 = 11. spa: history 3 per
		// engine, frame and top level 2 per engine, prerender 1: 16
		if got := count(siteStatic, integrity.SignalViews); got != 11 {
			t.Errorf("static signal views %d, want 11", got)
		}
		if got := count(siteSPA, integrity.SignalViews); got != 16 {
			t.Errorf("spa signal views %d, want 16", got)
		}
	})

	t.Run("tracker file requests are attributed to the site", func(t *testing.T) {
		for _, x := range sites {
			if count(x, coverage.ScriptRequests) == 0 {
				t.Errorf("%s: no tracker file request counted", x.Key)
			}
			if count(x, coverage.ScriptGPC) != 0 {
				t.Errorf("%s: %d gpc requests from browsers without gpc", x.Key, count(x, coverage.ScriptGPC))
			}
		}
	})

	s.AddReplayer(t, "core-a")
	s.Drained(t, 5*time.Minute)

	t.Run("umami derives browser, os and device without screen and language", func(t *testing.T) {
		evs := s.Events(t, siteStatic.ID, runRange())
		if len(evs) == 0 {
			t.Fatal("no static events in umami")
		}
		browsers := map[string]int{}
		for _, e := range evs {
			if e.Browser == nil || *e.Browser == "" || e.OS == nil || *e.OS == "" || e.Device == nil || *e.Device == "" {
				t.Errorf("event %s lacks browser, os or device: %v %v %v", e.ID, e.Browser, e.OS, e.Device)
				continue
			}
			browsers[*e.Browser+"/"+*e.Device]++
		}
		t.Logf("umami browser/device of the static events: %s", joinKeys(browsers))
		for _, want := range []string{"chrome/desktop", "firefox/desktop", "safari/desktop"} {
			found := false
			for k := range browsers {
				if strings.HasPrefix(k, want) {
					found = true
				}
			}
			if !found {
				t.Errorf("no %s event in umami", want)
			}
		}
		if n := s.psql(t, `select count(*) from website_event where website_id = '`+siteStatic.ID+`' and coalesce(page_title, '') <> ''`); n != "0" {
			t.Errorf("%s static events carry a title", n)
		}
	})
}

// TestConsentGatedTrack is the browser check, on the current tracker release,
// of the consent mode 1.3.1 introduced in chromium,
// firefox and webkit through caddy, the gateway, nats, the replayer and
// umami: with data-auto-track="false", umami.track() before umami.start()
// sends nothing and fetches no challenge, and after the consent click the
// pageview and a later umami.track() are accepted. run it alone with
// `go test -tags webpath -run TestConsentGatedTrack ./tests/webpath/`.
func TestConsentGatedTrack(t *testing.T) {
	s := newStack(t)
	fwdBin := buildTool(t, "tests/webpath/fwd")
	s.StartNATS(t)
	s.StartUmami(t)
	quarantine := uuid.NewString()
	s.AddWebsite(t, siteStatic.ID, siteStatic.Key, siteStatic.Host)
	s.AddWebsite(t, quarantine, siteStatic.Key+" quarantine", siteStatic.Host)
	s.PutSite(t, registry.Site{ID: siteStatic.ID, TenantID: uuid.NewString(), Status: registry.StatusActive,
		Origins: []string{"https://" + siteStatic.Host}, QuarantineWebsiteID: quarantine})
	s.StartEdge(t)
	s.AddCountry(t, "DE", countrySubnets(t, "DE")["DE"])
	s.StartForwarder(t, fwdBin, "DE")
	waitChallenge(t, s.FwdAddr, siteStatic)
	b := startBrowsersWith(t, s.FwdAddr, []siteRun{{Site: siteStatic, Version: currentTracker(t), Build: "build"}})
	b.MustRun(t, []string{"signals-chromium", "signals-firefox", "signals-webkit"}, "@consent")
	logged := b.NewEvents(t)
	all202(t, logged)
	byName := map[string]int{}
	for _, e := range logged {
		byName[e.Body.T+":"+e.Body.N]++
	}
	// one pageview and one custom event per engine, nothing from before
	if byName["pv:"] != 3 || byName["ev:after-consent"] != 3 || len(byName) != 2 {
		t.Errorf("events the browsers sent: %v, want 3 pageviews and 3 after-consent events", byName)
	}
}
