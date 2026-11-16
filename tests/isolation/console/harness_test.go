//go:build isolation

// Package consoleisolation tests the control plane api over real https,
// real postgresql, real nats and a real umami: the console api part of the
// tenant isolation gate.
// start wires every route group the way cmd/controlplane/main.go does, with
// the production constructors: the private session routes, the operator
// area behind its guard and the machine metrics api mount. users sign in
// through the real auth routes against the github oauth fixture, and the
// real provisioning reconciler runs after every change.
package consoleisolation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/justmert/veristar/db"
	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/account"
	"github.com/justmert/veristar/internal/controlplane/apimetrics"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	"github.com/justmert/veristar/internal/controlplane/erasure"
	"github.com/justmert/veristar/internal/controlplane/exports"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/ingest"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/mcpserver"
	"github.com/justmert/veristar/internal/controlplane/members"
	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/reports"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/sites"
	"github.com/justmert/veristar/internal/controlplane/sso"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/netguard"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
	"github.com/justmert/veristar/tests/fixtures/stepuptest"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

const ingestOrigin = "https://in.example.org"

// webOrigin is where sso sends a browser; nothing listens there in these
// tests, the redirect itself is what they check
const webOrigin = "https://web.example.org"

// operatorGHID is the one github id in the harness's OPERATOR_GITHUB_IDS
const operatorGHID int64 = 4_000_001

