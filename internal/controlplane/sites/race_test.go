package sites_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/tests/fixtures/consoleapi"
)

// startRace serves the console api over a real postgresql. the reconciler
// does nothing: these tests are about the cp tables only.
func startRace(t *testing.T) *consoleapi.Env {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "sites-race-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return consoleapi.Start(t, consoleapi.Options{
		PG: pg, DB: d,
		Reconciler: tenants.ReconcileFunc(func(context.Context, uuid.UUID) error { return nil }),
	})
}

// operatorTx holds the tenant row lock the way the operator lifecycle
// actions do (LifecycleLockTenant) and applies change inside it. the caller
// commits once the racing request waits behind the lock.
func operatorTx(t *testing.T, e *consoleapi.Env, tenant uuid.UUID, change string) pgx.Tx {
	t.Helper()
	p, err := e.DB.Pool(pdb.RoleCPSystem)
	require.NoError(t, err)
	tx, err := p.Begin(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err = tx.Exec(context.Background(), `select id from cp.tenants where id = $1 for update`, tenant)
	require.NoError(t, err)
	_, err = tx.Exec(context.Background(), change, tenant)
	require.NoError(t, err)
	return tx
}

// race sends a request that queues behind tx's lock, commits tx and
// returns the request's answer.
func race(t *testing.T, e *consoleapi.Env, tx pgx.Tx, send func() consoleapi.Resp) consoleapi.Resp {
	t.Helper()
	got := make(chan consoleapi.Resp, 1)
	go func() { got <- send() }()
	e.WaitForLockWait(t, "cp_tenant")
	require.NoError(t, tx.Commit(context.Background()))
	return <-got
}

//: a site create or a member add that waits on the tenant
// row lock while an operator removes or approves the tenant sees the status
// that operator committed, not the one read before the lock.
func TestTenantWritesRecheckStatusUnderTheLock(t *testing.T) {
	e := startRace(t)
	owner := e.SignIn(t, e.NewGitHubID(), "race-owner")
	friend := e.SignIn(t, e.NewGitHubID(), "race-friend")
	const removeSQL = `with t as (update cp.tenants set status = 'removed', status_changed_at = now() where id = $1)
		update cp.sites set status = 'removed', updated_at = now() where tenant_id = $1`

	t.Run("site create racing a removal", func(t *testing.T) {
		tenant, _ := owner.Enroll(t, "Removed While Creating")
		tx := operatorTx(t, e, tenant, removeSQL)
		r := race(t, e, tx, func() consoleapi.Resp {
			r, err := owner.Do(context.Background(), http.MethodPost, "/api/tenants/"+tenant.String()+"/sites",
				map[string]any{"name": "late", "domain": "late.example.org", "origins": []string{"https://late.example.org"}})
			require.NoError(t, err)
			return r
		})
		require.Equal(t, http.StatusNotFound, r.Status, string(r.Body))
		require.Zero(t, e.Count(t, `select count(*) from cp.sites where tenant_id = $1 and status <> 'removed'`, tenant),
			"no live site may exist in a removed tenant")
	})

	t.Run("member add racing a removal", func(t *testing.T) {
		tenant, _ := owner.Enroll(t, "Removed While Inviting")
		tx := operatorTx(t, e, tenant, removeSQL)
		r := race(t, e, tx, func() consoleapi.Resp {
			r, err := owner.Do(context.Background(), http.MethodPost, "/api/tenants/"+tenant.String()+"/members",
				map[string]any{"github_login": "race-friend", "role": "owner"})
			require.NoError(t, err)
			return r
		})
		require.Equal(t, http.StatusNotFound, r.Status, string(r.Body))
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.tenant_members where tenant_id = $1`, tenant))
		require.Zero(t, e.Count(t, `select count(*) from cp.invites where tenant_id = $1`, tenant))
		_ = friend
	})

	t.Run("site create racing an approval", func(t *testing.T) {
		tenant, _ := owner.Enroll(t, "Approved While Creating")
		e.Superuser(t, func(c *pgx.Conn) {
			_, err := c.Exec(context.Background(), `update cp.tenants set status = 'pending' where id = $1`, tenant)
			require.NoError(t, err)
		})
		// what approve does: the tenant and its pending sites become active
		tx := operatorTx(t, e, tenant, `with t as (update cp.tenants set status = 'active', status_changed_at = now() where id = $1)
			update cp.sites set status = 'active', updated_at = now() where tenant_id = $1 and status = 'pending'`)
		r := race(t, e, tx, func() consoleapi.Resp {
			r, err := owner.Do(context.Background(), http.MethodPost, "/api/tenants/"+tenant.String()+"/sites",
				map[string]any{"name": "early", "domain": "early.example.org", "origins": []string{"https://early.example.org"}})
			require.NoError(t, err)
			return r
		})
		require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
		require.Equal(t, "active", r.JSON(t)["status"], "a site created in an approved tenant is active")
		require.Equal(t, 1, e.Count(t, `select count(*) from cp.sites where tenant_id = $1 and status = 'active'`, tenant))
	})

	t.Run("site create racing a suspension", func(t *testing.T) {
		tenant, _ := owner.Enroll(t, "Suspended While Creating")
		tx := operatorTx(t, e, tenant, `update cp.tenants set status = 'suspended', status_changed_at = now() where id = $1`)
		r := race(t, e, tx, func() consoleapi.Resp {
			r, err := owner.Do(context.Background(), http.MethodPost, "/api/tenants/"+tenant.String()+"/sites",
				map[string]any{"name": "s", "domain": "s.example.org", "origins": []string{"https://s.example.org"}})
			require.NoError(t, err)
			return r
		})
		require.Equal(t, http.StatusConflict, r.Status, string(r.Body))
		require.Equal(t, "tenant_suspended", r.Code(t))
	})

	//: a tenant request does not wait for the tenant row
	// lock longer than its lock timeout (a reconcile holds the lock across
	// umami calls); it answers 503 with Retry-After instead of holding a
	// pooled connection
	t.Run("site create behind a long-held tenant lock answers 503", func(t *testing.T) {
		tenant, _ := owner.Enroll(t, "Busy Project")
		tx := operatorTx(t, e, tenant, `select $1::uuid`)
		started := time.Now()
		r, err := owner.Do(context.Background(), http.MethodPost, "/api/tenants/"+tenant.String()+"/sites",
			map[string]any{"name": "busy", "domain": "busy.example.org", "origins": []string{"https://busy.example.org"}})
		require.NoError(t, err)
		waited := time.Since(started)
		require.NoError(t, tx.Rollback(context.Background()))
		require.Equal(t, http.StatusServiceUnavailable, r.Status, string(r.Body))
		require.Equal(t, "unavailable", r.Code(t))
		require.NotEmpty(t, r.Header.Get("Retry-After"))
		require.Less(t, waited, 10*time.Second)
	})
}
