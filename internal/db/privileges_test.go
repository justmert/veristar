package db_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/tests/fixtures/rolematrix"
)

// the expected rights matrix lives in tests/fixtures/rolematrix, shared with
// the behavioural role gate in tests/isolation/dbroles, so both test the same
// matrix
var wantGrants = rolematrix.Want

var tablePrivs = func() []struct{ letter, name string } {
	out := make([]struct{ letter, name string }, 0, len(rolematrix.Rights))
	for _, r := range rolematrix.Rights {
		out = append(out, struct{ letter, name string }{r.Letter, r.Name})
	}
	return out
}()

func superuser(t *testing.T) *pgx.Conn {
	t.Helper()
	c, err := pg.Superuser(context.Background(), "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestGrantMatrix(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)

	rows, err := c.Query(ctx, `
		select c.oid, n.nspname || '.' || c.relname
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname in ('cp', 'api', 'agg') and c.relkind in ('r', 'p', 'v', 'm', 'f')
		  and not c.relispartition
		order by 2`)
	require.NoError(t, err)
	type rel struct {
		oid  uint32
		name string
	}
	rels, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rel, error) {
		var x rel
		return x, r.Scan(&x.oid, &x.name)
	})
	require.NoError(t, err)

	got := map[string]map[pdb.Role]string{}
	for _, r := range rels {
		got[r.name] = map[pdb.Role]string{}
		for _, role := range pdb.AppRoles {
			var table strings.Builder
			var cols []string
			for _, p := range tablePrivs {
				var has bool
				require.NoError(t, c.QueryRow(ctx, `select has_table_privilege($1, $2::oid, $3)`, string(role), r.oid, p.name).Scan(&has))
				if has {
					table.WriteString(p.letter)
					continue
				}
				if p.letter == "d" || p.letter == "D" || p.letter == "t" {
					continue
				}
				var names []string
				require.NoError(t, c.QueryRow(ctx, `
					select coalesce(array_agg(a.attname::text order by a.attname), '{}')
					from pg_attribute a
					where a.attrelid = $2::oid and a.attnum > 0 and not a.attisdropped
					  and has_column_privilege($1, $2::oid, a.attnum, $3)`,
					string(role), r.oid, p.name).Scan(&names))
				if len(names) > 0 {
					cols = append(cols, p.letter+"("+strings.Join(names, ",")+")")
				}
			}
			s := strings.TrimSpace(table.String() + " " + strings.Join(cols, " "))
			if s != "" {
				got[r.name][role] = s
			}
		}
	}
	require.Equal(t, wantGrants, got)
}

