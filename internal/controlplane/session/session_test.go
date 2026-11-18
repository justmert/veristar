package session_test

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

type env struct {
	d   *pdb.DB
	m   *session.Manager
	srv *httptest.Server
	// gates holds one channel per slow request name; the request has
	// loaded its session when it signals on loaded and finishes when the
	// test closes release
	mu    sync.Mutex
	gates map[string]*gate
}

type gate struct {
	loaded  chan struct{}
	release chan struct{}
	// renew makes the slow request renew its token after release, as a
	// step-up does
	renew bool
}

func (e *env) gate(name string, renew bool) *gate {
	e.mu.Lock()
	defer e.mu.Unlock()
	g := &gate{loaded: make(chan struct{}), release: make(chan struct{}), renew: renew}
	e.gates[name] = g
	return g
}

func start(t *testing.T) *env {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	require.NoError(t, err)
	e := &env{gates: map[string]*gate{}}
	e.d, err = pdb.Open(t.Context(), pcfg, "session-test")
	require.NoError(t, err)
	t.Cleanup(e.d.Close)
	e.m = session.New(e.d)
	e.srv = httptest.NewUnstartedServer(http.NotFoundHandler())
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	public := controlplane.RoutesFunc(func(r chi.Router) {
		r.Method(http.MethodGet, "/test-sign-in", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			id, err := uuid.Parse(r.URL.Query().Get("user"))
			if err != nil {
				return httpx.NotFound()
			}
			if err := e.m.SignIn(r.Context(), id); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
		r.Method(http.MethodGet, "/test-sign-out", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			if err := e.m.SignOut(r.Context(), nil); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
	})
	private := controlplane.RoutesFunc(func(r chi.Router) {
		r.Method(http.MethodGet, "/who", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		r.Method(http.MethodGet, "/renew", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			if err := e.m.Renew(r.Context()); err != nil {
				return err
			}
			e.m.SetValue(r.Context(), "renewed", "yes")
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
		r.Method(http.MethodGet, "/value", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Value", e.m.Value(r.Context(), r.URL.Query().Get("key")))
			w.WriteHeader(http.StatusNoContent)
		}))
		r.Method(http.MethodGet, "/age-sign-in", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			old := time.Now().Add(-session.Lifetime - time.Minute).Unix()
			e.m.SetValue(r.Context(), "signed_in_at", strconv.FormatInt(old, 10))
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
		// slow loads the session, waits for the test, then lets the
		// middleware commit it as any console poll would
		r.Method(http.MethodGet, "/slow", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			e.mu.Lock()
			g := e.gates[r.URL.Query().Get("gate")]
			e.mu.Unlock()
			close(g.loaded)
			<-g.release
			if g.renew {
				if err := e.m.Renew(r.Context()); err != nil {
					return err
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
	})
	e.srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: slog.New(slog.DiscardHandler), ConsoleOrigin: e.srv.URL, Sessions: e.m,
		Public: []controlplane.Routes{public}, Private: []controlplane.Routes{private},
	})
	return e
}

func (e *env) user(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `insert into cp.users (github_id, github_login)
			values ((select coalesce(max(github_id), 0) + 1 from cp.users), 'u') returning id`).Scan(&id)
	}))
	return id
}

