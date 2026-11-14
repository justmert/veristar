//go:build umamicompat

package umamicompat

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/justmert/veristar/internal/umami"
)

// timestamp in seconds sets the time of the event and of a
// new session, so a replayed backlog lands in its original buckets.
func TestTimestampSecondsStored(t *testing.T) {
	e := setup(t)
	_, w := e.site(t)
	ts := time.Now().UTC().Add(-50 * time.Hour).Truncate(time.Second)

	res := e.send(t, umami.SendRequest{Event: pageview(w, ts, "ts-visitor")})
	s := e.session(t, w, res.SessionID)
	for name, got := range map[string]string{"firstAt": s.FirstAt, "lastAt": s.LastAt} {
		at, err := time.Parse(time.RFC3339Nano, got)
		if err != nil {
			t.Fatalf("%s %q: %v", name, got, err)
		}
		if !at.Equal(ts) {
			t.Errorf("session %s = %s, want %s", name, at, ts)
		}
	}

	var activity []struct {
		CreatedAt string `json:"createdAt"`
		VisitID   string `json:"visitId"`
	}
	path := fmt.Sprintf("/api/websites/%s/sessions/%s/activity?startAt=%d&endAt=%d",
		w, res.SessionID, ts.Add(-time.Hour).UnixMilli(), ts.Add(time.Hour).UnixMilli())
	if code := e.get(t, e.token, path, &activity); code != http.StatusOK {
		t.Fatalf("activity: http %d", code)
	}
	if len(activity) != 1 {
		t.Fatalf("activity has %d events, want 1", len(activity))
	}
	at, err := time.Parse(time.RFC3339Nano, activity[0].CreatedAt)
	if err != nil || !at.Equal(ts) {
		t.Fatalf("event createdAt %q, want %s (%v)", activity[0].CreatedAt, ts, err)
	}

	// the stored time drives the reports: the minute of ts holds the view, the
	// last hour holds nothing
	st, err := e.admin.Stats(ctx(t), w, umami.Range{Start: ts.Add(-time.Minute), End: ts.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if st.Pageviews != 1 {
		t.Errorf("pageviews around ts = %d, want 1", st.Pageviews)
	}
	now := time.Now()
	st, err = e.admin.Stats(ctx(t), w, umami.Range{Start: now.Add(-time.Hour), End: now})
	if err != nil {
		t.Fatal(err)
	}
	if st.Pageviews != 0 {
		t.Errorf("pageviews in the last hour = %d, want 0", st.Pageviews)
	}
}

// with ENABLE_CLIENT_SALT=1 the salt splits sessions, and
// the same salt, user agent and website give one session.
func TestClientSaltSplitsSessions(t *testing.T) {
	e := setup(t)
	_, w := e.site(t)
	ts := time.Now().UTC().Add(-2 * time.Minute)

	a1 := e.send(t, umami.SendRequest{Event: pageview(w, ts, "salt-a")})
	a2 := e.send(t, umami.SendRequest{Event: pageview(w, ts.Add(time.Second), "salt-a")})
	b := e.send(t, umami.SendRequest{Event: pageview(w, ts.Add(2*time.Second), "salt-b")})
	if a1.SessionID != a2.SessionID {
		t.Errorf("same salt gave sessions %s and %s", a1.SessionID, a2.SessionID)
	}
	if a1.SessionID == b.SessionID {
		t.Errorf("different salts gave one session %s", b.SessionID)
	}
	if got := e.sessions(t, w).Count; got != 2 {
		t.Errorf("website has %d sessions, want 2", got)
	}
}

// the constant TEST-NET-1 address with cf-ipcountry stores
// that country and no region or city.
func TestCountryHeaderWithTestNetAddress(t *testing.T) {
	e := setup(t)
	_, w := e.site(t)
	res := e.send(t, umami.SendRequest{Event: pageview(w, time.Now(), "geo-de"), Country: "DE"})
	s := e.session(t, w, res.SessionID)
	if str(s.Country) != "DE" || s.Region != nil || s.City != nil {
		t.Fatalf("session geo = country %s region %s city %s, want DE, null, null",
			str(s.Country), str(s.Region), str(s.City))
	}
}

// stop rule: with no country header, 192.0.2.1 must give 200 and a null
// country. a 500 here invalidates the replayer's design (docs/reference/umami-contract.md).
func TestNoCountryHeaderStoresNullCountry(t *testing.T) {
	e := setup(t)
	_, w := e.site(t)
	code, body := e.rawSend(t, map[string]string{
		"User-Agent":             chromeUA,
		umami.EdgeClientIPHeader: umami.EdgeClientIP,
	}, map[string]any{"type": "event", "payload": pageview(w, time.Now(), "geo-none")})
	if code != http.StatusOK {
		t.Fatalf("STOP: the replayer design depends on this. /api/send answered %d %s", code, body)
	}
	p := e.sessions(t, w)
	if p.Count != 1 {
		t.Fatalf("website has %d sessions, want 1", p.Count)
	}
	s := p.Data[0]
	if s.Country != nil || s.Region != nil || s.City != nil {
		t.Fatalf("session geo = %s %s %s, want all null", str(s.Country), str(s.Region), str(s.City))
	}
}

// a body ip disables the country headers (src/lib/detect.ts getClientInfo),
// which is why the replayer sends the address only as a header.
func TestPayloadIPIgnoresCountryHeader(t *testing.T) {
	e := setup(t)
	_, w := e.site(t)
	ev := pageview(w, time.Now(), "geo-payload-ip")
	payload := map[string]any{
		"website": ev.Website, "hostname": ev.Hostname, "url": ev.URL,
		"timestamp": ev.Timestamp, "salt": ev.Salt, "ip": umami.EdgeClientIP,
	}
	code, body := e.rawSend(t, map[string]string{"User-Agent": chromeUA, umami.CountryHeader: "DE"},
		map[string]any{"type": "event", "payload": payload})
	if code != http.StatusOK {
		t.Fatalf("send: %d %s", code, body)
	}
	p := e.sessions(t, w)
	if p.Count != 1 || p.Data[0].Country != nil {
		t.Fatalf("payload ip with country header: %d sessions, country %v; want 1 session, null country", p.Count, p.Data)
	}
}

// umami's isbot check answers 200 {"beep":"boop"} and stores nothing.
func TestBotUserAgentStoresNothing(t *testing.T) {
	e := setup(t)
	_, w := e.site(t)
	res := e.send(t, umami.SendRequest{
		Event:     pageview(w, time.Now(), "bot"),
		UserAgent: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
	})
	if !res.Bot {
		t.Fatalf("googlebot was not classified as a bot: %+v", res)
	}
	code, body := e.rawSend(t, map[string]string{
		"User-Agent": "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
	}, map[string]any{"type": "event", "payload": pageview(w, time.Now(), "bot2")})
	if code != http.StatusOK || strings.TrimSpace(body) != `{"beep":"boop"}` {
		t.Fatalf("bingbot answer = %d %s, want 200 {\"beep\":\"boop\"}", code, body)
	}
	if got := e.sessions(t, w).Count; got != 0 {
		t.Fatalf("bot traffic stored %d sessions", got)
	}
	now := time.Now()
	st, err := e.admin.Stats(ctx(t), w, umami.Range{Start: now.Add(-time.Hour), End: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if st.Pageviews != 0 {
		t.Fatalf("bot traffic stored %d pageviews", st.Pageviews)
	}
}

// the cache token keeps the visit across utc hours; without
// it the visit id is derived from the session and the utc hour of the event.
func TestVisitIDCacheAndHourRule(t *testing.T) {
	e := setup(t)
	_, w := e.site(t)
	// 10:00 utc yesterday: every event lies in the past, and all of them share
	// one utc day, so the daily salt (SALT_ROTATION=daily) cannot change the session
	y := time.Now().UTC().AddDate(0, 0, -1)
	hour := time.Date(y.Year(), y.Month(), y.Day(), 10, 0, 0, 0, time.UTC)

	first := e.send(t, umami.SendRequest{Event: pageview(w, hour.Add(10*time.Minute), "visit")})

	// same hour, no cache, 45 minutes later: still the same visit, because the
	// id comes from session and hour and the 30 minute expiry is skipped for
	// events with a timestamp
	sameHour := e.send(t, umami.SendRequest{Event: pageview(w, hour.Add(55*time.Minute), "visit")})
	if sameHour.SessionID != first.SessionID {
		t.Fatalf("session changed within one utc day: %s vs %s", first.SessionID, sameHour.SessionID)
	}
	if sameHour.VisitID != first.VisitID {
		t.Errorf("same utc hour without cache: visit %s, want %s", sameHour.VisitID, first.VisitID)
	}

	// next hour, no cache: new visit
	nextHour := e.send(t, umami.SendRequest{Event: pageview(w, hour.Add(65*time.Minute), "visit")})
	if nextHour.VisitID == first.VisitID {
		t.Errorf("next utc hour without cache kept visit %s", first.VisitID)
	}

	// next hour with the first answer's cache: visit kept
	cached := e.send(t, umami.SendRequest{Event: pageview(w, hour.Add(70*time.Minute), "visit"), Cache: first.Cache})
	if cached.VisitID != first.VisitID || cached.SessionID != first.SessionID {
		t.Errorf("with cache: visit %s session %s, want %s %s", cached.VisitID, cached.SessionID, first.VisitID, first.SessionID)
	}

	st, err := e.admin.Stats(ctx(t), w, umami.Range{Start: hour, End: hour.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if st.Pageviews != 4 || st.Visits != 2 || st.Visitors != 1 {
		t.Errorf("stats = %+v, want 4 pageviews, 2 visits, 1 visitor", st.StatsValues)
	}
}

// a cache token of another website is ignored, so a replayer bug cannot join
// visits across websites.
func TestCacheTokenBoundToWebsite(t *testing.T) {
	e := setup(t)
	_, w1 := e.site(t)
	_, w2 := e.site(t)
	ts := time.Now().UTC().Add(-time.Minute)
	r1 := e.send(t, umami.SendRequest{Event: pageview(w1, ts, "x")})
	r2 := e.send(t, umami.SendRequest{Event: pageview(w2, ts, "x"), Cache: r1.Cache})
	if r2.SessionID == r1.SessionID || r2.VisitID == r1.VisitID {
		t.Fatalf("cache of website 1 reused on website 2: %+v", r2)
	}
}
