//go:build isolation

package consoleisolation

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// DELETE /api/me (contract 4.1.6) acts on the caller's own account only: another project's
// rows, members, sites and umami users are untouched, the deleted person's
// umami user is gone from the real umami, and the reconciler, which runs for
// each former project, does not bring it back.
func TestAccountDeletion(t *testing.T) {
	e := start(t)
	alice := e.signIn(t, "ad-alice")
	ta := alice.enroll(t, "AD A")
	sa := alice.addSite(t, ta, "ad-a.example.org")
	bobGH := e.githubAccount(t, "ad-bob")
	bob := e.signInAs(t, bobGH, "ad-bob")
	tp := "/api/tenants/" + ta.String()
	require.Equal(t, http.StatusCreated, alice.call(t, http.MethodPost, tp+"/members",
		map[string]any{"github_login": "ad-bob", "role": "member"}).status)
	tb := bob.enroll(t, "AD B")

	um := e.umami.Admin
	ctx := context.Background()
	exists := func(c *client) bool {
		_, ok, err := um.GetUser(ctx, c.userID.String())
		require.NoError(t, err)
		return ok
	}
	require.Eventually(t, func() bool { return exists(bob) && exists(alice) }, time.Minute, 200*time.Millisecond,
		"the reconciler never made the umami users")

	// bob owns AD B alone: refused, and only his own project is named
	r := bob.call(t, http.MethodDelete, "/api/me", nil)
	require.Equal(t, http.StatusConflict, r.status, string(r.body))
	require.Equal(t, "sole_owner", r.code(t))
	require.Equal(t, []any{map[string]any{"id": tb.String(), "name": "AD B"}}, r.json(t)["error"].(map[string]any)["tenants"])
	require.NotContains(t, string(r.body), ta.String())

	carolGH := e.githubAccount(t, "ad-carol")
	carol := e.signInAs(t, carolGH, "ad-carol")
	require.Equal(t, http.StatusCreated, bob.call(t, http.MethodPost, "/api/tenants/"+tb.String()+"/members",
		map[string]any{"github_login": "ad-carol", "role": "owner"}).status)

	r = bob.call(t, http.MethodDelete, "/api/me", nil)
	require.Equal(t, http.StatusNoContent, r.status, string(r.body))
	require.False(t, exists(bob), "the umami user is deleted before the answer")
	require.Equal(t, http.StatusUnauthorized, bob.call(t, http.MethodGet, "/api/me", nil).status)

	// alice's project: her site and membership as before, bob gone from its members
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, tp+"/sites/"+sa.String(), nil).status)
	m := alice.call(t, http.MethodGet, tp+"/members", nil)
	require.Equal(t, http.StatusOK, m.status)
	require.NotContains(t, string(m.body), "ad-bob")
	require.Contains(t, string(m.body), "ad-alice")
	require.Equal(t, http.StatusOK, carol.call(t, http.MethodGet, "/api/tenants/"+tb.String(), nil).status)
	require.True(t, exists(alice))

	// the reconcile runs of both former projects finish without making bob again
	time.Sleep(3 * time.Second)
	require.False(t, exists(bob))
	var gh int64
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(ctx, `select github_id from cp.users where id = $1`, bob.userID).Scan(&gh))
	})
	require.Zero(t, gh)
	require.Equal(t, 1, e.auditCount(t, "account.delete", "user:"+bob.userID.String()))
}
