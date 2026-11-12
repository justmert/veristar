package natsx_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/db"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
)

type txT = pgx.Tx

// fenceDB starts postgres from the dev image, applies the real platform
// migrations (which seed the fence rows at epoch 0, revision 0) and opens a
// pool as metrics_writer, the role that calls CheckFence in production.
func fenceDB(t *testing.T) *pdb.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	cfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	require.NoError(t, err)
	_, err = pdb.Migrate(ctx, cfg, db.Migrations(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	require.NoError(t, err)
	require.NoError(t, pdb.SetRolePasswords(ctx, rcfg))
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleMetricsWriter)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "natsx-fence-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return d
}

func withTx(ctx context.Context, d *pdb.DB, fn func(pgx.Tx) error) error {
	return d.WithRole(ctx, pdb.RoleMetricsWriter, fn)
}
