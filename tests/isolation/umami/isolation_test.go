//go:build isolation

// Package umamiisolation checks tenant isolation inside umami. two teams, two
// websites and two view-only users are provisioned the way the control plane
// does it, then user b calls every operation of umami's openapi contract with team a's ids,
// once on the private network and once through the web caddy, and must never
// get team a's data or change it.
package umamiisolation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

// a desktop chrome user agent that umami's isbot check accepts
const chromeUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

type member struct {
	id, name, password string
	token              umami.Token
}

// world is the provisioned state. ids maps each openapi path parameter to
// the team a entity it names.
type world struct {
	s     *umamictr.Stack
	web   *umamictr.Web
	admin umami.Token

	teamA, teamB string
	siteA, siteB string
	a, b         member
	ids          map[string]string
	// every string that only team a's data contains
	markers []string
	// a fixed range, so the same read gives the same answer before and
	// after b's writes
	start, end time.Time
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return c
}

type reply struct {
	status int
	body   string
}

// call sends one request to umami on the private network, or through the
// web caddy when edge is set.
func (w *world) call(t *testing.T, edge bool, tok umami.Token, method, path string, body any) reply {
	t.Helper()
	r, err := w.try(ctx(t), t, edge, tok, method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return r
}

func (w *world) try(c context.Context, t *testing.T, edge bool, tok umami.Token, method, path string, body any) (reply, error) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	base, hc := w.s.URL.String(), http.DefaultClient
	if edge {
		base, hc = w.web.Base, w.web.HTTP
	}
	req, err := http.NewRequestWithContext(c, method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if edge {
		req.Host = w.web.Host
	}
	req.Header.Set("User-Agent", chromeUA)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !tok.Empty() {
		req.Header.Set("Authorization", "Bearer "+tok.Reveal())
	}
	resp, err := hc.Do(req)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return reply{}, err
	}
	return reply{status: resp.StatusCode, body: string(b)}, nil
}

// adminCall creates or reads fixtures as the admin on the private network
// and decodes the answer.
func (w *world) adminCall(t *testing.T, tok umami.Token, method, path string, in, out any) {
	t.Helper()
	r := w.call(t, false, tok, method, path, in)
	if r.status != http.StatusOK {
		t.Fatalf("setup %s %s: http %d %s", method, path, r.status, r.body)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(r.body), out); err != nil {
			t.Fatalf("setup %s %s: decode %q: %v", method, path, r.body, err)
		}
	}
}

func (w *world) member(t *testing.T, team, label string) member {
	t.Helper()
	m := member{name: "gh-" + label + "-" + uuid.NewString()[:8], password: uuid.NewString()}
	u, err := w.s.Admin.CreateUser(ctx(t), umami.NewUser{Username: m.name, Password: m.password, Role: umami.RoleViewOnly})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.Admin.AddTeamUser(ctx(t), team, u.ID, umami.TeamRoleViewOnly); err != nil {
		t.Fatal(err)
	}
	s, err := w.s.Client.Login(ctx(t), m.name, m.password)
	if err != nil {
		t.Fatal(err)
	}
	m.id, m.token = u.ID, s.Token
	return m
}

