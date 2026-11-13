//go:build caddyweb

package caddy_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

// a desktop chrome user agent that umami's isbot check accepts
const webUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// liveWeb is one umami behind the web caddy, with a team, a website and a
// view-only member whose token the requests carry.
type liveWeb struct {
	s       *umamictr.Stack
	w       *umamictr.Web
	teamID  string
	website string
	userID  string
	token   umami.Token
}

func startLiveWeb(t *testing.T) *liveWeb {
	t.Helper()
	s := umamictr.Start(t)
	w := umamictr.StartWeb(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	team, _, err := s.Admin.CreateTeam(ctx, "web-caddy", "")
	require.NoError(t, err)
	site, err := s.Admin.CreateWebsite(ctx, umami.NewWebsite{
		ID: uuid.NewString(), Name: "web-caddy", Domain: "web-caddy.example", TeamID: team.ID,
	})
	require.NoError(t, err)
	name, pw := "gh-"+uuid.NewString()[:8], uuid.NewString()
	u, err := s.Admin.CreateUser(ctx, umami.NewUser{Username: name, Password: pw, Role: umami.RoleViewOnly})
	require.NoError(t, err)
	_, err = s.Admin.AddTeamUser(ctx, team.ID, u.ID, umami.TeamRoleViewOnly)
	require.NoError(t, err)
	// the control plane logs users in on the private network, as here
	sess, err := s.Client.Login(ctx, name, pw)
	require.NoError(t, err)
	// one pageview, so the read routes have something to return
	_, err = s.Client.Send(ctx, umami.SendRequest{UserAgent: webUA, Country: "FR", Event: umami.Event{
		Website: site.ID, Hostname: "web-caddy.example", URL: "/caddy-page", Title: "caddy",
		Referrer: "https://ref-caddy.example/from", Screen: "1920x1080", Language: "en-US",
		Timestamp: time.Now().Add(-time.Minute).Unix(), Salt: "caddy",
	}})
	require.NoError(t, err)
	return &liveWeb{s: s, w: w, teamID: team.ID, website: site.ID, userID: u.ID, token: sess.Token}
}

type webResp struct {
	status int
	header http.Header
	body   string
}

// do sends one request through caddy. rawPath goes on the wire unchanged, so
// encoded and unusual spellings reach caddy as written.
func (l *liveWeb) do(t *testing.T, method, rawPath string, hdr map[string]string, body string) webResp {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, l.w.Base+"/", rd)
	require.NoError(t, err)
	req.URL.Opaque = rawPath
	req.URL.RawQuery = ""
	if i := strings.IndexByte(rawPath, '?'); i >= 0 {
		req.URL.Opaque = rawPath[:i]
		req.URL.RawQuery = rawPath[i+1:]
	}
	// go writes an opaque path that starts with // as an authority, so such
	// a path is sent in absolute form, which caddy accepts the same way
	if strings.HasPrefix(req.URL.Opaque, "//") {
		req.URL.Opaque = "//" + l.w.Host + req.URL.Opaque
	}
	req.Host = l.w.Host
	req.Header.Set("User-Agent", webUA)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if !l.token.Empty() {
		req.Header.Set("Authorization", "Bearer "+l.token.Reveal())
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := l.w.HTTP.Do(req)
	require.NoError(t, err, "%s %s", method, rawPath)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return webResp{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

// direct calls umami on the private network with the same token.
func (l *liveWeb) direct(t *testing.T, method, path string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, l.s.URL.String()+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+l.token.Reveal())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

// one umami and one caddy serve every check, because umami's first start
// runs all its migrations
func TestWebCaddyLive(t *testing.T) {
	l := startLiveWeb(t)
	t.Run("deny list", func(t *testing.T) { webDenyList(t, l) })
	t.Run("api method allowlist", func(t *testing.T) { webAPIMethodAllowlist(t, l) })
	t.Run("allowed reads", func(t *testing.T) { webAllowedReads(t, l) })
	t.Run("sso rules", func(t *testing.T) { webSSORules(t, l) })
	t.Run("login redirect", func(t *testing.T) { webLoginRedirect(t, l) })
	t.Run("dashboard in a browser", func(t *testing.T) { webDashboardInBrowser(t, l) })
	// last, so the log holds every request made above
	t.Run("access log", func(t *testing.T) { webAccessLogHasNoClientIdentity(t, l) })
}

var allMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// openapiPaths reads the reviewed operation list of the pinned umami image and
// fills each path parameter with a value of the right shape.
func openapiPaths(t *testing.T, l *liveWeb) map[string][]string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	f, err := os.Open(filepath.Join(filepath.Dir(file), "..", "..", "tests", "umami-compat", "testdata", "openapi-routes.txt"))
	require.NoError(t, err)
	defer f.Close()
	param := regexp.MustCompile(`\{[A-Za-z]+\}`)
	fill := map[string]string{"{websiteId}": l.website, "{teamId}": l.teamID, "{userId}": l.userID}
	ops := map[string][]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		method, path, ok := strings.Cut(line, " ")
		require.True(t, ok, line)
		path = param.ReplaceAllStringFunc(path, func(p string) string {
			if v, ok := fill[p]; ok {
				return v
			}
			// other ids name no existing entity; one value per name keeps
			// the 141 paths distinct
			v := uuid.NewString()
			if p == "{slug}" {
				v = "someslug1"
			}
			fill[p] = v
			return v
		})
		ops[path] = append(ops[path], method)
	}
	require.NoError(t, sc.Err())
	require.Len(t, ops, 141, "the reviewed list has 141 paths")
	return ops
}

// requireCaddyRefusal checks that caddy itself refused the request: 403
// with the "forbidden" body of every respond in web.Caddyfile. umami refuses
// many of these routes for a view-only user as well, so without the body a
// deleted deny rule could go unnoticed. a HEAD answer has no body.
func requireCaddyRefusal(t *testing.T, r webResp, method, path string) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, r.status, "%s %s answered %d: %.200s", method, path, r.status, r.body)
	if method != http.MethodHead {
		require.Equal(t, "forbidden", r.body, "%s %s: 403 not from caddy", method, path)
	}
}

