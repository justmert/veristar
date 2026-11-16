package operator

import (
	"bytes"
	"context"
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
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db/dbtest"
)

const walletOp = "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN"

func superCount(t *testing.T, pg *dbtest.Postgres, sql string, args ...any) int {
	t.Helper()
	c, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	var n int
	require.NoError(t, c.QueryRow(t.Context(), sql, args...).Scan(&n))
	return n
}

// the flag of cp.users follows the roster in the database, whatever writes
// the users row or the roster row
func TestRosterDerivesOperatorFlag(t *testing.T) {
	_, pg := reviewDB(t)
	superExec(t, pg, `insert into cp.users (github_id, github_login, is_operator) values (21, 'not-listed', true)`)
	require.Zero(t, superCount(t, pg, `select count(*) from cp.users where github_id = 21 and is_operator`),
		"a flag written by hand is replaced by the roster's answer")

	superExec(t, pg, `insert into cp.operators (github_id, role) values (21, 'operator_admin'), (22, 'operator')`)
	require.Equal(t, 1, superCount(t, pg, `select count(*) from cp.users where github_id = 21 and is_operator`))
	superExec(t, pg, `insert into cp.users (github_id, github_login) values (22, 'listed-later')`)
	require.Equal(t, 1, superCount(t, pg, `select count(*) from cp.users where github_id = 22 and is_operator`))

	superExec(t, pg, `update cp.operators set status = 'suspended', status_reason = 'x' where github_id = 22`)
	require.Zero(t, superCount(t, pg, `select count(*) from cp.users where github_id = 22 and is_operator`))
	superExec(t, pg, `delete from cp.operators where github_id = 22`)
	require.Zero(t, superCount(t, pg, `select count(*) from cp.users where github_id = 22 and is_operator`))

	// a wallet identity linked later makes the account an operator
	superExec(t, pg, `insert into cp.operators (stellar_address, role) values ($1, 'operator')`, walletOp)
	superExec(t, pg, `update cp.users set stellar_address = $1 where github_id = 22`, walletOp)
	require.Equal(t, 1, superCount(t, pg, `select count(*) from cp.users where github_id = 22 and is_operator`))

	// the last active admin cannot go, by any statement
	c, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	for _, sql := range []string{
		`delete from cp.operators where github_id = 21`,
		`update cp.operators set status = 'suspended', status_reason = 'x' where github_id = 21`,
		`update cp.operators set role = 'operator' where github_id = 21`,
	} {
		_, err := c.Exec(t.Context(), sql)
		require.True(t, isLastAdmin(err), "%s: %v", sql, err)
	}
	// an identity is on the roster once, and a row holds one identity
	_, err = c.Exec(t.Context(), `insert into cp.operators (github_id, role) values (21, 'operator')`)
	require.Error(t, err)
	_, err = c.Exec(t.Context(), `insert into cp.operators (github_id, stellar_address, role) values (23, $1, 'operator')`, walletOp)
	require.Error(t, err)
}

