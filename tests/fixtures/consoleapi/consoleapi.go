// Package consoleapi is a TEST-ONLY harness: the console api of the control
// plane over https against a real postgresql, with sign-in through the
// github oauth fixture, wired the way cmd/controlplane wires the session,
// tenant, member, site and operator lifecycle routes. only test files
// import it.
package consoleapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/account"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/members"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/sites"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
	"github.com/justmert/veristar/tests/fixtures/stepuptest"
)

const (
	clientID     = "consoleapi-test"
	clientSecret = "consoleapi-test-secret"
)

// Options configure the harness. PG, DB and Reconciler are required.
type Options struct {
	PG *dbtest.Postgres
	// DB holds the cp_system, cp_tenant and cp_operator pools the requests use.
	DB *pdb.DB
	// Reconciler is what the routes trigger after a change.
	Reconciler tenants.Reconciler
	// Operators are github ids with operator rights.
	Operators map[int64]bool
	// StellarOperators are stellar addresses with operator rights.
	StellarOperators map[string]bool
	// Umami is what account deletion deletes the umami user with. nil uses
	// an in-memory Users, available as Env.Umami.
	Umami account.UmamiUsers
}

// Users is an in-memory stand-in for the umami users api, for tests that
// do not run umami. it holds the ids added with Add and records deletes.
type Users struct {
	mu      sync.Mutex
	users   map[string]bool
	deleted []string
	// Fail, when set, is returned by every call.
	Fail error
}

// Add makes a user with that id exist.
func (u *Users) Add(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.users == nil {
		u.users = map[string]bool{}
	}
	u.users[id] = true
}

// Has reports whether the user exists.
func (u *Users) Has(id string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.users[id]
}

// Deleted returns the ids deleted so far, in order.
func (u *Users) Deleted() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.deleted...)
}

// GetUser reports whether the user exists.
func (u *Users) GetUser(_ context.Context, id string) (umami.User, bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Fail != nil {
		return umami.User{}, false, u.Fail
	}
	if !u.users[id] {
		return umami.User{}, false, nil
	}
	return umami.User{ID: id}, true, nil
}

// DeleteUser deletes an existing user; an unknown id is an error, as umami
// answers 500 for it.
func (u *Users) DeleteUser(_ context.Context, id string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Fail != nil {
		return u.Fail
	}
	if !u.users[id] {
		return errors.New("umami delete user: unknown id " + id)
	}
	delete(u.users, id)
	u.deleted = append(u.deleted, id)
	return nil
}

// Env is a running console api.
type Env struct {
	PG      *dbtest.Postgres
	DB      *pdb.DB
	Scope   *tenants.Scope
	Srv     *httptest.Server
	Fixture *httptest.Server
	OAuth   *githuboauth.Server
	Terms   string
	// Umami is the in-memory umami users when Options.Umami was nil
	Umami  *Users
	nextGH atomic.Int64
	// keys are the operators' software authenticators (Client.StepUp)
	keys stepuptest.Keys
}

