package db_test

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/fenceq"
	"github.com/justmert/veristar/internal/db/operatorq"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/db/writerq"
)

// every cp relation, for the "reads nothing else" checks
var cpRelations = []string{
	"cp.users", "cp.sessions", "cp.tenants", "cp.tenant_members", "cp.invites", "cp.sites",
	"cp.otlp_tokens", "cp.operator_api_keys", "cp.api_key_usage_hourly", "cp.settings",
	"cp.audit_log", "cp.erasure_requests", "cp.web_ingest_hourly", "cp.stats_dedup",
	"cp.leader_fence", "cp.operator_sites", "cp.tenant_removals", "cp.chain_ids",
}

func TestOperatorBoundaries(t *testing.T) {
	ctx := context.Background()
	readable := map[string]bool{
		"cp.tenants": true, "cp.operator_sites": true, "cp.web_ingest_hourly": true,
		"cp.settings": true, "cp.operator_api_keys": true, "cp.api_key_usage_hourly": true,
		"cp.tenant_removals": true, "cp.chain_ids": true,
	}
	for _, rel := range cpRelations {
		_, err := count(t, pdb.RoleCPOperator, nil, `select count(*) from (select * from `+rel+`) x`)
		if readable[rel] {
			require.NoError(t, err, rel)
		} else {
			requireDenied(t, err, "cp_operator reading "+rel)
		}
	}

	require.NoError(t, conn.WithOperator(ctx, func(tx pgxTx) error {
		sites, err := operatorq.New(tx).ListOperatorSites(ctx)
		require.NoError(t, err)
		require.Len(t, sites, 2, "operators see every tenant's sites")
		rows, err := tx.Query(ctx, `select * from cp.operator_sites limit 0`)
		require.NoError(t, err)
		for _, f := range rows.FieldDescriptions() {
			require.NotEqual(t, "verify_token", f.Name)
		}
		rows.Close()
		return nil
	}))

	// usage counting and last_used_at are the only writes
	at := time.Now().UTC()
	require.NoError(t, conn.WithOperator(ctx, func(tx pgxTx) error {
		q := operatorq.New(tx)
		require.NoError(t, q.RecordAPIKeyUse(ctx, operatorq.RecordAPIKeyUseParams{KeyID: seed.opKey, At: at}))
		require.NoError(t, q.RecordAPIKeyUse(ctx, operatorq.RecordAPIKeyUseParams{KeyID: seed.opKey, At: at, Rejected: true}))
		n, err := q.TouchOperatorAPIKey(ctx, operatorq.TouchOperatorAPIKeyParams{At: at, ID: seed.opKey})
		require.Equal(t, int64(1), n)
		return err
	}))
	var req, rej int64
	p, err := conn.Pool(pdb.RoleCPOperator)
	require.NoError(t, err)
	require.NoError(t, p.QueryRow(ctx, `select requests, rejected from cp.api_key_usage_hourly where key_id = $1 and hour = date_trunc('hour', $2::timestamptz)`,
		seed.opKey, at).Scan(&req, &rej))
	require.Equal(t, [2]int64{2, 1}, [2]int64{req, rej})

	_, err = attempt(t, pdb.RoleCPOperator, nil, `update cp.operator_api_keys set name = 'x'`)
	requireDenied(t, err, "operator updating a key name")
	_, err = attempt(t, pdb.RoleCPOperator, nil, `insert into cp.settings (key, value) values ('x', '1')`)
	requireDenied(t, err, "operator writing settings")
	_, err = attempt(t, pdb.RoleCPOperator, nil, `update cp.tenants set status = 'active'`)
	requireDenied(t, err, "operator changing tenants")

	// aggregates of every tenant, read only; never minutes or
	// writer state, never another database
	for _, rel := range []string{"agg.web_daily", "agg.web_daily_dim", "api.metrics_hourly", "api.metrics_daily"} {
		n, err := count(t, pdb.RoleCPOperator, nil, `select count(*) from `+rel)
		require.NoError(t, err, rel)
		require.Positive(t, n, "operators see every tenant's rows of "+rel)
	}
	for _, rel := range []string{"api.metrics_minute", "api.series_state", "api.resource_seen", "api.ingest_dedup",
		"api.route_registry", "api.ingest_warnings", "api.rollup_pending"} {
		_, err := count(t, pdb.RoleCPOperator, nil, `select count(*) from `+rel)
		requireDenied(t, err, "cp_operator reading "+rel)
	}
	for _, sql := range []string{
		`delete from agg.web_daily`, `update agg.web_daily_dim set value = value`,
		`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
		 values (gen_random_uuid(), gen_random_uuid(), current_date, 'primary', 0, 0, 0, 0, 0)`,
	} {
		_, err := attempt(t, pdb.RoleCPOperator, nil, sql)
		requireDenied(t, err, "cp_operator writing agg: "+sql)
	}
	_, err = attempt(t, pdb.RoleCPOperator, nil, `select * from agg.purge_tenant($1)`, seed.tenantA)
	requireDenied(t, err, "cp_operator purging agg")

	// the umami database is reachable for umami_app only
	ocfg := pg.EnvFor("umami")
	dsn := fmt.Sprintf("postgres://cp_operator:%s@%s:%d/umami?sslmode=disable",
		url.QueryEscape(ocfg["PLATFORM_DB_CP_OPERATOR_PASSWORD"]), pg.Host, pg.Port)
	c, err := pgx.Connect(ctx, dsn)
	if err == nil {
		_ = c.Close(ctx)
	}
	require.ErrorContains(t, err, "permission denied for database", "cp_operator connecting to umami")
}

func TestMetricsWriterBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, rel := range cpRelations {
		_, err := count(t, pdb.RoleMetricsWriter, nil, `select count(*) from (select * from `+rel+`) x`)
		if rel == "cp.leader_fence" {
			require.NoError(t, err, rel)
		} else {
			requireDenied(t, err, "metrics_writer reading all columns of "+rel)
		}
	}
	n, err := count(t, pdb.RoleMetricsWriter, nil, `select count(id) from cp.otlp_tokens`)
	require.NoError(t, err)
	require.Equal(t, int64(2), n, "metrics_writer matches tokens of every tenant by id")

	require.NoError(t, conn.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgxTx) error {
		n, err := writerq.New(tx).TouchOTLPToken(ctx, writerq.TouchOTLPTokenParams{SeenAt: time.Now(), ID: seed.tokenB})
		require.Equal(t, int64(1), n)
		return err
	}))
	_, err = attempt(t, pdb.RoleMetricsWriter, nil, `update cp.otlp_tokens set status = 'revoked', revoked_at = now() where id = $1`, seed.tokenA)
	requireDenied(t, err, "metrics_writer revoking a token")
	_, err = attempt(t, pdb.RoleMetricsWriter, nil, `insert into cp.leader_fence (name) values ('x')`)
	requireDenied(t, err, "metrics_writer creating a fence")
	_, err = attempt(t, pdb.RoleMetricsWriter, nil, `delete from cp.leader_fence`)
	requireDenied(t, err, "metrics_writer deleting a fence")

	require.ErrorContains(t, conn.WithRole(ctx, pdb.RoleCPTenant, func(pgxTx) error { return nil }), "WithRole")
}

func TestJobsBoundaries(t *testing.T) {
	// chain-activity reads the declared ids (00032)
	writable := map[string]bool{"cp.web_ingest_hourly": true, "cp.stats_dedup": true, "cp.leader_fence": true, "cp.chain_ids": true}
	for _, rel := range cpRelations {
		_, err := count(t, pdb.RoleJobs, nil, `select count(*) from (select * from `+rel+`) x`)
		if writable[rel] {
			require.NoError(t, err, rel)
		} else {
			requireDenied(t, err, "jobs reading "+rel)
		}
	}
	n, err := attempt(t, pdb.RoleJobs, nil, `
		insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count)
		values ($1, $2, date_trunc('hour', now()), 'accepted', 3)
		on conflict (site_id, hour, outcome) do update set count = cp.web_ingest_hourly.count + excluded.count`,
		seed.siteA, seed.tenantA)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	n, err = attempt(t, pdb.RoleJobs, nil, `insert into cp.stats_dedup (message_id) values ('node-1.1') on conflict do nothing`)
	require.NoError(t, err)
	require.Zero(t, n, "an applied message id is not applied twice")
	_, err = attempt(t, pdb.RoleJobs, nil, `delete from cp.web_ingest_hourly`)
	requireDenied(t, err, "jobs deleting edge counters")
	// statsdedup-prune removes old message ids (migration 00006)
	_, err = attempt(t, pdb.RoleJobs, nil, `delete from cp.stats_dedup where applied_at < now() - interval '8 days'`)
	require.NoError(t, err)
	// aggregate-web reads a tenant's status and nothing else of cp.tenants
	n, err = count(t, pdb.RoleJobs, nil, `select count(*) from cp.tenants where status <> 'removed' and id is not null`)
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	_, err = count(t, pdb.RoleJobs, nil, `select count(name) from cp.tenants`)
	requireDenied(t, err, "jobs reading tenant names")
	_, err = attempt(t, pdb.RoleJobs, nil, `update cp.tenants set status = 'active'`)
	requireDenied(t, err, "jobs changing tenants")
	_, err = attempt(t, pdb.RoleJobs, nil, `select * from agg.purge_tenant($1)`, seed.tenantA)
	requireDenied(t, err, "jobs purging agg")
}

