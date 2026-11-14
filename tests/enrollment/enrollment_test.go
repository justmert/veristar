//go:build enrollmentit

// Package enrollmentit verifies the enrollment mode
// (control-plane-api 5.7) end to end: the control plane routes in process
// with the real sign-in (github oauth fixture), the real reconciler, real
// postgresql, real nats, a real umami, and the real gateway and replayer
// binaries built from cmd/. run it with
//
//	DBIP_MMDB=.scratch/geo/dbip-country-lite.mmdb \
//	  go test -tags enrollmentit -race -count=1 -v ./tests/enrollment/
//
// DBIP_MMDB is the db-ip country file the gateway needs (make geo-fetch).
package enrollmentit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/justmert/veristar/db"
	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/sites"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// the github id of the operator; listed in the guard and in auth's operator set
const operatorGHID int64 = 900001

// a desktop chrome user agent that umami's isbot check accepts
const ua = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// binaries built once in TestMain from the working tree
var bins struct{ dir, gateway, replayer string }

func TestMain(m *testing.M) {
	if os.Getenv("DBIP_MMDB") == "" {
		fmt.Fprintln(os.Stderr, "enrollmentit: DBIP_MMDB (path of the db-ip country file, make geo-fetch) is required")
		os.Exit(2)
	}
	dir, err := os.MkdirTemp("", "enrollmentit-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "enrollmentit:", err)
		os.Exit(1)
	}
	bins.dir = dir
	bins.gateway = filepath.Join(dir, "gateway")
	bins.replayer = filepath.Join(dir, "replayer")
	for out, pkg := range map[string]string{bins.gateway: "./cmd/gateway", bins.replayer: "./cmd/replayer"} {
		cmd := exec.Command("go", "build", "-trimpath", "-o", out, pkg)
		cmd.Dir = repoRoot()
		if b, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "enrollmentit: go build %s: %v\n%s", pkg, err, b)
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	srv     *httptest.Server
	fixture *httptest.Server
	pg      *dbtest.Postgres
	scope   *tenants.Scope
	rec     *provision.Reconciler
	sites   jetstream.KeyValue
	js      jetstream.JetStream
	umami   *umamictr.Stack
	terms   string
	gw      *gwclient.Client
	nextGH  int64
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, p, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	require.NoError(t, l.Close())
	return p
}

// runBinary starts a built binary with env and stops it at cleanup. its
// output goes to the test log on failure only.
func runBinary(t *testing.T, path string, env map[string]string) {
	t.Helper()
	cmd := exec.Command(path)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TZ=UTC"}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("%s output:\n%s", filepath.Base(path), out.String())
		}
	})
}

func waitOK(t *testing.T, u string) {
	t.Helper()
	require.Eventually(t, func() bool {
		r, err := http.Get(u)
		if err != nil {
			return false
		}
		_ = r.Body.Close()
		return r.StatusCode == http.StatusOK
	}, time.Minute, 200*time.Millisecond, "%s never answered 200", u)
}