func random32(t testing.TB) []byte {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// Start serves the api until the test ends.
func Start(t testing.TB, o Options) *Env {
	t.Helper()
	require.NotNil(t, o.PG)
	require.NotNil(t, o.DB)
	require.NotNil(t, o.Reconciler)
	log := slog.New(slog.DiscardHandler)
	cur, err := terms.Current()
	require.NoError(t, err)
	e := &Env{PG: o.PG, DB: o.DB, Terms: cur.Version}
	e.nextGH.Store(800_000)
	e.Scope, err = tenants.NewScope(o.DB, cur.Version, o.Reconciler, log)
	require.NoError(t, err)
	t.Cleanup(e.Scope.Wait)
	sessions := session.New(o.DB)

	var api atomic.Pointer[http.Handler]
	e.Srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*api.Load()).ServeHTTP(w, r)
	}))
	e.Srv.StartTLS()
	t.Cleanup(e.Srv.Close)
	e.OAuth = githuboauth.New(githuboauth.Config{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: e.Srv.URL + "/api/auth/github/callback",
	})
	e.Fixture = httptest.NewServer(e.OAuth.Handler())
	t.Cleanup(e.Fixture.Close)

	ops := o.Operators
	if len(ops) == 0 {
		// the guard needs at least one operator; this id never signs in
		ops = map[int64]bool{1: true}
	}
	operators := identity.Operators{GitHub: ops, Stellar: o.StellarOperators}
	endpoints := auth.Endpoints{
		OAuth: oauth2.Endpoint{
			AuthURL:   e.Fixture.URL + "/login/oauth/authorize",
			TokenURL:  e.Fixture.URL + "/login/oauth/access_token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		APIBase: e.Fixture.URL,
	}
	secret := config.FromMap(map[string]string{"S": clientSecret}).Secret("S", 1)
	ah, err := auth.New(auth.Config{
		Reconcile: e.Scope.Reconcile, ClientID: clientID, ClientSecret: secret,
		RedirectURL: e.Srv.URL + "/api/auth/github/callback", StateKey: random32(t),
		ConsoleOrigin: e.Srv.URL, Endpoints: endpoints,
	}, o.DB, sessions)
	require.NoError(t, err)
	users, err := auth.NewUserLookup(auth.UserLookupConfig{
		ClientID: clientID, ClientSecret: secret, Endpoints: endpoints,
	})
	require.NoError(t, err)
	_, guard := stepuptest.Guard(t, o.DB, sessions, e.Srv.URL, operators)
	mode, err := operator.NewEnrollmentMode(o.DB)
	require.NoError(t, err)
	actions, err := lifecycle.NewActions(o.DB, e.Scope.Reconcile)
	require.NoError(t, err)
	memberRoutes, err := members.NewRoutes(e.Scope, users)
	require.NoError(t, err)
	um := o.Umami
	if um == nil {
		e.Umami = &Users{}
		um = e.Umami
	}
	accountRoutes, err := account.NewRoutes(account.Config{
		DB: o.DB, Sessions: sessions, Umami: um, Reconcile: e.Scope.Reconcile,
	})
	require.NoError(t, err)
	h := controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: e.Srv.URL, Sessions: sessions,
		Public: []controlplane.Routes{controlplane.RoutesFunc(ah.Public)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(ah.Private), accountRoutes, tenants.NewRoutes(e.Scope), memberRoutes, sites.NewRoutes(e.Scope),
			controlplane.RoutesFunc(guard.Mount(mode, actions)),
		},
	})
	api.Store(&h)
	return e
}

// NewGitHubID returns a github id no other user of this env has.
func (e *Env) NewGitHubID() int64 { return e.nextGH.Add(1) }

// Client is a browser with its own cookie jar.
type Client struct {
	e    *Env
	hc   *http.Client
	GHID int64
}

// SignIn runs the real sign-in against the oauth fixture.
func (e *Env) SignIn(t testing.TB, ghID int64, login string) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.Srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Client{e: e, hc: &hc, GHID: ghID}
	res, err := c.hc.Do(newRequest(t, http.MethodGet, e.Srv.URL+"/api/auth/github/start?return_to=/", nil))
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
	post := newRequest(t, http.MethodPost, e.Fixture.URL+"/login/oauth/authorize", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err = plain.Do(post)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	res, err = c.hc.Do(newRequest(t, http.MethodGet, res.Header.Get("Location"), nil))
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/", res.Header.Get("Location"), "sign-in failed")
	return c
}

// SignInWallet runs the real wallet sign-in (contract 4.1.7) for kp: it asks
// for a challenge, signs it on the public network as a wallet would and
// verifies it.
func (e *Env) SignInWallet(t testing.TB, kp *keypair.Full) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.Srv.Client()
	hc.Jar = jar
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Client{e: e, hc: &hc}
	c.SignInWalletAgain(t, kp)
	return c
}

// SignInWalletAgain signs this browser in with kp's wallet.
func (c *Client) SignInWalletAgain(t testing.TB, kp *keypair.Full) {
	t.Helper()
	r := c.Call(t, http.MethodPost, "/api/auth/stellar/challenge", map[string]any{"address": kp.Address()})
	require.Equal(t, http.StatusOK, r.Status, string(r.Body))
	challenge, ok := r.JSON(t)["transaction"].(string)
	require.True(t, ok, "challenge without a transaction: %s", r.Body)
	r = c.Call(t, http.MethodPost, "/api/auth/stellar/verify", map[string]any{
		"transaction": SignChallenge(t, challenge, kp),
	})
	require.Equal(t, http.StatusOK, r.Status, "wallet sign-in failed: %s", r.Body)
}

