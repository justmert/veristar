package auth_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/justmert/stellar-analytics/db"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
)

// one real postgresql server for the package, migrated, with role passwords
var (
	pg   *dbtest.Postgres
	conn *pdb.DB
)

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var err error
	pg, err = dbtest.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = pg.Terminate(context.Background()) }()
	if err := setup(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	defer conn.Close()
	return m.Run()
}

func setup(ctx context.Context) error {
	quiet := slog.New(slog.DiscardHandler)
	cfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	if err != nil {
		return err
	}
	if _, err := pdb.Migrate(ctx, cfg, db.Migrations(), quiet); err != nil {
		return err
	}
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	if err != nil {
		return err
	}
	if err := pdb.SetRolePasswords(ctx, rcfg); err != nil {
		return err
	}
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	if err != nil {
		return err
	}
	conn, err = pdb.Open(ctx, pcfg, "auth-test")
	return err
}