func random32(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func start(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e := &env{nextGH: 910000}

	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	e.pg = pg
	acfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	require.NoError(t, err)
	_, err = pdb.Migrate(ctx, acfg, db.Migrations(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	require.NoError(t, err)
	require.NoError(t, pdb.SetRolePasswords(ctx, rcfg))
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	conn, err := pdb.Open(ctx, pcfg, "enrollment-test")
	require.NoError(t, err)
	t.Cleanup(conn.Close)

	ns := natsctr.StartSingle(t)
	nc, err := nats.Connect(ns.URL)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	e.js, err = jetstream.New(nc)
	require.NoError(t, err)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, e.js, spec)
	require.NoError(t, err)
	pub, err := registry.NewPublisher(ctx, e.js)
	require.NoError(t, err)
	e.sites, err = e.js.KeyValue(ctx, natsx.BucketRegistrySites)
	require.NoError(t, err)

	e.umami = umamictr.Start(t)
	key, err := umami.NewPasswordKey(random32(t))
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)
	e.rec = &provision.Reconciler{Admin: e.umami.Admin, Registry: pub, Key: key, Tx: conn.WithSystem, Now: time.Now, Log: log}

	// the real gateway binary on loopback, reading the same registry
	gwPort, gwOps := freePort(t), freePort(t)
	runBinary(t, bins.gateway, map[string]string{
		"NATS_URL": ns.URL, "NATS_REPLICAS": "1", "GATEWAY_NODE_ID": "edge-1",
		"GATEWAY_ADDR": "127.0.0.1:" + gwPort, "GATEWAY_OPS_ADDR": "127.0.0.1:" + gwOps,
		"PROOF_BITS": "8", "PROOF_REPLAY_LIMIT": "50", "IPCLUSTER_THRESHOLD": "20", "VISITOR_EVENTS_PER_HOUR": "600",
		"RATE_SITE_PER_SECOND": "200", "RATE_SITE_BURST": "400",
		"RATE_PREFIX_PER_SECOND": "20", "RATE_PREFIX_BURST": "60",
		"GATEWAY_MAX_BODY": "8192", "CLIENT_IP_HEADER": "X-Edge-Client-Ip",
		"GATEWAY_SECURE_TRANSPORT": "true",
		"DBIP_MMDB":                os.Getenv("DBIP_MMDB"),
		"TRACKER_RELEASE_DIR":      filepath.Join(repoRoot(), "packages", "tracker", "releases"),
	})
	waitOK(t, "http://127.0.0.1:"+gwOps+"/readyz")
	e.gw = gwclient.New("http://127.0.0.1:"+gwPort, false)
	e.gw.Header.Set("User-Agent", ua)
	// a documentation address stands in for caddy's client address header
	e.gw.Header.Set("X-Edge-Client-Ip", "198.51.100.23")
	e.gw.Header.Set("Sec-Fetch-Site", "cross-site")
	e.gw.Header.Set("Sec-Fetch-Mode", "cors")
	e.gw.Header.Set("Sec-Fetch-Dest", "empty")

	// the real replayer binary drains WEB_EVENTS into umami
	repOps := freePort(t)
	runBinary(t, bins.replayer, map[string]string{
		"NATS_URL": ns.URL, "NATS_REPLICAS": "1", "REPLAYER_NODE_ID": "core-1",
		"REPLAYER_OPS_ADDR": "127.0.0.1:" + repOps, "UMAMI_URL": e.umami.URL.String(),
		"REPLAYER_WORKERS": "4", "REPLAYER_MAX_IN_FLIGHT": "64",
	})
	waitOK(t, "http://127.0.0.1:"+repOps+"/healthz")

	cur, err := terms.Current()
	require.NoError(t, err)
	e.terms = cur.Version

	var api http.Handler
	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.ServeHTTP(w, r) }))
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	callback := e.srv.URL + "/api/auth/github/callback"
	fx := githuboauth.New(githuboauth.Config{ClientID: "enroll-test", ClientSecret: "enroll-test-secret", RedirectURL: callback})
	e.fixture = httptest.NewServer(fx.Handler())
	t.Cleanup(e.fixture.Close)

	e.scope, err = tenants.NewScope(conn, e.terms, e.rec, log)
	require.NoError(t, err)
	t.Cleanup(e.scope.Wait)
	sessions := session.New(conn)
	operators := map[int64]bool{operatorGHID: true}
	ah, err := auth.New(auth.Config{
		Reconcile:    e.scope.Reconcile,
		ClientID:     "enroll-test",
		ClientSecret: config.FromMap(map[string]string{"S": "enroll-test-secret"}).Secret("S", 1),
		RedirectURL:  callback,
		StateKey:     random32(t),
		Operators:    operators,
		Endpoints: auth.Endpoints{
			OAuth: oauth2.Endpoint{
				AuthURL:   e.fixture.URL + "/login/oauth/authorize",
				TokenURL:  e.fixture.URL + "/login/oauth/access_token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
			APIBase: e.fixture.URL,
		},
	}, conn, sessions)
	require.NoError(t, err)
	guard, err := operator.NewGuard(operators)
	require.NoError(t, err)
	mode, err := operator.NewEnrollmentMode(conn)
	require.NoError(t, err)
	actions, err := lifecycle.NewActions(conn, e.scope.Reconcile)
	require.NoError(t, err)
	review, err := operator.NewTenants(conn)
	require.NoError(t, err)
	auditLog, err := operator.NewAuditLog(conn)
	require.NoError(t, err)

	api = controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: e.srv.URL, Sessions: sessions,
		Public: []controlplane.Routes{controlplane.RoutesFunc(ah.Public)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(ah.Private),
			tenants.NewRoutes(e.scope), sites.NewRoutes(e.scope),
			controlplane.RoutesFunc(guard.Mount(mode, actions, review, auditLog)),
		},
	})
	return e
}

