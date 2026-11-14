package account_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/account"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/tests/fixtures/consoleapi"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
)

// operatorGH is the github id the harness treats as an operator
const operatorGH = 700_001

type env struct {
	*consoleapi.Env
	// reconciled records every tenant a reconcile run was asked for
	mu         sync.Mutex
	reconciled []uuid.UUID
}

func start(t *testing.T) *env {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "account-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	e := &env{}
	e.Env = consoleapi.Start(t, consoleapi.Options{
		PG: pg, DB: d,
		Reconciler: tenants.ReconcileFunc(func(_ context.Context, id uuid.UUID) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.reconciled = append(e.reconciled, id)
			return nil
		}),
		Operators: map[int64]bool{operatorGH: true},
	})
	return e
}

// person signs in a github account the user lookup also knows, so owners
// can add it by login
func (e *env) person(t *testing.T, login string) (*consoleapi.Client, uuid.UUID) {
	t.Helper()
	gh := e.NewGitHubID()
	return e.personWithID(t, gh, login)
}

func (e *env) personWithID(t *testing.T, gh int64, login string) (*consoleapi.Client, uuid.UUID) {
	t.Helper()
	e.OAuth.Register(githuboauth.User{ID: gh, Login: login})
	c := e.SignIn(t, gh, login)
	return c, e.userID(t, gh)
}

func (e *env) userID(t *testing.T, gh int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	e.Superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(),
			`select id from cp.users where github_id = $1 and deleted_at is null`, gh).Scan(&id))
	})
	return id
}

func addMember(t *testing.T, owner *consoleapi.Client, tenant uuid.UUID, login, role string) {
	t.Helper()
	r := owner.Call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/members",
		map[string]any{"github_login": login, "role": role})
	require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
	require.Equal(t, "member", r.JSON(t)["kind"], string(r.Body))
}

