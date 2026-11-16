package otlptokens_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
	"github.com/justmert/veristar/tests/fixtures/stepuptest"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

// env is a real postgresql, nats and umami, the oauth fixture and the
// control plane api over https with the token routes and the operator
// lifecycle actions mounted, wired the way cmd/controlplane wires them.
type env struct {
	pg      *dbtest.Postgres
	d       *pdb.DB
	kv      jetstream.KeyValue
	rec     *provision.Reconciler
	srv     *httptest.Server
	fixture *httptest.Server
	terms   string
	nextGH  int64
}

func random32(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func start(t *testing.T, operatorGH int64) *env {
	t.Helper()
	ctx := t.Context()
	e := &env{nextGH: 700_000}
	e.pg = jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(e.pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	e.d, err = pdb.Open(ctx, pcfg, "otlptokens-test")
	require.NoError(t, err)
	t.Cleanup(e.d.Close)

	ns := jobstest.NATS(t)
	_, js := jobstest.Connect(t, ns.URL)
	pub, err := registry.NewPublisher(ctx, js)
	require.NoError(t, err)
	e.kv, err = js.KeyValue(ctx, natsx.BucketRegistryOTLP)
	require.NoError(t, err)

	um := umamictr.Start(t)
	key, err := umami.NewPasswordKey(random32(t))
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)
	e.rec = &provision.Reconciler{Admin: um.Admin, Registry: pub, Key: key, Tx: e.d.WithSystem, Now: time.Now, Log: log}

	cur, err := terms.Current()
	require.NoError(t, err)
	e.terms = cur.Version
	scope, err := tenants.NewScope(e.d, e.terms, e.rec, log)
	require.NoError(t, err)
	t.Cleanup(scope.Wait)
	sessions := session.New(e.d)

	var api http.Handler
	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api.ServeHTTP(w, r) }))
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	fx := githuboauth.New(githuboauth.Config{
		ClientID: "otlp-test", ClientSecret: "otlp-test-secret", RedirectURL: e.srv.URL + "/api/auth/github/callback",
	})
	e.fixture = httptest.NewServer(fx.Handler())
	t.Cleanup(e.fixture.Close)

	ops := map[int64]bool{operatorGH: true}
	ah, err := auth.New(auth.Config{
		Reconcile:     scope.Reconcile,
		ClientID:      "otlp-test",
		ClientSecret:  config.FromMap(map[string]string{"S": "otlp-test-secret"}).Secret("S", 1),
		RedirectURL:   e.srv.URL + "/api/auth/github/callback",
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
	}, e.d, sessions)
	require.NoError(t, err)
	_, guard := stepuptest.Guard(t, e.d, sessions, e.srv.URL, identity.Operators{GitHub: ops})
	mode, err := operator.NewEnrollmentMode(e.d)
	require.NoError(t, err)
	actions, err := lifecycle.NewActions(e.d, scope.Reconcile)
	require.NoError(t, err)
	tokens, err := otlptokens.NewRoutes(scope, pub)
	require.NoError(t, err)
	api = controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: e.srv.URL, Sessions: sessions,
		Public: []controlplane.Routes{controlplane.RoutesFunc(ah.Public)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(ah.Private), tenants.NewRoutes(scope), tokens,
			controlplane.RoutesFunc(guard.Mount(mode, actions)),
		},
	})
	return e
}

type client struct {
	e  *env
	hc *http.Client
}

func (e *env) newGH() int64 { e.nextGH++; return e.nextGH }

// signIn runs the real sign-in against the oauth fixture.
func (e *env) signIn(t *testing.T, ghID int64, login string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &client{e: e, hc: &hc}
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
	return c
}

type resp struct {
	status int
	header http.Header
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
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", c.e.srv.URL)
	}
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return resp{status: res.StatusCode, header: res.Header, body: b}
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

func tokensPath(tenant uuid.UUID) string { return "/api/tenants/" + tenant.String() + "/otlp-tokens" }