type client struct {
	e      *env
	hc     *http.Client
	userID uuid.UUID
}

func (e *env) signIn(t *testing.T, ghID int64, login string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &client{e: e, hc: &hc}

	r, err := c.hc.Get(e.srv.URL + "/api/auth/github/start?return_to=/")
	require.NoError(t, err)
	_ = r.Body.Close()
	require.Equal(t, http.StatusFound, r.StatusCode)
	authz, err := url.Parse(r.Header.Get("Location"))
	require.NoError(t, err)
	form := authz.Query()
	form.Set("fixture_decision", "authorize")
	form.Set("fixture_user_id", strconv.FormatInt(ghID, 10))
	form.Set("fixture_login", login)
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err = plain.PostForm(e.fixture.URL+"/login/oauth/authorize", form)
	require.NoError(t, err)
	_ = r.Body.Close()
	require.Equal(t, http.StatusFound, r.StatusCode)
	r, err = c.hc.Get(r.Header.Get("Location"))
	require.NoError(t, err)
	_ = r.Body.Close()
	require.Equal(t, http.StatusSeeOther, r.StatusCode)

	me := c.call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, http.StatusOK, me.status, string(me.body))
	c.userID = uuid.MustParse(me.json(t)["user"].(map[string]any)["id"].(string))
	return c
}

func (e *env) newUser(t *testing.T, login string) *client {
	t.Helper()
	e.nextGH++
	return e.signIn(t, e.nextGH, login)
}

type resp struct {
	status int
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(r.body, &m), string(r.body))
	return m
}

func (r resp) code(t *testing.T) string {
	t.Helper()
	e, ok := r.json(t)["error"].(map[string]any)
	require.True(t, ok, "no error envelope in %s", r.body)
	return e["code"].(string)
}

func (c *client) call(t *testing.T, method, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.e.srv.URL+path, rd)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", c.e.srv.URL)
	}
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return resp{status: res.StatusCode, body: b}
}

// enroll creates a project and returns its id and status. it waits for the
// reconcile the route started, so the umami and registry state is final.
func (c *client) enroll(t *testing.T, name string) (uuid.UUID, string) {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants", map[string]any{
		"name": name,
		"enrollment": map[string]any{
			"website": "https://example.org", "stellar_usage": "soroban contracts on mainnet",
		},
		"accepted_terms_version": c.e.terms,
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	c.e.scope.Wait()
	m := r.json(t)
	return uuid.MustParse(m["id"].(string)), m["status"].(string)
}

func (c *client) addSite(t *testing.T, tenant uuid.UUID, domain string) uuid.UUID {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/sites", map[string]any{
		"name": "site " + domain, "domain": domain, "origins": []string{"https://" + domain},
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	c.e.scope.Wait()
	return uuid.MustParse(r.json(t)["id"].(string))
}

func (c *client) tenant(t *testing.T, id uuid.UUID) map[string]any {
	t.Helper()
	r := c.call(t, http.MethodGet, "/api/tenants/"+id.String(), nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	return r.json(t)
}

// send fetches a challenge and posts one pageview for site from origin
// https://domain. it returns the challenge status and body, and the event
// status and body. without a challenge for site, proof is attached instead:
// a well formed proof solved for another site, because the gateway parses
// the proof as part of the schema check, which comes before the site check
// (docs/reference/wire-v1.md).
func (e *env) send(t *testing.T, site uuid.UUID, domain, path string, proof *gwclient.Proof) (int, string, int, string) {
	t.Helper()
	ctx := t.Context()
	e.gw.Header.Set("Origin", "https://"+domain)
	cs, cb, _, err := e.gw.Get(ctx, "/v1/c?w="+site.String())
	require.NoError(t, err)
	if cs == http.StatusOK {
		var ch gwclient.Challenge
		require.NoError(t, json.Unmarshal(cb, &ch))
		p := gwclient.Solve(ch)
		proof = &p
	}
	require.NotNil(t, proof, "no challenge for %s and no proof given", site)
	b, err := json.Marshal(gwclient.Pageview(site.String(), domain, path, proof))
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.gw.Base+"/v1/e", bytes.NewReader(b))
	require.NoError(t, err)
	for k, v := range e.gw.Header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	res, err := e.gw.HTTP.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	eb, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return cs, strings.TrimSpace(string(cb)), res.StatusCode, strings.TrimSpace(string(eb))
}

