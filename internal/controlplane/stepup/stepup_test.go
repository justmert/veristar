package stepup_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/stepup"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/tests/fixtures/webauthnsoft"
)

type env struct {
	// origin is the console origin the relying party is bound to. webauthn
	// refuses an ip address as relying party id, so the test server's
	// 127.0.0.1 is named localhost there
	origin string
	d      *pdb.DB
	srv *httptest.Server
	svc *stepup.Service
	now time.Time
	gh  int64
}

// start serves the step-up routes behind a real console session. sign-in
// skips github: these tests are about what a signed-in account can do.
func start(t *testing.T) *env {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	require.NoError(t, err)
	e := &env{now: time.Now(), gh: 7_000_000}
	e.d, err = pdb.Open(t.Context(), pcfg, "stepup-test")
	require.NoError(t, err)
	t.Cleanup(e.d.Close)
	sessions := session.New(e.d)
	e.srv = httptest.NewUnstartedServer(http.NotFoundHandler())
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	e.origin = strings.Replace(e.srv.URL, "127.0.0.1", "localhost", 1)
	e.svc, err = stepup.New(stepup.Config{
		ConsoleOrigin: e.origin, MaxAge: time.Hour, EnrollWindow: time.Hour,
		Now: func() time.Time { return e.now },
	}, e.d, sessions)
	require.NoError(t, err)
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
	guarded := controlplane.RoutesFunc(func(r chi.Router) {
		r.Route("/operator", func(sub chi.Router) {
			e.svc.Routes(sub)
			sub.With(e.svc.Require).Method(http.MethodGet, "/protected", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
		})
	})
	e.srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: slog.New(slog.DiscardHandler), ConsoleOrigin: e.srv.URL, Sessions: sessions,
		Public: []controlplane.Routes{signIn}, Private: []controlplane.Routes{guarded},
	})
	return e
}

// operator makes an account and an active roster row for its github id
// whose enrollment window opened at opened
func (e *env) operator(t *testing.T, opened time.Time) uuid.UUID {
	t.Helper()
	e.gh++
	var id uuid.UUID
	require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `insert into cp.operators (github_id, role, enroll_opened_at, enroll_until)
			values ($1, 'operator_admin', $2, $3)`, e.gh, opened, opened.Add(time.Hour)); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `insert into cp.users (github_id, github_login) values ($1, 'op') returning id`,
			e.gh).Scan(&id)
	}))
	return id
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
	res, err := hc.Get(e.srv.URL + "/api/test-sign-in?user=" + user.String())
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	return &client{e: e, hc: &hc}
}

func (c *client) call(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.e.srv.URL+"/api/operator"+path, rd)
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
	return res.StatusCode, b
}

func code(t *testing.T, b []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(b, &env), string(b))
	return env.Error.Code
}

func (c *client) register(t *testing.T, a *webauthnsoft.Authenticator, name string) (int, []byte) {
	t.Helper()
	st, opts := c.call(t, http.MethodPost, "/passkeys/options", map[string]any{})
	if st != http.StatusOK {
		return st, opts
	}
	cred, err := a.Register(opts, c.e.origin)
	require.NoError(t, err)
	return c.call(t, http.MethodPost, "/passkeys", map[string]any{"name": name, "credential": cred})
}

func (c *client) stepUp(t *testing.T, a *webauthnsoft.Authenticator) (int, []byte) {
	t.Helper()
	st, opts := c.call(t, http.MethodPost, "/step-up/options", map[string]any{})
	if st != http.StatusOK {
		return st, opts
	}
	cred, err := a.Assert(opts, c.e.origin)
	require.NoError(t, err)
	return c.call(t, http.MethodPost, "/step-up", map[string]any{"credential": cred})
}