func TestBootstrap(t *testing.T) {
	d, pg := reviewDB(t)
	now := time.Now()
	_, err := Bootstrap(t.Context(), d, identity.Operators{}, time.Hour, now)
	require.ErrorContains(t, err, "no active operator admin")

	superExec(t, pg, `insert into cp.users (github_id, github_login) values (31, 'boot-gh')`)
	ops := identity.Operators{GitHub: map[int64]bool{31: true, 32: true}, Stellar: map[string]bool{walletOp: true}}
	res, err := Bootstrap(t.Context(), d, ops, time.Hour, now)
	require.NoError(t, err)
	// the roster trigger set the flag of boot-gh as the row went in, so the
	// recompute behind it finds nothing left to change
	require.Equal(t, BootstrapResult{Ensured: 3, Changed: 3, FlagsChanged: 0, Windows: 3, ActiveAdmins: 3}, res)
	require.Equal(t, 1, superCount(t, pg, `select count(*) from cp.users where github_id = 31 and is_operator`))
	require.Equal(t, 3, superCount(t, pg, `select count(*) from cp.audit_log where action = $1`, ActionOperatorBootstrap))
	require.Equal(t, 3, superCount(t, pg, `select count(*) from cp.operators where enroll_until > now()`))

	// a second start changes and audits nothing; the windows stay open
	res, err = Bootstrap(t.Context(), d, ops, time.Hour, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, BootstrapResult{Ensured: 3, ActiveAdmins: 3}, res)
	require.Equal(t, 3, superCount(t, pg, `select count(*) from cp.audit_log where action = $1`, ActionOperatorBootstrap))

	// a listed row demoted and suspended in the database comes back as an
	// active admin at the next start
	superExec(t, pg, `update cp.operators set role = 'operator', status = 'suspended', status_reason = 'x' where github_id = 32`)
	res, err = Bootstrap(t.Context(), d, ops, time.Hour, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, res.Changed)
	require.Equal(t, 1, superCount(t, pg, `select count(*) from cp.operators where github_id = 32 and role = 'operator_admin' and status = 'active'`))

	// a start without an id in the settings keeps its row
	_, err = Bootstrap(t.Context(), d, identity.Operators{GitHub: map[int64]bool{31: true}}, time.Hour, now.Add(3*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 3, superCount(t, pg, `select count(*) from cp.operators where status = 'active'`))
}

type fakeLookup map[string]int64

func (f fakeLookup) Lookup(_ context.Context, login string) (auth.GitHubUser, error) {
	id, ok := f[strings.ToLower(login)]
	if !ok {
		return auth.GitHubUser{}, auth.ErrGitHubUserNotFound
	}
	return auth.GitHubUser{ID: id, Login: login}, nil
}

type rosterClient struct {
	srv *httptest.Server
	hc  *http.Client
}

func (c *rosterClient) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.srv.URL+"/api/operator"+path, rd)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", c.srv.URL)
	}
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	var m map[string]any
	if len(b) > 0 {
		require.NoError(t, json.Unmarshal(b, &m), string(b))
	}
	return res.StatusCode, m
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestRosterRoutes(t *testing.T) {
	d, pg := reviewDB(t)
	_, err := Bootstrap(t.Context(), d, identity.Operators{GitHub: map[int64]bool{41: true}}, time.Hour, time.Now())
	require.NoError(t, err)
	adminID, plainOpID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.operators (github_id, role) values (42, 'operator')`)
	superExec(t, pg, `insert into cp.users (id, github_id, github_login) values ($1, 41, 'boot-admin'), ($2, 42, 'plain-op')`,
		adminID, plainOpID)

	sessions := session.New(d)
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.StartTLS()
	t.Cleanup(srv.Close)
	guard, stepUp := testGuard(t, d, sessions, srv.URL)
	roster, err := NewRoster(RosterConfig{
		DB: d, Guard: guard, Lookup: fakeLookup{"new-admin": 43, "new-op": 44},
		Bootstrap: identity.Operators{GitHub: map[int64]bool{41: true}}, EnrollWindow: time.Hour,
	})
	require.NoError(t, err)
	srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: slog.New(slog.DiscardHandler), ConsoleOrigin: srv.URL, Sessions: sessions,
		Public: []controlplane.Routes{controlplane.RoutesFunc(func(r chi.Router) {
			r.Get("/test-sign-in/{id}", func(w http.ResponseWriter, r *http.Request) {
				id := uuid.MustParse(chi.URLParam(r, "id"))
				if err := sessions.SignIn(r.Context(), id); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if r.URL.Query().Get("step_up") == "1" {
					if _, err := stepUp.Record(r.Context(), id); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				w.WriteHeader(http.StatusNoContent)
			})
		})},
		Private: []controlplane.Routes{controlplane.RoutesFunc(guard.Mount(roster))},
	})
	signIn := func(id uuid.UUID, stepped bool) *rosterClient {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		hc := *srv.Client()
		hc.Jar = jar
		q := ""
		if stepped {
			q = "?step_up=1"
		}
		res, err := hc.Get(srv.URL + "/api/test-sign-in/" + id.String() + q)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		return &rosterClient{srv: srv, hc: &hc}
	}
	admin, plain := signIn(adminID, true), signIn(plainOpID, true)
	reason := "roster test"

	// every operator reads the roster; without a step-up nobody does
	st, m := signIn(adminID, false).do(t, http.MethodGet, "/roster", nil)
	require.Equal(t, http.StatusUnauthorized, st)
	require.Equal(t, "step_up_required", errCode(m))
	st, m = plain.do(t, http.MethodGet, "/roster", nil)
	require.Equal(t, http.StatusOK, st)
	require.Len(t, m["items"], 2)

	// only an admin changes it
	st, m = plain.do(t, http.MethodPost, "/roster", map[string]any{"github_login": "new-op", "role": "operator", "reason": reason})
	require.Equal(t, http.StatusForbidden, st)
	require.Equal(t, "operator_admin_required", errCode(m))

	st, m = admin.do(t, http.MethodPost, "/roster", map[string]any{"github_login": "new-admin", "role": "operator_admin", "reason": reason})
	require.Equal(t, http.StatusCreated, st, m)
	newAdmin := m["id"].(string)
	require.NotNil(t, m["enrollment_until"])
	st, m = admin.do(t, http.MethodPost, "/roster", map[string]any{"github_login": "new-admin", "role": "operator", "reason": reason})
	require.Equal(t, http.StatusConflict, st)
	require.Equal(t, "operator_exists", errCode(m))
	st, m = admin.do(t, http.MethodPost, "/roster", map[string]any{"github_login": "nobody-here", "role": "operator", "reason": reason})
	require.Equal(t, http.StatusUnprocessableEntity, st, m)
	st, _ = admin.do(t, http.MethodPost, "/roster", map[string]any{"stellar_address": walletOp, "role": "operator"})
	require.Equal(t, http.StatusUnprocessableEntity, st, "a change without a reason")

	var bootRow, plainRow string
	_, m = admin.do(t, http.MethodGet, "/roster", nil)
	for _, it := range m["items"].([]any) {
		row := it.(map[string]any)
		switch int64(row["github_id"].(float64)) {
		case 41:
			bootRow = row["id"].(string)
			require.Equal(t, true, row["bootstrap"])
		case 42:
			plainRow = row["id"].(string)
			require.NotNil(t, row["account"])
		}
	}

	// the deploy settings keep their rows
	for _, act := range []string{"suspend", "remove"} {
		st, m = admin.do(t, http.MethodPost, "/roster/"+bootRow+"/"+act, map[string]any{"reason": reason})
		require.Equal(t, http.StatusConflict, st)
		require.Equal(t, "bootstrap_operator", errCode(m))
	}

	// a suspension ends the person's sessions and access at once
	st, m = admin.do(t, http.MethodPost, "/roster/"+plainRow+"/suspend", map[string]any{"reason": reason})
	require.Equal(t, http.StatusOK, st, m)
	require.Equal(t, "suspended", m["status"])
	st, _ = plain.do(t, http.MethodGet, "/roster", nil)
	require.Equal(t, http.StatusUnauthorized, st, "the session was ended")
	require.Zero(t, superCount(t, pg, `select count(*) from cp.users where id = $1 and is_operator`, plainOpID))
	st, _ = admin.do(t, http.MethodPost, "/roster/"+plainRow+"/resume", map[string]any{"reason": reason})
	require.Equal(t, http.StatusOK, st)

	// a passkey reset deletes the keys and opens a window
	superExec(t, pg, `insert into cp.operator_passkeys (user_id, credential_id, credential, name)
		values ($1, '\x00112233445566778899aabbccddeeff', '{}', 'k')`, plainOpID)
	st, m = admin.do(t, http.MethodPost, "/roster/"+plainRow+"/reset-passkeys", map[string]any{"reason": reason})
	require.Equal(t, http.StatusOK, st, m)
	require.NotNil(t, m["enrollment_until"])
	require.Zero(t, superCount(t, pg, `select count(*) from cp.operator_passkeys where user_id = $1`, plainOpID))

	// roles: the last admin rule counts active admins only. with two admins
	// (41 and the added one) one may step down; the bootstrap one may not
	st, m = admin.do(t, http.MethodPost, "/roster/"+newAdmin+"/role", map[string]any{"role": "operator", "reason": reason})
	require.Equal(t, http.StatusOK, st, m)
	st, m = admin.do(t, http.MethodPost, "/roster/"+bootRow+"/role", map[string]any{"role": "operator", "reason": reason})
	require.Equal(t, http.StatusConflict, st)
	require.Contains(t, []string{"last_operator_admin", "bootstrap_operator"}, errCode(m))

	// removal
	st, _ = admin.do(t, http.MethodPost, "/roster/"+plainRow+"/remove", map[string]any{"reason": reason})
	require.Equal(t, http.StatusNoContent, st)
	st, _ = admin.do(t, http.MethodPost, "/roster/"+plainRow+"/suspend", map[string]any{"reason": reason})
	require.Equal(t, http.StatusNotFound, st)

	for _, a := range []string{ActionOperatorAdd, ActionOperatorSuspend, ActionOperatorResume, ActionOperatorPasskeysReset,
		ActionOperatorRoleChange, ActionOperatorRemove} {
		require.Equal(t, 1, superCount(t, pg, `select count(*) from cp.audit_log where action = $1 and actor = $2
			and detail ->> 'reason' = $3`, a, "user:"+adminID.String(), reason), a)
	}
}
