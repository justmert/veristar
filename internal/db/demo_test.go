package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
)

// markDemo makes the seed's tenant A the only demo project for one test
func markDemo(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	c := superuser(t)
	_, err := c.Exec(ctx, `update cp.tenants set demo = (id = $1)`, seed.tenantA)
	require.NoError(t, err)
	t.Cleanup(func() {
		c := superuser(t)
		_, err := c.Exec(context.Background(), `update cp.tenants set demo = false`)
		require.NoError(t, err)
	})
}

// demoRelations lists every relation cp_demo may select and whether it has
// tenant rows, from the catalog, so a relation granted later is checked
// without editing this test
func demoRelations(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	c := superuser(t)
	rows, err := c.Query(ctx, `
		select n.nspname || '.' || c.relname,
		       case when n.nspname = 'cp' and c.relname = 'tenants' then 'id'
		            when exists (select 1 from pg_attribute a where a.attrelid = c.oid and a.attname = 'tenant_id'
		                         and a.attnum > 0 and not a.attisdropped) then 'tenant_id'
		            else '' end
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname in ('cp', 'api', 'agg') and c.relkind in ('r', 'p', 'v')
		  and not c.relispartition
		  and (has_table_privilege('cp_demo', c.oid, 'SELECT')
		       or exists (select 1 from pg_attribute a where a.attrelid = c.oid and a.attnum > 0
		                  and has_column_privilege('cp_demo', c.oid, a.attnum, 'SELECT')))
		order by 1`)
	require.NoError(t, err)
	out := map[string]string{}
	for rows.Next() {
		var name, key string
		require.NoError(t, rows.Scan(&name, &key))
		out[name] = key
	}
	require.NoError(t, rows.Err())
	return out
}

// the operator views of a demo request see the demo project's rows and no
// other tenant's, in every relation cp_demo can read. the seed holds a row
// for both tenants in each of them.
func TestDemoOperatorSeesDemoTenantsOnly(t *testing.T) {
	markDemo(t)
	ctx := pdb.WithDemo(context.Background())
	rels := demoRelations(t)
	require.Contains(t, rels, "cp.tenants")
	require.Contains(t, rels, "cp.operator_sites")
	keyed := 0
	for rel, key := range rels {
		if key == "" {
			continue
		}
		keyed++
		var demo, other int64
		require.NoError(t, conn.WithOperator(ctx, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `select count(*) filter (where `+key+` = $1), count(*) filter (where `+key+` <> $1) from `+rel,
				seed.tenantA).Scan(&demo, &other)
		}), rel)
		require.Zero(t, other, "%s shows another tenant's rows to the demo", rel)
		// the seed writes a demo row into every keyed relation except the
		// ones only the control plane fills; those still answer
		if rel != "cp.tenant_removals" || demo > 0 {
			require.Positive(t, demo, "%s shows no row of the demo project", rel)
		}
	}
	require.Greater(t, keyed, 15)

	// the same reads outside a demo see both tenants, so the filter above
	// is the demo's and not the seed's
	var tenants int64
	require.NoError(t, conn.WithOperator(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `select count(*) from cp.tenants where id = any($1)`,
			[]uuid.UUID{seed.tenantA, seed.tenantB}).Scan(&tenants)
	}))
	require.EqualValues(t, 2, tenants)
}

// the api functions run with their owner's rights, so cp_demo's policies do
// not bind them; they drop non-demo tenants from the list they are given
func TestDemoOperatorFunctions(t *testing.T) {
	markDemo(t)
	both := []uuid.UUID{seed.tenantA, seed.tenantB}
	for _, fn := range []string{"api.operator_daily", "api.operator_hourly"} {
		read := func(ctx context.Context) map[uuid.UUID]bool {
			seen := map[uuid.UUID]bool{}
			require.NoError(t, conn.WithOperator(ctx, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, `select distinct tenant_id from `+fn+`(now() - interval '3 days', now() + interval '1 day', $1)`, both)
				if err != nil {
					return err
				}
				for rows.Next() {
					var id uuid.UUID
					if err := rows.Scan(&id); err != nil {
						return err
					}
					seen[id] = true
				}
				return rows.Err()
			}), fn)
			return seen
		}
		require.Equal(t, map[uuid.UUID]bool{seed.tenantA: true, seed.tenantB: true}, read(context.Background()), fn)
		require.Equal(t, map[uuid.UUID]bool{seed.tenantA: true}, read(pdb.WithDemo(context.Background())), fn)
	}
}

