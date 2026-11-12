package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/controlplane"
	"github.com/justmert/stellar-analytics/internal/controlplane/auth"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/controlplane/terms"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/tests/fixtures/githuboauth"
)

const (
	clientID     = "Iv1.cptest"
	clientSecret = "cp-test-client-secret"
	callbackPath = "/api/auth/github/callback"
)

// env is one control plane (over tls, so Secure cookies behave as in a
// browser) in front of the real database, signing in against the github
// oauth fixture.
type env struct {
	cp      *httptest.Server
	origin  string
	fixture *githuboauth.Server
	fx      *httptest.Server

	mu         sync.Mutex
	reconciled []uuid.UUID
}

func (e *env) reconciledTenants() []uuid.UUID {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]uuid.UUID(nil), e.reconciled...)
}

// nextGitHubID hands every test its own github identity, so tests that
// share the database never see each other's users
var nextGitHubID atomic.Int64

func init() { nextGitHubID.Store(time.Now().UnixNano() % 1_000_000_000) }

func newGitHubID() int64 { return nextGitHubID.Add(1) }

type handlerBox struct{ h atomic.Value }

func (b *handlerBox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.h.Load().(http.Handler).ServeHTTP(w, r)
}

func newEnv(t *testing.T, operators ...int64) *env {
	t.Helper()
	box := &handlerBox{}
	cp := httptest.NewUnstartedServer(box)
	cp.StartTLS()
	t.Cleanup(cp.Close)

	fixture := githuboauth.New(githuboauth.Config{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: cp.URL + callbackPath,
	})
	fx := httptest.NewServer(fixture.Handler())
	t.Cleanup(fx.Close)

	ops := map[int64]bool{}
	for _, id := range operators {
		ops[id] = true
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	sessions := session.New(conn)
	e := &env{}
	h, err := auth.New(auth.Config{
		Reconcile: func(_ context.Context, tenantID uuid.UUID) {
			e.mu.Lock()
			e.reconciled = append(e.reconciled, tenantID)
			e.mu.Unlock()
		},
		ClientID:     clientID,
		ClientSecret: secret(t, clientSecret),
		RedirectURL:  cp.URL + callbackPath,
		StateKey:     key,
		Operators:    ops,
		Endpoints: auth.Endpoints{
			OAuth:   oauthEndpoint(fx.URL),
			APIBase: fx.URL,
		},
	}, conn, sessions)
	require.NoError(t, err)
	box.h.Store(controlplane.NewHandler(controlplane.Options{
		Log:           slog.New(slog.DiscardHandler),
		ConsoleOrigin: cp.URL,
		Sessions:      sessions,
		Public:        []controlplane.Routes{controlplane.RoutesFunc(h.Public), controlplane.RoutesFunc(terms.Routes)},
		Private:       []controlplane.Routes{controlplane.RoutesFunc(h.Private)},
	}))
	e.cp, e.origin, e.fixture, e.fx = cp, cp.URL, fixture, fx
	return e
}

func secret(t *testing.T, v string) config.Secret {
	t.Helper()
	s := config.FromMap(map[string]string{"S": v}).Secret("S", 1)
	return s
}

// browser is a cookie-keeping client that does not follow redirects, so
// every hop can be checked.
type browser struct {
	t   *testing.T
	e   *env
	c   *http.Client
	jar *cookiejar.Jar
}

func (e *env) browser(t *testing.T) *browser {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := e.cp.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &browser{t: t, e: e, c: c, jar: jar}
}

// resp is a fully read response, so no test leaks a body.
type resp struct {
	StatusCode int
	Header     http.Header
	body       []byte
	cookies    []*http.Cookie
}

func (r *resp) Cookies() []*http.Cookie { return r.cookies }

func fetch(t *testing.T, c *http.Client, req *http.Request) *resp {
	t.Helper()
	res, err := c.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return &resp{StatusCode: res.StatusCode, Header: res.Header, body: body, cookies: res.Cookies()}
}

func (b *browser) get(u string) *resp {
	b.t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	require.NoError(b.t, err)
	return fetch(b.t, b.c, req)
}

func (b *browser) send(method, path string, hdr map[string]string) *resp {
	b.t.Helper()
	req, err := http.NewRequest(method, b.e.origin+path, nil)
	require.NoError(b.t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return fetch(b.t, b.c, req)
}

// consoleHeaders are what the console sends on a state change.
func (b *browser) consoleHeaders() map[string]string {
	return map[string]string{"X-Requested-With": "sa-console", "Origin": b.e.origin}
}

// start opens /api/auth/github/start and returns github's authorize url.
func (b *browser) start(returnTo string) *url.URL {
	b.t.Helper()
	u := b.e.origin + "/api/auth/github/start"
	if returnTo != "" {
		u += "?return_to=" + url.QueryEscape(returnTo)
	}
	res := b.get(u)
	require.Equal(b.t, http.StatusFound, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(b.t, err)
	return loc
}

// consent plays the person on github's consent page and returns the
// callback url github redirects to.
func (b *browser) consent(authorize *url.URL, decision string, id int64, login string) *url.URL {
	b.t.Helper()
	form := authorize.Query()
	form.Set("fixture_decision", decision)
	form.Set("fixture_user_id", strconv.FormatInt(id, 10))
	form.Set("fixture_login", login)
	nr := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(http.MethodPost, b.e.fx.URL+"/login/oauth/authorize", strings.NewReader(form.Encode()))
	require.NoError(b.t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := fetch(b.t, nr, req)
	require.Equal(b.t, http.StatusFound, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(b.t, err)
	require.True(b.t, strings.HasPrefix(loc.String(), b.e.origin+callbackPath+"?"), loc.String())
	return loc
}

// signIn runs the whole flow and returns the callback response.
func (b *browser) signIn(id int64, login, returnTo string) *resp {
	b.t.Helper()
	return b.get(b.consent(b.start(returnTo), "authorize", id, login).String())
}

func (b *browser) cookie(name string) *http.Cookie {
	u, _ := url.Parse(b.e.origin + callbackPath)
	for _, c := range b.jar.Cookies(u) {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func setCookie(t *testing.T, res *resp, name string) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

type apiError struct {
	Error struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func decode[T any](t *testing.T, res *resp) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(res.body, &v))
	return v
}

func failureCode(t *testing.T, res *resp) string {
	t.Helper()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/sign-in", loc.Path)
	return loc.Query().Get("error")
}

func sessionRow(t *testing.T, cookieValue string) (userID uuid.NullUUID, found bool) {
	t.Helper()
	sum := sha256.Sum256([]byte(cookieValue))
	c, err := pg.Superuser(context.Background(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	err = c.QueryRow(context.Background(), `select user_id from cp.sessions where token_hash = $1`, sum[:]).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.NullUUID{}, false
	}
	require.NoError(t, err)
	return userID, true
}

func auditActions(t *testing.T, target string) []string {
	t.Helper()
	c, err := pg.Superuser(context.Background(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	rows, err := c.Query(context.Background(), `select action from cp.audit_log where target = $1 order by id`, target)
	require.NoError(t, err)
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return out
}

func TestSignInRoundTrip(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	id := newGitHubID()
	login := "octo-" + strconv.FormatInt(id, 10)

	authorize := b.start("/projects?tab=sites")
	// what github receives: client id, exact callback, state, pkce s256, no scope
	q := authorize.Query()
	require.Equal(t, e.fx.URL+"/login/oauth/authorize", authorize.Scheme+"://"+authorize.Host+authorize.Path)
	require.Equal(t, clientID, q.Get("client_id"))
	require.Equal(t, e.origin+callbackPath, q.Get("redirect_uri"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.Len(t, q.Get("code_challenge"), 43)
	require.Equal(t, "true", q.Get("allow_signup"))
	require.Equal(t, "code", q.Get("response_type"))
	require.False(t, q.Has("scope"))
	state, err := base64.RawURLEncoding.DecodeString(q.Get("state"))
	require.NoError(t, err)
	require.Len(t, state, 32)
	// the browser holds the sealed state, not the verifier in clear
	oc := b.cookie("sa_oauth")
	require.NotNil(t, oc)
	require.NotContains(t, oc.Value, q.Get("state"))

	res := b.get(b.consent(authorize, "authorize", id, login).String())
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/projects?tab=sites", res.Header.Get("Location"))
	require.Nil(t, b.cookie("sa_oauth"), "the state cookie is single use")
	sc := setCookie(t, res, session.CookieName)
	require.NotNil(t, sc)

	// the database holds only the sha-256 of the cookie, bound to the user
	uid, found := sessionRow(t, sc.Value)
	require.True(t, found)
	require.True(t, uid.Valid)

	me := b.get(e.origin + "/api/me")
	require.Equal(t, http.StatusOK, me.StatusCode)
	got := decode[auth.Me](t, me)
	require.Equal(t, uid.UUID, got.User.ID)
	require.Equal(t, id, got.User.GitHubID)
	require.Equal(t, login, got.User.GitHubLogin)
	require.False(t, got.User.IsOperator)
	require.Empty(t, got.Tenants)
	require.NotNil(t, got.Tenants, "tenants is an empty list, not null")
	require.Equal(t, auth.MeTerms{CurrentVersion: "2026-11-12-draft.1", Status: "draft", URL: "/terms"}, got.Terms)
	require.Equal(t, "no-store", me.Header.Get("Cache-Control"))

	// what the fixture saw on the back channel
	reqs := e.fixture.Requests()
	require.Len(t, reqs.Token, 1)
	require.Equal(t, "ok", reqs.Token[0].Result)
	require.Equal(t, "<set>", reqs.Token[0].Form["code_verifier"])
	require.Len(t, reqs.User, 1)
	require.Equal(t, "2022-11-28", reqs.User[0].APIVersion)
	require.Equal(t, "application/vnd.github+json", reqs.User[0].Accept)
	require.Equal(t, "bearer", strings.ToLower(reqs.User[0].Scheme))

	require.Equal(t, []string{"user.sign_in"}, auditActions(t, "user:"+uid.UUID.String()))
	require.Empty(t, e.reconciledTenants(), "no invite, nothing to reconcile")

	// the login can change on github; the numeric id is the identity
	b2 := e.browser(t)
	res = b2.signIn(id, login+"-renamed", "")
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	require.Equal(t, "/", res.Header.Get("Location"))
	got2 := decode[auth.Me](t, b2.get(e.origin+"/api/me"))
	require.Equal(t, got.User.ID, got2.User.ID)
	require.Equal(t, login+"-renamed", got2.User.GitHubLogin)
}

func TestSessionCookieFlags(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)

	res := b.get(e.origin + "/api/auth/github/start")
	oc := setCookie(t, res, "sa_oauth")
	require.NotNil(t, oc)
	require.True(t, oc.HttpOnly)
	require.True(t, oc.Secure)
	require.Equal(t, http.SameSiteLaxMode, oc.SameSite)
	require.Equal(t, callbackPath, oc.Path)
	require.Equal(t, 600, oc.MaxAge)
	require.Empty(t, oc.Domain)

	res = b.signIn(newGitHubID(), "flags-user", "")
	sc := setCookie(t, res, session.CookieName)
	require.NotNil(t, sc)
	require.True(t, sc.HttpOnly)
	require.True(t, sc.Secure)
	require.Equal(t, http.SameSiteLaxMode, sc.SameSite)
	require.Equal(t, "/", sc.Path)
	require.Empty(t, sc.Domain, "host-only cookie")
	// persistent, bounded by the 24 hour idle timeout
	require.Greater(t, sc.MaxAge, 23*3600)
	require.LessOrEqual(t, sc.MaxAge, 24*3600+1)
	raw, err := base64.RawURLEncoding.DecodeString(sc.Value)
	require.NoError(t, err)
	require.Len(t, raw, 32)
	// the cookie in the raw header carries the flags as written
	var line string
	for _, l := range res.Header.Values("Set-Cookie") {
		if strings.HasPrefix(l, session.CookieName+"=") {
			line = l
		}
	}
	require.Contains(t, line, "HttpOnly")
	require.Contains(t, line, "Secure")
	require.Contains(t, line, "SameSite=Lax")
	require.NotContains(t, strings.ToLower(line), "domain=")
}

func TestStateFailures(t *testing.T) {
	e := newEnv(t)

	t.Run("no state cookie", func(t *testing.T) {
		b := e.browser(t)
		cb := b.consent(b.start(""), "authorize", newGitHubID(), "nocookie")
		fresh := e.browser(t)
		res := fresh.get(cb.String())
		require.Equal(t, "state_mismatch", failureCode(t, res))
		require.Nil(t, setCookie(t, res, session.CookieName))
	})

	t.Run("state changed", func(t *testing.T) {
		b := e.browser(t)
		cb := b.consent(b.start(""), "authorize", newGitHubID(), "badstate")
		q := cb.Query()
		q.Set("state", q.Get("state")+"x")
		cb.RawQuery = q.Encode()
		res := b.get(cb.String())
		require.Equal(t, "state_mismatch", failureCode(t, res))
		require.Nil(t, setCookie(t, res, session.CookieName))
		require.Equal(t, http.StatusUnauthorized, b.get(e.origin+"/api/me").StatusCode)
	})

	t.Run("state missing", func(t *testing.T) {
		b := e.browser(t)
		cb := b.consent(b.start(""), "authorize", newGitHubID(), "nostate")
		q := cb.Query()
		q.Del("state")
		cb.RawQuery = q.Encode()
		require.Equal(t, "state_mismatch", failureCode(t, b.get(cb.String())))
	})

	t.Run("tampered cookie", func(t *testing.T) {
		b := e.browser(t)
		cb := b.consent(b.start(""), "authorize", newGitHubID(), "tamper")
		oc := b.cookie("sa_oauth")
		require.NotNil(t, oc)
		raw, err := base64.RawURLEncoding.DecodeString(oc.Value)
		require.NoError(t, err)
		raw[len(raw)-1] ^= 1
		u, _ := url.Parse(e.origin + callbackPath)
		b.jar.SetCookies(u, []*http.Cookie{{Name: "sa_oauth", Value: base64.RawURLEncoding.EncodeToString(raw), Path: callbackPath}})
		require.Equal(t, "state_mismatch", failureCode(t, b.get(cb.String())))
	})

	t.Run("replayed callback", func(t *testing.T) {
		b := e.browser(t)
		cb := b.consent(b.start(""), "authorize", newGitHubID(), "replay")
		require.Equal(t, http.StatusSeeOther, b.get(cb.String()).StatusCode)
		// the state cookie was consumed by the first visit
		require.Equal(t, "state_mismatch", failureCode(t, b.get(cb.String())))
	})

	t.Run("access denied", func(t *testing.T) {
		b := e.browser(t)
		res := b.get(b.consent(b.start(""), "cancel", newGitHubID(), "denier").String())
		require.Equal(t, "access_denied", failureCode(t, res))
		require.Nil(t, setCookie(t, res, session.CookieName))
	})

	t.Run("other github error", func(t *testing.T) {
		b := e.browser(t)
		cb := b.consent(b.start(""), "cancel", newGitHubID(), "suspended")
		q := cb.Query()
		q.Set("error", "application_suspended")
		cb.RawQuery = q.Encode()
		require.Equal(t, "github_error", failureCode(t, b.get(cb.String())))
	})
}

func TestPKCEFailure(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	id := newGitHubID()

	// the code is bound to the challenge of the first start; the browser's
	// cookie now holds the second start's state and verifier
	first := b.start("")
	second := b.start("")
	cb := b.consent(first, "authorize", id, "pkce-user")
	q := cb.Query()
	q.Set("state", second.Query().Get("state"))
	cb.RawQuery = q.Encode()

	res := b.get(cb.String())
	require.Equal(t, "exchange_failed", failureCode(t, res))
	require.Nil(t, setCookie(t, res, session.CookieName))
	reqs := e.fixture.Requests()
	require.Len(t, reqs.Token, 1)
	require.NotEqual(t, "ok", reqs.Token[0].Result)
	require.Empty(t, reqs.User, "no /user call after a failed exchange")
	require.Equal(t, http.StatusUnauthorized, b.get(e.origin+"/api/me").StatusCode)
}

func TestOperatorFlag(t *testing.T) {
	op := newGitHubID()
	e := newEnv(t, op)
	b := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b.signIn(op, "operator-user", "").StatusCode)
	require.True(t, decode[auth.Me](t, b.get(e.origin+"/api/me")).User.IsOperator)

	// removed from OPERATOR_GITHUB_IDS: the next sign-in clears the flag
	e2 := newEnv(t)
	b2 := e2.browser(t)
	require.Equal(t, http.StatusSeeOther, b2.signIn(op, "operator-user", "").StatusCode)
	require.False(t, decode[auth.Me](t, b2.get(e2.origin+"/api/me")).User.IsOperator)
}

func TestSignInResolvesInvites(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := newGitHubID()
	login := "Invitee-" + strconv.FormatInt(id, 10)

	// an owner, a tenant and an invite for the login in other case
	var tenantID, inviteID, expiredID uuid.UUID
	ownerGH := newGitHubID()
	err := conn.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		owner, err := q.SignInUser(ctx, systemq.SignInUserParams{GithubID: ownerGH, GithubLogin: "owner-" + strconv.FormatInt(ownerGH, 10)})
		if err != nil {
			return err
		}
		tn, err := q.CreateTenant(ctx, systemq.CreateTenantParams{
			Slug: "inv-" + strconv.FormatInt(id, 10), Name: "Zeta project", Description: "",
			CreatedBy: uuid.NullUUID{UUID: owner.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		tenantID = tn.ID
		if _, err := tx.Exec(ctx, `update cp.tenants set status = 'active', accepted_terms_version = '2026-11-12-draft.1' where id = $1`, tn.ID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `insert into cp.invites (tenant_id, github_login, role, created_by, expires_at)
			values ($1, $2, 'member', $3, now() + interval '14 days') returning id`,
			tn.ID, strings.ToLower(login), owner.ID).Scan(&inviteID); err != nil {
			return err
		}
		// an expired invite for another tenant is not resolved
		tn2, err := q.CreateTenant(ctx, systemq.CreateTenantParams{
			Slug: "exp-" + strconv.FormatInt(id, 10), Name: "Alpha project", Description: "",
			CreatedBy: uuid.NullUUID{UUID: owner.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `insert into cp.invites (tenant_id, github_login, role, created_by, created_at, expires_at)
			values ($1, $2, 'owner', $3, now() - interval '20 days', now() - interval '6 days') returning id`,
			tn2.ID, login, owner.ID).Scan(&expiredID)
	})
	require.NoError(t, err)

	b := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b.signIn(id, login, "").StatusCode)
	me := decode[auth.Me](t, b.get(e.origin+"/api/me"))
	require.Len(t, me.Tenants, 1)
	require.Equal(t, tenantID, me.Tenants[0].ID)
	require.Equal(t, "member", me.Tenants[0].Role)
	require.Equal(t, "active", me.Tenants[0].Status)
	require.True(t, me.Tenants[0].TermsCurrent)

	require.Equal(t, []string{"invite.resolve"}, auditActions(t, "invite:"+inviteID.String()))
	// the new member's umami access is provisioned at once
	require.Equal(t, []uuid.UUID{tenantID}, e.reconciledTenants())
	require.Empty(t, auditActions(t, "invite:"+expiredID.String()))
	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	var n int
	require.NoError(t, c.QueryRow(ctx, `select count(*) from cp.invites where id = $1`, inviteID).Scan(&n))
	require.Equal(t, 0, n, "the resolved invite is deleted")
	require.NoError(t, c.QueryRow(ctx, `select count(*) from cp.invites where id = $1`, expiredID).Scan(&n))
	require.Equal(t, 1, n, "the expired invite stays")

	// removed tenants are not listed
	_, err = c.Exec(ctx, `update cp.tenants set status = 'removed' where id = $1`, tenantID)
	require.NoError(t, err)
	gone := decode[auth.Me](t, b.get(e.origin+"/api/me"))
	require.Empty(t, gone.Tenants)
	// a former member is told the project is gone (contract 5.10)
	require.Len(t, gone.RemovedTenants, 1)
	require.Equal(t, tenantID, gone.RemovedTenants[0].ID)
}

func TestCSRFOnLogout(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	require.Equal(t, http.StatusSeeOther, b.signIn(newGitHubID(), "csrf-user", "").StatusCode)

	cases := []struct {
		name string
		hdr  map[string]string
		code string
	}{
		{"no header", map[string]string{"Origin": e.origin}, "csrf_header_missing"},
		{"wrong header value", map[string]string{"X-Requested-With": "XMLHttpRequest", "Origin": e.origin}, "csrf_header_missing"},
		{"no origin", map[string]string{"X-Requested-With": "sa-console"}, "origin_mismatch"},
		{"foreign origin", map[string]string{"X-Requested-With": "sa-console", "Origin": "https://evil.example"}, "origin_mismatch"},
		{"null origin", map[string]string{"X-Requested-With": "sa-console", "Origin": "null"}, "origin_mismatch"},
	}
	for _, c := range cases {
		res := b.send(http.MethodPost, "/api/auth/logout", c.hdr)
		require.Equal(t, http.StatusForbidden, res.StatusCode, c.name)
		ae := decode[apiError](t, res)
		require.Equal(t, c.code, ae.Error.Code, c.name)
		require.Equal(t, res.Header.Get("X-Request-Id"), ae.Error.RequestID, c.name)
		require.Nil(t, setCookie(t, res, session.CookieName), c.name)
	}
	// none of the refused requests ended the session
	require.Equal(t, http.StatusOK, b.get(e.origin+"/api/me").StatusCode)
	// the api never grants cors
	res := b.send(http.MethodOptions, "/api/auth/logout", map[string]string{
		"Origin": "https://evil.example", "Access-Control-Request-Method": "POST",
		"Access-Control-Request-Headers": "x-requested-with",
	})
	require.Empty(t, res.Header.Get("Access-Control-Allow-Origin"))
	require.Empty(t, res.Header.Get("Access-Control-Allow-Headers"))
}

func TestLogoutInvalidates(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	res := b.signIn(newGitHubID(), "logout-user", "")
	sc := setCookie(t, res, session.CookieName)
	require.NotNil(t, sc)
	uid, found := sessionRow(t, sc.Value)
	require.True(t, found)

	res = b.send(http.MethodPost, "/api/auth/logout", b.consoleHeaders())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	expired := setCookie(t, res, session.CookieName)
	require.NotNil(t, expired)
	require.Empty(t, expired.Value)
	require.Less(t, expired.MaxAge, 0)
	_, found = sessionRow(t, sc.Value)
	require.False(t, found, "the session row is deleted")
	require.Equal(t, []string{"user.sign_in", "user.sign_out"}, auditActions(t, "user:"+uid.UUID.String()))

	// a copy of the old cookie no longer works
	other := e.browser(t)
	u, _ := url.Parse(e.origin + "/")
	other.jar.SetCookies(u, []*http.Cookie{{Name: session.CookieName, Value: sc.Value, Path: "/"}})
	res = other.get(e.origin + "/api/me")
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "unauthenticated", decode[apiError](t, res).Error.Code)

	// logout without a session is idempotent
	res = b.send(http.MethodPost, "/api/auth/logout", b.consoleHeaders())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestSignInRenewsToken(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	id := newGitHubID()
	first := setCookie(t, b.signIn(id, "renew-user", ""), session.CookieName)
	require.NotNil(t, first)
	// signing in again from the same browser replaces the token
	second := setCookie(t, b.signIn(id, "renew-user", ""), session.CookieName)
	require.NotNil(t, second)
	require.NotEqual(t, first.Value, second.Value)
	_, found := sessionRow(t, first.Value)
	require.False(t, found, "the old token is deleted at sign-in")
	_, found = sessionRow(t, second.Value)
	require.True(t, found)

	// a session id chosen by an attacker before sign-in is never adopted
	planted := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	c := e.browser(t)
	u, _ := url.Parse(e.origin + "/")
	c.jar.SetCookies(u, []*http.Cookie{{Name: session.CookieName, Value: planted, Path: "/"}})
	got := setCookie(t, c.signIn(newGitHubID(), "planted-user", ""), session.CookieName)
	require.NotNil(t, got)
	require.NotEqual(t, planted, got.Value)
	_, found = sessionRow(t, planted)
	require.False(t, found)
}

func TestReturnTo(t *testing.T) {
	e := newEnv(t)
	cases := map[string]string{
		"/projects/1":                  "/projects/1",
		"//evil.example/x":             "/",
		`/\evil.example`:               "/",
		"https://evil.example":         "/",
		"projects":                     "/",
		"/" + strings.Repeat("a", 512): "/",
		"/a\r\nSet-Cookie: x=1":        "/",
	}
	for in, want := range cases {
		b := e.browser(t)
		res := b.signIn(newGitHubID(), "ret-user", in)
		require.Equal(t, http.StatusSeeOther, res.StatusCode, in)
		require.Equal(t, want, res.Header.Get("Location"), in)
	}
}

func TestUnauthenticatedAndTerms(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	res := b.get(e.origin + "/api/me")
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "unauthenticated", decode[apiError](t, res).Error.Code)
	require.Nil(t, setCookie(t, res, session.CookieName), "no session is created for an anonymous request")

	res = b.get(e.origin + "/api/terms")
	require.Equal(t, http.StatusOK, res.StatusCode)
	tm := decode[terms.Terms](t, res)
	require.Equal(t, "2026-11-12-draft.1", tm.Version)
	require.Equal(t, "draft", tm.Status)

	res = b.get(e.origin + "/api/nope")
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, "not_found", decode[apiError](t, res).Error.Code)
	res = b.send(http.MethodDelete, "/api/me", b.consoleHeaders())
	require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode)
	require.Equal(t, "method_not_allowed", decode[apiError](t, res).Error.Code)
}

func oauthEndpoint(base string) oauth2.Endpoint {
	return oauth2.Endpoint{
		AuthURL:   base + "/login/oauth/authorize",
		TokenURL:  base + "/login/oauth/access_token",
		AuthStyle: oauth2.AuthStyleInParams,
	}
}

func TestStartRateLimited(t *testing.T) {
	e := newEnv(t)
	b := e.browser(t)
	ok := 0
	var limited *resp
	for range auth.StartBurst + 10 {
		res := b.get(e.origin + "/api/auth/github/start")
		if res.StatusCode == http.StatusTooManyRequests {
			limited = res
			break
		}
		require.Equal(t, http.StatusFound, res.StatusCode)
		ok++
	}
	require.NotNil(t, limited, "the bucket never ran out")
	require.GreaterOrEqual(t, ok, auth.StartBurst)
	require.Equal(t, "rate_limited", decode[apiError](t, limited).Error.Code)
	require.Equal(t, "1", limited.Header.Get("Retry-After"))
	require.Nil(t, setCookie(t, limited, "sa_oauth"), "a refused start sets no state cookie")
}
