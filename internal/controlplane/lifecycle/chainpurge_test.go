package lifecycle_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// TestChainPurger runs the chain purger in a cp_system transaction: the
// read state always goes, the daily counts unless keep_aggregates, and a
// tenant that is not removed is refused.
func TestChainPurger(t *testing.T) {
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	cfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem)
	require.NoError(t, err)
	sys, err := pdb.Open(t.Context(), cfg, "chain-purge-test")
	require.NoError(t, err)
	t.Cleanup(sys.Close)

	const account = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB"
	newTenant := func(slug, status string) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, sys.WithSystem(t.Context(), func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), `insert into cp.tenants (slug, name, status) values ($1, $1, $2) returning id`, slug, status).Scan(&id)
		}))
		require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			if _, err := tx.Exec(t.Context(), `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
				values ($1, $2, current_date, 'horizon', 2, 1, 1)`, id, account); err != nil {
				return err
			}
			_, err := tx.Exec(t.Context(), `insert into agg.chain_reads (tenant_id, strkey, covered_from, covered_to, last_attempt_at)
				values ($1, $2, current_date, current_date, now())`, id, account)
			return err
		}))
		return id
	}
	counts := func(id uuid.UUID) [2]int {
		var out [2]int
		require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			if err := tx.QueryRow(t.Context(), `select count(*) from agg.chain_daily where tenant_id = $1`, id).Scan(&out[0]); err != nil {
				return err
			}
			return tx.QueryRow(t.Context(), `select count(*) from agg.chain_reads where tenant_id = $1`, id).Scan(&out[1])
		}))
		return out
	}
	purge := func(id uuid.UUID, keep bool) (map[string]any, error) {
		var detail map[string]any
		err := sys.WithSystem(context.Background(), func(tx pgx.Tx) error {
			var err error
			detail, err = lifecycle.ChainPurger{}.Purge(context.Background(), lifecycle.Removal{TenantID: id, KeepAggregates: keep, Tx: tx})
			return err
		})
		return detail, err
	}

	gone, kept, live := newTenant("gone", "removed"), newTenant("kept", "removed"), newTenant("live", "active")
	detail, err := purge(gone, false)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"chain_daily": 1, "chain_reads": 1}, detail["rows_deleted"])
	require.Equal(t, [2]int{0, 0}, counts(gone))

	_, err = purge(kept, true)
	require.NoError(t, err)
	require.Equal(t, [2]int{1, 0}, counts(kept))

	_, err = purge(live, false)
	require.ErrorContains(t, err, "is not removed")
	require.Equal(t, [2]int{1, 1}, counts(live))
}
