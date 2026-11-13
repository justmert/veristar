package sites_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/tests/fixtures/consoleapi"
)

// contract 4.3: collect_utm is a site setting that is off on a new site and
// that the site's owners change with the site edit route (review: a
// member gets 403 owner_required). a change starts
// a reconcile, which publishes it to the gateway (provision live test); an
// unchanged value does not. the change is audited.
func TestSiteCollectUTM(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "utm-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	var runs atomic.Int64
	e := consoleapi.Start(t, consoleapi.Options{PG: pg, DB: d,
		Reconciler: tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
			runs.Add(1)
			return nil
		})})

	owner := e.SignIn(t, e.NewGitHubID(), "utm-owner")
	tenant, _ := owner.Enroll(t, "Campaign Project")
	site := owner.AddSite(t, tenant, "utm.example.org")
	path := "/api/tenants/" + tenant.String() + "/sites/" + site.String()

	r := owner.Call(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, r.Status)
	require.Equal(t, false, r.JSON(t)["collect_utm"], "off on a new site")

	set := func(v any) consoleapi.Resp {
		return owner.Call(t, http.MethodPatch, path, map[string]any{"collect_utm": v})
	}
	before := runs.Load()
	r = set(true)
	require.Equal(t, http.StatusOK, r.Status, string(r.Body))
	require.Equal(t, true, r.JSON(t)["collect_utm"])
	require.Eventually(t, func() bool { return runs.Load() > before }, 5*time.Second, 10*time.Millisecond, "a change reconciles")
	r = owner.Call(t, http.MethodGet, path, nil)
	require.Equal(t, true, r.JSON(t)["collect_utm"])
	require.Equal(t, 1, e.Count(t,
		`select count(*) from cp.audit_log where target = $1 and detail ? 'collect_utm'`, "site:"+site.String()))

	// the same value again is no change: no reconcile, no audit row
	e.Scope.Wait()
	settled := runs.Load()
	r = set(true)
	require.Equal(t, http.StatusOK, r.Status)
	e.Scope.Wait()
	require.Equal(t, settled, runs.Load())

	r = set(false)
	require.Equal(t, http.StatusOK, r.Status)
	require.Equal(t, false, r.JSON(t)["collect_utm"])
	require.Equal(t, 2, e.Count(t,
		`select count(*) from cp.audit_log where target = $1 and detail ? 'collect_utm'`, "site:"+site.String()))

	r = set("yes")
	require.Equal(t, http.StatusBadRequest, r.Status, string(r.Body))

	// a member may edit the site but not turn campaign parameters on or off
	member := e.SignIn(t, e.NewGitHubID(), "utm-member")
	e.Superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(ctx, `insert into cp.tenant_members (tenant_id, user_id, role)
			select $1, id, 'member' from cp.users where github_id = $2`, tenant, member.GHID)
		require.NoError(t, err)
	})
	r = member.Call(t, http.MethodPatch, path, map[string]any{"collect_utm": true})
	require.Equal(t, http.StatusForbidden, r.Status, string(r.Body))
	require.Equal(t, "owner_required", r.Code(t))
	r = member.Call(t, http.MethodGet, path, nil)
	require.Equal(t, false, r.JSON(t)["collect_utm"], "the member's change was not applied")
	// the unchanged value with a name change is an ordinary member edit
	r = member.Call(t, http.MethodPatch, path, map[string]any{"collect_utm": false, "name": "renamed by member"})
	require.Equal(t, http.StatusOK, r.Status, string(r.Body))
	require.Equal(t, "renamed by member", r.JSON(t)["name"])
}
