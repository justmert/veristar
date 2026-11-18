package domaincheck_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// routeEnv is the verification routes behind the real control plane
// middleware (csrf, sessions, tenant scoping) on a real postgres.
type routeEnv struct {
	d      *pdb.DB
	srv    *httptest.Server
	routes *domaincheck.Routes
	net    *testNet
	files  *tokenFiles
	ghID   int64
}

func startRoutes(t *testing.T) *routeEnv {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "domaincheck-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)

	log := slog.New(slog.DiscardHandler)
	// the verification routes never reconcile; a call would be a bug
	scope, err := tenants.NewScope(d, "test-terms", tenants.ReconcileFunc(func(_ context.Context, _ uuid.UUID) error {
		return errors.New("domaincheck routes must not reconcile")
	}), log)
	require.NoError(t, err)
	files := &tokenFiles{}
	n := newNet(t, files, "a", "b", "c", "toml")
	routes, err := domaincheck.NewRoutes(scope, n.checker(t), time.Now, log)
	require.NoError(t, err)
	t.Cleanup(routes.Wait)

	sessions := session.New(d)
	e := &routeEnv{d: d, routes: routes, net: n, files: files, ghID: 9_100_000}
	var origin string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	e.srv = httptest.NewUnstartedServer(h)
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	origin = e.srv.URL
	signIn := controlplane.RoutesFunc(func(r chi.Router) {
		r.Method(http.MethodGet, "/test-sign-in", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			id, err := uuid.Parse(r.URL.Query().Get("user"))
			if err != nil {
				return httpx.NotFound()
			}
			if err := sessions.SignIn(r.Context(), id); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
	})
	e.srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: origin, Sessions: sessions,
		Public:  []controlplane.Routes{signIn},
		Private: []controlplane.Routes{routes},
	})
	return e
}

func (e *routeEnv) sys(t *testing.T, fn func(pgx.Tx) error) {
	t.Helper()
	require.NoError(t, e.d.WithSystem(t.Context(), fn))
}

func (e *routeEnv) user(t *testing.T) uuid.UUID {
	t.Helper()
	e.ghID++
	var id uuid.UUID
	e.sys(t, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
			e.ghID, "u"+strconv.FormatInt(e.ghID, 10)).Scan(&id)
	})
	return id
}

func (e *routeEnv) tenant(t *testing.T, owner uuid.UUID, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	e.sys(t, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(),
			`insert into cp.tenants (slug, name, status, created_by) values ($1, 'Project', $2, $3) returning id`,
			"p-"+uuid.NewString()[:8], status, owner).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, id, owner)
		return err
	})
	return id
}