type env struct {
	srv      *httptest.Server
	fixture  *httptest.Server
	conn     *pdb.DB
	pg       *dbtest.Postgres
	sites    jetstream.KeyValue
	otlp     jetstream.KeyValue
	terms    string
	tracker  *controlplane.Tracker
	umami    *umamictr.Stack
	nextGHID int64
	// accounts is the oauth fixture, which also answers github's public
	// users api; githubAccount registers a login there
	accounts *githuboauth.Server
	// router is the api handler, for walking its routes
	router chi.Routes
	// checks runs the stellar.toml fetch after a site is created; Wait
	// lets a test compare site rows once those writes are done
	checks *domaincheck.Routes
	// mcp is the mcp server, for its resource uri
	mcp *mcpserver.Server
	// keys are the operators' software authenticators
	keys stepuptest.Keys
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
	// the three pools of cmd/controlplane
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
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
	e.otlp, err = js.KeyValue(ctx, natsx.BucketRegistryOTLP)
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
	// logins the tests add without their holders ever signing in; members
	// are resolved to a github account id at add time (contract 4.2.5)
	fx.Register(githuboauth.User{ID: 9_000_001, Login: "gate-pending"})
	fx.Register(githuboauth.User{ID: 9_000_002, Login: "intruder-friend"})
	e.accounts = fx
	e.fixture = httptest.NewServer(fx.Handler())
	t.Cleanup(e.fixture.Close)

	scope, err := tenants.NewScope(e.conn, e.terms, rec, log)
	require.NoError(t, err)
	// runs before the containers stop, so no reconcile outlives them
	t.Cleanup(scope.Wait)

	sessions := session.New(e.conn)
	operators := map[int64]bool{operatorGHID: true}
	ah, err := auth.New(auth.Config{
		// as in cmd/controlplane: invites resolved at sign-in reconcile at once
		Reconcile:     scope.Reconcile,
		ClientID:      "console-test",
		ClientSecret:  config.FromMap(map[string]string{"S": "console-test-secret"}).Secret("S", 1),
		RedirectURL:   callback,
		StateKey:      random32(t),
		ConsoleOrigin: e.srv.URL,
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
	users, err := auth.NewUserLookup(auth.UserLookupConfig{
		ClientID:     "console-test",
		ClientSecret: config.FromMap(map[string]string{"S": "console-test-secret"}).Secret("S", 1),
		Endpoints:    auth.Endpoints{APIBase: e.fixture.URL},
	})
	require.NoError(t, err)

	// from here on the wiring follows cmd/controlplane/main.go serve, group
	// by group and in the same order, with the production constructors. the
	// settings are the values deploy/compose/gen-env.sh writes, read by the
	// production loaders
	settings := config.FromMap(map[string]string{"API_MINUTE_RETENTION_DAYS": "30", "API_HOURLY_RETENTION_MONTHS": "36"})
	retention, err := config.LoadAPIRetention(settings)
	require.NoError(t, err)
	erasureCfg, err := config.LoadErasure(settings)
	require.NoError(t, err)
	_, guard := stepuptest.Guard(t, e.conn, sessions, e.srv.URL, identity.Operators{GitHub: operators})
	roster, err := operator.NewRoster(operator.RosterConfig{
		DB: e.conn, Guard: guard, Lookup: users, Bootstrap: identity.Operators{GitHub: operators}, EnrollWindow: time.Hour,
	})
	require.NoError(t, err)
	ng, err := netguard.New(net.DefaultResolver)
	require.NoError(t, err)
	checks, err := domaincheck.NewRoutes(scope, &domaincheck.Checker{Guard: ng, Resolver: net.DefaultResolver}, time.Now, log)
	require.NoError(t, err)
	t.Cleanup(checks.Wait)
	e.checks = checks
	modeRoutes, err := operator.NewEnrollmentMode(e.conn)
	require.NoError(t, err)
	auditRoutes, err := operator.NewAuditLog(e.conn)
	require.NoError(t, err)
	keepRoutes, err := operator.NewRemovalKeepAggregates(e.conn)
	require.NoError(t, err)
	actions, err := lifecycle.NewActions(e.conn, scope.Reconcile)
	require.NoError(t, err)
	global, err := operator.NewGlobal(e.conn)
	require.NoError(t, err)
	review, err := operator.NewTenants(e.conn)
	require.NoError(t, err)
	apiKeys, err := operator.NewAPIKeys(e.conn)
	require.NoError(t, err)
	machine, err := operator.NewMachine(operator.MachineConfig{DB: e.conn, Umami: e.umami.Admin, Log: log})
	require.NoError(t, err)
	eraser, err := erasure.New(erasure.Config{
		DB: e.conn, NC: nc, Umami: e.umami.Admin, QueueWait: erasureCfg.QueueWait, Now: time.Now, Log: log,
	})
	require.NoError(t, err)
	t.Cleanup(eraser.Wait)
	erasureRoutes, err := erasure.NewRoutes(scope, eraser)
	require.NoError(t, err)
	otlpRoutes, err := otlptokens.NewRoutes(scope, pub)
	require.NoError(t, err)
	metricRoutes, err := apimetrics.NewRoutes(scope, apimetrics.Resolver{Now: time.Now, Retention: retention})
	require.NoError(t, err)
	snippet, err := controlplane.NewSnippetRoutes(scope, e.tracker, "")
	require.NoError(t, err)
	ssoH := &sso.Handler{
		Tx:        e.conn.WithSystem,
		Umami:     e.umami.Client,
		Key:       key,
		WebOrigin: webOrigin,
		UserID: func(r *http.Request) (uuid.UUID, bool) {
			u, ok := session.CurrentUser(r.Context())
			return u.ID, ok
		},
		Now: time.Now,
	}
	require.NoError(t, ssoH.Check())
	ssoPath, ok := strings.CutPrefix(sso.Path, "/api")
	require.True(t, ok)

	memberRoutes, err := members.NewRoutes(scope, users)
	require.NoError(t, err)
	// the on-chain witness and the exports (contract 15 and 16) against a
	// ledger api that knows no account: the gate's refusals come before
	// any ledger read
	ledger := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(ledger.Close)
	stellarCfg, err := config.LoadStellar(config.FromMap(map[string]string{
		"STELLAR_HORIZON_URL": ledger.URL, "STELLAR_RPC_URL": ledger.URL,
	}))
	require.NoError(t, err)
	horizon, rpc, err := chain.Sources(stellarCfg)
	require.NoError(t, err)
	onchainRoutes, err := onchain.NewRoutes(scope, horizon, rpc, time.Now)
	require.NoError(t, err)
	onchainOperator, err := operator.NewOnchain(e.conn, time.Now)
	require.NoError(t, err)
	operatorExports, err := operator.NewExports(global)
	require.NoError(t, err)
	overviewExport, err := exports.NewOverview(scope, time.Now)
	require.NoError(t, err)
	scfView, err := operator.NewSCFView(scope, e.conn, time.Now)
	require.NoError(t, err)
	// cohorts, threshold rules and signed usage reports (contract 19, 20)
	cohortRoutes, err := operator.NewCohorts(e.conn, time.Now)
	require.NoError(t, err)
	tenantCohort, err := operator.NewTenantCohort(scope, time.Now)
	require.NoError(t, err)
	ruleRoutes, err := operator.NewRules(e.conn, time.Now)
	require.NoError(t, err)
	reportKey := keypair.MustRandom()
	signing, err := config.LoadReportSigning(config.FromMap(map[string]string{
		"REPORT_SIGNING_SEED": reportKey.Seed(), "REPORT_SIGNING_KEY": reportKey.Address(),
	}))
	require.NoError(t, err)
	reportRoutes, err := reports.New(reports.Config{
		Scope: scope, DB: e.conn, Signing: signing, ConsoleOrigin: e.srv.URL, Platform: "test", Now: time.Now,
	})
	require.NoError(t, err)
	// the mcp server (contract 12), wired as cmd/controlplane wires it
	e.mcp, err = mcpserver.New(mcpserver.Config{
		DB:    e.conn,
		Guard: guard,
		Reads: []mcpserver.Routes{
			tenants.NewRoutes(scope), sites.NewRoutes(scope), metricRoutes, onchainRoutes, scfView, tenantCohort,
			controlplane.RoutesFunc(guard.Mount(review, global, onchainOperator, cohortRoutes, ruleRoutes)),
		},
		Sessions: ssoH,
		Umami:    e.umami.Client,
		Issuer:   e.srv.URL,
		StateKey: random32(t),
		Log:      log,
		Registry: prometheus.NewRegistry(),
		Version:  "test",
	})
	require.NoError(t, err)
	accountRoutes, err := account.NewRoutes(account.Config{
		DB: e.conn, Sessions: sessions, Umami: e.umami.Admin, Reconcile: scope.Reconcile,
	})
	require.NoError(t, err)
	api = controlplane.NewHandler(controlplane.Options{
		Log:           log,
		ConsoleOrigin: e.srv.URL,
		Sessions:      sessions,
		Public: []controlplane.Routes{controlplane.RoutesFunc(ah.Public), controlplane.RoutesFunc(terms.Routes),
			controlplane.RoutesFunc(reportRoutes.Public)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(ah.Private),
			accountRoutes,
			tenants.NewRoutes(scope),
			memberRoutes,
			sites.NewRoutes(scope).OnCreate(checks),
			checks,
			ingest.NewRoutes(scope),
			otlpRoutes,
			metricRoutes,
			erasureRoutes,
			snippet,
			onchainRoutes,
			overviewExport,
			scfView,
			tenantCohort,
			controlplane.RoutesFunc(reportRoutes.Private),
			controlplane.RoutesFunc(func(r chi.Router) { r.Method(http.MethodGet, ssoPath, ssoH) }),
			e.mcp.TenantRoutes(scope),
			e.mcp.ConsentRoutes(),
			controlplane.RoutesFunc(guard.Mount(modeRoutes, keepRoutes, auditRoutes, actions, review, global, apiKeys,
				e.mcp.OperatorRoutes(), onchainOperator, operatorExports, cohortRoutes, ruleRoutes, roster)),
		},
		Mounts: []controlplane.Mount{
			{Path: operator.MachinePath, Handler: machine.Handler()},
			{Path: mcpserver.Path, Handler: e.mcp.Handler()},
			{Path: "/oauth", Handler: e.mcp.OAuthHandler()},
			{Path: "/.well-known", Handler: e.mcp.MetadataHandler(reportRoutes.WellKnown)},
		},
	})
	routes, ok := api.(chi.Routes)
	require.True(t, ok, "the api handler is a chi router")
	e.router = routes
	return e
}

// stepUp gives an operator's session its passkey step-up (contract 5.15)
func (c *client) stepUp(t *testing.T) {
	t.Helper()
	c.e.keys.StepUp(t, c.userID, stepuptest.Origin(c.e.srv.URL), func(method, path string, body any) (int, []byte) {
		r := c.call(t, method, path, body)
		return r.status, r.body
	})
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

// githubAccount gives login a new github account id in the fixture, as
// github holds an account for every login a person can be invited by. the
// control plane binds an invite to that id (contract 4.2.5), so the invitee
// must later sign in with it: signInAs(t, id, login).
func (e *env) githubAccount(t *testing.T, login string) int64 {
	t.Helper()
	e.nextGHID++
	e.accounts.Register(githuboauth.User{ID: e.nextGHID, Login: login})
	return e.nextGHID
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