func requireWebHeaders(t *testing.T, r webResp, what string) {
	t.Helper()
	require.Equal(t, "no-referrer", r.header.Get("Referrer-Policy"), "%s: Referrer-Policy", what)
}

// every path of the deny list is refused for every method, through every
// spelling that umami would route to the same handler.
func webDenyList(t *testing.T, l *liveWeb) {
	ops := openapiPaths(t, l)
	paths := make([]string, 0, len(ops))
	for p := range ops {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// paths outside the openapi contract that the deny list names
	extra := []string{
		"/telemetry.js", "/openapi.json", "/script.js", "/recorder.js", "/mcp", "/mcp/x",
		"/share/abc", "/share/abc/websites/x", "/q/abcdefgh", "/p/abcdefgh",
		"/compat/api/reports/funnel", "/compat/api/websites/" + l.website + "/reports",
		"/api/users", "/api/admin", "/api/me/api-keys", "/api/2fa/setup/initiate",
	}
	var denied []string
	for _, p := range append(paths, extra...) {
		if umamictr.DeniedPath(p) {
			denied = append(denied, p)
		}
	}
	require.Greater(t, len(denied), 40)

	// spellings: umami's /teams rewrite, case, a trailing slash, percent
	// encoding, dot segments and doubled slashes
	variants := func(p string) []string {
		v := []string{p, "/teams/" + l.teamID + p, "/teams/x/teams/y" + p, strings.ToUpper(p), p + "/"}
		if strings.HasPrefix(p, "/api/") {
			v = append(v,
				"/api/%"+strings.ToUpper(hexByte(p[5]))+p[6:],
				"/api/."+p[4:],
				"/api/x/.."+p[4:],
				"/"+p,
				"/api"+strings.ReplaceAll(p[4:], "/", "%2F"),
			)
		}
		return v
	}
	for _, p := range denied {
		for _, v := range variants(p) {
			for _, m := range allMethods {
				r := l.do(t, m, v, nil, "{}")
				requireCaddyRefusal(t, r, m, v)
				requireWebHeaders(t, r, m+" "+v)
			}
		}
	}
	t.Logf("deny list: %d paths x spellings x %d methods refused", len(denied), len(allMethods))

	// from the private network the collection route still works, for the replayer
	res, err := l.s.Client.Send(context.Background(), umami.SendRequest{UserAgent: webUA, Event: umami.Event{
		Website: l.website, Hostname: "web-caddy.example", URL: "/private", Timestamp: time.Now().Unix(), Salt: "p",
	}})
	require.NoError(t, err)
	require.NotEmpty(t, res.SessionID)
}

// every other /api path takes only GET, HEAD and OPTIONS, plus POST to
// /api/auth/verify and /api/auth/logout.
func webAPIMethodAllowlist(t *testing.T, l *liveWeb) {
	ops := openapiPaths(t, l)
	var checked int
	for p := range ops {
		if umamictr.DeniedPath(p) {
			continue
		}
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if m == http.MethodPost && (p == "/api/auth/verify" || p == "/api/auth/logout") {
				continue
			}
			for _, v := range []string{p, "/teams/" + l.teamID + p} {
				r := l.do(t, m, v, nil, "{}")
				requireCaddyRefusal(t, r, m, v)
				requireWebHeaders(t, r, m+" "+v)
				checked++
			}
		}
	}
	t.Logf("method allowlist: %d non-allowlisted requests refused", checked)

	// the two /api/users reads take GET and HEAD only; every other
	// /api/users route stays denied, for reads as well
	for _, sub := range []string{"/teams", "/websites"} {
		p := "/api/users/" + l.userID + sub
		for _, m := range []string{http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			requireCaddyRefusal(t, l.do(t, m, p, nil, "{}"), m, p)
		}
	}
	for _, p := range []string{
		"/api/users", "/api/users/" + l.userID, "/api/users/" + l.userID + "/teams/x",
		"/api/users/" + l.userID + "/websitesx", "/api/usersx/" + l.userID + "/teams",
		"/api/auth/subscription", "/api/auth/subscription?teamId=" + l.teamID,
	} {
		requireCaddyRefusal(t, l.do(t, http.MethodGet, p, nil, ""), http.MethodGet, p)
	}

	// the allowed writes reach umami
	r := l.do(t, http.MethodPost, "/api/auth/verify", nil, "")
	require.Equal(t, http.StatusOK, r.status, r.body)
	var me struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.body), &me))
	require.Equal(t, l.userID, me.ID)
	requireWebHeaders(t, r, "verify")
}