func (e *env) sessions(t *testing.T, user uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select count(*) from cp.sessions where user_id = $1`, user).Scan(&n)
	}))
	return n
}

type client struct {
	e  *env
	hc *http.Client
}

func (e *env) signIn(t *testing.T, user uuid.UUID) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	c := &client{e: e, hc: &hc}
	require.Equal(t, http.StatusNoContent, c.get(t, "/test-sign-in?user="+user.String()))
	return c
}

func (c *client) get(t *testing.T, path string) int {
	t.Helper()
	res, err := c.hc.Get(c.e.srv.URL + "/api" + path)
	require.NoError(t, err)
	_ = res.Body.Close()
	return res.StatusCode
}

// cookie is the session cookie the client holds now
func (c *client) cookie(t *testing.T) *http.Cookie {
	t.Helper()
	u, err := url.Parse(c.e.srv.URL + "/api")
	require.NoError(t, err)
	for _, ck := range c.hc.Jar.Cookies(u) {
		if ck.Name == session.CookieName {
			return ck
		}
	}
	return nil
}

// with returns a client that sends only the given session cookie
func (c *client) with(t *testing.T, ck *http.Cookie) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	u, err := url.Parse(c.e.srv.URL + "/api")
	require.NoError(t, err)
	jar.SetCookies(u, []*http.Cookie{{Name: ck.Name, Value: ck.Value, Path: "/"}})
	hc := *c.hc
	hc.Jar = jar
	return &client{e: c.e, hc: &hc}
}

// inFlight starts a slow request of c and returns once it has loaded its
// session; the returned function releases it and waits for its answer.
func (c *client) inFlight(t *testing.T, name string, renew bool) func() int {
	t.Helper()
	g := c.e.gate(name, renew)
	done := make(chan int, 1)
	go func() {
		res, err := c.hc.Get(c.e.srv.URL + "/api/slow?gate=" + name)
		if err != nil {
			done <- 0
			return
		}
		_ = res.Body.Close()
		done <- res.StatusCode
	}()
	select {
	case <-g.loaded:
	case <-time.After(30 * time.Second):
		t.Fatal("the slow request did not start")
	}
	return func() int {
		close(g.release)
		return <-done
	}
}

func TestRevokedSessionsStayRevoked(t *testing.T) {
	e := start(t)

	t.Run("a request in flight during sign-out does not bring the session back", func(t *testing.T) {
		u := e.user(t)
		c := e.signIn(t, u)
		old := c.cookie(t)
		finish := c.with(t, old).inFlight(t, "logout", false)
		require.Equal(t, http.StatusNoContent, c.get(t, "/test-sign-out"))
		require.Equal(t, 0, e.sessions(t, u))
		require.Equal(t, http.StatusNoContent, finish())
		require.Equal(t, 0, e.sessions(t, u), "the slow request's commit must not insert the row again")
		require.Equal(t, http.StatusUnauthorized, c.with(t, old).get(t, "/who"))
	})

	t.Run("a request in flight when every session of the account ends does not bring one back", func(t *testing.T) {
		u := e.user(t)
		c := e.signIn(t, u)
		finish := c.inFlight(t, "roster", false)
		// what a roster suspension, removal or passkey reset and account
		// deletion run
		require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `delete from cp.sessions where user_id = $1`, u)
			return err
		}))
		require.Equal(t, http.StatusNoContent, finish())
		require.Equal(t, 0, e.sessions(t, u))
		require.Equal(t, http.StatusUnauthorized, c.get(t, "/who"))
	})

	t.Run("a renewed token does not leave the old one usable, even with a request in flight", func(t *testing.T) {
		u := e.user(t)
		c := e.signIn(t, u)
		old := c.cookie(t)
		finish := c.with(t, old).inFlight(t, "renew-race", false)
		require.Equal(t, http.StatusNoContent, c.get(t, "/renew"))
		renewed := c.cookie(t)
		require.NotEqual(t, old.Value, renewed.Value)
		require.Equal(t, 1, e.sessions(t, u))
		require.Equal(t, http.StatusNoContent, finish())
		require.Equal(t, 1, e.sessions(t, u), "the old token's row must not come back")
		require.Equal(t, http.StatusUnauthorized, c.with(t, old).get(t, "/who"))
		require.Equal(t, http.StatusNoContent, c.with(t, renewed).get(t, "/who"))
	})

	t.Run("a renewal of a session ended while the request ran stores nothing", func(t *testing.T) {
		u := e.user(t)
		c := e.signIn(t, u)
		finish := c.inFlight(t, "renew-revoked", true)
		require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `delete from cp.sessions where user_id = $1`, u)
			return err
		}))
		require.Equal(t, http.StatusNoContent, finish())
		require.Equal(t, 0, e.sessions(t, u), "the renewed token must not revive the ended session")
		require.Equal(t, http.StatusUnauthorized, c.get(t, "/who"))
	})

	t.Run("a sign-out right after a renewal in the same session ends it", func(t *testing.T) {
		u := e.user(t)
		c := e.signIn(t, u)
		require.Equal(t, http.StatusNoContent, c.get(t, "/renew"))
		require.Equal(t, http.StatusNoContent, c.get(t, "/test-sign-out"))
		require.Equal(t, 0, e.sessions(t, u))
	})
}

func deadline(t *testing.T, e *env, user uuid.UUID) time.Time {
	t.Helper()
	var data []byte
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select data from cp.sessions where user_id = $1`, user).Scan(&data)
	}))
	d, _, err := scs.GobCodec{}.Decode(data)
	require.NoError(t, err)
	return d
}

func TestLifetimeIsAbsolute(t *testing.T) {
	e := start(t)

	t.Run("a renewal keeps the sign-in's deadline", func(t *testing.T) {
		u := e.user(t)
		c := e.signIn(t, u)
		before := deadline(t, e, u)
		require.WithinDuration(t, time.Now().Add(session.Lifetime), before, time.Minute)
		time.Sleep(1100 * time.Millisecond)
		require.Equal(t, http.StatusNoContent, c.get(t, "/renew"))
		require.Equal(t, http.StatusNoContent, c.get(t, "/who"))
		require.True(t, before.Equal(deadline(t, e, u)), "renewing must not move the deadline")
	})

	t.Run("a session whose sign-in is older than the lifetime is refused", func(t *testing.T) {
		u := e.user(t)
		c := e.signIn(t, u)
		require.Equal(t, http.StatusNoContent, c.get(t, "/age-sign-in"))
		require.Equal(t, http.StatusUnauthorized, c.get(t, "/who"))
	})
}

// the cookie value is 32 random bytes in base64url
func TestCookieShape(t *testing.T) {
	e := start(t)
	c := e.signIn(t, e.user(t))
	ck := c.cookie(t)
	require.NotNil(t, ck)
	b, err := base64.RawURLEncoding.DecodeString(ck.Value)
	require.NoError(t, err)
	require.Len(t, b, 32)
}

// a sign-in starts a new session: nothing the browser's previous session
// held (another account's step-up, a ceremony in progress) carries over
func TestSignInStartsEmpty(t *testing.T) {
	e := start(t)
	a, b := e.user(t), e.user(t)
	c := e.signIn(t, a)
	require.Equal(t, http.StatusNoContent, c.get(t, "/renew"))
	value := func() string {
		res, err := c.hc.Get(c.e.srv.URL + "/api/value?key=renewed")
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		return res.Header.Get("X-Value")
	}
	require.Equal(t, "yes", value())
	require.Equal(t, http.StatusNoContent, c.get(t, "/test-sign-in?user="+b.String()))
	require.Empty(t, value(), "another account's sign-in does not inherit the values")
	require.Equal(t, http.StatusNoContent, c.get(t, "/test-sign-in?user="+a.String()))
	require.Empty(t, value(), "nor does a later sign-in of the first account")
	require.Zero(t, e.sessions(t, a)+e.sessions(t, b)-1, "one session row remains")
}