// created is a token as the create route answered it
type created struct {
	id    uuid.UUID
	plain string
}

func (c *client) createToken(t *testing.T, tenant uuid.UUID, name, environment string) created {
	t.Helper()
	r := c.call(t, http.MethodPost, tokensPath(tenant), map[string]any{"name": name, "environment": environment})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	m := r.json(t)
	id := uuid.MustParse(m["id"].(string))
	require.Equal(t, tokensPath(tenant)+"/"+id.String(), r.header.Get("Location"))
	return created{id: id, plain: m["token"].(string)}
}

// key reads the registry_otlp entry of a plaintext token.
func (e *env) key(t *testing.T, plain string) (registry.OTLPToken, bool) {
	t.Helper()
	en, err := e.kv.Get(context.Background(), registry.HashOTLPToken(plain))
	if err != nil {
		require.ErrorIs(t, err, jetstream.ErrKeyNotFound)
		return registry.OTLPToken{}, false
	}
	tok, err := registry.DecodeOTLPToken(en.Key(), en.Value())
	require.NoError(t, err)
	return tok, true
}

// eventuallyStatus waits for the asynchronous reconcile the request
// started; 5 seconds is the collector's refusal bound.
func (e *env) eventuallyStatus(t *testing.T, plain, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		k, ok := e.key(t, plain)
		return ok && k.Status == want
	}, 5*time.Second, 50*time.Millisecond, "registry status of the token never became %s", want)
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

var tokenRe = regexp.MustCompile(`^sa_otlp_[0-9A-Za-z]{40}$`)

