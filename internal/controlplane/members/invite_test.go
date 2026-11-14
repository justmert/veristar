package members_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/tests/fixtures/consoleapi"
	"github.com/justmert/veristar/tests/fixtures/githuboauth"
)

func start(t *testing.T) *consoleapi.Env {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "members-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return consoleapi.Start(t, consoleapi.Options{
		PG: pg, DB: d,
		Reconciler: tenants.ReconcileFunc(func(context.Context, uuid.UUID) error { return nil }),
	})
}

// an invite and a direct add name the github
// account that held the login when the tenant owner added it. whoever holds that
// login later, after a rename, gets nothing.
func TestMembersAreBoundToTheGitHubAccount(t *testing.T) {
	e := start(t)
	owner := e.SignIn(t, e.NewGitHubID(), "c5-owner")
	tenant, _ := owner.Enroll(t, "Bound Invites")
	tp := "/api/tenants/" + tenant.String()

	t.Run("an invite resolves for the account, not the login", func(t *testing.T) {
		alice := e.NewGitHubID()
		e.OAuth.Register(githuboauth.User{ID: alice, Login: "c5-alice"})
		r := owner.Call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "C5-Alice", "role": "owner"})
		require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
		require.Equal(t, "invite", r.JSON(t)["kind"])

		// alice renames her account and someone else takes the freed login
		e.OAuth.Register(githuboauth.User{ID: alice, Login: "c5-alice-renamed"})
		impostor := e.SignIn(t, e.NewGitHubID(), "c5-alice")
		require.Equal(t, http.StatusNotFound, impostor.Call(t, http.MethodGet, tp, nil).Status,
			"the new holder of the login must not get the invite")
		require.Zero(t, e.Count(t, `select count(*) from cp.tenant_members m join cp.users u on u.id = m.user_id
			where m.tenant_id = $1 and u.github_id = $2`, tenant, impostor.GHID))

		// alice signs in under her new login and gets the role she was invited with
		real := e.SignIn(t, alice, "c5-alice-renamed")
		r = real.Call(t, http.MethodGet, tp, nil)
		require.Equal(t, http.StatusOK, r.Status, string(r.Body))
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenant_members m join cp.users u on u.id = m.user_id
			where m.tenant_id = $1 and u.github_id = $2 and m.role = 'owner'`, tenant, alice))
	})

	t.Run("a direct add ignores a stale login on an old user row", func(t *testing.T) {
		carol := e.NewGitHubID()
		e.SignIn(t, carol, "c5-carol")
		// carol renames; dave takes "c5-carol" and has never signed in here
		e.OAuth.Register(githuboauth.User{ID: carol, Login: "c5-carol-2"})
		dave := e.NewGitHubID()
		e.OAuth.Register(githuboauth.User{ID: dave, Login: "c5-carol"})
		r := owner.Call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "c5-carol", "role": "member"})
		require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
		require.Equal(t, "invite", r.JSON(t)["kind"], "dave has no user row yet, so he is invited")
		require.Zero(t, e.Count(t, `select count(*) from cp.tenant_members m join cp.users u on u.id = m.user_id
			where m.tenant_id = $1 and u.github_id = $2`, tenant, carol), "carol was not the one added")

		// a signed-in account is added at once under the login github has now
		r = owner.Call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "c5-carol-2", "role": "member"})
		require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
		m := r.JSON(t)
		require.Equal(t, "member", m["kind"])
		require.EqualValues(t, carol, m["member"].(map[string]any)["github_id"])
	})

	t.Run("a login that names no github user is refused", func(t *testing.T) {
		r := owner.Call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "c5-nobody", "role": "member"})
		require.Equal(t, http.StatusUnprocessableEntity, r.Status, string(r.Body))
		fields := r.JSON(t)["error"].(map[string]any)["fields"].(map[string]any)
		require.Equal(t, "unknown_github_login", fields["github_login"])
	})
}
