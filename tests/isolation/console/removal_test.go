//go:build isolation

package consoleisolation

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane/lifecycle"
)

// a tenant removed through the real operator action (the design) answers
// 404 to every former member on every tenant route, owner included, while
// its row, member rows and audit trail stay
func TestRemovedTenantIsGoneForFormerMembers(t *testing.T) {
	e := start(t)
	operator := e.signIn(t, "rmt-operator")
	owner := e.signIn(t, "rmt-owner")
	member := e.signIn(t, "rmt-member")
	tenant := owner.enroll(t, "Removed Tenant")
	site := owner.addSite(t, tenant, "rmt.example.org")
	tp := "/api/tenants/" + tenant.String()
	require.Equal(t, http.StatusCreated,
		owner.call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "rmt-member", "role": "member"}).status)
	for _, rt := range tenantRoutes(e.terms, tenant.String(), member.userID.String(), uuid.NewString(), site.String()) {
		if rt.method == http.MethodGet {
			require.Equal(t, http.StatusOK, owner.call(t, rt.method, rt.path, rt.body).status, rt.path)
		}
	}

	// the reconcile the action triggers is recorded, not run: this test is
	// about the tenant routes, the purge has its own tests
	var mu sync.Mutex
	var triggered []uuid.UUID
	actions, err := lifecycle.NewActions(e.conn, func(_ context.Context, id uuid.UUID) {
		mu.Lock()
		triggered = append(triggered, id)
		mu.Unlock()
	})
	require.NoError(t, err)
	reason := "not an ecosystem project"
	sum, err := actions.TenantAction(context.Background(), operator.userID, tenant, lifecycle.Remove, &reason)
	require.NoError(t, err)
	require.Equal(t, "removed", sum.Status)
	require.Equal(t, []uuid.UUID{tenant}, triggered)

	for _, c := range []*client{owner, member} {
		for _, rt := range tenantRoutes(e.terms, tenant.String(), member.userID.String(), uuid.NewString(), site.String()) {
			r := c.call(t, rt.method, rt.path, rt.body)
			require.Equal(t, http.StatusNotFound, r.status, "%s %s: %s", rt.method, rt.path, r.body)
			require.Equal(t, "not_found", r.code(t))
		}
		require.Empty(t, c.call(t, http.MethodGet, "/api/tenants", nil).json(t)["items"])
	}
	require.Equal(t, 1, e.auditCount(t, "tenant.remove", "tenant:"+tenant.String()))
	require.Equal(t, 1, e.auditCount(t, "tenant.create", "tenant:"+tenant.String()))
}
