package lifecycle_test

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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/members"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/sites"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

// env is one real postgresql, nats and umami, the oauth fixture and the
// control plane api over https with the operator area mounted behind the
// real guard. the api can be rebuilt with another OPERATOR_GITHUB_IDS, the
// way a restart with a changed setting would.
type env struct {
	pg       *dbtest.Postgres
	d        *pdb.DB
	kv       jetstream.KeyValue
	um       *umamictr.Stack
	rec      *provision.Reconciler
	scope    *tenants.Scope
	sessions *session.Manager
	srv      *httptest.Server
	fixture  *httptest.Server
	stateKey []byte
	terms    string
	opGH     int64
	api      atomic.Pointer[http.Handler]
	nextGHID int64
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
	ctx := t.Context()
	e := &env{nextGHID: 900_000}
	e.pg = jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(e.pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	e.d, err = pdb.Open(ctx, pcfg, "lifecycle-test")
	require.NoError(t, err)
	t.Cleanup(e.d.Close)

	ns := jobstest.NATS(t)
	_, js := jobstest.Connect(t, ns.URL)
	pub, err := registry.NewPublisher(ctx, js)
	require.NoError(t, err)
	e.kv, err = js.KeyValue(ctx, natsx.BucketRegistrySites)
	require.NoError(t, err)

	e.um = umamictr.Start(t)
	key, err := umami.NewPasswordKey(random32(t))
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)
	e.rec = &provision.Reconciler{Admin: e.um.Admin, Registry: pub, Key: key, Tx: e.d.WithSystem, Now: time.Now, Log: log}

	cur, err := terms.Current()
	require.NoError(t, err)
	e.terms = cur.Version
	e.scope, err = tenants.NewScope(e.d, e.terms, e.rec, log)
	require.NoError(t, err)
	t.Cleanup(e.scope.Wait)
	e.sessions = session.New(e.d)
	e.stateKey = random32(t)

	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*e.api.Load()).ServeHTTP(w, r)
	}))
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	fx := githuboauth.New(githuboauth.Config{
		ClientID: "lifecycle-test", ClientSecret: "lifecycle-test-secret", RedirectURL: e.srv.URL + "/api/auth/github/callback",
	})
	// invited below without ever signing in; invites are bound to the
	// github account id the login resolves to (contract 4.2.5)
	fx.Register(githuboauth.User{ID: 9_000_001, Login: "not-yet-signed-in"})
	e.fixture = httptest.NewServer(fx.Handler())
	t.Cleanup(e.fixture.Close)
	return e
}

// build makes the api of a control plane started with the given
// OPERATOR_GITHUB_IDS and serves it from now on.
func (e *env) build(t *testing.T, operators ...int64) {
	t.Helper()
	ids := map[int64]bool{}
	for _, id := range operators {
		ids[id] = true
	}
	log := slog.New(slog.DiscardHandler)
	ah, err := auth.New(auth.Config{
		Reconcile:    e.scope.Reconcile,
		ClientID:     "lifecycle-test",
		ClientSecret: config.FromMap(map[string]string{"S": "lifecycle-test-secret"}).Secret("S", 1),
		RedirectURL:  e.srv.URL + "/api/auth/github/callback",
		StateKey:     e.stateKey,
		Operators:    ids,
		Endpoints: auth.Endpoints{
			OAuth: oauth2.Endpoint{
				AuthURL:   e.fixture.URL + "/login/oauth/authorize",
				TokenURL:  e.fixture.URL + "/login/oauth/access_token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
			APIBase: e.fixture.URL,
		},
	}, e.d, e.sessions)
	require.NoError(t, err)
	users, err := auth.NewUserLookup(auth.UserLookupConfig{
		ClientID:     "lifecycle-test",
		ClientSecret: config.FromMap(map[string]string{"S": "lifecycle-test-secret"}).Secret("S", 1),
		Endpoints:    auth.Endpoints{APIBase: e.fixture.URL},
	})
	require.NoError(t, err)
	guard, err := operator.NewGuard(ids)
	require.NoError(t, err)
	mode, err := operator.NewEnrollmentMode(e.d)
	require.NoError(t, err)
	keep, err := operator.NewRemovalKeepAggregates(e.d)
	require.NoError(t, err)
	auditLog, err := operator.NewAuditLog(e.d)
	require.NoError(t, err)
	actions, err := lifecycle.NewActions(e.d, e.scope.Reconcile)
	require.NoError(t, err)
	memberRoutes, err := members.NewRoutes(e.scope, users)
	require.NoError(t, err)
	h := controlplane.NewHandler(controlplane.Options{
		Log:           log,
		ConsoleOrigin: e.srv.URL,
		Sessions:      e.sessions,
		Public:        []controlplane.Routes{controlplane.RoutesFunc(ah.Public)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(ah.Private),
			tenants.NewRoutes(e.scope), memberRoutes, sites.NewRoutes(e.scope),
			controlplane.RoutesFunc(guard.Mount(mode, keep, auditLog, actions)),
		},
	})
	e.api.Store(&h)
}

type client struct {
	e      *env
	hc     *http.Client
	userID uuid.UUID
	ghID   int64
}

func (e *env) newGHID() int64 { e.nextGHID++; return e.nextGHID }

func (e *env) anonymous(t *testing.T) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{e: e, hc: &hc}
}

