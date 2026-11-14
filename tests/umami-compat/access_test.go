//go:build umamicompat

package umamicompat

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/umami"
)

// DISABLE_LOGIN=1 blocks the /login page and leaves
// POST /api/auth/login working.
func TestDisableLoginBlocksOnlyThePage(t *testing.T) {
	e := setup(t)
	req, err := http.NewRequestWithContext(ctx(t), http.MethodGet, e.base.String()+"/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /login = %d, want 403", resp.StatusCode)
	}
	if e.token.Empty() {
		t.Fatal("api login returned no token")
	}
}

type viewer struct {
	userID string
	token  umami.Token
}

// viewOnly creates a user with global role view-only, adds it to teamID as
// team-view-only and logs it in, which is how the control plane maps a tenant member.
func (e *env) viewOnly(t *testing.T, teamID string) viewer {
	t.Helper()
	name := "compat-" + uuid.NewString()[:12]
	pw := uuid.NewString()
	u, err := e.admin.CreateUser(ctx(t), umami.NewUser{Username: name, Password: pw, Role: umami.RoleViewOnly})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.admin.DeleteUser(ctx(t), u.ID); err != nil {
			t.Errorf("cleanup: delete user: %v", err)
		}
	})
	if _, err := e.admin.AddTeamUser(ctx(t), teamID, u.ID, umami.TeamRoleViewOnly); err != nil {
		t.Fatal(err)
	}
	s, err := e.client.Login(ctx(t), name, pw)
	if err != nil {
		t.Fatal(err)
	}
	return viewer{userID: u.ID, token: s.Token}
}

// a view-only user with team-view-only reads every rfp metric of a
// team website and can change nothing.
func TestViewOnlyTeamMemberReadsButCannotChange(t *testing.T) {
	e := setup(t)
	team, w := e.site(t)
	v := e.viewOnly(t, team)
	now := time.Now()
	e.send(t, umami.SendRequest{Event: umami.Event{
		Website: w, Hostname: "compat.example", URL: "/a", Referrer: "https://ref.example/x",
		Timestamp: now.Add(-time.Minute).Unix(), Salt: "v",
	}, Country: "FR"})

	r := umami.Range{Start: now.Add(-time.Hour), End: now.Add(time.Minute)}
	st, err := e.client.Stats(ctx(t), v.token, w, r)
	if err != nil {
		t.Fatalf("stats as viewer: %v", err)
	}
	if st.Pageviews != 1 || st.Visitors != 1 || st.Visits != 1 || st.Bounces != 1 {
		t.Errorf("viewer stats = %+v", st.StatsValues)
	}
	want := map[string]string{"referrer": "ref.example", "country": "FR", "device": "desktop", "browser": "chrome", "os": "Linux", "path": "/a"}
	for typ, x := range want {
		rows, err := e.client.Metrics(ctx(t), v.token, w, umami.MetricsQuery{Range: r, Type: typ})
		if err != nil {
			t.Fatalf("metrics %s as viewer: %v", typ, err)
		}
		if len(rows) != 1 || rows[0].X != x || rows[0].Y != 1 {
			t.Errorf("metrics %s = %+v, want %s:1", typ, rows, x)
		}
	}
	if _, err := e.client.Pageviews(ctx(t), v.token, w, umami.PageviewsQuery{Range: r, Unit: umami.UnitHour, Timezone: "UTC"}); err != nil {
		t.Errorf("pageviews as viewer: %v", err)
	}

	// every write a tenant must not make. umami answers 401 for a permission
	// denial (src/lib/response.ts unauthorized).
	writes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/websites/" + w, map[string]string{"name": "x"}},
		{http.MethodPost, "/api/websites/" + w + "/reset", nil},
		{http.MethodDelete, "/api/websites/" + w, nil},
		{http.MethodPost, "/api/websites/" + w + "/shares", map[string]any{"name": "x"}},
		{http.MethodPost, "/api/websites/" + w + "/transfer", map[string]string{"userId": v.userID}},
		{http.MethodPost, "/api/websites", map[string]string{"name": "x", "domain": "x.example", "teamId": team}},
		{http.MethodPost, "/api/websites", map[string]string{"name": "x", "domain": "x.example"}},
		{http.MethodPost, "/api/teams", map[string]string{"name": "x"}},
		{http.MethodPost, "/api/teams/" + team, map[string]string{"name": "x"}},
		{http.MethodDelete, "/api/teams/" + team, nil},
		{http.MethodPost, "/api/teams/" + team + "/users", map[string]string{"userId": v.userID, "role": "team-manager"}},
		{http.MethodPost, "/api/teams/" + team + "/users/" + v.userID, map[string]string{"role": "team-manager"}},
		{http.MethodPost, "/api/websites/" + w + "/segments", map[string]any{"type": "segment", "name": "x", "parameters": map[string]any{}}},
		{http.MethodPost, "/api/websites/" + w + "/annotations", map[string]any{"date": time.Now().UTC().Format(time.RFC3339), "allDay": true, "note": "x"}},
		{http.MethodPost, "/api/websites/" + w + "/goals", map[string]any{"name": "x", "parameters": map[string]any{"type": "path", "value": "/"}}},
		{http.MethodPost, "/api/boards", map[string]any{"type": "website", "name": "x", "teamId": team, "parameters": map[string]any{"websiteId": w}}},
		{http.MethodPost, "/api/links", map[string]any{"name": "x", "url": "https://a.example", "slug": "compat" + uuid.NewString()[:6], "teamId": team}},
		{http.MethodPost, "/api/pixels", map[string]any{"name": "x", "slug": "compat" + uuid.NewString()[:6], "teamId": team}},
		{http.MethodPost, "/api/users", map[string]string{"username": "x" + uuid.NewString()[:8], "password": "12345678", "role": "user"}},
	}
	for _, wr := range writes {
		code := e.call(t, v.token, wr.method, wr.path, wr.body, nil)
		t.Logf("viewer %s %s: http %d", wr.method, wr.path, code)
		// a 400 would mean the body failed validation before the permission
		// check ran, which proves nothing, so only a denial counts
		if code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("viewer %s %s: http %d, want a permission denial", wr.method, wr.path, code)
		}
	}
	// the website is untouched
	st, err = e.admin.Stats(ctx(t), w, r)
	if err != nil || st.Pageviews != 1 {
		t.Errorf("after denied writes: stats %+v, err %v", st.StatsValues, err)
	}
	var site umami.Website
	if code := e.get(t, e.token, "/api/websites/"+w, &site); code != http.StatusOK || site.Name != "compat" || site.TeamID != team {
		t.Errorf("after denied writes: website %+v (http %d)", site, code)
	}
}