func TestSchemaDatabaseAndFunctionRights(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)

	wantUsage := map[string][]pdb.Role{
		"cp": pdb.AppRoles,
		// cp_system only to call api.purge_tenant (00017); it has no table right
		"api": pdb.AppRoles,
		// cp_system only to call agg.purge_tenant (00019); cp_tenant reads
		// its own chain and web rows (00032)
		"agg":    {pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator, pdb.RoleJobs},
		"public": pdb.AppRoles,
	}
	for schema, roles := range wantUsage {
		for _, role := range pdb.AppRoles {
			var usage, create bool
			require.NoError(t, c.QueryRow(ctx,
				`select has_schema_privilege($1, $2, 'USAGE'), has_schema_privilege($1, $2, 'CREATE')`,
				string(role), schema).Scan(&usage, &create))
			require.Equal(t, contains(roles, role), usage, "usage on %s for %s", schema, role)
			require.False(t, create, "create on %s for %s", schema, role)
		}
	}

	for _, role := range pdb.AppRoles {
		var connect, temp bool
		require.NoError(t, c.QueryRow(ctx,
			`select has_database_privilege($1, current_database(), 'CONNECT'), has_database_privilege($1, current_database(), 'TEMP')`,
			string(role)).Scan(&connect, &temp))
		require.True(t, connect, "connect for %s", role)
		require.False(t, temp, "temp for %s", role)
	}

	rows, err := c.Query(ctx, `
		select p.oid, p.oid::regprocedure::text
		from pg_proc p join pg_namespace n on n.oid = p.pronamespace
		where n.nspname in ('cp', 'api', 'agg') order by 2`)
	require.NoError(t, err)
	type fn struct {
		oid  uint32
		name string
	}
	fns, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (fn, error) {
		var f fn
		return f, r.Scan(&f.oid, &f.name)
	})
	require.NoError(t, err)
	wantExec := map[string][]pdb.Role{
		"cp.current_tenant_id()":      {pdb.RoleCPTenant},
		"cp.audit_log_append_only()":  nil,
		"cp.leader_fence_monotonic()": nil,
		// cp_tenant write limits (00024)
		"cp.tenant_site_status_guard()":  nil,
		"cp.tenant_token_status_guard()": nil,
		"cp.tenant_audit_actor_guard()":  nil,
		// the session user of a tenant transaction and the member guard
		// (00028)
		"cp.app_user_id()":         {pdb.RoleCPTenant},
		"cp.tenant_member_guard()": nil,
		// mcp credentials (00030): the cp_tenant guard and the revocation
		// triggers; no role calls them directly
		"cp.tenant_mcp_credential_guard()":            nil,
		"cp.mcp_revoke_matching(uuid,uuid,text,text)": nil,
		"cp.mcp_revoke_member()":                      nil,
		"cp.mcp_revoke_tenant()":                      nil,
		"cp.mcp_revoke_operator()":                    nil,
		// the operator roster (00041): cp_system's triggers on cp.users and
		// cp.operators call roster_active; the trigger functions themselves
		// are called by no role
		"cp.roster_active(bigint,text,text)": {pdb.RoleCPSystem},
		"cp.users_operator_flag()":           nil,
		"cp.operators_sync_flags()":          nil,
		"cp.operators_last_admin()":          nil,

		"api.add_buckets(bigint[],bigint[])": {pdb.RoleCPTenant, pdb.RoleCPOperator, pdb.RoleMetricsWriter, pdb.RoleJobs},
		"api.sum_buckets(bigint[])":          {pdb.RoleCPTenant, pdb.RoleCPOperator, pdb.RoleJobs},
		"api.histogram_quantile(double precision,bigint[])": {
			pdb.RoleCPTenant, pdb.RoleCPOperator, pdb.RoleJobs,
		},
		"api.protect_partition(text)":       nil,
		"api.create_minute_partition(date)": {pdb.RoleJobs},
		"api.create_hourly_partition(date)": {pdb.RoleJobs},
		"api.drop_minute_partition(date)":   {pdb.RoleJobs},
		"api.drop_hourly_partition(date)":   {pdb.RoleJobs},
		"api.minute_partitions()":           {pdb.RoleJobs},
		"api.hourly_partitions()":           {pdb.RoleJobs},
		"api.purge_tenant(uuid,boolean)":    {pdb.RoleCPSystem},
		// eviction of idle writer streams by api-retention (00029)
		"api.evict_idle_series_states(timestamp with time zone,bigint)": {pdb.RoleJobs},
		// the hour lock key the writer and the rollup share (00026)
		"api.rollup_lock_key(uuid,timestamp with time zone)": {pdb.RoleMetricsWriter, pdb.RoleJobs},
		// hour and day grain only; cp_operator never reads minute rows (00022),
		// and only of the tenants it names (00023)
		"api.operator_hourly(timestamp with time zone,timestamp with time zone,uuid[])": {pdb.RoleCPOperator},
		"api.operator_daily(timestamp with time zone,timestamp with time zone,uuid[])":  {pdb.RoleCPOperator},
		"agg.purge_tenant(uuid)": {pdb.RoleCPSystem},
		// the reconciler's way to the integrity baselines (00033)
		"agg.site_baselines(uuid)": {pdb.RoleCPSystem},
		// the chain purge (00032); the enrollment trigger runs as its owner
		"agg.purge_chain(uuid,boolean)":  {pdb.RoleCPSystem},
		"cp.chain_ids_from_enrollment()": nil,
		// cohort proposals and usage reports (00035, 00036): two guards, and
		// the report purge of a removed tenant
		"cp.tenant_cohort_proposal_guard()": nil,
		"cp.usage_report_guard()":           nil,
		"cp.purge_usage_reports(uuid)":      {pdb.RoleCPSystem},
	}
	var names []string
	for _, f := range fns {
		names = append(names, f.name)
		for _, role := range pdb.AppRoles {
			var exec bool
			require.NoError(t, c.QueryRow(ctx, `select has_function_privilege($1, $2::oid, 'EXECUTE')`, string(role), f.oid).Scan(&exec))
			require.Equal(t, contains(wantExec[f.name], role), exec, "execute %s for %s", f.name, role)
		}
	}
	want := make([]string, 0, len(wantExec))
	for k := range wantExec {
		want = append(want, k)
	}
	sort.Strings(want)
	require.Equal(t, want, names)
}