// signIn runs the real sign-in against the oauth fixture.
func (e *env) signIn(t *testing.T, ghID int64, login string) *client {
	t.Helper()
	c := e.anonymous(t)
	res, err := c.hc.Get(e.srv.URL + "/api/auth/github/start?return_to=/")
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	authz, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	form := authz.Query()
	form.Set("fixture_decision", "authorize")
	form.Set("fixture_user_id", strconv.FormatInt(ghID, 10))
	form.Set("fixture_login", login)
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err = plain.PostForm(e.fixture.URL+"/login/oauth/authorize", form)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	res, err = c.hc.Get(res.Header.Get("Location"))
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	me := c.call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, http.StatusOK, me.status, string(me.body))
	c.userID = uuid.MustParse(me.json(t)["user"].(map[string]any)["id"].(string))
	c.ghID = ghID
	return c
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

func (c *client) enroll(t *testing.T, name string) (uuid.UUID, string) {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants", map[string]any{
		"name":                   name,
		"enrollment":             map[string]any{"website": "https://example.org", "stellar_usage": "soroban contracts"},
		"accepted_terms_version": c.e.terms,
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	m := r.json(t)
	return uuid.MustParse(m["id"].(string)), m["status"].(string)
}

func (c *client) addSite(t *testing.T, tenant uuid.UUID, domain string) (uuid.UUID, string) {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/sites", map[string]any{
		"name": "site " + domain, "domain": domain, "origins": []string{"https://" + domain},
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	m := r.json(t)
	return uuid.MustParse(m["id"].(string)), m["status"].(string)
}

func (c *client) action(t *testing.T, tenant uuid.UUID, act string, body map[string]any) resp {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	return c.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/"+act, body)
}

func (e *env) reconcile(t *testing.T, id uuid.UUID) {
	t.Helper()
	require.NoError(t, e.rec.Reconcile(context.Background(), id))
}

func (e *env) superuser(t *testing.T, fn func(c *pgx.Conn)) {
	t.Helper()
	c, err := e.pg.Superuser(context.Background(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	fn(c)
}

func (e *env) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(), query, args...).Scan(&n))
	})
	return n
}

func (e *env) registryStatus(t *testing.T, site uuid.UUID) (string, bool) {
	t.Helper()
	en, err := e.kv.Get(context.Background(), site.String())
	if err != nil {
		require.ErrorIs(t, err, jetstream.ErrKeyNotFound)
		return "", false
	}
	s, err := registry.DecodeSite(en.Key(), en.Value())
	require.NoError(t, err)
	return s.Status, true
}

func (e *env) teamID(t *testing.T, tenant uuid.UUID) uuid.NullUUID {
	t.Helper()
	var id uuid.NullUUID
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(), `select umami_team_id from cp.tenants where id = $1`, tenant).Scan(&id))
	})
	return id
}

func (e *env) quarantineID(t *testing.T, site uuid.UUID) string {
	t.Helper()
	var id uuid.UUID
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(), `select quarantine_website_id from cp.sites where id = $1`, site).Scan(&id))
	})
	return id.String()
}

// one set of containers; every scenario uses its own users and tenants
func TestLifecycle(t *testing.T) {
	e := start(t)
	e.opGH = e.newGHID()
	e.build(t, e.opGH)
	op := e.signIn(t, e.opGH, "scf-operator")

	t.Run("NonOperatorsGet403", func(t *testing.T) { testNonOperators(t, e) })
	t.Run("OperatorIDRemoved", func(t *testing.T) { testOperatorIDRemoved(t, e) })
	t.Run("ApproveAndReject", func(t *testing.T) { testApproveReject(t, e, op) })
	t.Run("SuspendResume", func(t *testing.T) { testSuspendResume(t, e, op) })
	t.Run("Remove", func(t *testing.T) { testRemove(t, e, op) })
	t.Run("KeepAggregates", func(t *testing.T) { testKeepAggregates(t, e, op) })
	t.Run("PurgeResumes", func(t *testing.T) { testPurgeResumes(t, e, op) })
	t.Run("SitesInReview", func(t *testing.T) { testSitesInReview(t, e, op) })
	t.Run("AuditList", func(t *testing.T) { testAuditList(t, e, op) })
}