func randomHash(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// mcpCredential gives user an access key and an oauth grant with a token
// and a pending code for tenant, as the mcp routes would
func (e *env) mcpCredentials(t *testing.T, tenant, user uuid.UUID) {
	t.Helper()
	e.Superuser(t, func(c *pgx.Conn) {
		ctx := context.Background()
		_, err := c.Exec(ctx, `insert into cp.mcp_credentials
			(kind, scope, tenant_id, user_id, name, prefix, hash, resource, expires_at)
			values ('key', 'tenant', $1, $2, 'laptop key', 'sa_mcp_abcd', $3, 'https://app.example.org/mcp', now() + interval '30 days')`,
			tenant, user, randomHash(t))
		require.NoError(t, err)
		client := "mcpc_" + strings.Repeat("a", 31) + string(rune('a'+len(user.String())%26))
		_, err = c.Exec(ctx, `insert into cp.mcp_oauth_clients (id, client_name, redirect_uris)
			values ($1, 'desktop client', array['http://127.0.0.1:9/cb']) on conflict do nothing`, client)
		require.NoError(t, err)
		var cred uuid.UUID
		require.NoError(t, c.QueryRow(ctx, `insert into cp.mcp_credentials
			(kind, scope, tenant_id, user_id, name, client_id, resource)
			values ('oauth', 'tenant', $1, $2, 'desktop client', $3, 'https://app.example.org/mcp') returning id`,
			tenant, user, client).Scan(&cred))
		_, err = c.Exec(ctx, `insert into cp.mcp_oauth_tokens (hash, credential_id, kind, expires_at)
			values ($1, $2, 'refresh', now() + interval '30 days')`, randomHash(t), cred)
		require.NoError(t, err)
		_, err = c.Exec(ctx, `insert into cp.mcp_oauth_codes
			(hash, client_id, user_id, scope, tenant_id, redirect_uri, code_challenge, resource, expires_at)
			values ($1, $2, $3, 'tenant', $4, 'http://127.0.0.1:9/cb', $5, 'https://app.example.org/mcp', now() + interval '60 seconds')`,
			randomHash(t), client, user, tenant, strings.Repeat("A", 43))
		require.NoError(t, err)
	})
}

// snapshot counts what an account deletion must not touch for other people
func (e *env) snapshot(t *testing.T, users ...uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, u := range users {
		k := u.String()
		out[k+" memberships"] = e.Count(t, `select count(*) from cp.tenant_members where user_id = $1`, u)
		out[k+" sessions"] = e.Count(t, `select count(*) from cp.sessions where user_id = $1`, u)
		out[k+" credentials"] = e.Count(t, `select count(*) from cp.mcp_credentials where user_id = $1 and revoked_at is null`, u)
		out[k+" identity"] = e.Count(t, `select count(*) from cp.users where id = $1 and deleted_at is null and github_id > 0`, u)
	}
	out["tenants"] = e.Count(t, `select count(*) from cp.tenants`)
	out["sites"] = e.Count(t, `select count(*) from cp.sites`)
	return out
}

func TestDeleteAccount(t *testing.T) {
	e := start(t)

	t.Run("the only owner of a project is refused and nothing changes", func(t *testing.T) {
		solo, soloID := e.person(t, "acct-solo")
		tenant, _ := solo.Enroll(t, "Solo Project")
		e.Umami.Add(soloID.String())
		before := e.snapshot(t, soloID)
		audits := e.Count(t, `select count(*) from cp.audit_log`)

		r := solo.Call(t, http.MethodDelete, "/api/me", nil)
		require.Equal(t, http.StatusConflict, r.Status, string(r.Body))
		require.Equal(t, "sole_owner", r.Code(t))
		list, ok := r.JSON(t)["error"].(map[string]any)["tenants"].([]any)
		require.True(t, ok, string(r.Body))
		require.Len(t, list, 1)
		require.Equal(t, map[string]any{"id": tenant.String(), "name": "Solo Project"}, list[0])

		require.Equal(t, before, e.snapshot(t, soloID))
		require.Equal(t, audits, e.Count(t, `select count(*) from cp.audit_log`))
		require.True(t, e.Umami.Has(soloID.String()))
		require.Equal(t, http.StatusOK, solo.Call(t, http.MethodGet, "/api/me", nil).Status, "the session must survive")

		// once the project is removed it blocks nothing
		e.Superuser(t, func(c *pgx.Conn) {
			_, err := c.Exec(context.Background(),
				`update cp.tenants set status = 'removed', status_changed_at = now() where id = $1`, tenant)
			require.NoError(t, err)
		})
		r = solo.Call(t, http.MethodDelete, "/api/me", nil)
		require.Equal(t, http.StatusNoContent, r.Status, string(r.Body))
		require.Zero(t, e.Count(t, `select count(*) from cp.tenant_members where user_id = $1`, soloID))
	})

	t.Run("every project and its error lists each project the person owns alone", func(t *testing.T) {
		p, _ := e.person(t, "acct-many")
		a, _ := p.Enroll(t, "Beta Project")
		b, _ := p.Enroll(t, "alpha project")
		r := p.Call(t, http.MethodDelete, "/api/me", nil)
		require.Equal(t, http.StatusConflict, r.Status, string(r.Body))
		list := r.JSON(t)["error"].(map[string]any)["tenants"].([]any)
		require.Equal(t, []any{
			map[string]any{"id": b.String(), "name": "alpha project"},
			map[string]any{"id": a.String(), "name": "Beta Project"},
		}, list, "ordered by name, case-insensitively")
	})

	t.Run("deletion removes the account and leaves everyone else as they were", func(t *testing.T) {
		alice, aliceID := e.person(t, "acct-alice")
		dave, daveID := e.person(t, "acct-dave")
		bobGH := e.NewGitHubID()
		bob, bobID := e.personWithID(t, bobGH, "acct-bob")
		// a second browser of bob's
		bob2 := e.SignIn(t, bobGH, "acct-bob")

		ta, _ := alice.Enroll(t, "Alice Project")
		siteA := alice.AddSite(t, ta, "alice.example.org")
		addMember(t, alice, ta, "acct-bob", "member")
		tc, _ := dave.Enroll(t, "Shared Project")
		addMember(t, dave, tc, "acct-bob", "owner")
		// an open invite addressed to bob's account in a project bob is not in
		td, _ := dave.Enroll(t, "Invite Project")
		e.Superuser(t, func(c *pgx.Conn) {
			_, err := c.Exec(context.Background(), `insert into cp.invites
				(tenant_id, github_id, github_login, role, created_by, expires_at)
				values ($1, $2, 'acct-bob', 'member', $3, now() + interval '7 days')`, td, bobGH, daveID)
			require.NoError(t, err)
		})
		e.mcpCredentials(t, ta, bobID)
		e.mcpCredentials(t, ta, aliceID)
		e.Umami.Add(bobID.String())
		e.Umami.Add(aliceID.String())

		others := e.snapshot(t, aliceID, daveID)
		auditBefore := e.Count(t, `select count(*) from cp.audit_log`)
		e.mu.Lock()
		e.reconciled = nil
		e.mu.Unlock()

		r := bob.Call(t, http.MethodDelete, "/api/me", nil)
		require.Equal(t, http.StatusNoContent, r.Status, string(r.Body))
		cookie := strings.Join(r.Header.Values("Set-Cookie"), "\n")
		require.Contains(t, cookie, "sa_session=")
		require.Contains(t, cookie, "Max-Age=0", "the cookie must be expired")

		// the account row keeps its id and nothing that names the person
		e.Superuser(t, func(c *pgx.Conn) {
			var gh int64
			var login string
			var op bool
			var last, deleted *time.Time
			require.NoError(t, c.QueryRow(context.Background(),
				`select github_id, github_login, is_operator, last_login_at, deleted_at from cp.users where id = $1`,
				bobID).Scan(&gh, &login, &op, &last, &deleted))
			require.Zero(t, gh)
			require.Empty(t, login)
			require.False(t, op)
			require.Nil(t, last)
			require.NotNil(t, deleted)
		})
		require.Zero(t, e.Count(t, `select count(*) from cp.users where github_id = $1 or github_login = 'acct-bob'`, bobGH))
		require.Zero(t, e.Count(t, `select count(*) from cp.tenant_members where user_id = $1`, bobID))
		require.Zero(t, e.Count(t, `select count(*) from cp.sessions where user_id = $1`, bobID))
		require.Zero(t, e.Count(t, `select count(*) from cp.invites where github_id = $1`, bobGH))
		require.Zero(t, e.Count(t, `select count(*) from cp.mcp_credentials where user_id = $1`, bobID))
		require.Zero(t, e.Count(t, `select count(*) from cp.mcp_oauth_codes where user_id = $1`, bobID))
		deleted := e.Umami.Deleted()
		require.Equal(t, bobID.String(), deleted[len(deleted)-1])
		require.NotContains(t, deleted, aliceID.String())
		require.True(t, e.Umami.Has(aliceID.String()))

		// both of bob's browsers are signed out
		require.Equal(t, http.StatusUnauthorized, bob.Call(t, http.MethodGet, "/api/me", nil).Status)
		require.Equal(t, http.StatusUnauthorized, bob2.Call(t, http.MethodGet, "/api/me", nil).Status)
		require.Equal(t, http.StatusUnauthorized, bob.Call(t, http.MethodDelete, "/api/me", nil).Status)

		// everyone else, their sessions, credentials and projects are unchanged
		require.Equal(t, others, e.snapshot(t, aliceID, daveID))
		require.Equal(t, http.StatusOK, alice.Call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/sites/"+siteA.String(), nil).Status)
		members := dave.Call(t, http.MethodGet, "/api/tenants/"+tc.String()+"/members", nil)
		require.Equal(t, http.StatusOK, members.Status)
		require.NotContains(t, string(members.Body), "acct-bob")
		require.Contains(t, string(members.Body), "acct-dave")

		// one account.delete row naming bob by id only; earlier rows are kept
		require.Greater(t, e.Count(t, `select count(*) from cp.audit_log`), auditBefore)
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.audit_log where action = 'account.delete'
			and actor = $1 and target = $1`, "user:"+bobID.String()))
		require.Zero(t, e.Count(t, `select count(*) from cp.audit_log where action = 'account.delete'
			and (detail::text like '%acct-bob%' or detail::text like '%' || $1 || '%')`, strconv.FormatInt(bobGH, 10)))
		require.Equal(t, 2, e.Count(t, `select count(*) from cp.audit_log where action = 'mcp_credential.revoke'
			and detail ->> 'reason' = 'member_removed'`), "the membership trigger revoked bob's two credentials first")

		// each former project of bob's gets a reconcile run
		e.mu.Lock()
		got := append([]uuid.UUID(nil), e.reconciled...)
		e.mu.Unlock()
		require.ElementsMatch(t, []uuid.UUID{ta, tc}, got)
	})

	t.Run("deleting a deleted account changes nothing", func(t *testing.T) {
		p, id := e.person(t, "acct-twice")
		require.Equal(t, http.StatusNoContent, p.Call(t, http.MethodDelete, "/api/me", nil).Status)
		audits := e.Count(t, `select count(*) from cp.audit_log`)
		res, err := account.Delete(t.Context(), e.DB, e.Umami, id)
		require.NoError(t, err)
		require.False(t, res.Deleted)
		require.Equal(t, audits, e.Count(t, `select count(*) from cp.audit_log`))
		_, err = account.Delete(t.Context(), e.DB, e.Umami, uuid.New())
		require.Error(t, err, "an unknown id is not found")
	})

	t.Run("signing in again makes a new account with nothing of the old one", func(t *testing.T) {
		gh := e.NewGitHubID()
		p, oldID := e.personWithID(t, gh, "acct-return")
		owner, _ := e.person(t, "acct-return-owner")
		tenant, _ := owner.Enroll(t, "Return Project")
		addMember(t, owner, tenant, "acct-return", "member")
		require.Equal(t, http.StatusNoContent, p.Call(t, http.MethodDelete, "/api/me", nil).Status)

		again := e.SignIn(t, gh, "acct-return")
		newID := e.userID(t, gh)
		require.NotEqual(t, oldID, newID)
		me := again.Call(t, http.MethodGet, "/api/me", nil)
		require.Equal(t, http.StatusOK, me.Status)
		require.Empty(t, me.JSON(t)["tenants"])
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.users where id = $1 and github_id = 0`, oldID))
	})

	t.Run("an operator loses the flag, and the allowlist grants it to a new account at the next sign-in", func(t *testing.T) {
		op, opID := e.personWithID(t, operatorGH, "acct-operator")
		require.Equal(t, true, op.Call(t, http.MethodGet, "/api/me", nil).JSON(t)["user"].(map[string]any)["is_operator"])
		require.Equal(t, http.StatusNoContent, op.Call(t, http.MethodDelete, "/api/me", nil).Status)
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.users where id = $1 and not is_operator`, opID))

		again := e.SignIn(t, operatorGH, "acct-operator")
		require.NotEqual(t, opID, e.userID(t, operatorGH))
		require.Equal(t, true, again.Call(t, http.MethodGet, "/api/me", nil).JSON(t)["user"].(map[string]any)["is_operator"])
	})

	t.Run("umami unreachable rolls everything back", func(t *testing.T) {
		owner, ownerID := e.person(t, "acct-fail-owner")
		p, id := e.person(t, "acct-fail")
		tenant, _ := owner.Enroll(t, "Fail Project")
		addMember(t, owner, tenant, "acct-fail", "member")
		e.mcpCredentials(t, tenant, id)
		before := e.snapshot(t, id, ownerID)
		e.Umami.Fail = errors.New("connection refused")
		t.Cleanup(func() { e.Umami.Fail = nil })

		r := p.Call(t, http.MethodDelete, "/api/me", nil)
		require.Equal(t, http.StatusServiceUnavailable, r.Status, string(r.Body))
		require.Equal(t, "unavailable", r.Code(t))
		require.Equal(t, before, e.snapshot(t, id, ownerID))
		require.Zero(t, e.Count(t, `select count(*) from cp.audit_log where action = 'account.delete' and actor = $1`,
			"user:"+id.String()))
		require.Equal(t, http.StatusOK, p.Call(t, http.MethodGet, "/api/me", nil).Status)

		e.Umami.Fail = nil
		require.Equal(t, http.StatusNoContent, p.Call(t, http.MethodDelete, "/api/me", nil).Status)
	})

}

// the deletion locks the owner rows as a demotion does, so two owners who
// delete their accounts at the same moment cannot leave a project without one
func TestConcurrentDeletionsKeepAnOwner(t *testing.T) {
	e := start(t)
	for i := range 3 {
		x, _ := e.person(t, fmt.Sprintf("acct-race-x%d", i))
		y, _ := e.person(t, fmt.Sprintf("acct-race-y%d", i))
		tenant, _ := x.Enroll(t, fmt.Sprintf("Race Project %d", i))
		addMember(t, x, tenant, fmt.Sprintf("acct-race-y%d", i), "owner")
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for j, c := range []*consoleapi.Client{x, y} {
			wg.Go(func() { codes[j] = c.Call(t, http.MethodDelete, "/api/me", nil).Status })
		}
		wg.Wait()
		require.ElementsMatch(t, []int{http.StatusNoContent, http.StatusConflict}, codes, "round %d", i)
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenant_members where tenant_id = $1 and role = 'owner'`, tenant))
	}
}

