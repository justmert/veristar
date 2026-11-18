package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/db/dbtest"
)

// a restore node runs no umami, so the disaster recovery comparison cannot
// read /stats on its side. --no-stats compares the databases alone, needs
// no UMAMI_URL, and says what it left out; without it the env file must
// name a umami
func TestVerifyMigrationWithoutStats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	require.NoError(t, runDBWith(ctx, []string{"migrate"}, config.FromMap(pg.Env), slog.New(slog.DiscardHandler)))

	u, err := pg.Superuser(ctx, "umami")
	require.NoError(t, err)
	_, err = u.Exec(ctx, `create table _prisma_migrations (migration_name text primary key,
		finished_at timestamptz, rolled_back_at timestamptz);
		insert into _prisma_migrations values ('01_init', now(), null)`)
	require.NoError(t, err)
	_ = u.Close(ctx)

	var b strings.Builder
	for k, v := range pg.Env {
		if strings.HasPrefix(k, "PLATFORM_DB_") {
			b.WriteString(k + "=" + v + "\n")
		}
	}
	b.WriteString("UMAMI_DB_NAME=umami\n")
	env := filepath.Join(t.TempDir(), "side.env")
	require.NoError(t, os.WriteFile(env, []byte(b.String()), 0o600))

	var out bytes.Buffer
	require.NoError(t, runVerifyMigration(ctx, []string{"--source", env, "--target", env, "--no-stats"}, &out), out.String())
	require.Contains(t, out.String(), "not compared: umami /stats sample (--no-stats)")
	require.Contains(t, out.String(), "compared umami-stats 0 keys")
	require.Contains(t, out.String(), "result: source and target are equal")

	out.Reset()
	err = runVerifyMigration(ctx, []string{"--source", env, "--target", env}, &out)
	require.ErrorContains(t, err, "UMAMI_URL")
}