// every operator route, with a body valid for it
func operatorRoutes(tenant, site string) []struct {
	method, path string
	body         any
} {
	reason := map[string]any{"reason": "test"}
	return []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/operator/settings/enrollment-mode", nil},
		{http.MethodPut, "/api/operator/settings/enrollment-mode", map[string]any{"mode": "approval"}},
		{http.MethodGet, "/api/operator/settings/removal-keep-aggregates", nil},
		{http.MethodPut, "/api/operator/settings/removal-keep-aggregates", map[string]any{"keep": true}},
		{http.MethodGet, "/api/operator/audit", nil},
		{http.MethodGet, "/api/operator/tenants", nil},
		{http.MethodGet, "/api/operator/tenants/" + tenant, nil},
		{http.MethodPost, "/api/operator/tenants/" + tenant + "/approve", map[string]any{}},
		{http.MethodPost, "/api/operator/tenants/" + tenant + "/reject", reason},
		{http.MethodPost, "/api/operator/tenants/" + tenant + "/suspend", reason},
		{http.MethodPost, "/api/operator/tenants/" + tenant + "/resume", map[string]any{}},
		{http.MethodPost, "/api/operator/tenants/" + tenant + "/remove", reason},
		{http.MethodPost, "/api/operator/sites/" + site + "/approve", map[string]any{}},
		{http.MethodPost, "/api/operator/sites/" + site + "/reject", reason},
		{http.MethodGet, "/api/operator/no-such-route", nil},
		{http.MethodDelete, "/api/operator/audit", nil},
	}
}

func testNonOperators(t *testing.T, e *env) {
	owner := e.signIn(t, e.newGHID(), "plain-owner")
	tenant, status := owner.enroll(t, "Plain Project")
	require.Equal(t, "active", status)
	site, _ := owner.addSite(t, tenant, "plain.example.org")
	e.reconcile(t, tenant)

	anon := e.anonymous(t)
	for _, rt := range operatorRoutes(tenant.String(), site.String()) {
		r := owner.call(t, rt.method, rt.path, rt.body)
		require.Equal(t, http.StatusForbidden, r.status, "%s %s: %s", rt.method, rt.path, r.body)
		require.Equal(t, "operator_required", r.code(t), "%s %s", rt.method, rt.path)
		r = anon.call(t, rt.method, rt.path, rt.body)
		require.Equal(t, http.StatusUnauthorized, r.status, "%s %s", rt.method, rt.path)
	}
	// nothing changed: no lifecycle audit row, the tenant is still active
	require.Equal(t, 0, e.count(t, `select count(*) from cp.audit_log where target = $1 and action like 'tenant.%' and action <> 'tenant.create'`,
		"tenant:"+tenant.String()))
	require.Equal(t, 0, e.count(t, `select count(*) from cp.audit_log where action like 'settings.%' and actor = $1`,
		"user:"+owner.userID.String()))
	st, ok := e.registryStatus(t, site)
	require.True(t, ok)
	require.Equal(t, "active", st)
}