// proofFor solves a real challenge of an active site.
func (e *env) proofFor(t *testing.T, site uuid.UUID, domain string) *gwclient.Proof {
	t.Helper()
	e.gw.Header.Set("Origin", "https://"+domain)
	ch, err := e.gw.Challenge(t.Context(), site.String())
	require.NoError(t, err)
	p := gwclient.Solve(ch)
	return &p
}

// accepted waits until the gateway's registry snapshot has the site and an
// event is accepted (202), then until umami shows the pageview.
func (e *env) accepted(t *testing.T, site uuid.UUID, domain string) {
	t.Helper()
	require.Eventually(t, func() bool {
		cs, _, es, _ := e.send(t, site, domain, "/enroll-check", nil)
		return cs == http.StatusOK && es == http.StatusAccepted
	}, 30*time.Second, 300*time.Millisecond, "gateway never accepted an event for %s", site)
	require.Eventually(t, func() bool {
		st, err := e.umami.Admin.Stats(t.Context(), site.String(), umami.Range{
			Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour),
		})
		return err == nil && st.Pageviews >= 1
	}, time.Minute, time.Second, "umami never showed a pageview for %s", site)
}

// unknownAtGateway checks that the gateway refuses the site as unknown on
// both routes.
func (e *env) unknownAtGateway(t *testing.T, site uuid.UUID, domain string, donor *gwclient.Proof) {
	t.Helper()
	cs, cb, es, eb := e.send(t, site, domain, "/pending-check", donor)
	require.Equal(t, http.StatusNotFound, cs)
	require.Equal(t, "rejected_unknown_site", cb)
	require.Equal(t, http.StatusNotFound, es)
	require.Equal(t, "rejected_unknown_site", eb)
}

// nothingProvisioned checks umami and the registry for a tenant and its sites.
func (e *env) nothingProvisioned(t *testing.T, tenant uuid.UUID, siteIDs ...uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	teams, err := e.umami.Admin.FindTeamsByName(ctx, provision.TeamName(tenant))
	require.NoError(t, err)
	require.Empty(t, teams, "umami has a team for the pending tenant")
	var teamID *uuid.UUID
	var quarantine []uuid.UUID
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(ctx, `select umami_team_id from cp.tenants where id = $1`, tenant).Scan(&teamID))
		rows, err := c.Query(ctx, `select quarantine_website_id from cp.sites where tenant_id = $1`, tenant)
		require.NoError(t, err)
		quarantine, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		require.NoError(t, err)
	})
	require.Nil(t, teamID)
	for _, id := range append(append([]uuid.UUID{}, siteIDs...), quarantine...) {
		_, ok, err := e.umami.Admin.GetWebsite(ctx, id.String())
		require.NoError(t, err)
		require.False(t, ok, "umami has website %s", id)
	}
	for _, id := range siteIDs {
		_, err := e.sites.Get(ctx, id.String())
		require.ErrorIs(t, err, jetstream.ErrKeyNotFound, "registry has a key for site %s", id)
	}
	var n int
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(ctx, `select count(*) from cp.audit_log
			where action like 'provision.%' and (detail->>'tenant_id' = $1::text or target = any($2))`,
			tenant.String(), siteTargets(siteIDs)).Scan(&n))
	})
	require.Zero(t, n, "the reconciler recorded provisioning for the pending tenant")
}

func siteTargets(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, "site:"+id.String())
	}
	return out
}

func (e *env) superuser(t *testing.T, fn func(c *pgx.Conn)) {
	t.Helper()
	c, err := e.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.WithoutCancel(t.Context())) }()
	fn(c)
}

type auditRow struct {
	actor, target string
	detail        map[string]any
}

func (e *env) audit(t *testing.T, action string) []auditRow {
	t.Helper()
	var out []auditRow
	e.superuser(t, func(c *pgx.Conn) {
		rows, err := c.Query(t.Context(),
			`select actor, target, detail from cp.audit_log where action = $1 order by id`, action)
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var r auditRow
			var d []byte
			require.NoError(t, rows.Scan(&r.actor, &r.target, &d))
			require.NoError(t, json.Unmarshal(d, &r.detail))
			out = append(out, r)
		}
		require.NoError(t, rows.Err())
	})
	return out
}

