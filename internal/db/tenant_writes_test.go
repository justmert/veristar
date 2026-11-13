package db_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
)

// tenantStmt runs statements as cp_tenant for tenant A in one transaction
// that is rolled back, and returns the first error. a failed statement ends
// the transaction, so each case runs in its own.
func tenantStmt(t *testing.T, stmts ...string) error {
	t.Helper()
	ctx := context.Background()
	p, err := conn.Pool(pdb.RoleCPTenant)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `select set_config('app.tenant_id', $1, true)`, seed.tenantA.String())
	require.NoError(t, err)
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s, pgx.QueryExecModeSimpleProtocol); err != nil {
			return err
		}
	}
	return nil
}

//: row-level security keeps cp_tenant inside its own
// tenant; these limits keep a handler bug inside what tenant handlers are
// meant to write.
func TestTenantWriteLimits(t *testing.T) {
	site, token := seed.siteA.String(), seed.tokenA.String()

	// what tenant handlers do keeps working
	require.NoError(t, tenantStmt(t, `update cp.sites set name = 'renamed', origins = '{https://a.example}', updated_at = now() where id = '`+site+`'`))
	require.NoError(t, tenantStmt(t, `update cp.sites set status = 'removed', updated_at = now() where id = '`+site+`'`))
	require.NoError(t, tenantStmt(t, `update cp.sites set verify_method = 'dns', verified_at = now(), last_verify_at = now(), last_verify_error = null where id = '`+site+`'`))
	require.NoError(t, tenantStmt(t, `update cp.otlp_tokens set status = 'revoked', revoked_at = now() where id = '`+token+`' and status = 'active'`))
	require.NoError(t, tenantStmt(t, `insert into cp.audit_log (actor, action, target) values ('user:`+uuid.NewString()+`', 'site.update', 'site:`+site+`')`))

	for name, stmts := range map[string][]string{
		"reset the verify token":    {`update cp.sites set verify_token = 'x' where id = '` + site + `'`},
		"change the domain":         {`update cp.sites set domain = 'other.example' where id = '` + site + `'`},
		"activate a site":           {`update cp.sites set status = case when status = 'active' then 'pending' else 'active' end where id = '` + site + `'`},
		"bring back a removed site": {`update cp.sites set status = 'removed' where id = '` + site + `'`, `update cp.sites set status = 'active' where id = '` + site + `'`},
		"change a token hash":       {`update cp.otlp_tokens set hash = sha256('x'::bytea) where id = '` + token + `'`},
		"re-activate a token": {
			`update cp.otlp_tokens set status = 'revoked', revoked_at = now() where id = '` + token + `'`,
			`update cp.otlp_tokens set status = 'active', revoked_at = null where id = '` + token + `'`,
		},
		"clear revoked_at": {
			`update cp.otlp_tokens set status = 'revoked', revoked_at = now() where id = '` + token + `'`,
			`update cp.otlp_tokens set revoked_at = null where id = '` + token + `'`,
		},
		"forge an operator audit row": {`insert into cp.audit_log (actor, action, target) values ('operator:x', 'tenant.approve', 'tenant:x')`},
		"forge a system audit row":    {`insert into cp.audit_log (actor, action, target) values ('system:reconcile', 'provision.team_create', 'team:x')`},
	} {
		requireDenied(t, tenantStmt(t, stmts...), name)
	}

	// cp_system keeps every transition the reconciler and operators need
	sys, err := conn.Pool(pdb.RoleCPSystem)
	require.NoError(t, err)
	tx, err := sys.Begin(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(context.Background(), `update cp.sites set status = 'pending' where id = $1`, seed.siteA)
	require.NoError(t, err)
	_, err = tx.Exec(context.Background(), `update cp.sites set status = 'active' where id = $1`, seed.siteA)
	require.NoError(t, err)
}