// SignChallenge signs a challenge transaction with kp on the public network,
// as a wallet does.
func SignChallenge(t testing.TB, xdr string, kp *keypair.Full) string {
	t.Helper()
	gtx, err := txnbuild.TransactionFromXDR(xdr)
	require.NoError(t, err)
	tx, ok := gtx.Transaction()
	require.True(t, ok)
	tx, err = tx.Sign(network.PublicNetworkPassphrase, kp)
	require.NoError(t, err)
	out, err := tx.Base64()
	require.NoError(t, err)
	return out
}

func newRequest(t testing.TB, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, target, body)
	require.NoError(t, err)
	return req
}

// Resp is one answer.
type Resp struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body as an object.
func (r Resp) JSON(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &m), string(r.Body))
	return m
}

// Code is the error code of the envelope.
func (r Resp) Code(t testing.TB) string {
	t.Helper()
	e, ok := r.JSON(t)["error"].(map[string]any)
	require.True(t, ok, "no error envelope in %s", r.Body)
	code, ok := e["code"].(string)
	require.True(t, ok, "no error code in %s", r.Body)
	return code
}

// Do sends a request as the console does, with the csrf headers on state
// changes. it fails the test only for transport errors.
func (c *Client) Do(ctx context.Context, method, path string, body any) (Resp, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Resp{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.e.Srv.URL+path, rd)
	if err != nil {
		return Resp{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", c.e.Srv.URL)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return Resp{}, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return Resp{}, err
	}
	return Resp{Status: res.StatusCode, Header: res.Header, Body: b}, nil
}

// Call is Do that fails the test on a transport error.
// StepUp gives this browser's operator session its passkey step-up
// (contract 5.15) with a software authenticator.
func (c *Client) StepUp(t testing.TB) {
	t.Helper()
	me := c.Call(t, http.MethodGet, "/api/me", nil)
	require.Equal(t, http.StatusOK, me.Status, string(me.Body))
	id := uuid.MustParse(me.JSON(t)["user"].(map[string]any)["id"].(string))
	c.e.keys.StepUp(t, id, stepuptest.Origin(c.e.Srv.URL), func(method, path string, body any) (int, []byte) {
		r := c.Call(t, method, path, body)
		return r.Status, r.Body
	})
}

func (c *Client) Call(t testing.TB, method, path string, body any) Resp {
	t.Helper()
	r, err := c.Do(context.Background(), method, path, body)
	require.NoError(t, err)
	return r
}

// Enroll creates a tenant and returns its id and status.
func (c *Client) Enroll(t testing.TB, name string) (uuid.UUID, string) {
	t.Helper()
	r := c.Call(t, http.MethodPost, "/api/tenants", map[string]any{
		"name":                   name,
		"enrollment":             map[string]any{"website": "https://example.org", "stellar_usage": "soroban contracts"},
		"accepted_terms_version": c.e.Terms,
	})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
	m := r.JSON(t)
	id, ok := m["id"].(string)
	require.True(t, ok)
	status, ok := m["status"].(string)
	require.True(t, ok)
	return uuid.MustParse(id), status
}

// AddSite creates a site and returns its id.
func (c *Client) AddSite(t testing.TB, tenant uuid.UUID, domain string) uuid.UUID {
	t.Helper()
	r := c.Call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/sites", map[string]any{
		"name": domain, "domain": domain, "origins": []string{"https://" + domain},
	})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
	id, ok := r.JSON(t)["id"].(string)
	require.True(t, ok, string(r.Body))
	return uuid.MustParse(id)
}

// Superuser runs fn on a superuser connection to the platform database.
func (e *Env) Superuser(t testing.TB, fn func(c *pgx.Conn)) {
	t.Helper()
	c, err := e.PG.Superuser(context.Background(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	fn(c)
}

// Count runs a count query as superuser.
func (e *Env) Count(t testing.TB, query string, args ...any) int {
	t.Helper()
	var n int
	e.Superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(), query, args...).Scan(&n))
	})
	return n
}

// WaitForLockWait blocks until a backend of role waits on a lock, so a test
// can commit the transaction that holds the lock only once the request it
// races is queued behind it.
func (e *Env) WaitForLockWait(t testing.TB, role string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return e.Count(t, `select count(*) from pg_stat_activity where usename = $1 and wait_event_type = 'Lock'`, role) > 0
	}, 10*time.Second, 20*time.Millisecond, "no %s backend ever waited on a lock", role)
}