// the reads a tenant dashboard makes (docs/reference/umami-contract.md) work
// through caddy with a valid token and match the private-network answer.
func webAllowedReads(t *testing.T, l *liveWeb) {
	now := time.Now()
	rng := "startAt=" + itoa(now.Add(-time.Hour).UnixMilli()) + "&endAt=" + itoa(now.Add(time.Minute).UnixMilli())
	reads := []string{
		"/api/heartbeat",
		"/api/2fa/status",
		"/api/realtime/" + l.website,
		"/api/teams/" + l.teamID,
		"/api/teams/" + l.teamID + "/users",
		"/api/teams/" + l.teamID + "/websites",
		"/api/users/" + l.userID + "/teams",
		"/api/users/" + l.userID + "/websites",
		"/api/websites/charts?ids=" + l.website,
		"/api/websites/" + l.website,
		"/api/websites/" + l.website + "/stats?" + rng,
		"/api/websites/" + l.website + "/metrics?type=path&" + rng,
		"/api/websites/" + l.website + "/pageviews?unit=hour&timezone=UTC&" + rng,
		"/api/websites/" + l.website + "/sessions?" + rng,
		"/api/websites/" + l.website + "/events?" + rng,
		"/api/websites/" + l.website + "/daterange",
		"/api/websites/" + l.website + "/active",
		"/teams/" + l.teamID + "/api/websites/" + l.website + "/stats?" + rng,
	}
	for _, p := range reads {
		r := l.do(t, http.MethodGet, p, nil, "")
		require.Equal(t, http.StatusOK, r.status, "GET %s: %.300s", p, r.body)
		requireWebHeaders(t, r, p)
		if !strings.HasPrefix(p, "/teams/") && !strings.HasPrefix(p, "/api/realtime/") && !strings.HasPrefix(p, "/api/heartbeat") {
			code, body := l.direct(t, http.MethodGet, p)
			require.Equal(t, http.StatusOK, code, p)
			require.JSONEq(t, body, r.body, "GET %s differs from the private-network answer", p)
		}
	}
	var st struct {
		Pageviews int `json:"pageviews"`
	}
	require.NoError(t, json.Unmarshal([]byte(l.do(t, http.MethodGet, "/api/websites/"+l.website+"/stats?"+rng, nil, "").body), &st))
	require.GreaterOrEqual(t, st.Pageviews, 1, "the pageview is visible through caddy")

	// without a token the read is refused by umami itself
	tok := l.token
	l.token = umami.Token{}
	r := l.do(t, http.MethodGet, "/api/websites/"+l.website+"/stats?"+rng, nil, "")
	l.token = tok
	require.Equal(t, http.StatusUnauthorized, r.status, r.body)

	// the dashboard page itself renders
	r = l.do(t, http.MethodGet, "/websites/"+l.website, nil, "")
	require.Equal(t, http.StatusOK, r.status)
	require.Contains(t, r.header.Get("Content-Type"), "text/html")
	requireWebHeaders(t, r, "website page")
}