func TestEnrollmentMode(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	op := e.signIn(t, operatorGHID, "enroll-operator")
	owner := e.newUser(t, "enroll-owner")
	other := e.newUser(t, "enroll-other")

	const path = "/api/operator/settings/enrollment-mode"

	t.Run("non operators get 403 on every method and unknown path", func(t *testing.T) {
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, path}, {http.MethodPut, path}, {http.MethodDelete, path},
			{http.MethodGet, "/api/operator/no-such-route"},
		} {
			var body any
			if c.method == http.MethodPut {
				body = map[string]any{"mode": "approval"}
			}
			r := owner.call(t, c.method, c.path, body)
			require.Equal(t, http.StatusForbidden, r.status, "%s %s: %s", c.method, c.path, r.body)
			require.Equal(t, "operator_required", r.code(t))
		}
		anon := &client{e: e, hc: e.srv.Client()}
		require.Equal(t, http.StatusUnauthorized, anon.call(t, http.MethodGet, path, nil).status)
	})

	t.Run("validation", func(t *testing.T) {
		for _, b := range []any{map[string]any{"mode": "closed"}, map[string]any{"mode": ""}, map[string]any{"mode": "Open"}} {
			r := op.call(t, http.MethodPut, path, b)
			require.Equal(t, http.StatusUnprocessableEntity, r.status, string(r.body))
			require.Equal(t, map[string]any{"mode": "invalid_value"}, r.json(t)["error"].(map[string]any)["fields"])
		}
		r := op.call(t, http.MethodPut, path, map[string]any{})
		require.Equal(t, http.StatusUnprocessableEntity, r.status)
		require.Equal(t, map[string]any{"mode": "required"}, r.json(t)["error"].(map[string]any)["fields"])
		r = op.call(t, http.MethodPut, path, map[string]any{"mode": 1})
		require.Equal(t, http.StatusBadRequest, r.status)
		require.Equal(t, "invalid_body", r.code(t))
		require.Empty(t, e.audit(t, operator.ActionEnrollmentMode))
	})

	// open mode: the seeded default
	r := op.call(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, map[string]any{"mode": "open"}, r.json(t))

	// setting the current value: 200, nothing audited
	r = op.call(t, http.MethodPut, path, map[string]any{"mode": "open"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Empty(t, e.audit(t, operator.ActionEnrollmentMode))

	// open: a new project is active, provisioned and receives events
	openTenant, status := owner.enroll(t, "Open Project")
	require.Equal(t, "active", status)
	openSite := owner.addSite(t, openTenant, "open-project.example")
	require.Equal(t, "ready", owner.tenant(t, openTenant)["provisioning"])
	e.accepted(t, openSite, "open-project.example")

	// switch to approval
	r = op.call(t, http.MethodPut, path, map[string]any{"mode": "approval"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, map[string]any{"mode": "approval"}, r.json(t))
	r = op.call(t, http.MethodGet, path, nil)
	require.Equal(t, map[string]any{"mode": "approval"}, r.json(t))
	rows := e.audit(t, operator.ActionEnrollmentMode)
	require.Len(t, rows, 1)
	require.Equal(t, "user:"+op.userID.String(), rows[0].actor)
	require.Equal(t, operator.TargetEnrollmentMode, rows[0].target)
	require.Equal(t, map[string]any{"mode": map[string]any{"old": "open", "new": "approval"}}, rows[0].detail)

	// a repeated switch to the same mode writes no second row
	r = op.call(t, http.MethodPut, path, map[string]any{"mode": "approval"})
	require.Equal(t, http.StatusOK, r.status)
	require.Len(t, e.audit(t, operator.ActionEnrollmentMode), 1)

	// approval: a new project is pending; its owner can still prepare sites
	pendingTenant, status := other.enroll(t, "Pending Project")
	require.Equal(t, "pending", status)
	pendingSite := other.addSite(t, pendingTenant, "pending-project.example")
	pendingSite2 := other.addSite(t, pendingTenant, "pending-two.example")
	r = other.call(t, http.MethodGet, "/api/tenants/"+pendingTenant.String()+"/sites/"+pendingSite.String(), nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, "pending", r.json(t)["status"])
	pt := other.tenant(t, pendingTenant)
	require.Equal(t, "pending", pt["status"])
	require.Equal(t, "pending", pt["provisioning"])
	r = other.call(t, http.MethodPatch, "/api/tenants/"+pendingTenant.String()+"/sites/"+pendingSite2.String(),
		map[string]any{"name": "renamed while pending"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	r = other.call(t, http.MethodDelete, "/api/tenants/"+pendingTenant.String()+"/sites/"+pendingSite2.String(), nil)
	require.Equal(t, http.StatusAccepted, r.status, string(r.body))
	e.scope.Wait()

	// the periodic reconcile of every tenant changes nothing for it either
	changes, err := e.rec.ReconcileAll(ctx)
	require.NoError(t, err)
	for _, c := range changes {
		require.NotContains(t, c.Target, pendingTenant.String())
		require.NotContains(t, c.Target, pendingSite.String())
	}
	e.nothingProvisioned(t, pendingTenant, pendingSite, pendingSite2)
	e.unknownAtGateway(t, pendingSite, "pending-project.example", e.proofFor(t, openSite, "open-project.example"))

	// the existing open-mode project is unaffected by the switch
	require.Equal(t, "active", owner.tenant(t, openTenant)["status"])
	cs, _, es, _ := e.send(t, openSite, "open-project.example", "/after-switch", nil)
	require.Equal(t, http.StatusOK, cs)
	require.Equal(t, http.StatusAccepted, es)
	reg, err := e.sites.Get(ctx, openSite.String())
	require.NoError(t, err)
	var entry registry.Site
	require.NoError(t, json.Unmarshal(reg.Value(), &entry))
	require.Equal(t, "active", entry.Status)

	// a registry key that exists for a pending tenant anyway (written outside
	// the reconciler) is removed on the next reconcile, so it never takes
	// traffic
	pub, err := registry.NewPublisher(ctx, e.js)
	require.NoError(t, err)
	require.NoError(t, pub.PutSite(ctx, registry.Site{
		ID: pendingSite.String(), TenantID: pendingTenant.String(), Status: "active",
		Origins: []string{"https://pending-project.example"}, QuarantineWebsiteID: uuid.NewString(),
	}))
	require.NoError(t, e.rec.Reconcile(ctx, pendingTenant))
	_, err = e.sites.Get(ctx, pendingSite.String())
	require.ErrorIs(t, err, jetstream.ErrKeyNotFound)
	require.Len(t, e.auditTarget(t, provision.ActionRegistryDelete, "site:"+pendingSite.String()), 1)

	// back to open: only new enrollments follow, the pending one stays pending
	r = op.call(t, http.MethodPut, path, map[string]any{"mode": "open"})
	require.Equal(t, http.StatusOK, r.status)
	rows = e.audit(t, operator.ActionEnrollmentMode)
	require.Len(t, rows, 2)
	require.Equal(t, map[string]any{"mode": map[string]any{"old": "approval", "new": "open"}}, rows[1].detail)
	require.Equal(t, "pending", other.tenant(t, pendingTenant)["status"])
	again, status := other.enroll(t, "Open Again")
	require.Equal(t, "active", status)
	require.Equal(t, "ready", other.tenant(t, again)["provisioning"])

	t.Run("approval contract for stream a", func(t *testing.T) {
		ctx := t.Context()
		// what approve does (contract 5.5) in the database, then the one call
		// it must make afterwards: Reconcile(tenant). the same reconciler then
		// provisions the tenant and the gateway accepts its events.
		e.superuser(t, func(c *pgx.Conn) {
			_, err := c.Exec(ctx, `update cp.tenants set status = 'active', approved_by = $2,
				status_reason = null, status_changed_at = now(), updated_at = now() where id = $1`, pendingTenant, op.userID)
			require.NoError(t, err)
			_, err = c.Exec(ctx, `update cp.sites set status = 'active', updated_at = now()
				where tenant_id = $1 and status = 'pending'`, pendingTenant)
			require.NoError(t, err)
		})
		require.NoError(t, e.rec.Reconcile(ctx, pendingTenant))
		require.Equal(t, "ready", other.tenant(t, pendingTenant)["provisioning"])
		e.accepted(t, pendingSite, "pending-project.example")
	})
}

func (e *env) auditTarget(t *testing.T, action, target string) []auditRow {
	t.Helper()
	var out []auditRow
	for _, r := range e.audit(t, action) {
		if r.target == target {
			out = append(out, r)
		}
	}
	return out
}
