package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/config"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
)

// runs migrate and roles twice each against a real server, as an operator
// would, then logs in as every role with the configured password
func TestDBMigrateAndRoles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	log := slog.New(slog.DiscardHandler)

	for range 2 {
		require.NoError(t, runDBWith(ctx, []string{"migrate"}, config.FromMap(pg.Env), log))
	}
	for range 2 {
		require.NoError(t, runDBWith(ctx, []string{"roles"}, config.FromMap(pg.Env), log))
	}
	for _, r := range pdb.AppRoles {
		c, err := pgx.Connect(ctx, "host="+pg.Host+" port="+pg.Env["PLATFORM_DB_PORT"]+
			" dbname=platform sslmode=disable user="+string(r)+" password="+pg.Env[r.PasswordKey()])
		require.NoError(t, err, r)
		var user string
		require.NoError(t, c.QueryRow(ctx, `select current_user`).Scan(&user))
		require.Equal(t, string(r), user)
		_ = c.Close(ctx)
	}

	require.ErrorContains(t, runDBWith(ctx, []string{"drop"}, config.FromMap(pg.Env), log), "unknown subcommand")
	require.ErrorContains(t, runDBWith(ctx, nil, config.FromMap(pg.Env), log), "usage")
	err = runDBWith(ctx, []string{"roles"}, config.FromMap(map[string]string{}), log)
	require.ErrorContains(t, err, "PLATFORM_DB_ADMIN_PASSWORD")
	require.ErrorContains(t, err, "PLATFORM_DB_JOBS_PASSWORD")
}

// runs the commands against the dev stack when PLATFORM_DB_IT is set; make
// db-it loads deploy/compose/.env.dev for it
func TestDBDevStack(t *testing.T) {
	if os.Getenv("PLATFORM_DB_IT") == "" {
		t.Skip("set PLATFORM_DB_IT=1 with the dev env loaded (make db-it) to run against the dev stack")
	}
	ctx := context.Background()
	require.NoError(t, runDB(ctx, []string{"migrate"}))
	require.NoError(t, runDB(ctx, []string{"roles"}))
}
