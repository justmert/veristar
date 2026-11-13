package db_test

import (
	"context"
	"math/rand/v2"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
)

// tenantStmt runs statements as cp_tenant for tenant A with its owner as the
// session user, in one transaction that is rolled back, and returns the
// first error. a failed statement ends the transaction, so each case runs
// in its own.
func tenantStmt(t *testing.T, stmts ...string) error {
	t.Helper()
	return tenantStmtAs(t, seed.userA, stmts...)
}

// tenantStmtAs is tenantStmt with another session user (app.user_id, set
// by tenants.Scope.InTenant); uuid.Nil leaves it unset.
func tenantStmtAs(t *testing.T, user uuid.UUID, stmts ...string) error {
	t.Helper()
	ctx := context.Background()
	p, err := conn.Pool(pdb.RoleCPTenant)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `select set_config('app.tenant_id', $1, true)`, seed.tenantA.String())
	require.NoError(t, err)
	if user != uuid.Nil {
		_, err = tx.Exec(ctx, `select set_config('app.user_id', $1, true)`, user.String())
		require.NoError(t, err)
	}
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
	require.NoError(t, tenantStmt(t, `update cp.otlp_tokens set status = 'revoked', revoked_at = now() where id = '`+token+`' and status = 'active'`))
	require.NoError(t, tenantStmt(t, `insert into cp.audit_log (actor, action, target) values ('user:`+seed.userA.String()+`', 'site.update', 'site:`+site+`')`))

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
		// second resolution (migration 00028)
		"self-verify a domain":            {`update cp.sites set verify_method = 'dns', verified_at = now() where id = '` + site + `'`},
		"clear a verify error":            {`update cp.sites set last_verify_at = now(), last_verify_error = null where id = '` + site + `'`},
		"forge a stellar.toml":            {`update cp.sites set stellar_toml = '{"status":"found"}', stellar_toml_checked_at = now() where id = '` + site + `'`},
		"audit as another user":           {`insert into cp.audit_log (actor, action, target) values ('user:` + uuid.NewString() + `', 'site.update', 'site:` + site + `')`},
		"move a member to another tenant": {`update cp.tenant_members set tenant_id = tenant_id where user_id = '` + seed.userA.String() + `'`},
	} {
		requireDenied(t, tenantStmt(t, stmts...), name)
	}

	// without a session user no audit row and no member change is accepted
	requireDenied(t, tenantStmtAs(t, uuid.Nil, `insert into cp.audit_log (actor, action, target) values ('user:`+seed.userA.String()+`', 'site.update', 'site:`+site+`')`), "audit without a session user")

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

//, second resolution: member rows change only through an
// owner of the tenant, except that a member may remove itself.
func TestTenantMemberWritesNeedAnOwner(t *testing.T) {
	ctx := context.Background()
	member, outsider := uuid.New(), uuid.New()
	require.NoError(t, conn.WithSystem(ctx, func(tx pgx.Tx) error {
		for _, u := range []uuid.UUID{member, outsider} {
			if _, err := tx.Exec(ctx, `insert into cp.users (id, github_id, github_login) values ($1, $2, $3)`,
				u, rand.Int64N(1<<40)+1, "c6-"+u.String()[:8]); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, seed.tenantA, member)
		return err
	}))
	t.Cleanup(func() {
		_ = conn.WithSystem(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `delete from cp.tenant_members where user_id = any($1)`, []uuid.UUID{member, outsider})
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `delete from cp.users where id = any($1)`, []uuid.UUID{member, outsider})
			return err
		})
	})
	m, o := member.String(), outsider.String()

	// the operator manages members
	require.NoError(t, tenantStmt(t, `update cp.tenant_members set role = 'owner' where user_id = '`+m+`'`))
	require.NoError(t, tenantStmt(t, `insert into cp.tenant_members (tenant_id, user_id, role) values ('`+seed.tenantA.String()+`', '`+o+`', 'member')`))
	require.NoError(t, tenantStmt(t, `delete from cp.tenant_members where user_id = '`+m+`'`))
	// a member removes itself
	require.NoError(t, tenantStmtAs(t, member, `delete from cp.tenant_members where user_id = '`+m+`'`))

	for name, c := range map[string]struct {
		as   uuid.UUID
		stmt string
	}{
		"a member promotes itself":    {member, `update cp.tenant_members set role = 'owner' where user_id = '` + m + `'`},
		"a member demotes the operator":  {member, `update cp.tenant_members set role = 'member' where user_id = '` + seed.userA.String() + `'`},
		"a member adds someone":       {member, `insert into cp.tenant_members (tenant_id, user_id, role) values ('` + seed.tenantA.String() + `', '` + o + `', 'owner')`},
		"a member removes the operator":  {member, `delete from cp.tenant_members where user_id = '` + seed.userA.String() + `'`},
		"no session user":             {uuid.Nil, `update cp.tenant_members set role = 'member' where user_id = '` + m + `'`},
		"an outsider as session user": {outsider, `delete from cp.tenant_members where user_id = '` + m + `'`},
	} {
		requireDenied(t, tenantStmtAs(t, c.as, c.stmt), name)
	}
}