func TestStepUp(t *testing.T) {
	e := start(t)

	t.Run("first passkey through the enrollment window, then step-ups", func(t *testing.T) {
		user := e.operator(t, e.now.Add(-time.Minute))
		c := e.signIn(t, user)
		st, b := c.call(t, http.MethodGet, "/protected", nil)
		require.Equal(t, http.StatusUnauthorized, st)
		require.Equal(t, "step_up_required", code(t, b))

		var s stepup.Status
		st, b = c.call(t, http.MethodGet, "/step-up", nil)
		require.Equal(t, http.StatusOK, st, string(b))
		require.NoError(t, json.Unmarshal(b, &s))
		require.Equal(t, stepup.EnrollmentOpen, s.Enrollment)
		require.False(t, s.SteppedUp)

		st, b = c.stepUp(t, nil)
		require.Equal(t, http.StatusConflict, st)
		require.Equal(t, "passkey_required", code(t, b))

		key, err := webauthnsoft.New()
		require.NoError(t, err)
		st, b = c.register(t, key, "desk key")
		require.Equal(t, http.StatusCreated, st, string(b))
		// the first registration counts as the step-up
		st, _ = c.call(t, http.MethodGet, "/protected", nil)
		require.Equal(t, http.StatusNoContent, st)

		// the window closed: a second account session cannot enroll again
		c2 := e.signIn(t, user)
		st, b = c2.register(t, key, "again")
		require.Equal(t, http.StatusUnauthorized, st)
		require.Equal(t, "step_up_required", code(t, b))
		st, b = c2.stepUp(t, key)
		require.Equal(t, http.StatusOK, st, string(b))
		st, _ = c2.call(t, http.MethodGet, "/protected", nil)
		require.Equal(t, http.StatusNoContent, st)

		// a second key needs the step-up, which c2 holds; the last key
		// cannot be deleted, one of two can
		key2, err := webauthnsoft.New()
		require.NoError(t, err)
		st, b = c2.register(t, key2, "spare key")
		require.Equal(t, http.StatusCreated, st, string(b))
		var list struct {
			Items []stepup.Passkey `json:"items"`
		}
		st, b = c2.call(t, http.MethodGet, "/passkeys", nil)
		require.Equal(t, http.StatusOK, st)
		require.NoError(t, json.Unmarshal(b, &list))
		require.Len(t, list.Items, 2)
		st, _ = c2.call(t, http.MethodDelete, "/passkeys/"+list.Items[1].ID.String(), nil)
		require.Equal(t, http.StatusNoContent, st)
		st, b = c2.call(t, http.MethodDelete, "/passkeys/"+list.Items[0].ID.String(), nil)
		require.Equal(t, http.StatusConflict, st)
		require.Equal(t, "last_passkey", code(t, b))

		// the step-up ages out
		e.now = e.now.Add(time.Hour + time.Second)
		st, b = c2.call(t, http.MethodGet, "/protected", nil)
		require.Equal(t, http.StatusUnauthorized, st)
		require.Equal(t, "step_up_required", code(t, b))
		st, _ = c2.call(t, http.MethodGet, "/passkeys", nil)
		require.Equal(t, http.StatusUnauthorized, st)
		e.now = time.Now()
	})

	t.Run("a session from before the window cannot enroll", func(t *testing.T) {
		user := e.operator(t, e.now.Add(time.Hour))
		c := e.signIn(t, user)
		e.now = e.now.Add(time.Hour + time.Minute)
		defer func() { e.now = time.Now() }()
		var s stepup.Status
		_, b := c.call(t, http.MethodGet, "/step-up", nil)
		require.NoError(t, json.Unmarshal(b, &s))
		require.Equal(t, stepup.EnrollmentSignInRequired, s.Enrollment)
		key, err := webauthnsoft.New()
		require.NoError(t, err)
		st, b := c.register(t, key, "late")
		require.Equal(t, http.StatusUnauthorized, st)
		require.Equal(t, "enrollment_closed", code(t, b))
	})

	t.Run("a ceremony answer is used once, and only by its own key", func(t *testing.T) {
		user := e.operator(t, e.now.Add(-time.Minute))
		c := e.signIn(t, user)
		key, err := webauthnsoft.New()
		require.NoError(t, err)
		st, _ := c.register(t, key, "k")
		require.Equal(t, http.StatusCreated, st)

		st, opts := c.call(t, http.MethodPost, "/step-up/options", map[string]any{})
		require.Equal(t, http.StatusOK, st)
		cred, err := key.Assert(opts, e.origin)
		require.NoError(t, err)
		st, _ = c.call(t, http.MethodPost, "/step-up", map[string]any{"credential": cred})
		require.Equal(t, http.StatusOK, st)
		st, b := c.call(t, http.MethodPost, "/step-up", map[string]any{"credential": cred})
		require.Equal(t, http.StatusBadRequest, st)
		require.Equal(t, "ceremony_expired", code(t, b))

		// another account's key does not verify for this account
		other, err := webauthnsoft.New()
		require.NoError(t, err)
		_, opts = c.call(t, http.MethodPost, "/step-up/options", map[string]any{})
		forged, err := other.Assert(opts, e.origin)
		require.NoError(t, err)
		st, b = c.call(t, http.MethodPost, "/step-up", map[string]any{"credential": forged})
		require.Equal(t, http.StatusBadRequest, st)
		require.Equal(t, "step_up_failed", code(t, b))

		// an answer for another origin does not verify
		_, opts = c.call(t, http.MethodPost, "/step-up/options", map[string]any{})
		phished, err := key.Assert(opts, "https://console.example.net")
		require.NoError(t, err)
		st, b = c.call(t, http.MethodPost, "/step-up", map[string]any{"credential": phished})
		require.Equal(t, http.StatusBadRequest, st)
		require.Equal(t, "step_up_failed", code(t, b))
	})

	t.Run("the audit log records each ceremony", func(t *testing.T) {
		var n int
		require.NoError(t, e.d.WithSystem(t.Context(), func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), `select count(*) from cp.audit_log where action in ($1, $2, $3)`,
				stepup.ActionStepUp, stepup.ActionPasskeyAdd, stepup.ActionPasskeyDelete).Scan(&n)
		}))
		require.GreaterOrEqual(t, n, 6)
	})
}