func testOperatorIDRemoved(t *testing.T, e *env) {
	gh := e.newGHID()
	// the main operator stays listed, which also keeps the list non-empty
	keepGH := e.opGH
	e.build(t, gh, keepGH)
	defer e.build(t, e.opGH)
	c := e.signIn(t, gh, "leaving-operator")
	r := c.call(t, http.MethodGet, "/api/operator/audit?limit=1", nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))

	// another control plane instance already runs without the id; the
	// person signs in there, which clears is_operator
	old := e.api.Load()
	e.build(t, keepGH)
	c2 := e.signIn(t, gh, "leaving-operator")
	me := c2.call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, false, me.json(t)["user"].(map[string]any)["is_operator"])

	// the instance that still lists the id refuses the old session too,
	// because the flag is read per request
	e.api.Store(old)
	r = c.call(t, http.MethodGet, "/api/operator/audit?limit=1", nil)
	require.Equal(t, http.StatusForbidden, r.status, string(r.body))
	require.Equal(t, "operator_required", r.code(t))

	// and a restart without the id ends access even before the next sign-in
	gh2 := e.newGHID()
	e.build(t, gh2, keepGH)
	c3 := e.signIn(t, gh2, "restart-operator")
	require.Equal(t, http.StatusOK, c3.call(t, http.MethodGet, "/api/operator/audit?limit=1", nil).status)
	e.build(t, keepGH)
	r = c3.call(t, http.MethodGet, "/api/operator/audit?limit=1", nil)
	require.Equal(t, http.StatusForbidden, r.status)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.users where github_id = $1 and is_operator`, gh2),
		"the flag is only cleared at sign-in or startup sync")
}

func testApproveReject(t *testing.T, e *env, op *client) {
	r := op.call(t, http.MethodPut, "/api/operator/settings/enrollment-mode", map[string]any{"mode": "approval"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	defer func() {
		r := op.call(t, http.MethodPut, "/api/operator/settings/enrollment-mode", map[string]any{"mode": "open"})
		require.Equal(t, http.StatusOK, r.status, string(r.body))
	}()

	owner := e.signIn(t, e.newGHID(), "pending-owner")
	tenant, status := owner.enroll(t, "Pending Project")
	require.Equal(t, "pending", status)
	site, _ := owner.addSite(t, tenant, "pending.example.org")

	// wrong transitions and a missing reason change nothing
	for _, act := range []string{"suspend", "resume"} {
		r := op.action(t, tenant, act, map[string]any{"reason": "x"})
		require.Equal(t, http.StatusConflict, r.status, act)
		require.Equal(t, "invalid_transition", r.code(t))
	}
	r = op.action(t, tenant, "reject", nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "required", r.json(t)["error"].(map[string]any)["fields"].(map[string]any)["reason"])
	r = op.action(t, tenant, "reject", map[string]any{"reason": "   "})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	r = op.action(t, tenant, "reject", map[string]any{"reason": "bad\x00"})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	r = op.action(t, tenant, "reject", map[string]any{"reason": "x", "extra": 1})
	require.Equal(t, http.StatusBadRequest, r.status)

	// reject: the tenant sees the reason, nothing is provisioned
	r = op.action(t, tenant, "reject", map[string]any{"reason": "  not a stellar project yet  "})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	sum := r.json(t)
	require.Equal(t, "rejected", sum["status"])
	require.Equal(t, "not a stellar project yet", sum["status_reason"])
	require.EqualValues(t, 1, sum["site_count"])
	tv := owner.call(t, http.MethodGet, "/api/tenants/"+tenant.String(), nil)
	require.Equal(t, http.StatusOK, tv.status)
	require.Equal(t, "rejected", tv.json(t)["status"])
	require.Equal(t, "not a stellar project yet", tv.json(t)["status_reason"])
	e.reconcile(t, tenant)
	require.False(t, e.teamID(t, tenant).Valid)
	_, ok := e.registryStatus(t, site)
	require.False(t, ok)
	r = op.action(t, tenant, "reject", map[string]any{"reason": "again"})
	require.Equal(t, http.StatusConflict, r.status, "a repeat writes nothing")
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'tenant.reject' and target = $1`, "tenant:"+tenant.String()))

	// resubmission is the tenant owner's route; this test sets the tenant
	// back to pending the way that route does it
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(),
			`update cp.tenants set status = 'pending', status_reason = null where id = $1 and status = 'rejected'`, tenant)
		require.NoError(t, err)
	})

	r = op.action(t, tenant, "approve", map[string]any{"reason": "checked the contracts"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	sum = r.json(t)
	require.Equal(t, "active", sum["status"])
	require.Nil(t, sum["status_reason"])
	require.Equal(t, 1, e.count(t, `select count(*) from cp.tenants where id = $1 and approved_by = $2`, tenant, op.userID))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.sites where id = $1 and status = 'active'`, site))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'tenant.approve' and target = $1
		and actor = $2 and detail->>'reason' = 'checked the contracts' and detail->>'tenant_id' = $3
		and detail->'status'->>'old' = 'pending' and detail->'status'->>'new' = 'active'`,
		"tenant:"+tenant.String(), "user:"+op.userID.String(), tenant.String()))
	e.reconcile(t, tenant)
	team := e.teamID(t, tenant)
	require.True(t, team.Valid)
	_, ok, err := e.um.Admin.GetWebsite(context.Background(), site.String())
	require.NoError(t, err)
	require.True(t, ok)
	st, ok := e.registryStatus(t, site)
	require.True(t, ok)
	require.Equal(t, "active", st)
	r = op.action(t, tenant, "approve", nil)
	require.Equal(t, http.StatusConflict, r.status)

	// unknown and malformed ids are 404
	require.Equal(t, http.StatusNotFound, op.action(t, uuid.New(), "approve", nil).status)
	require.Equal(t, http.StatusNotFound, op.call(t, http.MethodPost, "/api/operator/tenants/not-an-id/approve", map[string]any{}).status)
}