func TestOTLPTokens(t *testing.T) {
	opGH := int64(699_999)
	e := start(t, opGH)
	op := e.signIn(t, opGH, "scf-operator")
	var keys stepuptest.Keys
	keys.StepUpMe(t, stepuptest.Origin(e.srv.URL), func(method, path string, body any) (int, []byte) {
		r := op.call(t, method, path, body)
		return r.status, r.body
	})
	owner := e.signIn(t, e.newGH(), "otlp-owner")
	tenant, status := owner.enroll(t, "OTLP Project")
	require.Equal(t, "active", status)
	var plains []string

	// create: shown once in the sa_otlp_ format, published active
	prod := owner.createToken(t, tenant, "Production API", "production")
	plains = append(plains, prod.plain)
	require.Regexp(t, tokenRe, prod.plain)
	k, ok := e.key(t, prod.plain)
	require.True(t, ok, "the token is in registry_otlp as soon as the create answered")
	require.Equal(t, registry.OTLPToken{Hash: registry.HashOTLPToken(prod.plain), TenantID: tenant.String(),
		TokenID: prod.id.String(), Status: "active"}, k)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.otlp_tokens where id = $1 and hash = sha256($2::bytea) and prefix = $3`,
		prod.id, []byte(prod.plain), prod.plain[:12]))
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'otlp_token.create' and target = $1 and detail->>'tenant_id' = $2`,
		"otlp_token:"+prod.id.String(), tenant.String()))

	// list and get never carry the plaintext
	r := owner.call(t, http.MethodGet, tokensPath(tenant), nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	items := r.json(t)["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	require.Equal(t, prod.id.String(), item["id"])
	require.Equal(t, "Production API", item["name"])
	require.Equal(t, "production", item["environment"])
	require.Equal(t, prod.plain[:12], item["prefix"])
	require.Equal(t, "active", item["status"])
	require.Nil(t, item["revoked_at"])
	require.NotContains(t, item, "token")
	require.NotContains(t, string(r.body), prod.plain)
	r = owner.call(t, http.MethodGet, tokensPath(tenant)+"/"+prod.id.String(), nil)
	require.Equal(t, http.StatusOK, r.status)
	require.NotContains(t, string(r.body), prod.plain[12:])

	// validation
	r = owner.call(t, http.MethodPost, tokensPath(tenant), map[string]any{"environment": "x"})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "required", r.json(t)["error"].(map[string]any)["fields"].(map[string]any)["name"])
	r = owner.call(t, http.MethodPost, tokensPath(tenant), map[string]any{"name": "a", "environment": "Prod EU"})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "invalid_format", r.json(t)["error"].(map[string]any)["fields"].(map[string]any)["environment"])
	r = owner.call(t, http.MethodPost, tokensPath(tenant), map[string]any{"name": "a", "token": "sa_otlp_x"})
	require.Equal(t, http.StatusBadRequest, r.status)

	// isolation: another tenant's member gets 404 on every route, through
	// tenant a's path and through its own tenant's path
	other := e.signIn(t, e.newGH(), "otlp-other")
	otherTenant, _ := other.enroll(t, "Other Project")
	otherTok := other.createToken(t, otherTenant, "other", "")
	plains = append(plains, otherTok.plain)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, tokensPath(tenant)},
		{http.MethodPost, tokensPath(tenant)},
		{http.MethodGet, tokensPath(tenant) + "/" + prod.id.String()},
		{http.MethodDelete, tokensPath(tenant) + "/" + prod.id.String()},
		{http.MethodGet, tokensPath(otherTenant) + "/" + prod.id.String()},
		{http.MethodDelete, tokensPath(otherTenant) + "/" + prod.id.String()},
	} {
		var body any
		if rt.method == http.MethodPost {
			body = map[string]any{"name": "intruder"}
		}
		r := other.call(t, rt.method, rt.path, body)
		require.Equal(t, http.StatusNotFound, r.status, "%s %s: %s", rt.method, rt.path, r.body)
		require.Equal(t, "not_found", r.code(t))
	}
	r = other.call(t, http.MethodGet, tokensPath(otherTenant), nil)
	require.Len(t, r.json(t)["items"].([]any), 1, "a tenant lists only its own tokens")
	k, _ = e.key(t, prod.plain)
	require.Equal(t, "active", k.Status)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.otlp_tokens where tenant_id = $1`, tenant))

	// revoke flips the key before the answer; repeating changes nothing
	staging := owner.createToken(t, tenant, "Staging", "staging")
	plains = append(plains, staging.plain)
	r = owner.call(t, http.MethodDelete, tokensPath(tenant)+"/"+staging.id.String(), nil)
	require.Equal(t, http.StatusNoContent, r.status, string(r.body))
	k, ok = e.key(t, staging.plain)
	require.True(t, ok)
	require.Equal(t, "revoked", k.Status)
	r = owner.call(t, http.MethodDelete, tokensPath(tenant)+"/"+staging.id.String(), nil)
	require.Equal(t, http.StatusNoContent, r.status)
	require.Equal(t, 1, e.count(t, `select count(*) from cp.audit_log where action = 'otlp_token.revoke' and target = $1`,
		"otlp_token:"+staging.id.String()))
	r = owner.call(t, http.MethodGet, tokensPath(tenant), nil)
	require.Len(t, r.json(t)["items"].([]any), 1, "revoked tokens are left out by default")
	r = owner.call(t, http.MethodGet, tokensPath(tenant)+"?include_revoked=true", nil)
	items = r.json(t)["items"].([]any)
	require.Len(t, items, 2)
	require.Equal(t, "revoked", items[1].(map[string]any)["status"])
	require.NotNil(t, items[1].(map[string]any)["revoked_at"])
	// a reconcile keeps both entries as they are
	require.NoError(t, e.rec.Reconcile(context.Background(), tenant))
	k, _ = e.key(t, staging.plain)
	require.Equal(t, "revoked", k.Status)
	k, _ = e.key(t, prod.plain)
	require.Equal(t, "active", k.Status)

	// suspend: active tokens publish suspended, revoked stay revoked, no new
	// token; resume brings them back
	r = op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/suspend", map[string]any{"reason": "abuse"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.eventuallyStatus(t, prod.plain, "suspended")
	k, _ = e.key(t, staging.plain)
	require.Equal(t, "revoked", k.Status)
	r = owner.call(t, http.MethodPost, tokensPath(tenant), map[string]any{"name": "late"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "tenant_suspended", r.code(t))
	// the key is written mid-run; the audit row commits when the run's
	// umami work is done
	require.Eventually(t, func() bool {
		return e.count(t, `select count(*) from cp.audit_log where action = 'provision.otlp_registry_put' and target = $1 and detail->>'status' = 'suspended'`,
			"otlp_token:"+prod.id.String()) == 1
	}, 10*time.Second, 50*time.Millisecond)
	r = op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/resume", map[string]any{})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.eventuallyStatus(t, prod.plain, "active")

	// a pending tenant cannot create tokens
	r = op.call(t, http.MethodPut, "/api/operator/settings/enrollment-mode", map[string]any{"mode": "approval"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	waiting := e.signIn(t, e.newGH(), "otlp-pending")
	pending, status := waiting.enroll(t, "Pending Project")
	require.Equal(t, "pending", status)
	r = waiting.call(t, http.MethodPost, tokensPath(pending), map[string]any{"name": "early"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "tenant_not_active", r.code(t))
	require.Equal(t, 0, e.count(t, `select count(*) from cp.otlp_tokens where tenant_id = $1`, pending))

	// remove: the purge revokes every row and flips every key
	r = op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/remove", map[string]any{"reason": "gone"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.eventuallyStatus(t, prod.plain, "revoked")
	require.Eventually(t, func() bool {
		return e.count(t, `select count(*) from cp.tenant_removals where tenant_id = $1 and steps ? 'otlp_tokens'`, tenant) == 1
	}, 10*time.Second, 50*time.Millisecond)
	require.Equal(t, 0, e.count(t, `select count(*) from cp.otlp_tokens where tenant_id = $1 and status <> 'revoked'`, tenant))
	require.Equal(t, 2, e.count(t, `select count(*) from cp.otlp_tokens where tenant_id = $1 and revoked_at is not null`, tenant))
	r = owner.call(t, http.MethodGet, tokensPath(tenant), nil)
	require.Equal(t, http.StatusNotFound, r.status)
	// the other tenant is untouched
	k, _ = e.key(t, otherTok.plain)
	require.Equal(t, "active", k.Status)

	t.Run("PlaintextStoredNowhere", func(t *testing.T) { testPlaintextNowhere(t, e, plains) })
}

// every row of every table in the platform database, as text, must not
// hold any plaintext token, nor its 40 random characters; the registry
// values must not either. only the sha-256 and the 12-character prefix are
// kept.
func testPlaintextNowhere(t *testing.T, e *env, plains []string) {
	var tables []string
	e.superuser(t, func(c *pgx.Conn) {
		rows, err := c.Query(context.Background(), `
			select format('%I.%I', table_schema, table_name) from information_schema.tables
			where table_schema not in ('pg_catalog', 'information_schema') and table_type = 'BASE TABLE'`)
		require.NoError(t, err)
		tables, err = pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
	})
	require.Contains(t, tables, "cp.otlp_tokens")
	require.Contains(t, tables, "cp.audit_log")
	var dump strings.Builder
	e.superuser(t, func(c *pgx.Conn) {
		for _, tbl := range tables {
			var s *string
			require.NoError(t, c.QueryRow(context.Background(),
				fmt.Sprintf(`select string_agg(t::text, E'\n') from %s t`, tbl)).Scan(&s), tbl)
			if s != nil {
				dump.WriteString(*s)
			}
		}
	})
	all := dump.String()
	require.Contains(t, all, plains[0][:12], "the scan must see the token rows")
	for _, p := range plains {
		require.NotContains(t, all, p[12:], "a token's secret part is in the database")
	}

	keys, err := e.kv.ListKeys(context.Background())
	require.NoError(t, err)
	n := 0
	for key := range keys.Keys() {
		en, err := e.kv.Get(context.Background(), key)
		require.NoError(t, err)
		for _, p := range plains {
			require.NotContains(t, key+string(en.Value()), p[12:])
		}
		n++
	}
	require.Equal(t, len(plains), n)
}
