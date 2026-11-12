//go:build isolation

// Package consoleisolation tests the tenant, member, site, snippet and
// ingest routes of the control plane over real https, real postgresql,
// real nats and a real umami (the design, isolation tests for the
// console api). users sign in through the real auth routes against the
// github oauth fixture, and the real provisioning reconciler runs after
// every change.
package consoleisolation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/justmert/stellar-analytics/db"
	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/controlplane"
	"github.com/justmert/stellar-analytics/internal/controlplane/auth"
	"github.com/justmert/stellar-analytics/internal/controlplane/ingest"
	"github.com/justmert/stellar-analytics/internal/controlplane/members"
	"github.com/justmert/stellar-analytics/internal/controlplane/provision"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/controlplane/sites"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	"github.com/justmert/stellar-analytics/internal/controlplane/terms"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/githuboauth"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

const ingestOrigin = "https://in.example.org"

type env struct {
	srv      *httptest.Server
	fixture  *httptest.Server
	conn     *pdb.DB
	pg       *dbtest.Postgres
	sites    jetstream.KeyValue
	terms    string
	tracker  *controlplane.Tracker
	umami    *umamictr.Stack
	nextGHID int64
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func random32(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// start brings up postgresql, nats, umami, the oauth fixture and the
// control plane api on https. the session cookie is Secure, so the api is
// served over tls like in production.
func start(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e := &env{nextGHID: 5000}

	// database
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
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	require.NoError(t, err)
	e.conn, err = pdb.Open(ctx, pcfg, "console-isolation-test")
	require.NoError(t, err)
	t.Cleanup(e.conn.Close)

	// nats with the section 4.6 topology, for the site registry
	ns := natsctr.StartSingle(t)
	nc, err := nats.Connect(ns.URL)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	pub, err := registry.NewPublisher(ctx, js)
	require.NoError(t, err)
	e.sites, err = js.KeyValue(ctx, natsx.BucketRegistrySites)
	require.NoError(t, err)

	// umami and the real reconciler
	e.umami = umamictr.Start(t)
	key, err := umami.NewPasswordKey(random32(t))
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)
	rec := &provision.Reconciler{
		Admin: e.umami.Admin, Registry: pub, Key: key,
		Tx: e.conn.WithSystem, Now: time.Now, Log: log,
	}

	cur, err := terms.Current()
	require.NoError(t, err)
	e.terms = cur.Version
	e.tracker, err = controlplane.LoadTracker(filepath.Join(repoRoot(t), "packages", "tracker", "releases"), ingestOrigin)
	require.NoError(t, err)

	// the api, started before it has a handler, because the oauth callback
	// url and the console origin are its own address
	var api http.Handler
	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.ServeHTTP(w, r) }))
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)

	callback := e.srv.URL + "/api/auth/github/callback"
	fx := githuboauth.New(githuboauth.Config{ClientID: "console-test", ClientSecret: "console-test-secret", RedirectURL: callback})
	e.fixture = httptest.NewServer(fx.Handler())
	t.Cleanup(e.fixture.Close)

	scope, err := tenants.NewScope(e.conn, e.terms, rec, log)
	require.NoError(t, err)
	// runs before the containers stop, so no reconcile outlives them
	t.Cleanup(scope.Wait)

	sessions := session.New(e.conn)
	ah, err := auth.New(auth.Config{
		// as in cmd/controlplane: invites resolved at sign-in reconcile at once
		Reconcile:    scope.Reconcile,
		ClientID:     "console-test",
		ClientSecret: config.FromMap(map[string]string{"S": "console-test-secret"}).Secret("S", 1),
		RedirectURL:  callback,
		StateKey:     random32(t),
		Endpoints: auth.Endpoints{
			OAuth: oauth2.Endpoint{
				AuthURL:   e.fixture.URL + "/login/oauth/authorize",
				TokenURL:  e.fixture.URL + "/login/oauth/access_token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
			APIBase: e.fixture.URL,
		},
	}, e.conn, sessions)
	require.NoError(t, err)

	snippet, err := controlplane.NewSnippetRoutes(scope, e.tracker, "")
	require.NoError(t, err)

	api = controlplane.NewHandler(controlplane.Options{
		Log:           log,
		ConsoleOrigin: e.srv.URL,
		Sessions:      sessions,
		Public:        []controlplane.Routes{controlplane.RoutesFunc(ah.Public)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(ah.Private),
			tenants.NewRoutes(scope), members.NewRoutes(scope), sites.NewRoutes(scope),
			ingest.NewRoutes(scope), snippet,
		},
	})
	return e
}

// client is one browser: a cookie jar, no automatic redirects.
type client struct {
	e      *env
	hc     *http.Client
	userID uuid.UUID
	login  string
}

func (e *env) anonymous(t *testing.T) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{e: e, hc: &hc}
}

// signIn runs the real sign-in: start, the fixture's consent, the callback.
func (e *env) signIn(t *testing.T, login string) *client {
	t.Helper()
	e.nextGHID++
	return e.signInAs(t, e.nextGHID, login)
}

func (e *env) signInAs(t *testing.T, ghID int64, login string) *client {
	t.Helper()
	c := e.anonymous(t)
	resp, err := c.hc.Get(e.srv.URL + "/api/auth/github/start?return_to=/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	authz, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)

	form := authz.Query()
	form.Set("fixture_decision", "authorize")
	form.Set("fixture_user_id", strconv.FormatInt(ghID, 10))
	form.Set("fixture_login", login)
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = plain.PostForm(e.fixture.URL+"/login/oauth/authorize", form)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	resp, err = c.hc.Get(resp.Header.Get("Location"))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "/", resp.Header.Get("Location"))

	me := c.call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, http.StatusOK, me.status, string(me.body))
	c.userID = uuid.MustParse(me.json(t)["user"].(map[string]any)["id"].(string))
	c.login = login
	return c
}

type resp struct {
	status int
	body   []byte
	header http.Header
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

func (r resp) fields(t *testing.T) map[string]any {
	t.Helper()
	f, _ := r.json(t)["error"].(map[string]any)["fields"].(map[string]any)
	return f
}

// call sends a request the way the console does: json, the csrf header and
// the console origin on state changes.
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
	return resp{status: res.StatusCode, body: b, header: res.Header}
}

// enroll creates a tenant and returns its id.
func (c *client) enroll(t *testing.T, name string) uuid.UUID {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants", map[string]any{
		"name": name,
		"enrollment": map[string]any{
			"website":       "https://example.org",
			"stellar_usage": "soroban contracts on mainnet",
		},
		"accepted_terms_version": c.e.terms,
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	return uuid.MustParse(r.json(t)["id"].(string))
}

// addSite creates a site and returns its id.
func (c *client) addSite(t *testing.T, tenant uuid.UUID, domain string) uuid.UUID {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/sites", map[string]any{
		"name": "site " + domain, "domain": domain, "origins": []string{"https://" + domain},
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	return uuid.MustParse(r.json(t)["id"].(string))
}

// superuser runs fn as the postgres superuser on the platform database, for
// test setup the api does not offer (another terms version, ingest rows,
// a removed tenant) and for reading the audit log.
func (e *env) superuser(t *testing.T, fn func(c *pgx.Conn)) {
	t.Helper()
	c, err := e.pg.Superuser(context.Background(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	fn(c)
}

func (e *env) auditCount(t *testing.T, action, target string) int {
	t.Helper()
	var n int
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(),
			`select count(*) from cp.audit_log where action = $1 and target = $2`, action, target).Scan(&n))
	})
	return n
}