func testSuspendResume(t *testing.T, e *env, op *client) {
	owner := e.signIn(t, e.newGHID(), "suspend-owner")
	tenant, _ := owner.enroll(t, "Suspend Project")
	site, _ := owner.addSite(t, tenant, "suspend.example.org")
	e.reconcile(t, tenant)

	r := op.action(t, tenant, "suspend", map[string]any{"reason": "spam traffic"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, "suspended", r.json(t)["status"])
	e.reconcile(t, tenant)
	st, ok := e.registryStatus(t, site)
	require.True(t, ok)
	require.Equal(t, "suspended", st, "the gateway refuses every status but active")
	tv := owner.call(t, http.MethodGet, "/api/tenants/"+tenant.String(), nil)
	require.Equal(t, "spam traffic", tv.json(t)["status_reason"])
	// dashboards stay readable: the websites remain
	_, ok, err := e.um.Admin.GetWebsite(context.Background(), site.String())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, http.StatusConflict, op.action(t, tenant, "approve", nil).status)

	r = op.action(t, tenant, "resume", nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, "active", r.json(t)["status"])
	require.Nil(t, r.json(t)["status_reason"])
	e.reconcile(t, tenant)
	st, _ = e.registryStatus(t, site)
	require.Equal(t, "active", st)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'tenant.resume' and target = $1 and detail ? 'reason' = false`,
		"tenant:"+tenant.String()))
}

func testRemove(t *testing.T, e *env, op *client) {
	owner := e.signIn(t, e.newGHID(), "remove-owner")
	only := e.signIn(t, e.newGHID(), "remove-only-member")
	shared := e.signIn(t, e.newGHID(), "remove-shared-member")
	tenant, _ := owner.enroll(t, "Remove Project")
	other, _ := shared.enroll(t, "Other Project")
	site, _ := owner.addSite(t, tenant, "remove.example.org")
	for _, m := range []*client{only, shared} {
		r := owner.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/members", map[string]any{
			"github_login": loginOf(t, e, m), "role": "member",
		})
		require.Equal(t, http.StatusCreated, r.status, string(r.body))
	}
	r := owner.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/members", map[string]any{
		"github_login": "not-yet-signed-in", "role": "member",
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	e.reconcile(t, tenant)
	e.reconcile(t, other)
	team := e.teamID(t, tenant)
	require.True(t, team.Valid)
	quarantine := e.quarantineID(t, site)
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count)
			values ($1, $2, date_trunc('hour', now()), 'accepted', 7)`, site, tenant)
		require.NoError(t, err)
		for _, q := range apiRowsSQL {
			_, err = c.Exec(context.Background(), q, tenant)
			require.NoError(t, err)
		}
	})
	for _, u := range []*client{owner, only, shared} {
		_, ok, err := e.um.Admin.GetUser(context.Background(), u.userID.String())
		require.NoError(t, err)
		require.True(t, ok)
	}

	require.Equal(t, http.StatusUnprocessableEntity, op.action(t, tenant, "remove", nil).status)
	r = op.action(t, tenant, "remove", map[string]any{"reason": "not built on stellar"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	sum := r.json(t)
	require.Equal(t, "removed", sum["status"])
	require.Equal(t, "not built on stellar", sum["status_reason"])
	require.EqualValues(t, 0, sum["site_count"])
	require.Equal(t, http.StatusConflict, op.action(t, tenant, "remove", map[string]any{"reason": "again"}).status)

	// former members get 404 at once, before the purge
	for _, c := range []*client{owner, only, shared} {
		for _, p := range []string{"/api/tenants/" + tenant.String(), "/api/tenants/" + tenant.String() + "/members",
			"/api/tenants/" + tenant.String() + "/sites"} {
			r := c.call(t, http.MethodGet, p, nil)
			require.Equal(t, http.StatusNotFound, r.status, p)
			require.Equal(t, "not_found", r.code(t))
		}
	}
	r = owner.call(t, http.MethodPatch, "/api/tenants/"+tenant.String(), map[string]any{"name": "x"})
	require.Equal(t, http.StatusNotFound, r.status)
	require.Equal(t, 0, e.count(t, `select count(*) from cp.invites where tenant_id = $1`, tenant))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.sites where id = $1 and status = 'removed' and domain = 'remove.example.org'`, site))

	e.reconcile(t, tenant)
	ctx := context.Background()
	_, ok := e.registryStatus(t, site)
	require.False(t, ok, "registry key deleted")
	for _, w := range []string{site.String(), quarantine} {
		_, ok, err := e.um.Admin.GetWebsite(ctx, w)
		require.NoError(t, err)
		require.False(t, ok, "website %s deleted", w)
	}
	_, ok, err := e.um.Admin.GetTeam(ctx, team.UUID.String())
	require.NoError(t, err)
	require.False(t, ok, "team deleted")
	for _, u := range []*client{owner, only} {
		_, ok, err := e.um.Admin.GetUser(ctx, u.userID.String())
		require.NoError(t, err)
		require.False(t, ok, "orphaned umami user deleted")
	}
	_, ok, err = e.um.Admin.GetUser(ctx, shared.userID.String())
	require.NoError(t, err)
	require.True(t, ok, "a member of another live project keeps the umami user")
	require.Equal(t, 0, e.count(t, `select count(*) from cp.web_ingest_hourly where tenant_id = $1`, tenant))
	require.Equal(t, 0, e.count(t, `select count(*) from api.metrics_minute where tenant_id = $1`, tenant))
	require.Equal(t, 0, e.count(t, `select count(*) from api.metrics_hourly where tenant_id = $1`, tenant))

	// the tenant row, members and audit trail remain
	require.Equal(t, 1, e.count(t, `select count(*) from cp.tenants where id = $1 and status = 'removed'`, tenant))
	require.Equal(t, 3, e.count(t, `select count(*) from cp.tenant_members where tenant_id = $1`, tenant))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.tenant_removals where tenant_id = $1 and completed_at is not null
		and requested_by = $2 and reason = 'not built on stellar' and keep_aggregates = false
		and steps ?& array['registry', 'otlp_tokens', 'umami', 'web_ingest', 'api', 'agg']`, tenant, op.userID))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'tenant.remove' and target = $1
		and actor = $2 and detail->>'reason' = 'not built on stellar'`, "tenant:"+tenant.String(), "user:"+op.userID.String()))
	require.Equal(t, 6, e.count(t, `select count(*) from cp.audit_log where action = 'purge.step' and actor = 'system:purge'
		and detail->>'tenant_id' = $1`, tenant.String()))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'purge.complete' and detail->>'tenant_id' = $1`, tenant.String()))
	require.Positive(t, e.count(t, `select count(*) from cp.audit_log where action = 'tenant.create' and target = $1`, "tenant:"+tenant.String()))

	// a later reconcile has nothing left to do and writes nothing
	before := e.count(t, `select count(*) from cp.audit_log where detail->>'tenant_id' = $1 or target = $2`, tenant.String(), "tenant:"+tenant.String())
	e.reconcile(t, tenant)
	after := e.count(t, `select count(*) from cp.audit_log where detail->>'tenant_id' = $1 or target = $2`, tenant.String(), "tenant:"+tenant.String())
	require.Equal(t, before, after)

	// the other project is untouched
	tv := shared.call(t, http.MethodGet, "/api/tenants/"+other.String(), nil)
	require.Equal(t, http.StatusOK, tv.status)
	_, ok, err = e.um.Admin.GetTeam(ctx, e.teamID(t, other).UUID.String())
	require.NoError(t, err)
	require.True(t, ok)
}