func (e *routeEnv) site(t *testing.T, tenant uuid.UUID, domain, verifyToken string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
			insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
			values ($1, $2, 'Site', $3, $4, $5, 'active', $6)`,
			id, tenant, domain, []string{"https://" + domain}, uuid.Must(uuid.NewV7()), verifyToken)
		return err
	})
	return id
}

type client struct {
	e  *routeEnv
	hc *http.Client
}

func (e *routeEnv) client(t *testing.T, user uuid.UUID) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	// a copy: srv.Client returns one shared client, and each user needs
	// its own cookie jar
	hc := *e.srv.Client()
	hc.Jar = jar
	c := &client{e: e, hc: &hc}
	res, err := hc.Get(e.srv.URL + "/api/test-sign-in?user=" + user.String())
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	return c
}

type resp struct {
	status int
	header http.Header
	body   map[string]any
}

func (c *client) call(t *testing.T, method, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, c.e.srv.URL+path, rd)
	require.NoError(t, err)
	req.Header.Set("X-Requested-With", "sa-console")
	req.Header.Set("Origin", c.e.srv.URL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	out := resp{status: res.StatusCode, header: res.Header}
	require.NoError(t, json.Unmarshal(raw, &out.body), string(raw))
	return out
}

func errCode(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (e *routeEnv) auditRows(t *testing.T, target, action string) []map[string]any {
	t.Helper()
	var out []map[string]any
	e.sys(t, func(tx pgx.Tx) error {
		rows, err := tx.Query(t.Context(), `select actor, detail from cp.audit_log where target = $1 and action = $2 order by id`, target, action)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (map[string]any, error) {
			var actor string
			var d map[string]any
			err := r.Scan(&actor, &d)
			d["actor"] = actor
			return d, err
		})
		return err
	})
	return out
}

func TestVerifyRoutes(t *testing.T) {
	e := startRoutes(t)
	owner := e.user(t)
	tenant := e.tenant(t, owner, "active")
	domain := "a." + zone
	site := e.site(t, tenant, domain, token)
	c := e.client(t, owner)
	path := "/api/sites/" + site.String()

	r := c.call(t, http.MethodGet, path+"/verification", nil)
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, token, r.body["verify_token"])
	require.Equal(t, "unchecked", r.body["verification"])
	require.Equal(t, map[string]any{"name": "_veristar." + domain, "type": "TXT", "value": "veristar-verify=" + token}, r.body["dns"])
	require.Equal(t, map[string]any{"url": "https://" + domain + "/.well-known/veristar.txt", "body": token}, r.body["file"])
	require.Nil(t, r.body["last_verify_at"])

	r = c.call(t, http.MethodPost, path+"/verify", map[string]any{"method": "smtp"})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "invalid_value", r.body["error"].(map[string]any)["fields"].(map[string]any)["method"])

	// nothing published yet: both methods fail, the file code is stored
	r = c.call(t, http.MethodPost, path+"/verify", map[string]any{})
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, "unverified", r.body["verification"])
	require.Equal(t, domaincheck.CodeFileNotFound, r.body["last_verify_error"])
	require.Nil(t, r.body["verified_at"])

	// a real txt record with the token
	e.net.dns.SetTXT("_veristar."+domain, []string{domaincheck.DNSValue(token)})
	r = c.call(t, http.MethodPost, path+"/verify", map[string]any{})
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, "verified", r.body["verification"])
	require.Equal(t, "dns", r.body["verify_method"])
	require.Nil(t, r.body["last_verify_error"])
	verifiedAt := r.body["verified_at"]
	require.NotNil(t, verifiedAt)

	// the record now carries a wrong token: unverified, last success kept
	e.net.dns.SetTXT("_veristar."+domain, []string{domaincheck.DNSValue("wrong-token")})
	r = c.call(t, http.MethodPost, path+"/verify", map[string]any{"method": "dns"})
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, "unverified", r.body["verification"])
	require.Equal(t, domaincheck.CodeDNSMismatch, r.body["last_verify_error"])
	require.Equal(t, "dns", r.body["verify_method"])
	require.Equal(t, verifiedAt, r.body["verified_at"])

	// the file method on the real https server
	e.files.set(domain, token+"\n")
	r = c.call(t, http.MethodPost, path+"/verify", map[string]any{"method": "file"})
	require.Equal(t, http.StatusOK, r.status)
	require.Equal(t, "verified", r.body["verification"])
	require.Equal(t, "file", r.body["verify_method"])

	rows := e.auditRows(t, "site:"+site.String(), domaincheck.ActionVerify)
	require.Len(t, rows, 4)
	require.Equal(t, "user:"+owner.String(), rows[0]["actor"])
	require.Equal(t, tenant.String(), rows[0]["tenant_id"])
	require.Equal(t, map[string]any{"dns": domaincheck.CodeDNSNoRecord, "file": domaincheck.CodeFileNotFound}, rows[0]["results"])
	require.Equal(t, "ok", rows[3]["result"])
	for _, row := range rows {
		require.NotContains(t, row, "verify_token")
	}

	// six console checks within the hour, the seventh is refused
	for range 2 {
		r = c.call(t, http.MethodPost, path+"/verify", map[string]any{"method": "file"})
		require.Equal(t, http.StatusOK, r.status)
	}
	r = c.call(t, http.MethodPost, path+"/verify", map[string]any{"method": "file"})
	require.Equal(t, http.StatusTooManyRequests, r.status)
	require.Equal(t, "rate_limited", errCode(r))
	ra, err := strconv.Atoi(r.header.Get("Retry-After"))
	require.NoError(t, err)
	require.Greater(t, ra, 3000)
	require.LessOrEqual(t, ra, 3600)

	// another member sees the same object; a stranger gets 404
	stranger := e.client(t, e.user(t))
	r = stranger.call(t, http.MethodGet, path+"/verification", nil)
	require.Equal(t, http.StatusNotFound, r.status)
	r = stranger.call(t, http.MethodPost, path+"/verify", map[string]any{})
	require.Equal(t, http.StatusNotFound, r.status)

	// a removed site answers 409
	removed := e.site(t, tenant, "b."+zone, token)
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `update cp.sites set status = 'removed' where id = $1`, removed)
		return err
	})
	r = c.call(t, http.MethodPost, "/api/sites/"+removed.String()+"/verify", map[string]any{})
	require.Equal(t, http.StatusConflict, r.status, r.body)
	require.Equal(t, "site_removed", errCode(r))

	// unauthenticated
	plain := *e.srv.Client()
	anon := &client{e: e, hc: &plain}
	r = anon.call(t, http.MethodGet, path+"/verification", nil)
	require.Equal(t, http.StatusUnauthorized, r.status)
}

func TestAfterSiteCreate(t *testing.T) {
	e := startRoutes(t)
	owner := e.user(t)
	tenant := e.tenant(t, owner, "active")
	domain := "toml." + zone
	site := e.site(t, tenant, domain, token)
	actor := "user:" + owner.String()

	read := func() (map[string]any, *time.Time) {
		var doc map[string]any
		var at *time.Time
		e.sys(t, func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), `select stellar_toml, stellar_toml_checked_at from cp.sites where id = $1`, site).Scan(&doc, &at)
		})
		return doc, at
	}

	// no stellar.toml on the test server yet: not_found
	e.routes.AfterSiteCreate(t.Context(), tenant, site, domain, actor)
	e.routes.Wait()
	doc, at := read()
	require.NotNil(t, at)
	require.Equal(t, "not_found", doc["status"])
	require.EqualValues(t, 404, doc["http_status"])
	// a repeat with the same outcome writes no second audit row
	e.routes.AfterSiteCreate(t.Context(), tenant, site, domain, actor)
	e.routes.Wait()
	rows := e.auditRows(t, "site:"+site.String(), domaincheck.ActionStellarToml)
	require.Len(t, rows, 1)
	require.Equal(t, actor, rows[0]["actor"])
	require.Equal(t, map[string]any{"old": nil, "new": "not_found"}, rows[0]["status"])

	// the file appears: found, and the status change is audited
	e.files.setPath(domain, domaincheck.StellarTomlPath, string(fixture(t, "valid.toml")))
	e.routes.AfterSiteCreate(t.Context(), tenant, site, domain, actor)
	e.routes.Wait()
	doc, _ = read()
	require.Equal(t, "found", doc["status"])
	require.Equal(t, "public", doc["network"])
	require.EqualValues(t, 2, doc["currencies_total"])
	rows = e.auditRows(t, "site:"+site.String(), domaincheck.ActionStellarToml)
	require.Len(t, rows, 2)
	require.Equal(t, map[string]any{"old": nil, "new": "public"}, rows[1]["network"])
}

// TestVerifyLimitConcurrent: a burst of checks of one site keeps the limit
// of six per hour; checks still running count against it.
func TestVerifyLimitConcurrent(t *testing.T) {
	e := startRoutes(t)
	owner := e.user(t)
	tenant := e.tenant(t, owner, "active")
	domain := "burst." + zone
	site := e.site(t, tenant, domain, token)
	e.files.set(domain, token)
	c := e.client(t, owner)
	path := "/api/sites/" + site.String() + "/verify"

	const n = 20
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { codes <- c.call(t, http.MethodPost, path, map[string]any{"method": "file"}).status })
	}
	wg.Wait()
	close(codes)
	ok := 0
	for code := range codes {
		if code == http.StatusOK {
			ok++
		} else {
			require.Equal(t, http.StatusTooManyRequests, code)
		}
	}
	require.LessOrEqual(t, ok, 6)
	require.LessOrEqual(t, len(e.auditRows(t, "site:"+site.String(), domaincheck.ActionVerify)), 6)
}
