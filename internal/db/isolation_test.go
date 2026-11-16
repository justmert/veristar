package db_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/tenantq"
)

type tenantTable struct {
	name string
	key  string
	// insert writes one valid row for tenant; only row-level security or a
	// missing grant can refuse it. empty when cp_tenant has no insert right.
	insert func(tenant, user, site uuid.UUID) (string, []any)
	// denied: cp_tenant has no right on the table at all; row-level
	// security is still forced, so a later grant cannot leak other rows
	denied bool
}

var tenantTables = []tenantTable{
	{name: "cp.tenants", key: "id"},
	{name: "cp.tenant_members", key: "tenant_id", insert: func(tenant, _, _ uuid.UUID) (string, []any) {
		// the other tenant's owner, not yet a member of tenant
		other := seed.userA
		if tenant == seed.tenantA {
			other = seed.userB
		}
		return `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, []any{tenant, other}
	}},
	{name: "cp.invites", key: "tenant_id", insert: func(tenant, user, _ uuid.UUID) (string, []any) {
		return `insert into cp.invites (tenant_id, github_id, github_login, role, created_by, expires_at)
		        values ($1, 4242, 'newcomer', 'member', $2, now() + interval '1 day')`, []any{tenant, user}
	}},
	{name: "cp.sites", key: "tenant_id", insert: func(tenant, _, _ uuid.UUID) (string, []any) {
		return `insert into cp.sites (id, tenant_id, name, domain, verify_token)
		        values ($1, $2, 'new', 'new.example.org', 'v')`, []any{uuid.New(), tenant}
	}},
	{name: "cp.otlp_tokens", key: "tenant_id", insert: func(tenant, user, _ uuid.UUID) (string, []any) {
		return `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by)
		        values ($1, 'new', 'sa_otlp_n', sha256($2::bytea), $3)`, []any{tenant, []byte(uuid.NewString()), user}
	}},
	{name: "cp.erasure_requests", key: "tenant_id", insert: func(tenant, user, site uuid.UUID) (string, []any) {
		// a finished request: one site may hold any number of those, but only
		// one running request (00020 erasure_requests_one_active_idx)
		return `insert into cp.erasure_requests (tenant_id, site_id, kind, requested_by, status, outcome, completed_at)
		        values ($1, $2, 'site_reset', $3, 'done', 'erased', now())`, []any{tenant, site, user}
	}},
	{name: "cp.web_ingest_hourly", key: "tenant_id"},
	{name: "cp.tenant_removals", key: "tenant_id", denied: true},
	// mcp access keys (00030): a member creates keys that act as itself
	{name: "cp.mcp_credentials", key: "tenant_id", insert: func(tenant, user, _ uuid.UUID) (string, []any) {
		return `insert into cp.mcp_credentials (kind, scope, tenant_id, user_id, name, prefix, hash, resource, expires_at)
		        values ('key', 'tenant', $1, $2, 'new', 'sa_mcp_n', sha256($3::bytea), 'https://app.example.org/mcp',
		                now() + interval '1 day')`, []any{tenant, user, []byte(uuid.NewString())}
	}},
	{name: "cp.mcp_oauth_codes", key: "tenant_id", denied: true},

	// api: cp_tenant reads its metrics and warnings, and has no
	// right at all on the writer's bookkeeping
	{name: "api.metrics_minute", key: "tenant_id"},
	{name: "api.metrics_hourly", key: "tenant_id"},
	{name: "api.metrics_daily", key: "tenant_id"},
	{name: "api.ingest_warnings", key: "tenant_id"},
	{name: "api.series_state", key: "tenant_id", denied: true},
	{name: "api.resource_seen", key: "tenant_id", denied: true},
	{name: "api.ingest_dedup", key: "tenant_id", denied: true},
	{name: "api.route_registry", key: "tenant_id", denied: true},
	// read-only since 00021: hourly and daily answers read pending hours
	{name: "api.rollup_pending", key: "tenant_id"},
	// the on-chain footprint (00032): a tenant adds its own ids
	{name: "cp.chain_ids", key: "tenant_id", insert: func(tenant, _, _ uuid.UUID) (string, []any) {
		return `insert into cp.chain_ids (tenant_id, strkey, kind, added_by) values ($1, $2, 'contract', 'console')`,
			[]any{tenant, randomContract()}
	}},
	// agg: cp_tenant reads its own web and chain rows (00032) and writes
	// none; the dimensions stay operators only
	{name: "agg.web_daily", key: "tenant_id"},
	{name: "agg.web_daily_dim", key: "tenant_id", denied: true},
	{name: "agg.chain_daily", key: "tenant_id"},
	{name: "agg.chain_reads", key: "tenant_id"},
	// integrity (00033): a project reads its own grades; the evidence and
	// network rows behind them are jobs' only
	{name: "agg.web_integrity_daily", key: "tenant_id"},
	{name: "cp.web_evidence_daily", key: "tenant_id", denied: true},
	{name: "cp.web_networks_daily", key: "tenant_id", denied: true},
}

// randomContract is a string of the strkey shape cp.chain_ids checks
func randomContract() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	b := []byte("C")
	a, c := uuid.New(), uuid.New()
	for _, x := range append(append(append(append([]byte{}, a[:]...), c[:]...), a[:]...), c[:]...)[:55] {
		b = append(b, alphabet[int(x)%len(alphabet)])
	}
	return string(b)
}

// the isolation tests only mean something if they cover every tenant table
// the catalog knows about
func TestTenantTablesMatchCatalog(t *testing.T) {
	ctx := context.Background()
	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(ctx) }()
	rows, err := c.Query(ctx, `
		select n.nspname || '.' || c.relname
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname in ('cp', 'api', 'agg') and c.relkind in ('r', 'p') and not c.relispartition
		  and (exists (select 1 from pg_attribute a where a.attrelid = c.oid and a.attname = 'tenant_id' and not a.attisdropped)
		       or (n.nspname = 'cp' and c.relname = 'tenants'))
		order by 1`)
	require.NoError(t, err)
	var got []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		got = append(got, s)
	}
	require.NoError(t, rows.Err())
	var want []string
	for _, tt := range tenantTables {
		want = append(want, tt.name)
	}
	require.ElementsMatch(t, want, got)
}

func TestTenantIsolation(t *testing.T) {
	type side struct{ tenant, user, site uuid.UUID }
	a := side{seed.tenantA, seed.userA, seed.siteA}
	b := side{seed.tenantB, seed.userB, seed.siteB}

	for _, pair := range [][2]side{{a, b}, {b, a}} {
		self, other := pair[0], pair[1]
		for _, tt := range tenantTables {
			t.Run(fmt.Sprintf("%s/as_%s", tt.name, self.tenant), func(t *testing.T) {
				// the true counts, read past row-level security
				c := superuser(t)
				var own, all int64
				require.NoError(t, c.QueryRow(context.Background(),
					`select count(*) from `+tt.name+` where `+tt.key+` = $1`, self.tenant).Scan(&own))
				require.NoError(t, c.QueryRow(context.Background(), `select count(*) from `+tt.name).Scan(&all))
				require.Positive(t, own, "seed must give every tenant table rows for each tenant")
				require.Greater(t, all, own, "seed must hold rows of the other tenant too")

				if tt.denied {
					_, err := count(t, pdb.RoleCPTenant, &self.tenant, `select count(*) from `+tt.name)
					requireDenied(t, err, "cp_tenant reading "+tt.name)
					_, err = attempt(t, pdb.RoleCPTenant, &self.tenant, `delete from `+tt.name)
					requireDenied(t, err, "cp_tenant deleting from "+tt.name)
					return
				}
				seen, err := count(t, pdb.RoleCPTenant, &self.tenant, `select count(*) from `+tt.name)
				require.NoError(t, err)
				require.Equal(t, own, seen, "cp_tenant sees exactly its own rows")

				seen, err = count(t, pdb.RoleCPTenant, &self.tenant, `select count(*) from `+tt.name+` where `+tt.key+` = $1`, other.tenant)
				require.NoError(t, err)
				require.Zero(t, seen, "cp_tenant must not see the other tenant's rows")

				n, err := attempt(t, pdb.RoleCPTenant, &self.tenant,
					`update `+tt.name+` set `+updateColumn(tt)+` = `+updateColumn(tt)+` where `+tt.key+` = $1`, other.tenant)
				if err != nil {
					requireDenied(t, err, "update of the other tenant's rows")
				} else {
					require.Zero(t, n, "update of the other tenant's rows")
				}

				n, err = attempt(t, pdb.RoleCPTenant, &self.tenant, `delete from `+tt.name+` where `+tt.key+` = $1`, other.tenant)
				if err != nil {
					requireDenied(t, err, "delete of the other tenant's rows")
				} else {
					require.Zero(t, n, "delete of the other tenant's rows")
				}

				// moving an own row into the other tenant must fail the check
				_, err = attempt(t, pdb.RoleCPTenant, &self.tenant,
					`update `+tt.name+` set `+tt.key+` = $1 where `+tt.key+` = $2`, other.tenant, self.tenant)
				requireDenied(t, err, "moving a row to the other tenant")

				if tt.insert == nil {
					_, err := attempt(t, pdb.RoleCPTenant, &self.tenant, `insert into `+tt.name+` default values`)
					requireDenied(t, err, "insert without an insert grant")
					return
				}
				// the same row is accepted for the own tenant, so the refusal
				// below comes from row-level security and not from the data
				sql, args := tt.insert(self.tenant, self.user, self.site)
				n, err = attempt(t, pdb.RoleCPTenant, &self.tenant, sql, args...)
				require.NoError(t, err, "insert for the own tenant")
				require.Equal(t, int64(1), n)

				sql, args = tt.insert(other.tenant, self.user, other.site)
				_, err = attempt(t, pdb.RoleCPTenant, &self.tenant, sql, args...)
				requireDenied(t, err, "insert for the other tenant")
			})
		}
	}
}

// updateColumn is a column cp_tenant may update on the table, or the key
// when it may update nothing (the update is then refused by the grant).
func updateColumn(tt tenantTable) string {
	if tt.name == "cp.tenants" {
		return "name"
	}
	return tt.key
}

func TestNoTenantSetSeesNothing(t *testing.T) {
	ctx := context.Background()
	p, err := conn.Pool(pdb.RoleCPTenant)
	require.NoError(t, err)

	tables := []string{"cp.users"}
	for _, tt := range tenantTables {
		if !tt.denied {
			tables = append(tables, tt.name)
		}
	}
	for _, name := range tables {
		n, err := count(t, pdb.RoleCPTenant, nil, `select count(*) from `+name)
		require.NoError(t, err, name)
		require.Zero(t, n, "%s without app.tenant_id", name)
	}

	// after a transaction that set the tenant locally, the same connection
	// holds an empty string, which must also match nothing
	c, err := p.Acquire(ctx)
	require.NoError(t, err)
	defer c.Release()
	tx, err := c.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `select set_config('app.tenant_id', $1, true)`, seed.tenantA.String())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var setting string
	require.NoError(t, c.QueryRow(ctx, `select current_setting('app.tenant_id', true)`).Scan(&setting))
	require.Empty(t, setting)
	for _, name := range tables {
		var n int64
		require.NoError(t, c.QueryRow(ctx, `select count(*) from `+name).Scan(&n), name)
		require.Zero(t, n, "%s after the tenant transaction ended", name)
	}
}

func TestTenantSeesOnlyItsMembers(t *testing.T) {
	n, err := count(t, pdb.RoleCPTenant, &seed.tenantA, `select count(*) from cp.users`)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	_, err = count(t, pdb.RoleCPTenant, &seed.tenantA, `select count(is_operator::int) from cp.users`)
	requireDenied(t, err, "cp_tenant reads only id and login columns of users")
}

func TestWithTenant(t *testing.T) {
	ctx := context.Background()
	require.ErrorIs(t, conn.WithTenant(ctx, uuid.Nil, nil), pdb.ErrNoTenant)

	var sites []tenantq.CpSite
	require.NoError(t, conn.WithTenant(ctx, seed.tenantA, func(tx pgxTx) error {
		var err error
		sites, err = tenantq.New(tx).ListSites(ctx)
		return err
	}))
	require.Len(t, sites, 1)
	require.Equal(t, seed.siteA, sites[0].ID)

	// a failing unit of work leaves nothing behind
	created := uuid.New()
	boom := fmt.Errorf("handler failed")
	err := conn.WithTenant(ctx, seed.tenantB, func(tx pgxTx) error {
		s, err := tenantq.New(tx).CreateSite(ctx, tenantq.CreateSiteParams{
			ID: created, Name: "rolled back", Domain: "rb.example.org", Origins: []string{}, VerifyToken: "v",
		})
		require.NoError(t, err)
		require.Equal(t, seed.tenantB, s.TenantID, "insert takes the tenant from app.tenant_id")
		return boom
	})
	require.ErrorIs(t, err, boom)
	n, err := count(t, pdb.RoleCPSystem, nil, `select count(*) from cp.sites where id = $1`, created)
	require.NoError(t, err)
	require.Zero(t, n)

	// revoking another tenant's token through the generated query does nothing
	require.NoError(t, conn.WithTenant(ctx, seed.tenantA, func(tx pgxTx) error {
		n, err := tenantq.New(tx).RevokeOTLPToken(ctx, seed.tokenB)
		require.Zero(t, n)
		return err
	}))
}