func loginOf(t *testing.T, e *env, c *client) string {
	t.Helper()
	var login string
	e.superuser(t, func(conn *pgx.Conn) {
		require.NoError(t, conn.QueryRow(context.Background(), `select github_login from cp.users where id = $1`, c.userID).Scan(&login))
	})
	return login
}

func testKeepAggregates(t *testing.T, e *env, op *client) {
	r := op.call(t, http.MethodGet, "/api/operator/settings/removal-keep-aggregates", nil)
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, false, r.json(t)["keep"])
	r = op.call(t, http.MethodPut, "/api/operator/settings/removal-keep-aggregates", map[string]any{})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	r = op.call(t, http.MethodPut, "/api/operator/settings/removal-keep-aggregates", map[string]any{"keep": "yes"})
	require.Equal(t, http.StatusBadRequest, r.status)
	r = op.call(t, http.MethodPut, "/api/operator/settings/removal-keep-aggregates", map[string]any{"keep": true})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	require.Equal(t, true, r.json(t)["keep"])
	r = op.call(t, http.MethodPut, "/api/operator/settings/removal-keep-aggregates", map[string]any{"keep": true})
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'settings.removal_keep_aggregates'
		and target = 'setting:removal_keep_aggregates' and detail->'keep'->>'old' = 'false' and detail->'keep'->>'new' = 'true'`))

	owner := e.signIn(t, e.newGHID(), "keep-owner")
	tenant, _ := owner.enroll(t, "Keep Project")
	site, _ := owner.addSite(t, tenant, "keep.example.org")
	e.reconcile(t, tenant)
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count)
			values ($1, $2, date_trunc('hour', now()), 'accepted', 3)`, site, tenant)
		require.NoError(t, err)
		for _, q := range apiRowsSQL {
			_, err = c.Exec(context.Background(), q, tenant)
			require.NoError(t, err)
		}
	})
	r = op.action(t, tenant, "remove", map[string]any{"reason": "duplicate project"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	// changing the setting now does not change the removal in progress
	r = op.call(t, http.MethodPut, "/api/operator/settings/removal-keep-aggregates", map[string]any{"keep": false})
	require.Equal(t, http.StatusOK, r.status)
	e.reconcile(t, tenant)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.web_ingest_hourly where tenant_id = $1`, tenant), "aggregates kept")
	require.Equal(t, 1, e.count(t, `select count(*) from api.metrics_hourly where tenant_id = $1`, tenant), "api aggregates kept")
	require.Equal(t, 0, e.count(t, `select count(*) from api.metrics_minute where tenant_id = $1`, tenant), "api minutes always go")
	_, ok, err := e.um.Admin.GetWebsite(context.Background(), site.String())
	require.NoError(t, err)
	require.False(t, ok, "raw data is always deleted")
	require.Equal(t, 1, e.count(t, `select count(*) from cp.tenant_removals where tenant_id = $1 and keep_aggregates
		and steps->'web_ingest'->'detail'->>'skipped' = 'keep_aggregates' and completed_at is not null`, tenant))
}

// a store that fails stops the purge; the next reconcile resumes after the
// stores already done, without running them again
func testPurgeResumes(t *testing.T, e *env, op *client) {
	ctx := context.Background()
	owner := e.signIn(t, e.newGHID(), "resume-owner")
	tenant, _ := owner.enroll(t, "Resume Project")
	site, _ := owner.addSite(t, tenant, "resume.example.org")
	e.reconcile(t, tenant)
	team := e.teamID(t, tenant)
	quarantine := e.quarantineID(t, site)

	// the quarantine website is replaced by one with the same id in a
	// foreign team, which the umami purger must refuse to delete
	foreign, _, err := e.um.Admin.CreateTeam(ctx, "foreign-"+uuid.NewString()[:8], "")
	require.NoError(t, err)
	require.NoError(t, e.um.Admin.DeleteWebsite(ctx, quarantine))
	_, err = e.um.Admin.CreateWebsite(ctx, umami.NewWebsite{ID: quarantine, Name: "clash", Domain: "clash.example.org", TeamID: foreign.ID})
	if err != nil {
		// umami keeps deleted ids; then no clash can be built this way
		t.Fatalf("umami refused to reuse the website id: %v", err)
	}

	r := op.action(t, tenant, "remove", map[string]any{"reason": "abandoned"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.scope.Wait()
	err = e.rec.Reconcile(ctx, tenant)
	require.ErrorContains(t, err, "belongs to team")
	require.Equal(t, 1, e.count(t, `select count(*) from cp.tenant_removals where tenant_id = $1 and completed_at is null
		and steps ? 'registry' and not steps ? 'umami' and not steps ? 'web_ingest'`, tenant))
	_, ok := e.registryStatus(t, site)
	require.False(t, ok, "ingestion stopped before the failing store")

	require.NoError(t, e.um.Admin.DeleteWebsite(ctx, quarantine))
	e.reconcile(t, tenant)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.tenant_removals where tenant_id = $1 and completed_at is not null`, tenant))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'purge.step' and detail->>'tenant_id' = $1
		and detail->>'step' = 'registry'`, tenant.String()), "registry ran once")
	_, ok, err = e.um.Admin.GetTeam(ctx, team.UUID.String())
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = e.um.Admin.GetTeam(ctx, foreign.ID)
	require.NoError(t, err)
	require.True(t, ok, "the foreign team is left alone")
}

func testSitesInReview(t *testing.T, e *env, op *client) {
	owner := e.signIn(t, e.newGHID(), "review-removed")
	gone, _ := owner.enroll(t, "Gone Project")
	owner.addSite(t, gone, "review.example.org")
	e.reconcile(t, gone)
	require.Equal(t, http.StatusOK, op.action(t, gone, "remove", map[string]any{"reason": "gone"}).status)
	e.reconcile(t, gone)

	next := e.signIn(t, e.newGHID(), "review-next")
	tenant, _ := next.enroll(t, "Next Project")
	a, _ := next.addSite(t, tenant, "shop.review.example.org")
	b, _ := next.addSite(t, tenant, "other-review.example.org")
	// the review rule at site creation lives in sites/; the state it
	// produces is set here so the operator routes are tested on their own
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.sites set status = 'pending' where id = any($1)`, []uuid.UUID{a, b})
		require.NoError(t, err)
	})

	r := op.call(t, http.MethodPost, "/api/operator/sites/"+a.String()+"/approve", map[string]any{"reason": "same team, new tenant"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	s := r.json(t)
	require.Equal(t, "active", s["status"])
	require.Equal(t, false, s["in_review"])
	require.Equal(t, true, s["domain_previously_removed"], "subdomain of a removed project's domain")
	require.Equal(t, "unchecked", s["verification"])
	require.NotContains(t, string(r.body), "verify_token")
	e.reconcile(t, tenant)
	st, ok := e.registryStatus(t, a)
	require.True(t, ok)
	require.Equal(t, "active", st)

	require.Equal(t, http.StatusUnprocessableEntity,
		op.call(t, http.MethodPost, "/api/operator/sites/"+b.String()+"/reject", map[string]any{}).status)
	r = op.call(t, http.MethodPost, "/api/operator/sites/"+b.String()+"/reject", map[string]any{"reason": "impersonates a removed project"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	s = r.json(t)
	require.Equal(t, "removed", s["status"])
	require.Equal(t, "impersonates a removed project", s["status_reason"])
	require.Equal(t, false, s["domain_previously_removed"])
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'site.reject' and target = $1 and detail->>'tenant_id' = $2`,
		"site:"+b.String(), tenant.String()))

	// only a pending site of an active tenant is in review
	r = op.call(t, http.MethodPost, "/api/operator/sites/"+a.String()+"/approve", map[string]any{})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "invalid_transition", r.code(t))
	require.Equal(t, http.StatusNotFound, op.call(t, http.MethodPost, "/api/operator/sites/"+uuid.NewString()+"/approve", map[string]any{}).status)
}

func testAuditList(t *testing.T, e *env, op *client) {
	owner := e.signIn(t, e.newGHID(), "audit-owner")
	tenant, _ := owner.enroll(t, "Audit Project")
	require.Equal(t, http.StatusOK, op.action(t, tenant, "suspend", map[string]any{"reason": "check"}).status)
	require.Equal(t, http.StatusOK, op.action(t, tenant, "resume", nil).status)

	list := func(q string) []any {
		t.Helper()
		r := op.call(t, http.MethodGet, "/api/operator/audit?"+q, nil)
		require.Equal(t, http.StatusOK, r.status, string(r.body))
		return r.json(t)["items"].([]any)
	}
	// reconciles triggered by the actions add provision rows for the tenant
	e.scope.Wait()
	all := e.count(t, `select count(*) from cp.audit_log where target = $1 or detail->>'tenant_id' = $2`,
		"tenant:"+tenant.String(), tenant.String())
	require.Len(t, list("tenant_id="+tenant.String()+"&limit=200"), all)
	items := list("tenant_id=" + tenant.String() + "&action=tenant.")
	require.Len(t, items, 3, "create, suspend, resume")
	first := items[0].(map[string]any)
	require.Equal(t, "tenant.resume", first["action"])
	require.Equal(t, "scf-operator", first["actor_login"])
	require.Equal(t, "user:"+op.userID.String(), first["actor"])
	require.Equal(t, "tenant:"+tenant.String(), first["target"])
	require.IsType(t, float64(0), first["id"])
	susp := items[1].(map[string]any)
	require.Equal(t, "check", susp["detail"].(map[string]any)["reason"])

	require.Len(t, list("tenant_id="+tenant.String()+"&action=tenant.suspend"), 1)
	require.Len(t, list("tenant_id="+tenant.String()+"&actor=user:"+op.userID.String()), 2)
	require.Len(t, list("target=tenant:"+tenant.String()+"&action=tenant.resume"), 1)
	// "_" in a prefix is literal, not a like wildcard
	require.Empty(t, list("action=tenant_"))
	require.NotEmpty(t, list("action=settings."))
	now := time.Now().UTC()
	require.Len(t, list("tenant_id="+tenant.String()+"&action=tenant.&from="+now.Add(-time.Hour).Format(time.RFC3339)+
		"&to="+now.Add(time.Hour).Format(time.RFC3339)), 3)
	require.Empty(t, list("tenant_id="+tenant.String()+"&to="+now.Add(-time.Hour).Format(time.RFC3339)))

	// pages of one, newest first, no row twice
	seen := map[float64]bool{}
	path := "/api/operator/audit?limit=1&action=tenant.&tenant_id=" + tenant.String()
	var last float64 = 1 << 62
	for {
		r := op.call(t, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, r.status, string(r.body))
		m := r.json(t)
		it := m["items"].([]any)
		require.Len(t, it, 1)
		id := it[0].(map[string]any)["id"].(float64)
		require.False(t, seen[id])
		require.Less(t, id, last)
		seen[id], last = true, id
		if m["next_cursor"] == nil {
			break
		}
		path = "/api/operator/audit?limit=1&action=tenant.&tenant_id=" + tenant.String() + "&cursor=" + url.QueryEscape(m["next_cursor"].(string))
	}
	require.Len(t, seen, 3)

	for _, q := range []string{"tenant_id=nope", "from=yesterday", "limit=0", "limit=201", "cursor=abc",
		"from=2026-01-01T00:00:00Z&to=2025-01-01T00:00:00Z", "from=2024-01-01T00:00:00Z&to=2026-01-01T00:00:00Z",
		"unknown=1", "action=a&action=b"} {
		r := op.call(t, http.MethodGet, "/api/operator/audit?"+q, nil)
		require.Equal(t, http.StatusBadRequest, r.status, q)
	}
}

// apiRowsSQL writes one minute and one hourly api row of tenant $1, as the
// writer and the rollup would, so a removal has api data to purge
var apiRowsSQL = []string{
	`insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values ($1, date_trunc('minute', now(), 'UTC'), 'api', 'GET', '/', 200, 'metric', '{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.001, 1)`,
	`insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values ($1, date_trunc('hour', now(), 'UTC'), 'api', 'GET', '/', 200, 'metric', '{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.001, 1)`,
}