func TestDeleteRequiresTheConsoleHeaders(t *testing.T) {
	e := start(t)
	owner, _ := e.person(t, "acct-csrf-owner")
	p, id := e.person(t, "acct-csrf")
	tenant, _ := owner.Enroll(t, "Csrf Project")
	addMember(t, owner, tenant, "acct-csrf", "member")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, e.Srv.URL+"/api/me", nil)
	require.NoError(t, err)
	res, err := e.Srv.Client().Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, 1, e.Count(t, `select count(*) from cp.users where id = $1 and deleted_at is null`, id))
	require.Equal(t, http.StatusOK, p.Call(t, http.MethodGet, "/api/me", nil).Status)
}

// migration 00031: a row is either a live github account or fully emptied
func TestUserRowIsLiveOrEmptied(t *testing.T) {
	e := start(t)
	_, id := e.person(t, "acct-shape")
	for _, stmt := range []string{
		`update cp.users set github_id = 0 where id = $1`,
		`update cp.users set github_login = '' where id = $1`,
		`update cp.users set deleted_at = now() where id = $1`,
		`update cp.users set github_id = 0, github_login = '', deleted_at = now() where id = $1`,
	} {
		e.Superuser(t, func(c *pgx.Conn) {
			_, err := c.Exec(context.Background(), stmt, id)
			require.ErrorContains(t, err, "users_identity_check", stmt)
		})
	}
	e.Superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.users set github_id = 0, github_login = '', is_operator = false,
			last_login_at = null, deleted_at = now() where id = $1`, id)
		require.NoError(t, err)
	})
}