func TestAuditLogAppendOnly(t *testing.T) {
	ctx := context.Background()
	for _, role := range pdb.AppRoles {
		_, err := attempt(t, role, &seed.tenantA, `update cp.audit_log set actor = 'forged'`)
		requireDenied(t, err, string(role)+" updating the audit log")
		_, err = attempt(t, role, &seed.tenantA, `delete from cp.audit_log`)
		requireDenied(t, err, string(role)+" deleting from the audit log")
		_, err = attempt(t, role, &seed.tenantA, `truncate cp.audit_log`)
		requireDenied(t, err, string(role)+" truncating the audit log")
	}

	require.NoError(t, conn.WithSystem(ctx, func(tx pgxTx) error {
		_, err := systemq.New(tx).InsertAuditLog(ctx, systemq.InsertAuditLogParams{Actor: "t", Action: "check", Detail: []byte(`{"k":1}`)})
		return err
	}))
	n, err := count(t, pdb.RoleCPSystem, nil, `select count(*) from cp.audit_log`)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(2))

	// the triggers stop even a superuser
	c := superuser(t)
	for _, stmt := range []string{`update cp.audit_log set actor = 'forged'`, `delete from cp.audit_log`, `truncate cp.audit_log`} {
		_, err := c.Exec(ctx, stmt)
		require.ErrorContains(t, err, "append-only", stmt)
	}
}

func fence(t *testing.T, role pdb.Role, name string, epoch, rev int64) int64 {
	t.Helper()
	var n int64
	require.NoError(t, conn.WithRole(context.Background(), role, func(tx pgxTx) error {
		var err error
		n, err = fenceq.New(tx).AdvanceFence(context.Background(), fenceq.AdvanceFenceParams{Epoch: epoch, Revision: rev, Name: name})
		return err
	}))
	return n
}

func TestLeaderFence(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		role pdb.Role
		name string
	}{{pdb.RoleMetricsWriter, "metricswriter"}, {pdb.RoleJobs, "jobs"}} {
		require.Equal(t, int64(1), fence(t, tc.role, tc.name, 100, 10), "a new leader advances the fence")
		require.Equal(t, int64(1), fence(t, tc.role, tc.name, 100, 10), "the same leader renews with its revision")
		require.Zero(t, fence(t, tc.role, tc.name, 100, 9), "a stale revision is refused")
		require.Zero(t, fence(t, tc.role, tc.name, 99, 50), "an older epoch is refused whatever its revision")
		require.Zero(t, fence(t, tc.role, "unknown", 100, 1), "an unknown fence matches nothing")

		_, err := attempt(t, tc.role, nil, `update cp.leader_fence set revision = 5 where name = $1`, tc.name)
		require.Error(t, err)
		require.Equal(t, "23514", sqlState(err), "the trigger refuses a backwards revision: %v", err)
		_, err = attempt(t, tc.role, nil, `update cp.leader_fence set epoch = 1, revision = 50 where name = $1`, tc.name)
		require.Error(t, err)
		require.Equal(t, "23514", sqlState(err), "the trigger refuses a backwards epoch: %v", err)

		// after a nats reset the next epoch starts at a low revision again
		require.Equal(t, int64(1), fence(t, tc.role, tc.name, 200, 1), "a newer epoch advances with a lower revision")
		require.Zero(t, fence(t, tc.role, tc.name, 100, 11), "the old epoch stays fenced off")
	}

	// a former leader racing the new one waits on the row lock, then sees
	// the newer revision and is refused
	p, err := conn.Pool(pdb.RoleMetricsWriter)
	require.NoError(t, err)
	newer, err := p.Begin(ctx)
	require.NoError(t, err)
	n, err := fenceq.New(newer).AdvanceFence(ctx, fenceq.AdvanceFenceParams{Epoch: 200, Revision: 20, Name: "metricswriter"})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	var wg sync.WaitGroup
	var staleRows int64
	var staleErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		staleErr = conn.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgxTx) error {
			var err error
			staleRows, err = fenceq.New(tx).AdvanceFence(ctx, fenceq.AdvanceFenceParams{Epoch: 200, Revision: 15, Name: "metricswriter"})
			return err
		})
	}()
	c := superuser(t)
	require.Eventually(t, func() bool {
		var waiting int
		_ = c.QueryRow(ctx, `select count(*) from pg_locks where not granted`).Scan(&waiting)
		return waiting > 0
	}, 10*time.Second, 20*time.Millisecond, "the stale writer must block on the fence row")
	require.NoError(t, newer.Commit(ctx))
	wg.Wait()
	require.NoError(t, staleErr)
	require.Zero(t, staleRows)

	var got fenceq.GetFenceRow
	require.NoError(t, conn.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgxTx) error {
		var err error
		got, err = fenceq.New(tx).GetFence(ctx, "metricswriter")
		return err
	}))
	require.Equal(t, fenceq.GetFenceRow{Epoch: 200, Revision: 20}, got)
}
