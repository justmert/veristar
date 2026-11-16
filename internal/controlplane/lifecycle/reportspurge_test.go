package lifecycle_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// TestReportsPurger runs the reports purger in a cp_system transaction: a
// removed tenant's reports go, a tenant that is not removed is refused, and
// cp_system can never delete a report directly.
func TestReportsPurger(t *testing.T) {
	pg := jobstest.Postgres(t)
	cfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem)
	require.NoError(t, err)
	sys, err := pdb.Open(t.Context(), cfg, "reports-purge-test")
	require.NoError(t, err)
	t.Cleanup(sys.Close)
	c, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()

	n := 0
	newTenant := func(slug, status string) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, c.QueryRow(t.Context(), `insert into cp.tenants (slug, name, status) values ($1, $1, 'active') returning id`, slug).Scan(&id))
		for range 2 {
			n++
			rid := strings.Repeat(string(rune('a'+n)), 26)
			_, err := c.Exec(t.Context(), `insert into cp.usage_reports (id, tenant_id, period_from, period_to, body, signature, signer)
				values ($1, $2, current_date - 1, current_date - 1, '{}', $3, $4)`,
				rid, id, strings.Repeat("A", 86)+"==", "G"+strings.Repeat("A", 55))
			require.NoError(t, err)
		}
		_, err := c.Exec(t.Context(), `update cp.tenants set status = $2 where id = $1`, id, status)
		require.NoError(t, err)
		return id
	}
	count := func(id uuid.UUID) int {
		var out int
		require.NoError(t, c.QueryRow(t.Context(), `select count(*) from cp.usage_reports where tenant_id = $1`, id).Scan(&out))
		return out
	}
	purge := func(id uuid.UUID) (map[string]any, error) {
		var detail map[string]any
		err := sys.WithSystem(context.Background(), func(tx pgx.Tx) error {
			var err error
			detail, err = lifecycle.ReportsPurger{}.Purge(context.Background(), lifecycle.Removal{TenantID: id, Tx: tx})
			return err
		})
		return detail, err
	}

	gone, live := newTenant("gone", "removed"), newTenant("live", "active")
	detail, err := purge(gone)
	require.NoError(t, err)
	require.Equal(t, int64(2), detail["reports_deleted"])
	require.Zero(t, count(gone))

	_, err = purge(live)
	require.ErrorContains(t, err, "is not removed")
	require.Equal(t, 2, count(live))

	err = sys.WithSystem(t.Context(), func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `delete from cp.usage_reports where tenant_id = $1`, live)
		return err
	})
	require.ErrorContains(t, err, "permission denied")
}
