package umami

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// a desktop chrome user agent that umami's isbot check accepts
const testUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

func TestSendValidates(t *testing.T) {
	c, err := New(&url.URL{Scheme: "http", Host: "umami.invalid"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok := SendRequest{Event: Event{Website: "w", Timestamp: 1}, UserAgent: testUA}
	cases := map[string]func(*SendRequest){
		"no website":    func(r *SendRequest) { r.Event.Website = "" },
		"no timestamp":  func(r *SendRequest) { r.Event.Timestamp = 0 },
		"no user agent": func(r *SendRequest) { r.UserAgent = "" },
		"long salt": func(r *SendRequest) {
			b := make([]byte, 101)
			for i := range b {
				b[i] = 'a'
			}
			r.Event.Salt = string(b)
		},
		"lower country": func(r *SendRequest) { r.Country = "de" },
		"long country":  func(r *SendRequest) { r.Country = "DEU" },
	}
	for name, mut := range cases {
		r := ok
		mut(&r)
		// validation fails before any request, so the invalid host is never dialled
		if _, err := c.Send(context.Background(), r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestQueryPaths(t *testing.T) {
	r := Range{Start: time.UnixMilli(1000), End: time.UnixMilli(2000)}
	p, err := metricsPath("w", MetricsQuery{Range: r, Type: "referrer", Limit: 5})
	if err != nil || p != "/api/websites/w/metrics?endAt=2000&limit=5&startAt=1000&type=referrer" {
		t.Errorf("metrics path %q %v", p, err)
	}
	if _, err := metricsPath("w", MetricsQuery{Range: r, Type: "bogus"}); err == nil {
		t.Error("unknown metric type accepted")
	}
	if _, err := statsPath("w", Range{Start: r.End, End: r.Start}); err == nil {
		t.Error("reversed range accepted")
	}
	if _, err := pageviewsPath("w", PageviewsQuery{Range: r, Unit: "week", Timezone: "UTC"}); err == nil {
		t.Error("unknown unit accepted")
	}
	if _, err := pageviewsPath("w", PageviewsQuery{Range: r, Unit: UnitDay, Timezone: "Mars/Base"}); err == nil {
		t.Error("unknown timezone accepted")
	}
}

func liveAdmin(t *testing.T) (*Client, *Admin) {
	t.Helper()
	c := liveClient(t)
	pw := os.Getenv("UMAMI_ADMIN_PASSWORD")
	if pw == "" {
		t.Skip("UMAMI_ADMIN_PASSWORD not set; run make test-integration against the dev stack")
	}
	a, err := NewAdmin(c, SeededUsername, pw)
	if err != nil {
		t.Fatal(err)
	}
	return c, a
}

func liveCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// the full provisioning lifecycle the control plane runs, then the
// collection and read calls the replayer and the aggregates job run. every
// object it creates is deleted again.
func TestLiveLifecycle(t *testing.T) {
	c, a := liveAdmin(t)
	ctx := liveCtx(t)

	name := "it-" + uuid.NewString()[:12]
	pw := uuid.NewString()
	u, err := a.CreateUser(ctx, NewUser{ID: uuid.NewString(), Username: name, Password: pw, Role: RoleViewOnly})
	if err != nil {
		t.Fatal(err)
	}
	userGone := false
	t.Cleanup(func() {
		if !userGone {
			_ = a.DeleteUser(context.Background(), u.ID)
		}
	})
	if u.Username != name || u.Role != RoleViewOnly {
		t.Fatalf("created user %+v", u)
	}
	if _, err := a.CreateUser(ctx, NewUser{Username: name, Password: pw, Role: RoleViewOnly}); err == nil {
		t.Fatal("duplicate username accepted")
	}

	// an admin password change makes the old token useless (tokens carry a
	// hash of the password), which is the daily rotation in the design
	old, err := c.Login(ctx, name, pw)
	if err != nil {
		t.Fatal(err)
	}
	pw2 := uuid.NewString()
	if _, err := a.UpdateUser(ctx, u.ID, UserUpdate{Password: pw2}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Login(ctx, name, pw); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old password after update: %v", err)
	}
	viewer, err := c.Login(ctx, name, pw2)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r := Range{Start: now.Add(-time.Hour), End: now.Add(time.Minute)}

	team, owner, err := a.CreateTeam(ctx, "it-"+name, "")
	if err != nil {
		t.Fatal(err)
	}
	teamGone := false
	t.Cleanup(func() {
		if !teamGone {
			_ = a.DeleteTeam(context.Background(), team.ID)
		}
	})
	if owner.TeamID != team.ID || owner.Role != TeamRoleOwner {
		t.Fatalf("owner membership %+v", owner)
	}
	if _, err := a.AddTeamUser(ctx, team.ID, u.ID, TeamRoleViewOnly); err != nil {
		t.Fatal(err)
	}

	siteID := uuid.NewString()
	w, err := a.CreateWebsite(ctx, NewWebsite{ID: siteID, Name: "it", Domain: "it.example", TeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != siteID || w.TeamID != team.ID {
		t.Fatalf("website %+v", w)
	}
	if _, err := a.CreateWebsite(ctx, NewWebsite{ID: siteID, Name: "it", Domain: "it.example", TeamID: team.ID}); err == nil {
		t.Fatal("duplicate website id accepted")
	}

	// the old token was invalidated by the password change
	if _, err := c.Stats(ctx, old.Token, siteID, r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stats with pre-change token: %v", err)
	}

	ts := now.Add(-2 * time.Minute).Unix()
	first, err := c.Send(ctx, SendRequest{
		Event: Event{
			Website: siteID, Hostname: "it.example", URL: "/one", Referrer: "https://ref.example/a",
			Title: "one", Screen: "1920x1080", Language: "en-US", Timestamp: ts, Salt: "vt-1",
		},
		UserAgent: testUA, Country: "DE",
	})
	if err != nil || first.Bot {
		t.Fatalf("send: %+v %v", first, err)
	}
	second, err := c.Send(ctx, SendRequest{
		Event:     Event{Website: siteID, Hostname: "it.example", URL: "/two", Timestamp: ts + 30, Salt: "vt-1"},
		UserAgent: testUA, Country: "DE", Cache: first.Cache,
	})
	if err != nil || second.VisitID != first.VisitID || second.SessionID != first.SessionID {
		t.Fatalf("second send: %+v %v", second, err)
	}
	bot, err := c.Send(ctx, SendRequest{
		Event:     Event{Website: siteID, URL: "/bot", Timestamp: ts},
		UserAgent: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
	})
	if err != nil || !bot.Bot {
		t.Fatalf("bot send: %+v %v", bot, err)
	}
	_, err = c.Send(ctx, SendRequest{Event: Event{Website: uuid.NewString(), URL: "/", Timestamp: ts}, UserAgent: testUA})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("send to unknown website: want 400, got %v", err)
	}

	st, err := a.Stats(ctx, siteID, r)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pageviews != 2 || st.Visitors != 1 || st.Visits != 1 || st.Bounces != 0 || st.Totaltime != 30 {
		t.Errorf("stats %+v", st.StatsValues)
	}
	// umami calls a desktop user agent with a screen up to 1920 wide a laptop
	for typ, want := range map[string]string{"referrer": "ref.example", "country": "DE", "browser": "chrome", "device": "laptop"} {
		rows, err := a.Metrics(ctx, siteID, MetricsQuery{Range: r, Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].X != want {
			t.Errorf("metrics %s = %+v, want %s", typ, rows, want)
		}
	}
	pv, err := a.Pageviews(ctx, siteID, PageviewsQuery{Range: r, Unit: UnitHour, Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, p := range pv.Pageviews {
		total += p.Y
	}
	if total != 2 {
		t.Errorf("pageviews series sums to %d: %+v", total, pv)
	}
	// the viewer reads the same numbers with its own token
	vst, err := c.Stats(ctx, viewer.Token, siteID, r)
	if err != nil || vst.StatsValues != st.StatsValues {
		t.Errorf("viewer stats %+v %v", vst.StatsValues, err)
	}

	if err := a.DeleteSession(ctx, siteID, first.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteSession(ctx, siteID, first.SessionID); !isStatus(err, http.StatusNotFound) {
		t.Fatalf("second session delete: want 404, got %v", err)
	}
	if st, err = a.Stats(ctx, siteID, r); err != nil || st.Pageviews != 0 {
		t.Fatalf("after session delete: %+v %v", st.StatsValues, err)
	}

	if _, err := c.Send(ctx, SendRequest{
		Event: Event{Website: siteID, URL: "/three", Timestamp: ts, Salt: "vt-2"}, UserAgent: testUA,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetWebsite(ctx, siteID); err != nil {
		t.Fatal(err)
	}
	if st, err = a.Stats(ctx, siteID, r); err != nil || st.Pageviews != 0 {
		t.Fatalf("after reset: %+v %v", st.StatsValues, err)
	}

	if err := a.RemoveTeamUser(ctx, team.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stats(ctx, viewer.Token, siteID, r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("removed member reading stats: %v", err)
	}
	if err := a.DeleteWebsite(ctx, siteID); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteTeam(ctx, team.ID); err != nil {
		t.Fatal(err)
	}
	teamGone = true
	if err := a.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	userGone = true
	if _, err := c.Login(ctx, name, pw2); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted user can still log in: %v", err)
	}
}

func isStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// a token that umami no longer accepts is replaced by a fresh login. the
// stale token here is a real one, made stale the way it happens in
// production: a password change of the account.
func TestLiveAdminReloginOn401(t *testing.T) {
	c, a := liveAdmin(t)
	ctx := liveCtx(t)

	// a separate admin account, so the shared admin password is never touched
	name := "it-admin-" + uuid.NewString()[:8]
	pw := uuid.NewString()
	u, err := a.CreateUser(ctx, NewUser{Username: name, Password: pw, Role: RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.DeleteUser(context.Background(), u.ID) })

	b, err := NewAdmin(c, name, pw)
	if err != nil {
		t.Fatal(err)
	}
	team, _, err := b.CreateTeam(ctx, "it-relogin", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.DeleteTeam(context.Background(), team.ID) })
	b.mu.Lock()
	before := b.tok
	b.mu.Unlock()

	// changing the password invalidates b's token. b is then given the new
	// password, which is what a rotated secret looks like to the client.
	pw2 := uuid.NewString()
	if _, err := a.UpdateUser(ctx, u.ID, UserUpdate{Password: pw2}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.password = pw2
	b.mu.Unlock()

	w, err := b.CreateWebsite(ctx, NewWebsite{ID: uuid.NewString(), Name: "it", Domain: "it.example", TeamID: team.ID})
	if err != nil {
		t.Fatalf("call after token invalidation: %v", err)
	}
	if w.TeamID != team.ID {
		t.Fatalf("website %+v", w)
	}
	b.mu.Lock()
	after := b.tok
	b.mu.Unlock()
	if after == before {
		t.Fatal("admin did not log in again")
	}
}