// what the demo never reads: operator settings and keys, the audit log,
// people, sessions, members, tokens. cp_demo has no right on them, so the
// statement fails instead of returning rows
func TestDemoOperatorRefused(t *testing.T) {
	markDemo(t)
	ctx := pdb.WithDemo(context.Background())
	for _, rel := range []string{
		"cp.settings", "cp.operator_api_keys", "cp.api_key_usage_hourly", "cp.audit_log", "cp.users",
		"cp.sessions", "cp.tenant_members", "cp.invites", "cp.sites", "cp.otlp_tokens", "cp.operators",
		"cp.mcp_credentials", "cp.erasure_requests", "cp.scf_awards", "api.metrics_minute",
	} {
		err := conn.WithOperator(ctx, func(tx pgx.Tx) error {
			var n int64
			return tx.QueryRow(ctx, `select count(*) from `+rel).Scan(&n)
		})
		requireDenied(t, err, "cp_demo reading "+rel)
	}
	// the transaction runs as cp_demo from before the handler's first statement
	var role string
	require.NoError(t, conn.WithOperator(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select current_user`).Scan(&role)
	}))
	require.Equal(t, string(pdb.RoleCPDemo), role)
}

// every transaction of a demo request is read only, whatever its role
func TestDemoTransactionsReadOnly(t *testing.T) {
	markDemo(t)
	ctx := pdb.WithDemo(context.Background())
	const readOnly = "25006"
	err := conn.WithSystem(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update cp.tenants set name = name where id = $1`, seed.tenantA)
		return err
	})
	require.Equal(t, readOnly, sqlState(err), "%v", err)
	err = conn.WithTenant(ctx, seed.tenantA, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update cp.tenants set name = name`)
		return err
	})
	require.Equal(t, readOnly, sqlState(err), "%v", err)
	err = conn.WithOperator(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `select 1`)
		return err
	})
	require.NoError(t, err)
}

// a tenant transaction of a demo request opens for a demo project only
func TestDemoTenantTransaction(t *testing.T) {
	markDemo(t)
	ctx := pdb.WithDemo(context.Background())
	var name string
	require.NoError(t, conn.WithTenant(ctx, seed.tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select name from cp.tenants`).Scan(&name)
	}))
	require.Equal(t, "Tenant A", name)
	ran := false
	err := conn.WithTenant(ctx, seed.tenantB, func(pgx.Tx) error {
		ran = true
		return nil
	})
	require.True(t, errors.Is(err, pdb.ErrNotDemo), "%v", err)
	require.False(t, ran, "the handler ran for a tenant that is not a demo")

	// a removed demo project is no demo any more
	c := superuser(t)
	var status string
	require.NoError(t, c.QueryRow(context.Background(), `select status from cp.tenants where id = $1`, seed.tenantA).Scan(&status))
	_, err = c.Exec(context.Background(), `update cp.tenants set status = 'removed' where id = $1`, seed.tenantA)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := superuser(t).Exec(context.Background(), `update cp.tenants set status = $2 where id = $1`, seed.tenantA, status)
		require.NoError(t, err)
	})
	err = conn.WithTenant(ctx, seed.tenantA, func(pgx.Tx) error { return nil })
	require.True(t, errors.Is(err, pdb.ErrNotDemo), "%v", err)
}

// cp_tenant cannot set the flag; only cp_system can (platformctl)
func TestDemoFlagWrites(t *testing.T) {
	_, err := attempt(t, pdb.RoleCPTenant, &seed.tenantA, `update cp.tenants set demo = true`)
	requireDenied(t, err, "cp_tenant setting demo")
	_, err = attempt(t, pdb.RoleCPOperator, nil, `update cp.tenants set demo = true`)
	requireDenied(t, err, "cp_operator setting demo")
	n, err := attempt(t, pdb.RoleCPSystem, nil, `update cp.tenants set demo = true where id = $1`, seed.tenantA)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}