func (w *world) send(t *testing.T, e umami.Event) umami.SendResult {
	t.Helper()
	r, err := w.s.Client.Send(ctx(t), umami.SendRequest{Event: e, UserAgent: chromeUA, Country: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type entity struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// provision builds the two tenants the way the control plane maps them, then fills
// team a with every kind of entity a route can name, so a leak shows up as
// one of team a's markers.
func provision(t *testing.T) *world {
	t.Helper()
	now := time.Now()
	w := &world{s: umamictr.Start(t), start: now.Add(-2 * time.Hour), end: now.Add(2 * time.Hour)}
	w.web = umamictr.StartWeb(t, w.s)
	as, err := w.s.Client.Login(ctx(t), umami.SeededUsername, w.s.AdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	w.admin = as.Token

	ta, _, err := w.s.Admin.CreateTeam(ctx(t), "isoa-team", "")
	if err != nil {
		t.Fatal(err)
	}
	tb, _, err := w.s.Admin.CreateTeam(ctx(t), "isob-team", "")
	if err != nil {
		t.Fatal(err)
	}
	w.teamA, w.teamB = ta.ID, tb.ID
	for _, s := range []struct {
		id   *string
		team string
		name string
	}{{&w.siteA, w.teamA, "isoa"}, {&w.siteB, w.teamB, "isob"}} {
		site, err := w.s.Admin.CreateWebsite(ctx(t), umami.NewWebsite{
			ID: uuid.NewString(), Name: s.name + "-site", Domain: s.name + "-site.example", TeamID: s.team,
		})
		if err != nil {
			t.Fatal(err)
		}
		*s.id = site.ID
	}
	w.a = w.member(t, w.teamA, "a")
	w.b = w.member(t, w.teamB, "b")

	// traffic on both websites; team a's carries the markers
	ts := time.Now().Add(-time.Hour).Unix()
	pv := w.send(t, umami.Event{
		Website: w.siteA, Hostname: "isoa-site.example", URL: "/isoa-path?isoa_q=1", Referrer: "https://isoa-ref.example/x",
		Title: "isoa-title", Screen: "1920x1080", Language: "en-US", Timestamp: ts, Salt: "isoa-visitor",
	})
	w.send(t, umami.Event{
		Website: w.siteA, Hostname: "isoa-site.example", URL: "/isoa-path", Name: "isoa-event",
		Data: map[string]any{"isoakey": "isoa-value", "isoanum": 7}, Timestamp: ts + 5, Salt: "isoa-visitor",
	})
	w.send(t, umami.Event{
		Website: w.siteB, Hostname: "isob-site.example", URL: "/isob-path", Timestamp: ts, Salt: "isob-visitor",
	})

	rng := w.rangeQuery()
	var events struct {
		Data []entity `json:"data"`
	}
	w.adminCall(t, w.admin, http.MethodGet, "/api/websites/"+w.siteA+"/events?"+rng, nil, &events)
	if len(events.Data) == 0 {
		t.Fatal("setup: team a has no events")
	}

	site := "/api/websites/" + w.siteA
	var annotation, funnel, goal, segment, share, board, link, pixel, key entity
	w.adminCall(t, w.admin, http.MethodPost, site+"/annotations",
		map[string]any{"date": time.Now().UTC().Format(time.RFC3339), "allDay": true, "note": "isoa-note"}, &annotation)
	w.adminCall(t, w.admin, http.MethodPost, site+"/funnels", map[string]any{"name": "isoa-funnel", "parameters": map[string]any{
		"window": 60, "steps": []map[string]string{{"type": "path", "value": "/isoa-path"}, {"type": "event", "value": "isoa-event"}},
	}}, &funnel)
	w.adminCall(t, w.admin, http.MethodPost, site+"/goals",
		map[string]any{"name": "isoa-goal", "parameters": map[string]string{"type": "path", "value": "/isoa-path"}}, &goal)
	w.adminCall(t, w.admin, http.MethodPost, site+"/segments",
		map[string]any{"type": "segment", "name": "isoa-segment", "parameters": map[string]any{
			"filters": []map[string]string{{"name": "path", "operator": "eq", "value": "/isoa-path"}},
		}}, &segment)
	w.adminCall(t, w.admin, http.MethodPost, site+"/shares", map[string]any{"name": "isoa-share"}, &share)
	w.adminCall(t, w.admin, http.MethodPost, "/api/boards", map[string]any{
		"type": "website", "name": "isoa-board", "teamId": w.teamA, "parameters": map[string]string{"websiteId": w.siteA},
	}, &board)
	w.adminCall(t, w.admin, http.MethodPost, "/api/links", map[string]any{
		"name": "isoa-link", "url": "https://isoa-link.example/x", "slug": "isoalink" + uuid.NewString()[:6], "teamId": w.teamA,
	}, &link)
	w.adminCall(t, w.admin, http.MethodPost, "/api/pixels", map[string]any{
		"name": "isoa-pixel", "slug": "isoapixl" + uuid.NewString()[:6], "teamId": w.teamA,
	}, &pixel)
	// a view-only user can create its own api key (umami-contract.md
	// finding 1), so team a's member holds one
	var keyOut struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	w.adminCall(t, w.a.token, http.MethodPost, "/api/me/api-keys", map[string]string{"name": "isoa-key"}, &keyOut)
	key.ID = keyOut.ID
	var team struct {
		AccessCode string `json:"accessCode"`
	}
	w.adminCall(t, w.admin, http.MethodGet, "/api/teams/"+w.teamA, nil, &team)
	for name, e := range map[string]entity{
		"annotation": annotation, "funnel": funnel, "goal": goal, "segment": segment, "share": share,
		"board": board, "link": link, "pixel": pixel, "key": key,
	} {
		if e.ID == "" {
			t.Fatalf("setup: %s has no id", name)
		}
	}
	if share.Slug == "" || team.AccessCode == "" || pv.SessionID == "" || keyOut.Key == "" {
		t.Fatal("setup: share slug, access code, session id or api key missing")
	}

	w.ids = map[string]string{
		"websiteId": w.siteA, "teamId": w.teamA, "userId": w.a.id,
		"sessionId": pv.SessionID, "eventId": events.Data[0].ID,
		"annotationId": annotation.ID, "funnelId": funnel.ID, "goalId": goal.ID, "segmentId": segment.ID,
		"shareId": share.ID, "slug": share.Slug, "boardId": board.ID, "linkId": link.ID, "pixelId": pixel.ID,
		"keyId": key.ID,
		// team a has no error issues or replays; these ids name nothing,
		// and the website id in the same path is team a's
		"issueId": uuid.NewString(), "replayId": uuid.NewString(),
	}
	w.markers = []string{
		w.siteA, w.teamA, w.a.id, w.a.name, pv.SessionID, events.Data[0].ID,
		annotation.ID, funnel.ID, goal.ID, segment.ID, share.ID, share.Slug, board.ID, link.ID, pixel.ID, key.ID,
		keyOut.Key, team.AccessCode,
		"isoa-team", "isoa-site", "isoa-path", "isoa-ref", "isoa-title", "isoa-event", "isoa-value", "isoakey",
		"isoa-note", "isoa-funnel", "isoa-goal", "isoa-segment", "isoa-share", "isoa-board", "isoa-link", "isoa-pixel",
		"isoa-key", "isoa_q",
	}
	return w
}

func (w *world) rangeQuery() string {
	return "startAt=" + strconv.FormatInt(w.start.UnixMilli(), 10) +
		"&endAt=" + strconv.FormatInt(w.end.UnixMilli(), 10)
}

// op is one line of the reviewed openapi list.
type op struct {
	method, template string
}

func (o op) String() string { return o.method + " " + o.template }

func routes(t *testing.T) []op {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate source file")
	}
	f, err := os.Open(filepath.Join(filepath.Dir(file), "..", "..", "umami-compat", "testdata", "openapi-routes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var ops []op
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m, p, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("bad line %q", line)
		}
		ops = append(ops, op{m, p})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 186 {
		t.Fatalf("the reviewed list has 186 operations, read %d", len(ops))
	}
	return ops
}

var param = regexp.MustCompile(`\{([A-Za-z]+)\}`)

func (w *world) fill(t *testing.T, tmpl string) string {
	t.Helper()
	return param.ReplaceAllStringFunc(tmpl, func(m string) string {
		v, ok := w.ids[m[1:len(m)-1]]
		if !ok {
			t.Fatalf("no team a value for %s in %s", m, tmpl)
		}
		return v
	})
}

// query returns the query string a read needs to pass umami's validation,
// which runs before the permission check (src/lib/request.ts parseRequest),
// so a 400 would prove nothing.
func (w *world) query(o op) string {
	rng := w.rangeQuery()
	analytics := rng + "&unit=day&timezone=UTC"
	extra := map[string]string{
		"/api/links/charts":                                           "ids=" + w.ids["linkId"] + "&" + rng,
		"/api/pixels/charts":                                          "ids=" + w.ids["pixelId"] + "&" + rng,
		"/api/websites/charts":                                        "ids=" + w.siteA + "&" + rng,
		"/api/websites/{websiteId}/metrics":                           analytics + "&type=path",
		"/api/websites/{websiteId}/metrics/expanded":                  analytics + "&type=path",
		"/api/websites/{websiteId}/utm/metrics":                       analytics + "&type=utm_source",
		"/api/websites/{websiteId}/performance/metrics":               analytics + "&type=path",
		"/api/websites/{websiteId}/breakdown":                         analytics + `&fields=["path"]`,
		"/api/websites/{websiteId}/funnels/stats":                     analytics + `&window=60&steps=[{"type":"path","value":"/isoa-path"},{"type":"path","value":"/x"}]`,
		"/api/websites/{websiteId}/goals/stats":                       analytics + "&type=path&value=/isoa-path",
		"/api/websites/{websiteId}/journeys":                          analytics + "&steps=3",
		"/api/websites/{websiteId}/attribution":                       analytics + "&model=first-click&type=path&step=/isoa-path",
		"/api/websites/{websiteId}/segments":                          "type=segment",
		"/api/websites/{websiteId}/errors/values":                     rng + "&type=browser",
		"/api/websites/{websiteId}/errors/{issueId}/values":           rng + "&type=browser",
		"/api/websites/{websiteId}/values":                            analytics + "&type=path",
		"/api/websites/{websiteId}/event-data/values":                 analytics + "&eventName=isoa-event&propertyName=isoakey",
		"/api/websites/{websiteId}/event-data/fields":                 analytics,
		"/api/websites/{websiteId}/event-data/properties":             analytics,
		"/api/websites/{websiteId}/event-data/events":                 analytics + "&event=isoa-event",
		"/api/websites/{websiteId}/session-data/values":               analytics + "&propertyName=isoakey",
		"/api/websites/{websiteId}/session-data/property-series":      analytics + "&propertyName=isoakey",
		"/api/websites/{websiteId}/event-data-pivot/property-series":  analytics + "&eventName=isoa-event&propertyName=isoakey",
		"/api/websites/{websiteId}/revenue/metrics":                   analytics + "&currency=USD&type=country",
		"/api/websites/{websiteId}/revenue/stats":                     analytics + "&currency=USD",
		"/api/websites/{websiteId}/revenue/chart":                     analytics + "&currency=USD",
		"/api/websites/{websiteId}/revenue/sessions":                  analytics + "&currency=USD",
		"/api/websites/{websiteId}/pageviews":                         analytics,
		"/api/websites/{websiteId}/sessions/{sessionId}/activity":     rng,
		"/api/websites/{websiteId}/heatmaps":                          analytics + "&urlPath=/isoa-path&mode=click",
		"/api/websites/{websiteId}/event-data-pivot/numeric-series":   analytics + "&eventName=isoa-event&propertyName=isoanum",
		"/api/websites/{websiteId}/event-data-pivot/numeric-stats":    analytics + "&eventName=isoa-event&propertyName=isoanum",
		"/api/websites/{websiteId}/event-data-pivot/date-series":      analytics + "&eventName=isoa-event&propertyName=isoakey",
		"/api/websites/{websiteId}/event-data-pivot/array-series":     analytics + "&eventName=isoa-event&propertyName=isoakey",
		"/api/websites/{websiteId}/session-data/numeric-series":       analytics + "&propertyName=isoanum",
		"/api/websites/{websiteId}/session-data/numeric-stats":        analytics + "&propertyName=isoanum",
		"/api/websites/{websiteId}/session-data/date-series":          analytics + "&propertyName=isoakey",
		"/api/websites/{websiteId}/session-data/array-series":         analytics + "&propertyName=isoakey",
		"/api/websites/{websiteId}/performance/chart":                 analytics + "&metric=lcp",
		"/api/websites/{websiteId}/errors/{issueId}/events/{eventId}": rng,
		"/api/websites/{websiteId}/session-data-pivot":                analytics + "&propertyName=isoakey",
		"/api/websites/{websiteId}/event-data-pivot":                  analytics + "&eventName=isoa-event",
		"/api/websites/{websiteId}/errors/{issueId}/events":           rng,
		"/api/websites/{websiteId}/errors/{issueId}":                  rng,
		"/api/websites/{websiteId}/errors":                            rng,
		"/api/websites/{websiteId}/errors/stats":                      rng,
		"/api/websites/{websiteId}/funnels/{funnelId}/stats":          analytics,
		"/api/websites/{websiteId}/goals/{goalId}/stats":              analytics,
		"/api/websites/{websiteId}/retention":                         analytics,
		"/api/websites/{websiteId}/events/series":                     analytics,
		"/api/websites/{websiteId}/events/stats":                      analytics,
		"/api/websites/{websiteId}/sessions/weekly":                   analytics,
		"/api/websites/{websiteId}/sessions/stats":                    analytics,
		"/api/websites/{websiteId}/performance/stats":                 analytics,
		"/api/websites/{websiteId}/event-data/stats":                  analytics,
		"/api/websites/{websiteId}/session-data/stats":                analytics + "&propertyName=isoakey",
		"/api/websites/{websiteId}/session-data/properties":           analytics,
		"/api/websites/{websiteId}/stats":                             analytics,
		"/api/websites/{websiteId}/sessions":                          analytics,
		"/api/websites/{websiteId}/events":                            analytics,
		"/api/websites/{websiteId}/replays":                           analytics,
		"/api/websites/{websiteId}/event-data":                        analytics,
		"/api/websites/{websiteId}/event-data/{eventId}":              analytics,
		"/api/websites/{websiteId}/export":                            analytics,
		"/api/websites/{websiteId}/sessions/{sessionId}/properties":   analytics,
		"/api/websites/{websiteId}/sessions/{sessionId}/replays":      analytics,
		"/api/websites/{websiteId}/active":                            analytics,
		"/api/websites/{websiteId}/daterange":                         analytics,
		"/api/websites/{websiteId}/sessions/{sessionId}":              analytics,
		"/api/websites/{websiteId}/replays/saved":                     analytics,
		"/api/websites/{websiteId}/replays/{replayId}":                analytics,
		"/api/websites/{websiteId}/replays/saved/{replayId}":          analytics,
		"/api/realtime/{websiteId}":                                   rng,
		// without teamId this route reads the caller's billing account from
		// redis, which is not configured, and never answers. with team a's
		// id the permission check answers first.
		"/api/auth/subscription": "teamId=" + w.teamA,
	}
	if q, ok := extra[o.template]; ok {
		return "?" + q
	}
	return ""
}

// class says what user b may get back.
type class int

const (
	// the operation names a team a entity in its path or body: b must get
	// 401, 403 or 404 (umami answers 401 for a permission denial)
	scoped class = iota
	// the operation names nothing of team a and answers about the caller
	// (a list of its own teams, its own profile): any status, but the body
	// must hold none of team a's markers
	own
	// the operation acts on the caller's own account and names nothing of
	// team a; on the private network it is not sent, because it would only
	// change user b itself. through caddy it is sent and must be refused or
	// hold no marker.
	self
	// umami serves it without authentication by design (collection, login,
	// public share and recorder configuration). on the private network the
	// answer is recorded; through caddy it must be refused unless listed in
	// publicAtEdge.
	public
)

// classes lists every operation that is not scoped, with the reason.
var classes = map[string]class{
	"GET /api/heartbeat":                     own,
	"GET /api/me":                            own,
	"GET /api/me/teams":                      own,
	"GET /api/me/websites":                   own,
	"GET /api/me/api-keys":                   own,
	"GET /api/teams":                         own,
	"GET /api/websites":                      own,
	"GET /api/websites/charts":               own,
	"GET /api/links":                         own,
	"GET /api/links/charts":                  own,
	"GET /api/pixels":                        own,
	"GET /api/pixels/charts":                 own,
	"GET /api/boards":                        own,
	"GET /api/dashboard":                     own,
	"GET /api/2fa/status":                    own,
	"GET /api/admin/teams":                   own,
	"GET /api/admin/users":                   own,
	"GET /api/admin/websites":                own,
	"GET /api/scripts/telemetry":             own,
	"POST /api/auth/verify":                  own,
	"POST /api/2fa/disable":                  self,
	"POST /api/2fa/setup/cancel":             self,
	"POST /api/2fa/setup/confirm":            self,
	"POST /api/2fa/setup/initiate":           self,
	"POST /api/auth/logout":                  self,
	"POST /api/auth/sso":                     self,
	"POST /api/dashboard":                    self,
	"POST /api/me/api-keys":                  self,
	"POST /api/me/password":                  self,
	"POST /api/send":                         public,
	"POST /api/batch":                        public,
	"POST /api/record":                       public,
	"POST /api/auth/login":                   public,
	"POST /api/2fa/verify":                   public,
	"GET /api/share/{slug}":                  public,
	"GET /api/websites/{websiteId}/recorder": public,
}

// collection routes accept events for any website without authentication.
// they exist for the replayer on the private network.
var collection = map[string]bool{"POST /api/send": true, "POST /api/batch": true, "POST /api/record": true}

// publicAtEdge are the unauthenticated routes caddy lets through, with the
// only answer allowed for team a's website: the recorder is disabled and
// tenants cannot enable it, because every write is refused at the edge.
var publicAtEdge = map[string]string{
	"GET /api/websites/{websiteId}/recorder": `{"enabled":false}`,
}

// body returns a request body that passes the route's schema, so the
// permission check is what answers.
func (w *world) body(o op) any {
	site := w.siteA
	funnel := map[string]any{"name": "isob-x", "parameters": map[string]any{
		"window": 60, "steps": []map[string]string{{"type": "path", "value": "/a"}, {"type": "path", "value": "/b"}},
	}}
	goal := map[string]any{"name": "isob-x", "parameters": map[string]string{"type": "path", "value": "/a"}}
	segment := map[string]any{"type": "segment", "name": "isob-x", "parameters": map[string]any{}}
	annotation := map[string]any{"date": time.Now().UTC().Format(time.RFC3339), "allDay": true, "note": "isob-x"}
	named := map[string]any{"name": "isob-x"}
	required := map[string]any{"required": true}
	bodies := map[string]any{
		"POST /api/admin/2fa/global":                                required,
		"POST /api/admin/teams/{teamId}/2fa":                        required,
		"POST /api/admin/users/{userId}/2fa":                        required,
		"POST /api/boards":                                          map[string]any{"type": "website", "name": "isob-x", "teamId": w.teamA, "parameters": map[string]string{"websiteId": site}},
		"POST /api/boards/{boardId}":                                named,
		"POST /api/boards/{boardId}/clone":                          named,
		"POST /api/boards/{boardId}/shares":                         named,
		"POST /api/links":                                           map[string]any{"name": "isob-x", "url": "https://isob.example/", "slug": "isobslug" + uuid.NewString()[:6], "teamId": w.teamA},
		"POST /api/links/{linkId}":                                  named,
		"POST /api/links/{linkId}/shares":                           named,
		"POST /api/pixels":                                          map[string]any{"name": "isob-x", "slug": "isobpixl" + uuid.NewString()[:6], "teamId": w.teamA},
		"POST /api/pixels/{pixelId}":                                named,
		"POST /api/pixels/{pixelId}/shares":                         named,
		"POST /api/share":                                           map[string]any{"entityId": site, "shareType": 1, "name": "isob-x", "parameters": map[string]any{}},
		"POST /api/share/id/{shareId}":                              map[string]any{"name": "isob-x", "slug": "isobshare", "parameters": map[string]any{}},
		"POST /api/teams":                                           named,
		"POST /api/teams/join":                                      map[string]any{"accessCode": "isob-wrong-code"},
		"POST /api/teams/{teamId}":                                  map[string]any{"name": "isob-x", "accessCode": "isob-code"},
		"POST /api/teams/{teamId}/users":                            map[string]any{"userId": w.b.id, "role": umami.TeamRoleManager},
		"POST /api/teams/{teamId}/users/{userId}":                   map[string]any{"role": umami.TeamRoleManager},
		"POST /api/users":                                           map[string]any{"username": "isob-" + uuid.NewString()[:8], "password": "isob-password", "role": umami.RoleAdmin},
		"POST /api/users/{userId}":                                  map[string]any{"password": "isob-password", "role": umami.RoleAdmin},
		"POST /api/websites":                                        map[string]any{"name": "isob-x", "domain": "isob-x.example", "teamId": w.teamA},
		"POST /api/websites/{websiteId}":                            map[string]any{"name": "isob-x", "domain": "isob-x.example"},
		"POST /api/websites/{websiteId}/annotations":                annotation,
		"POST /api/websites/{websiteId}/annotations/{annotationId}": annotation,
		"POST /api/websites/{websiteId}/funnels":                    funnel,
		"POST /api/websites/{websiteId}/funnels/{funnelId}":         funnel,
		"POST /api/websites/{websiteId}/goals":                      goal,
		"POST /api/websites/{websiteId}/goals/{goalId}":             goal,
		"POST /api/websites/{websiteId}/replays/saved/{replayId}":   map[string]any{"isSaved": true},
		"POST /api/websites/{websiteId}/segments":                   segment,
		"POST /api/websites/{websiteId}/segments/{segmentId}":       segment,
		"POST /api/websites/{websiteId}/shares":                     named,
		"POST /api/websites/{websiteId}/transfer":                   map[string]any{"teamId": w.teamB},
		"PATCH /api/websites/{websiteId}/errors/{issueId}":          map[string]any{"status": "resolved"},
		"POST /api/send":                                            map[string]any{"type": "event", "payload": map[string]any{"website": site, "hostname": "isob.example", "url": "/isob"}},
		"POST /api/batch":                                           []any{map[string]any{"type": "event", "payload": map[string]any{"website": site, "hostname": "isob.example", "url": "/isob"}}},
		"POST /api/record":                                          map[string]any{"website": site},
		"POST /api/auth/login":                                      map[string]any{"username": w.a.name, "password": "isob-guess"},
		"POST /api/2fa/verify":                                      map[string]any{"token": "isob-x", "code": "000000"},
		"POST /api/me/api-keys":                                     named,
		"POST /api/me/password":                                     map[string]any{"currentPassword": "wrong-password", "newPassword": "isob-password"},
		"POST /api/dashboard":                                       named,
		"POST /api/2fa/disable":                                     map[string]any{"password": "x", "token": "000000"},
		"POST /api/2fa/setup/confirm":                               map[string]any{"token": "000000"},
	}
	if b, ok := bodies[o.String()]; ok {
		return b
	}
	return nil
}

// caddyRefusal is the body of every respond ... 403 in web.Caddyfile
const caddyRefusal = "forbidden"

func denied(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound
}

func (w *world) leaked(body string) []string {
	var hit []string
	for _, m := range w.markers {
		if strings.Contains(body, m) {
			hit = append(hit, m)
		}
	}
	return hit
}

func classOf(o op) class {
	if c, ok := classes[o.String()]; ok {
		return c
	}
	return scoped
}

// check sends o as user b and applies its class. it returns a line for the
// log, or fails the test.
func (w *world) check(t *testing.T, o op, edge bool) string {
	t.Helper()
	path := w.fill(t, o.template) + w.query(o)
	c := classOf(o)
	where := "private"
	if edge {
		where = "edge"
	}
	if !edge && c == self {
		return fmt.Sprintf("%s %s: %s not sent, acts only on the caller", where, o, path)
	}
	if !edge && collection[o.String()] {
		return fmt.Sprintf("%s %s: collection route, unauthenticated by design for the replayer; not sent, it would add events to team a", where, o)
	}
	if !edge && c == public {
		r := w.call(t, false, w.b.token, o.method, path, w.body(o))
		return fmt.Sprintf("%s %s: unauthenticated by design, http %d, team a markers %v", where, o, r.status, w.leaked(r.body))
	}
	r := w.call(t, edge, w.b.token, o.method, path, w.body(o))
	if edge && umamictr.RefusedAtEdge(o.method, path) {
		// the refusal must be caddy's own (web.Caddyfile answers "forbidden"
		// with 403), not umami's: umami also refuses most of these for user
		// b, so a status check alone would stay green after a deny rule was
		// deleted and the route reached umami again
		if r.status != http.StatusForbidden || r.body != caddyRefusal {
			t.Errorf("%s %s %s: http %d %.300q, want caddy's own refusal: http 403 %q", where, o.method, path, r.status, r.body, caddyRefusal)
		}
		return ""
	}
	switch c {
	case scoped:
		if !denied(r.status) {
			t.Errorf("%s %s %s: user b got http %d, want 401, 403 or 404: %.300s", where, o.method, path, r.status, r.body)
		}
	case own, self:
		if hit := w.leaked(r.body); len(hit) > 0 {
			t.Errorf("%s %s %s: user b got team a data %v in http %d: %.300s", where, o.method, path, hit, r.status, r.body)
		}
	case public:
		want, ok := publicAtEdge[o.String()]
		if !ok {
			t.Errorf("%s %s: unauthenticated route reachable through caddy: http %d", where, o, r.status)
		} else if strings.TrimSpace(r.body) != want {
			t.Errorf("%s %s %s: http %d %.300s, want %s", where, o.method, path, r.status, r.body, want)
		}
	}
	if r.status == http.StatusBadRequest {
		// validation runs before the permission check, so a 400 proves nothing
		t.Errorf("%s %s %s: http 400, fix the request so the permission check answers: %.300s", where, o.method, path, r.body)
	}
	return ""
}

// a's own reads of the scoped routes are the control: they must pass
// validation (not 400), or b's denial would prove nothing.
func (w *world) control(t *testing.T, o op) int {
	t.Helper()
	path := w.fill(t, o.template) + w.query(o)
	if o.String() == "GET /api/auth/subscription" {
		// past the permission check this route reads the billing account
		// from redis. REDIS_URL is unset (deploy/compose/dev.yml), and the call never
		// answers. b's denial comes before that point, so it still counts.
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := w.try(c, t, false, w.a.token, o.method, path, nil)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("control: user a %s: expected no answer without redis, got err %v", path, err)
		}
		return 0
	}
	r := w.call(t, false, w.a.token, o.method, path, nil)
	if r.status == http.StatusBadRequest || r.status >= 500 {
		t.Errorf("control: user a %s %s: http %d: %.300s", o.method, path, r.status, r.body)
	}
	return r.status
}

func TestUmamiTenantIsolation(t *testing.T) {
	w := provision(t)
	ops := routes(t)
	unclassified := map[string]bool{}
	for k := range classes {
		unclassified[k] = true
	}
	for _, o := range ops {
		delete(unclassified, o.String())
	}
	if len(unclassified) > 0 {
		t.Fatalf("classes name operations that are not in the openapi list: %v", unclassified)
	}

	var reads, writes []op
	for _, o := range ops {
		if o.method == http.MethodGet {
			reads = append(reads, o)
		} else {
			writes = append(writes, o)
		}
	}
	controls := map[int]int{}
	for _, o := range reads {
		if classOf(o) == scoped {
			controls[w.control(t, o)]++
		}
	}
	t.Logf("control, user a on its own team's read routes: %v (status: count)", controls)

	var notes []string
	t.Run("reads", func(t *testing.T) {
		for _, edge := range []bool{false, true} {
			for _, o := range reads {
				if n := w.check(t, o, edge); n != "" {
					notes = append(notes, n)
				}
			}
		}
	})
	// the two /api/users reads caddy lets through: with team a's user id
	// they are scoped (checked above with every other read); with b's own id
	// they answer, list team b and hold nothing of team a
	t.Run("own user reads", func(t *testing.T) {
		for _, edge := range []bool{false, true} {
			for _, sub := range []string{"teams", "websites"} {
				r := w.call(t, edge, w.b.token, http.MethodGet, "/api/users/"+w.b.id+"/"+sub, nil)
				if r.status != http.StatusOK {
					t.Errorf("edge=%v b reads its own %s: http %d %.300s", edge, sub, r.status, r.body)
				}
				if hit := w.leaked(r.body); len(hit) > 0 {
					t.Errorf("edge=%v b's own %s holds team a data %v", edge, sub, hit)
				}
				// the websites read lists personal websites only, and a
				// view-only member has none (umami-contract.md row 9)
				if sub == "teams" && !strings.Contains(r.body, w.teamB) {
					t.Errorf("edge=%v b's own teams do not list team b: %.300s", edge, r.body)
				}
			}
		}
	})
	before := w.snapshot(t)
	t.Run("writes", func(t *testing.T) {
		for _, edge := range []bool{false, true} {
			for _, o := range writes {
				if o.String() == "POST /api/auth/logout" {
					// last, so b's token stays valid for every other check
					continue
				}
				if n := w.check(t, o, edge); n != "" {
					notes = append(notes, n)
				}
			}
		}
		if n := w.check(t, op{http.MethodPost, "/api/auth/logout"}, true); n != "" {
			notes = append(notes, n)
		}
	})
	sort.Strings(notes)
	for _, n := range notes {
		t.Log(n)
	}
	t.Run("team a unchanged", func(t *testing.T) {
		after := w.snapshot(t)
		for k, v := range before {
			if after[k] != v {
				t.Errorf("%s changed:\nbefore %s\nafter  %s", k, v, after[k])
			}
		}
		// user a still logs in with its password, so b changed nothing of it
		if _, err := w.s.Client.Login(ctx(t), w.a.name, w.a.password); err != nil {
			t.Errorf("user a login after b's writes: %v", err)
		}
	})
	t.Logf("checked %d read and %d write operations as user b, on the private network and through caddy", len(reads), len(writes))
}

// snapshot reads, as the admin, everything of team a that b's writes could
// have changed.
func (w *world) snapshot(t *testing.T) map[string]string {
	t.Helper()
	site := "/api/websites/" + w.siteA
	paths := []string{
		site,
		site + "/stats?" + w.rangeQuery(),
		"/api/teams/" + w.teamA,
		"/api/teams/" + w.teamA + "/users",
		"/api/teams/" + w.teamA + "/websites",
		"/api/teams/" + w.teamB + "/websites",
		"/api/users/" + w.a.id,
		"/api/users/" + w.b.id,
		site + "/annotations/" + w.ids["annotationId"],
		site + "/funnels/" + w.ids["funnelId"],
		site + "/goals/" + w.ids["goalId"],
		site + "/segments/" + w.ids["segmentId"],
		site + "/sessions/" + w.ids["sessionId"],
		site + "/shares",
		"/api/share/id/" + w.ids["shareId"],
		"/api/boards/" + w.ids["boardId"],
		"/api/links/" + w.ids["linkId"],
		"/api/pixels/" + w.ids["pixelId"],
		"/api/admin/users",
		"/api/admin/teams",
		"/api/admin/websites",
	}
	out := map[string]string{}
	for _, p := range paths {
		r := w.call(t, false, w.admin, http.MethodGet, p, nil)
		if r.status != http.StatusOK {
			t.Fatalf("snapshot %s: http %d %s", p, r.status, r.body)
		}
		out[p] = r.body
	}
	// a's api keys, read as a
	r := w.call(t, false, w.a.token, http.MethodGet, "/api/me/api-keys", nil)
	if r.status != http.StatusOK {
		t.Fatalf("snapshot api keys: http %d", r.status)
	}
	out["user a api keys"] = r.body
	return out
}
