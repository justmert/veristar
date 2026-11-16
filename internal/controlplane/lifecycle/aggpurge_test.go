package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

func seedAgg(t *testing.T, tx pgx.Tx, tenant uuid.UUID) {
	t.Helper()
	site := uuid.New()
	for _, s := range []string{
		`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
		 values ($1, $2, current_date, 'primary', 4, 2, 2, 1, 30)`,
		`insert into agg.web_daily_dim (site_id, tenant_id, day, band, dimension, key, value)
		 values ($1, $2, current_date, 'primary', 'country', 'DE', 2)`,
		`insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed, events, quarantined,
		   proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown, networks, visitors,
		   pageviews, baseline_visitors, baseline_days, reasons)
		 values ($1, $2, current_date, 1, null, false, 4, 0, 0, 4, 0, 0, 0, 0, 1, 2, 4, null, 0,
		   '[{"code":"too_little_traffic","level":"none","value":4,"limit":50}]')`,
		`insert into agg.web_coverage_daily (site_id, tenant_id, day, script_requests, gpc_requests)
		 values ($1, $2, current_date, 120, 12)`,
	} {
		_, err := tx.Exec(t.Context(), s, site, tenant)
		require.NoError(t, err)
	}
}

func aggCounts(t *testing.T, jobs *pdb.DB, tenant uuid.UUID) [4]int {
	t.Helper()
	var out [4]int
	require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(), `select count(*) from agg.web_daily where tenant_id = $1`, tenant).Scan(&out[0]); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `select count(*) from agg.web_daily_dim where tenant_id = $1`, tenant).Scan(&out[1]); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `select count(*) from agg.web_integrity_daily where tenant_id = $1`, tenant).Scan(&out[2]); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `select count(*) from agg.web_coverage_daily where tenant_id = $1`, tenant).Scan(&out[3])
	}))
	return out
}

// TestAggPurger runs the agg purger in a cp_system transaction, as the
// reconciler does, and checks keep_aggregates, the refusal of a live
// tenant, and that a purge waits for an aggregate-web write in flight.
func TestAggPurger(t *testing.T) {
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	cfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem)
	require.NoError(t, err)
	sys, err := pdb.Open(t.Context(), cfg, "agg-purge-test")
	require.NoError(t, err)
	t.Cleanup(sys.Close)

	newTenant := func(slug, status string) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, sys.WithSystem(t.Context(), func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), `insert into cp.tenants (slug, name, status) values ($1, $1, $2) returning id`, slug, status).Scan(&id)
		}))
		return id
	}
	purge := func(tenant uuid.UUID, keep bool) (map[string]any, error) {
		var detail map[string]any
		err := sys.WithSystem(context.Background(), func(tx pgx.Tx) error {
			var err error
			detail, err = lifecycle.AggPurger{}.Purge(context.Background(), lifecycle.Removal{TenantID: tenant, KeepAggregates: keep, Tx: tx})
			return err
		})
		return detail, err
	}

	gone, kept, live := newTenant("gone", "removed"), newTenant("kept", "removed"), newTenant("live", "active")
	for _, id := range []uuid.UUID{gone, kept, live} {
		require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			seedAgg(t, tx, id)
			return nil
		}))
		require.Equal(t, [4]int{1, 1, 1, 1}, aggCounts(t, jobs, id))
	}

	detail, err := purge(gone, false)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"web_daily": 1, "web_daily_dim": 1, "web_integrity_daily": 1, "web_coverage_daily": 1}, detail["rows_deleted"])
	require.Equal(t, [4]int{0, 0, 0, 0}, aggCounts(t, jobs, gone))
	_, err = purge(gone, false)
	require.NoError(t, err, "a retried step is a no-op")

	detail, err = purge(kept, true)
	require.NoError(t, err)
	require.Equal(t, "keep_aggregates", detail["skipped"])
	require.Equal(t, [4]int{1, 1, 1, 1}, aggCounts(t, jobs, kept))

	_, err = purge(live, false)
	require.ErrorContains(t, err, "is not removed")
	require.Equal(t, [4]int{1, 1, 1, 1}, aggCounts(t, jobs, live))

	err = sys.WithSystem(t.Context(), func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `delete from agg.web_daily where tenant_id = $1`, live)
		return err
	})
	require.ErrorContains(t, err, "permission denied", "cp_system reaches agg only through the function")

	// an aggregate-web write in flight holds the tenant's lock shared; the
	// purge waits for its commit and then deletes what it wrote
	late := newTenant("late", "removed")
	p, err := jobs.Pool(pdb.RoleJobs)
	require.NoError(t, err)
	tx, err := p.Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	require.NoError(t, jobsq.New(tx).LockAggTenant(t.Context(), late.String()))
	seedAgg(t, tx, late)
	done := make(chan error, 1)
	go func() {
		_, err := purge(late, false)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("purge did not wait for the write in flight: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(t.Context()))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("purge did not finish after the write committed")
	}
	require.Equal(t, [4]int{0, 0, 0, 0}, aggCounts(t, jobs, late))
}
