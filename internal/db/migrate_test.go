package db_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/db"
	"github.com/justmert/veristar/internal/config"
	pdb "github.com/justmert/veristar/internal/db"
)

func migrationCount(t *testing.T) int64 {
	t.Helper()
	files, err := fs.Glob(db.Migrations(), "*.sql")
	require.NoError(t, err)
	return int64(len(files))
}

func TestMigrateCleanDatabase(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pg.CreateDatabase(ctx, "clean"))
	cfg, err := pdb.LoadAdminConfig(pg.Config("clean"))
	require.NoError(t, err)

	res, err := pdb.Migrate(ctx, cfg, db.Migrations(), quietLog())
	require.NoError(t, err)
	n := migrationCount(t)
	require.Len(t, res.Applied, int(n))
	require.Equal(t, n, res.Version)

	res, err = pdb.Migrate(ctx, cfg, db.Migrations(), quietLog())
	require.NoError(t, err, "a second run is a no-op")
	require.Empty(t, res.Applied)
	require.Equal(t, n, res.Version)

	c, err := pg.Superuser(ctx, "clean")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	var notOwned []string
	require.NoError(t, c.QueryRow(ctx, `
		select coalesce(array_agg(x), '{}') from (
		  select n.nspname || '.' || c.relname as x from pg_class c join pg_namespace n on n.oid = c.relnamespace
		  where (n.nspname in ('cp', 'api', 'agg') or c.relname = 'goose_db_version') and pg_get_userbyid(c.relowner) <> 'platform_owner'
		  union all
		  select p.proname from pg_proc p join pg_namespace n on n.oid = p.pronamespace
		  where n.nspname in ('cp', 'api', 'agg') and pg_get_userbyid(p.proowner) <> 'platform_owner'
		  union all
		  select nspname from pg_namespace where nspname in ('cp', 'api', 'agg') and pg_get_userbyid(nspowner) <> 'platform_owner'
		  union all
		  select datname from pg_database where datname = current_database() and pg_get_userbyid(datdba) <> 'platform_owner'
		) s`).Scan(&notOwned))
	require.Empty(t, notOwned, "every object is owned by platform_owner")

	var settings int
	require.NoError(t, c.QueryRow(ctx, `select count(*) from cp.settings where (key, value) in
		(('enrollment_mode', '"open"'::jsonb), ('removal_keep_aggregates', 'false'::jsonb))`).Scan(&settings))
	require.Equal(t, 2, settings)

	// down sections work, and the database migrates up again afterwards
	require.NoError(t, pdb.DownAll(ctx, cfg, db.Migrations()))
	var schemas int
	require.NoError(t, c.QueryRow(ctx, `select count(*) from pg_namespace where nspname in ('cp', 'api', 'agg')`).Scan(&schemas))
	require.Zero(t, schemas)
	res, err = pdb.Migrate(ctx, cfg, db.Migrations(), quietLog())
	require.NoError(t, err)
	require.Equal(t, n, res.Version)
}

func TestPolicyCoverage(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)
	v, err := pdb.CheckPolicyCoverage(ctx, c)
	require.NoError(t, err)
	require.Empty(t, v, "the platform database passes the coverage check")

	// a test migration that adds a tenant table without protection makes
	// migrate fail and names the table
	require.NoError(t, pg.CreateDatabase(ctx, "coverage"))
	cfg, err := pdb.LoadAdminConfig(pg.Config("coverage"))
	require.NoError(t, err)
	fsys := fstest.MapFS{}
	require.NoError(t, fs.WalkDir(db.Migrations(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(db.Migrations(), path)
		fsys[path] = &fstest.MapFile{Data: b}
		return err
	}))
	fsys["00999_unprotected.sql"] = &fstest.MapFile{Data: []byte(
		"-- +goose Up\ncreate table cp.unprotected (id int primary key, tenant_id uuid not null);\n" +
			"-- +goose Down\ndrop table cp.unprotected;\n")}
	_, err = pdb.Migrate(ctx, cfg, fsys, quietLog())
	require.ErrorContains(t, err, "cp.unprotected: row-level security is not enabled")
	require.ErrorContains(t, err, "cp.unprotected: row-level security is not forced")
	require.ErrorContains(t, err, "cp.unprotected: has 0 policies for cp_tenant, want exactly 1")

	cc, err := pg.Superuser(ctx, "coverage")
	require.NoError(t, err)
	defer func() { _ = cc.Close(ctx) }()
	steps := []struct {
		sql  string
		want []string
	}{
		{`alter table cp.unprotected enable row level security`,
			[]string{"row-level security is not forced", "has 0 policies"}},
		{`alter table cp.unprotected force row level security`, []string{"has 0 policies"}},
		{`create policy p_wrong on cp.unprotected for select to cp_tenant using (true)`,
			[]string{"must be a permissive policy for all commands", "must use and check (tenant_id = cp.current_tenant_id())"}},
		{`drop policy p_wrong on cp.unprotected;
		  create policy p_tenant on cp.unprotected for all to cp_tenant
		    using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id())`, nil},
		{`create policy p_public on cp.unprotected for select using (true)`, []string{"policy p_public applies to public"}},
	}
	for _, s := range steps {
		_, err := cc.Exec(ctx, s.sql)
		require.NoError(t, err, s.sql)
		v, err := pdb.CheckPolicyCoverage(ctx, cc)
		require.NoError(t, err)
		var got []string
		for _, x := range v {
			require.Equal(t, "cp.unprotected", x.Table)
			got = append(got, x.Problem)
		}
		require.Len(t, got, len(s.want), "%s: %v", s.sql, got)
		for i, w := range s.want {
			require.Contains(t, got[i], w)
		}
	}
}