// the design: /sso only same-site, only towards /websites/, never cached.
func webSSORules(t *testing.T, l *liveWeb) {
	tok := l.token
	l.token = umami.Token{}
	defer func() { l.token = tok }()
	target := "/websites/" + l.website
	q := func(u string) string { return "/sso?token=t0ken&url=" + u }
	ok := []struct {
		path, site string
	}{
		{q(target), "same-site"},
		{q(target), "same-origin"},
		{q(target + "/realtime"), "same-site"},
		{q("%2Fwebsites%2F" + l.website), "same-site"},
	}
	for _, c := range ok {
		r := l.do(t, http.MethodGet, c.path, map[string]string{"Sec-Fetch-Site": c.site}, "")
		require.Equal(t, http.StatusOK, r.status, "%s with %s: %.200s", c.path, c.site, r.body)
		require.Contains(t, r.header.Get("Content-Type"), "text/html")
		require.Equal(t, "no-store", r.header.Get("Cache-Control"), c.path)
		requireWebHeaders(t, r, c.path)
	}
	refused := []struct {
		method, path, site string
	}{
		{http.MethodGet, q(target), ""},
		{http.MethodGet, q(target), "cross-site"},
		{http.MethodGet, q(target), "none"},
		{http.MethodGet, q(target), "Same-Site-ish"},
		{http.MethodGet, "/sso?token=t0ken", "same-site"},
		{http.MethodGet, q("/admin/users"), "same-site"},
		{http.MethodGet, q("/settings/profile"), "same-site"},
		{http.MethodGet, q("/websites"), "same-site"},
		{http.MethodGet, q("/websites/"), "same-site"},
		{http.MethodGet, q("/websitesx/" + l.website), "same-site"},
		{http.MethodGet, q("/websites/../admin/users"), "same-site"},
		{http.MethodGet, q("/websites/%2E%2E/admin/users"), "same-site"},
		{http.MethodGet, q("/websites//evil.example"), "same-site"},
		{http.MethodGet, q("//evil.example/websites/x"), "same-site"},
		{http.MethodGet, q("https://evil.example/websites/x"), "same-site"},
		{http.MethodGet, q(target + "%3Fx%3D1"), "same-site"},
		{http.MethodGet, q("%20/websites/" + l.website), "same-site"},
		{http.MethodGet, q("/websites/" + l.website + "%0A"), "same-site"},
		{http.MethodGet, "/sso?url=/admin&url=" + target + "&token=t", "same-site"},
		{http.MethodPost, q(target), "same-site"},
		{http.MethodGet, "/sso/?token=t0ken&url=" + target, "cross-site"},
		{http.MethodGet, "/SSO?token=t0ken&url=" + target, "cross-site"},
		{http.MethodGet, "/teams/" + l.teamID + q(target), "same-site"},
		{http.MethodGet, "/teams/" + l.teamID + q(target), "cross-site"},
	}
	for _, c := range refused {
		hdr := map[string]string{}
		if c.site != "" {
			hdr["Sec-Fetch-Site"] = c.site
		}
		r := l.do(t, c.method, c.path, hdr, "")
		require.Equal(t, http.StatusForbidden, r.status, "%s %s with %q answered %d", c.method, c.path, c.site, r.status)
		requireWebHeaders(t, r, c.path)
		if !strings.HasPrefix(c.path, "/teams/") {
			require.Equal(t, "no-store", r.header.Get("Cache-Control"), c.path)
		}
	}
}

func webLoginRedirect(t *testing.T, l *liveWeb) {
	for _, p := range []string{"/login", "/login/", "/login/two-factor", "/login?next=x", "/LOGIN"} {
		r := l.do(t, http.MethodGet, p, nil, "")
		require.Equal(t, http.StatusFound, r.status, p)
		require.Equal(t, "https://app."+umamictr.WebDomain+"/", r.header.Get("Location"), p)
		requireWebHeaders(t, r, p)
	}
	r := l.do(t, http.MethodGet, "/teams/"+l.teamID+"/login", nil, "")
	require.Equal(t, http.StatusForbidden, r.status)
}