// every partition of the api metric tables is reachable only through its
// parent: no application role holds any right on it, and the security
// definer functions that create partitions keep it that way
func TestPartitionRights(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)

	// one more of each, created at runtime the way the partition job does
	require.NoError(t, conn.WithRole(ctx, pdb.RoleJobs, func(tx pgxTx) error {
		var a, b bool
		if err := tx.QueryRow(ctx, `select api.create_minute_partition('2031-01-02'), api.create_hourly_partition('2031-01-01')`).Scan(&a, &b); err != nil {
			return err
		}
		require.True(t, a && b)
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, conn.WithRole(ctx, pdb.RoleJobs, func(tx pgxTx) error {
			_, err := tx.Exec(ctx, `select api.drop_minute_partition('2031-01-02'), api.drop_hourly_partition('2031-01-01')`)
			return err
		}))
	})

	rows, err := c.Query(ctx, `
		select c.oid, c.relname, p.relname, pg_get_userbyid(c.relowner)
		from pg_inherits i
		join pg_class c on c.oid = i.inhrelid
		join pg_class p on p.oid = i.inhparent
		join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = 'api' and c.relkind = 'r'
		order by 2`)
	require.NoError(t, err)
	type part struct {
		oid                 uint32
		name, parent, owner string
	}
	parts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (part, error) {
		var p part
		return p, r.Scan(&p.oid, &p.name, &p.parent, &p.owner)
	})
	require.NoError(t, err)

	perParent := map[string]int{}
	for _, p := range parts {
		perParent[p.parent]++
		require.Equal(t, pdb.OwnerRole, p.owner, p.name)
		for _, role := range append([]pdb.Role{"public"}, pdb.AppRoles...) {
			for _, priv := range tablePrivs {
				var has bool
				require.NoError(t, c.QueryRow(ctx, `select has_table_privilege($1, $2::oid, $3)`, string(role), p.oid, priv.name).Scan(&has))
				require.False(t, has, "%s holds %s on partition %s", role, priv.name, p.name)
			}
		}
		_, err := count(t, pdb.RoleCPTenant, &seed.tenantA, `select count(*) from api.`+p.name)
		requireDenied(t, err, "cp_tenant reading partition "+p.name+" directly")
	}
	// the migration's window (00016), plus the one of each added above
	today := time.Now().UTC().Truncate(24 * time.Hour)
	first := time.Date(today.AddDate(0, 0, -31).Year(), today.AddDate(0, 0, -31).Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(today.Year(), today.Month()+3, 1, 0, 0, 0, 0, time.UTC)
	months := 0
	for m := first; !m.After(last); m = m.AddDate(0, 1, 0) {
		months++
	}
	require.Equal(t, map[string]int{"metrics_minute": 15 + 1, "metrics_hourly": months + 1}, perParent)
}

func TestRoleAttributes(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)
	for _, role := range pdb.AppRoles {
		var super, bypass, createrole, createdb, login bool
		require.NoError(t, c.QueryRow(ctx,
			`select rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolcanlogin from pg_roles where rolname = $1`,
			string(role)).Scan(&super, &bypass, &createrole, &createdb, &login))
		require.False(t, super || bypass || createrole || createdb, "%s must be a plain role", role)
		require.True(t, login, "%s can log in after db roles", role)
	}
	var super, bypass, login bool
	require.NoError(t, c.QueryRow(ctx,
		`select rolsuper, rolbypassrls, rolcanlogin from pg_roles where rolname = $1`, pdb.OwnerRole).Scan(&super, &bypass, &login))
	require.False(t, super || bypass || login, "platform_owner must not log in or skip row-level security")
}

// tables later phases add to api and agg get their rights from default
// privileges; this checks those defaults on throwaway tables
func TestDefaultPrivilegesForLaterTables(t *testing.T) {
	ctx := context.Background()
	c := superuser(t)
	tx, err := c.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `set local role platform_owner`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `create table api.metrics_minute_probe (v int); create table agg.web_daily_probe (v int)`)
	require.NoError(t, err)

	want := map[string]map[pdb.Role]string{
		"api.metrics_minute_probe": {pdb.RoleMetricsWriter: "raw", pdb.RoleJobs: "rawd"},
		"agg.web_daily_probe":      {pdb.RoleCPOperator: "r", pdb.RoleJobs: "rawd"},
	}
	for table, roles := range want {
		for _, role := range pdb.AppRoles {
			var s strings.Builder
			for _, p := range tablePrivs {
				var has bool
				require.NoError(t, tx.QueryRow(ctx, `select has_table_privilege($1, $2, $3)`, string(role), table, p.name).Scan(&has))
				if has {
					s.WriteString(p.letter)
				}
			}
			require.Equal(t, roles[role], s.String(), "%s for %s", table, role)
		}
	}
}

func contains(roles []pdb.Role, r pdb.Role) bool {
	for _, x := range roles {
		if x == r {
			return true
		}
	}
	return false
}