func TestOpenRefusesPrivilegedLogin(t *testing.T) {
	ctx := context.Background()
	env := config.FromMap(pg.EnvFor("platform"))
	cfg, err := pdb.LoadAdminConfig(env)
	require.NoError(t, err)
	// the admin login handed to a service pool is refused, because it could
	// skip row-level security
	_, err = pdb.Open(ctx, pdb.PoolConfig{Server: cfg.Server, Roles: []pdb.Login{cfg.Admin}}, "db-test")
	require.ErrorContains(t, err, "want plain role")

	bad := pg.EnvFor("platform")
	bad[pdb.RoleCPTenant.PasswordKey()] = "0123456789abcdef0123456789abcdef-wrong"
	pcfg, err := pdb.LoadPoolConfig(config.FromMap(bad), pdb.RoleCPTenant)
	require.NoError(t, err)
	_, err = pdb.Open(ctx, pcfg, "db-test")
	require.ErrorContains(t, err, "connect as role cp_tenant")

	_, err = pdb.LoadPoolConfig(config.FromMap(map[string]string{}), pdb.RoleCPTenant)
	require.ErrorContains(t, err, "PLATFORM_DB_CP_TENANT_PASSWORD")
	require.ErrorContains(t, err, "PLATFORM_DB_HOST")
}

// the down steps of 00031, 00034 and 00037 refuse while rows exist that the
// older schema cannot hold. they run as platform_owner, which cp.users'
// forced row-level security gives no policy, so each check lifts the force
// first; without that every exists() saw zero rows and the down step went
// on, dropping a linked passkey or failing later on a constraint.
func TestDownGuardsSeeRows(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, pg.CreateDatabase(ctx, "downguards"))
	cfg, err := pdb.LoadAdminConfig(pg.Config("downguards"))
	require.NoError(t, err)
	_, err = pdb.Migrate(ctx, cfg, db.Migrations(), quietLog())
	require.NoError(t, err)
	c, err := pg.Superuser(ctx, "downguards")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	exec := func(sql string) {
		t.Helper()
		_, err := c.Exec(ctx, sql)
		require.NoError(t, err)
	}
	version := func() int64 {
		t.Helper()
		var v int64
		require.NoError(t, c.QueryRow(ctx, `select max(version_id) from goose_db_version where is_applied`).Scan(&v))
		return v
	}
	passkey := `'C` + strings.Repeat("A", 55) + `', 'cred-1', decode('04' || repeat('00', 64), 'hex')`

	// a github account with a linked passkey: 00037's down would drop it
	exec(`insert into cp.users (github_id, github_login, passkey_address, passkey_credential_id, passkey_public_key)
	      values (9001, 'linked', ` + passkey + `)`)
	err = pdb.DownTo(ctx, cfg, db.Migrations(), 36)
	require.ErrorContains(t, err, "passkey identities")
	require.Equal(t, int64(37), version(), "the down step of 00037 stopped and rolled back")
	var n int
	require.NoError(t, c.QueryRow(ctx, `select count(*) from cp.users where passkey_address is not null`).Scan(&n))
	require.Equal(t, 1, n, "the passkey is still there")
	exec(`delete from cp.users`)

	// a wallet-only account: 00034's down cannot express it
	exec(`insert into cp.users (stellar_address) values ('G` + strings.Repeat("A", 55) + `')`)
	err = pdb.DownTo(ctx, cfg, db.Migrations(), 33)
	require.ErrorContains(t, err, "wallet-only accounts")
	require.Equal(t, int64(34), version())
	exec(`delete from cp.users`)

	// a deleted account: 00031's down cannot restore it
	exec(`insert into cp.users (github_id, github_login, deleted_at) values (null, null, now())`)
	err = pdb.DownTo(ctx, cfg, db.Migrations(), 30)
	require.ErrorContains(t, err, "deleted accounts")
	require.Equal(t, int64(31), version())
}