// a view-only member of team a gets nothing of team b.
func TestViewOnlyCannotReadOtherTeam(t *testing.T) {
	e := setup(t)
	teamA, _ := e.site(t)
	_, wB := e.site(t)
	v := e.viewOnly(t, teamA)
	now := time.Now()
	_, err := e.client.Stats(ctx(t), v.token, wB, umami.Range{Start: now.Add(-time.Hour), End: now})
	var apiErr *umami.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("viewer of team a reading team b: %v", err)
	}
}

// the route list of the running image must equal the reviewed list in
// testdata. a difference means the deny list of deploy/caddy/web.Caddyfile and the caddy
// configuration must be reviewed before the upgrade.
func TestOpenAPIRouteListUnchanged(t *testing.T) {
	e := setup(t)
	req, err := http.NewRequestWithContext(ctx(t), http.MethodGet, e.base.String()+"/openapi.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /openapi.json: %d %v", resp.StatusCode, err)
	}
	var doc struct {
		Info  struct{ Version string } `json:"info"`
		Paths map[string]map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for p, ops := range doc.Paths {
		for m := range ops {
			switch m {
			case "get", "post", "put", "patch", "delete":
				got = append(got, strings.ToUpper(m)+" "+p)
			}
		}
	}
	sort.Strings(got)

	f, err := os.Open("testdata/openapi-routes.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var want []string
	version := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "# version "); ok {
			version = v
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		want = append(want, line)
	}
	if doc.Info.Version != version {
		t.Errorf("openapi version %s, reviewed list is for %s", doc.Info.Version, version)
	}
	if a, b := strings.Join(got, "\n"), strings.Join(want, "\n"); a != b {
		t.Fatalf("route list differs from testdata/openapi-routes.txt; review the deny list.\n%s", diff(want, got))
	}
}

func diff(want, got []string) string {
	w := map[string]bool{}
	for _, s := range want {
		w[s] = true
	}
	g := map[string]bool{}
	for _, s := range got {
		g[s] = true
	}
	var b strings.Builder
	for _, s := range got {
		if !w[s] {
			fmt.Fprintf(&b, "+ %s\n", s)
		}
	}
	for _, s := range want {
		if !g[s] {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	return b.String()
}