// the access log carries no address, user agent, forwarded header, referer
// or query string, and the client address never reaches umami.
func webAccessLogHasNoClientIdentity(t *testing.T, l *liveWeb) {
	const ua = "Mozilla/5.0 (X11; Linux x86_64) WebLogProbe/9.3"
	const spoofed = "198.51.100.77"
	const queryMarker = "qmarker-5d1c"
	tokenMarker := "tmarker-" + uuid.NewString()[:8]
	hdr := map[string]string{
		"User-Agent":       ua,
		"X-Forwarded-For":  spoofed,
		"X-Real-Ip":        spoofed,
		"Forwarded":        "for=" + spoofed,
		"Referer":          "https://app." + umamictr.WebDomain + "/x?" + queryMarker,
		"X-Edge-Client-Ip": spoofed,
		"Sec-Fetch-Site":   "same-site",
	}
	paths := []string{
		"/api/websites/" + l.website + "?" + queryMarker + "=1",
		"/sso?token=" + tokenMarker + "&url=/websites/" + l.website,
		"/sso?token=" + tokenMarker + "&url=/admin",
		"/sso/?token=" + tokenMarker + "&url=/websites/" + l.website,
		"/login?" + queryMarker,
		"/api/send?" + queryMarker,
	}
	for _, p := range paths {
		l.do(t, http.MethodGet, p, hdr, "")
	}
	// a host the file does not serve is logged by caddy's default logger
	host := l.w.Host
	l.w.Host = "other." + umamictr.WebDomain
	l.do(t, http.MethodGet, "/unmapped?"+queryMarker, hdr, "")
	l.w.Host = host

	var all string
	var access []string
	require.Eventually(t, func() bool {
		all = umamictr.Logs(t, l.w.Container)
		access = access[:0]
		for _, line := range strings.Split(all, "\n") {
			if strings.Contains(line, `"logger":"http.log.access`) {
				access = append(access, line)
			}
		}
		// the startup heartbeat probes are logged too
		return len(access) >= len(paths)+1
	}, 10*time.Second, 100*time.Millisecond, "access log lines missing: %s", all)
	var sawSSO bool
	for _, line := range access {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)
		req, ok := entry["request"].(map[string]any)
		require.True(t, ok, line)
		for _, k := range []string{"remote_ip", "remote_port", "client_ip"} {
			require.NotContains(t, req, k, line)
		}
		uri, ok := req["uri"].(string)
		require.True(t, ok, line)
		require.NotContains(t, uri, "?", "query string in the log: %s", line)
		if strings.HasPrefix(uri, "/sso") {
			sawSSO = true
		}
		// no request header at all, so neither the user agent nor any
		// header that can carry an address
		require.NotContains(t, req, "headers", line)
		require.NotContains(t, entry, "resp_headers", line)
	}
	require.True(t, sawSSO, "the sso requests are logged")
	for _, s := range []string{ua, "WebLogProbe", spoofed, queryMarker, tokenMarker} {
		for _, line := range strings.Split(all, "\n") {
			require.NotContains(t, line, s, "caddy wrote %q", s)
		}
	}
	// and no address of any kind is left in an access line
	ipv4 := regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)
	for _, line := range access {
		require.False(t, ipv4.MatchString(line), "address in access line: %s", line)
	}
}

func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0x0f]})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// the tenant dashboard in chromium through caddy (tests/caddy-web): overview
// metrics, pageviews, referrers, countries, devices and browsers, with no
// request refused. screenshots go to .scratch/scratch/caddy-web.
func webDashboardInBrowser(t *testing.T, l *liveWeb) {
	pnpm, err := exec.LookPath("pnpm")
	require.NoError(t, err, "the browser check needs pnpm with the playwright browsers installed")
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	_, port, err := net.SplitHostPort(strings.TrimPrefix(l.w.Base, "https://"))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, pnpm, "--filter", "@stellar-analytics/caddy-web", "test:live")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"WEB_PORT="+port,
		"WEB_DOMAIN="+umamictr.WebDomain,
		"WEB_TOKEN="+l.token.Reveal(),
		"WEB_WEBSITE="+l.website,
		"WEB_TEAM="+l.teamID,
	)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	require.NoError(t, err, "playwright suite failed")
}
