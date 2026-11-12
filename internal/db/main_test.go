package db_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/db"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// one server for the package. the platform database is migrated, has role
// passwords and holds the seed below; tests that change it either roll back
// or use their own rows.
var (
	pg   *dbtest.Postgres
	conn *pdb.DB
	seed seedData
)

type seedData struct {
	userA, userB     uuid.UUID
	tenantA, tenantB uuid.UUID
	siteA, siteB     uuid.UUID
	tokenA, tokenB   uuid.UUID
	opKey            uuid.UUID
}

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
	cfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	if err != nil {
		return err
	}
	if _, err := pdb.Migrate(ctx, cfg, db.Migrations(), quietLog()); err != nil {
		return err
	}
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	if err != nil {
		return err
	}
	if err := pdb.SetRolePasswords(ctx, rcfg); err != nil {
		return err
	}
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.AppRoles...)
	if err != nil {
		return err
	}
	if conn, err = pdb.Open(ctx, pcfg, "db-test"); err != nil {
		return err
	}
	if err := conn.WithSystem(ctx, seedRows); err != nil {
		return err
	}
	return conn.WithRole(ctx, pdb.RoleJobs, seedAPIRows)
}

// seedAPIRows gives each tenant one row in every api table, written as jobs,
// the one role with rights on all of them
func seedAPIRows(tx pgx.Tx) error {
	ctx := context.Background()
	for i, tenant := range []uuid.UUID{seed.tenantA, seed.tenantB} {
		hash := []byte(fmt.Sprintf("%032d", i))
		counts := []int64{0, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		stmts := []struct {
			sql  string
			args []any
		}{
			{`insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
			  values ($1, date_trunc('minute', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`, []any{tenant, counts}},
			{`insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
			  values ($1, date_trunc('hour', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`, []any{tenant, counts}},
			{`insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
			  values ($1, date_trunc('day', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`, []any{tenant, counts}},
			{`insert into api.ingest_warnings (tenant_id, day, kind, count) values ($1, current_date, 'lossy_histogram', 1)`, []any{tenant}},
			{`insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano, last_count, last_sum, last_counts, last_seen_at)
			  values ($1, $2, 1, 2, 4, 11.5, $3, now())`, []any{tenant, hash, counts}},
			{`insert into api.resource_seen (tenant_id, resource_hash, start_unix_nano, first_seen_at, last_seen_at)
			  values ($1, $2, 1, now(), now())`, []any{tenant, hash}},
			{`insert into api.ingest_dedup (msg_id, tenant_id) values ($1, $2)`, []any{fmt.Sprintf("seed-%d", i), tenant}},
			{`insert into api.route_registry (tenant_id, day, service, route) values ($1, current_date, 'svc', '/a')`, []any{tenant}},
			{`insert into api.rollup_pending (tenant_id, hour) values ($1, date_trunc('hour', now(), 'UTC'))`, []any{tenant}},
			// agg (this stage); jobs writes these too
			{`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
			  values ($1, $2, current_date, 'primary', 5, 2, 3, 1, 40)`, []any{uuid.New(), tenant}},
			{`insert into agg.web_daily_dim (site_id, tenant_id, day, band, dimension, key, value)
			  values ($1, $2, current_date, 'primary', 'country', 'DE', 2)`, []any{uuid.New(), tenant}},
		}
		for _, s := range stmts {
			if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
				return fmt.Errorf("%s: %w", s.sql, err)
			}
		}
	}
	return nil
}

// seedRows gives each of two tenants one row in every tenant table, written
// as cp_system the way the control plane will.
func seedRows(tx pgx.Tx) error {
	ctx := context.Background()
	q := systemq.New(tx)
	ua, err := q.UpsertUser(ctx, systemq.UpsertUserParams{GithubID: 1001, GithubLogin: "alice"})
	if err != nil {
		return err
	}
	ub, err := q.UpsertUser(ctx, systemq.UpsertUserParams{GithubID: 1002, GithubLogin: "bob"})
	if err != nil {
		return err
	}
	ta, err := q.CreateTenant(ctx, systemq.CreateTenantParams{Slug: "tenant-a", Name: "Tenant A", CreatedBy: uuid.NullUUID{UUID: ua.ID, Valid: true}})
	if err != nil {
		return err
	}
	tb, err := q.CreateTenant(ctx, systemq.CreateTenantParams{Slug: "tenant-b", Name: "Tenant B", CreatedBy: uuid.NullUUID{UUID: ub.ID, Valid: true}})
	if err != nil {
		return err
	}
	seed = seedData{userA: ua.ID, userB: ub.ID, tenantA: ta.ID, tenantB: tb.ID,
		siteA: uuid.New(), siteB: uuid.New(), tokenA: uuid.New(), tokenB: uuid.New(), opKey: uuid.New()}
	for _, m := range []systemq.AddTenantMemberParams{
		{TenantID: ta.ID, UserID: ua.ID, Role: "owner"},
		{TenantID: tb.ID, UserID: ub.ID, Role: "owner"},
	} {
		if err := q.AddTenantMember(ctx, m); err != nil {
			return err
		}
	}
	per := []struct {
		tenant, user, site, token uuid.UUID
		n                         int
	}{
		{ta.ID, ua.ID, seed.siteA, seed.tokenA, 1},
		{tb.ID, ub.ID, seed.siteB, seed.tokenB, 2},
	}
	for _, p := range per {
		stmts := []struct {
			sql  string
			args []any
		}{
			{`insert into cp.invites (tenant_id, github_login, role, created_by, expires_at)
			  values ($1, $2, 'member', $3, now() + interval '7 days')`, []any{p.tenant, fmt.Sprintf("invitee%d", p.n), p.user}},
			{`insert into cp.sites (id, tenant_id, name, domain, origins, verify_token)
			  values ($1, $2, $3, $4, $5, $6)`, []any{p.site, p.tenant, fmt.Sprintf("site %d", p.n),
				fmt.Sprintf("s%d.example.org", p.n), []string{fmt.Sprintf("https://s%d.example.org", p.n)}, fmt.Sprintf("verify-secret-%d", p.n)}},
			{`insert into cp.otlp_tokens (id, tenant_id, name, prefix, hash, created_by)
			  values ($1, $2, 'default', $3, sha256($4::bytea), $5)`, []any{p.token, p.tenant, fmt.Sprintf("sa_otlp_%d", p.n), []byte(fmt.Sprintf("token-%d", p.n)), p.user}},
			{`insert into cp.erasure_requests (tenant_id, site_id, kind, requested_by)
			  values ($1, $2, 'session', $3)`, []any{p.tenant, p.site, p.user}},
			{`insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count)
			  values ($1, $2, date_trunc('hour', now()), 'accepted', 5)`, []any{p.site, p.tenant}},
			{`insert into cp.tenant_removals (tenant_id, requested_by, reason, keep_aggregates)
			  values ($1, $2, 'isolation seed', false)`, []any{p.tenant, p.user}},
			{`insert into cp.sessions (token_hash, user_id, data, expiry)
			  values (sha256($1::bytea), $2, '\x00', now() + interval '1 day')`, []any{[]byte(fmt.Sprintf("session-%d", p.n)), p.user}},
		}
		for _, s := range stmts {
			if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
				return fmt.Errorf("%s: %w", s.sql, err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `insert into cp.operator_api_keys (id, name, prefix, hash, scope, expires_at, created_by)
		values ($1, 'ops', 'sa_op_x', sha256('op-key'::bytea), 'global', now() + interval '90 days', $2)`, seed.opKey, ua.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into cp.stats_dedup (message_id) values ('node-1.1')`); err != nil {
		return err
	}
	_, err = q.InsertAuditLog(ctx, systemq.InsertAuditLogParams{Actor: "test", Action: "seed", Target: "all", Detail: []byte(`{}`)})
	return err
}

// pgxTx keeps the callback signatures in tests short.
type pgxTx = pgx.Tx

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// attempt runs one statement as a role in its own transaction and always
// rolls back, so the shared seed stays as it is. tenant is set with
// set_config exactly as WithTenant does; nil leaves app.tenant_id unset.
func attempt(t *testing.T, role pdb.Role, tenant *uuid.UUID, sql string, args ...any) (int64, error) {
	t.Helper()
	ctx := context.Background()
	p, err := conn.Pool(role)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	if tenant != nil {
		_, err := tx.Exec(ctx, `select set_config('app.tenant_id', $1, true)`, tenant.String())
		require.NoError(t, err)
	}
	tag, err := tx.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}

// count runs a select count(*) style query the same way as attempt.
func count(t *testing.T, role pdb.Role, tenant *uuid.UUID, sql string, args ...any) (int64, error) {
	t.Helper()
	ctx := context.Background()
	p, err := conn.Pool(role)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	if tenant != nil {
		_, err := tx.Exec(ctx, `select set_config('app.tenant_id', $1, true)`, tenant.String())
		require.NoError(t, err)
	}
	var n int64
	err = tx.QueryRow(ctx, sql, args...).Scan(&n)
	return n, err
}

// sqlState returns the sqlstate of a postgresql error, or "" for nil or a
// non-postgresql error.
func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// insufficient_privilege, raised both for a missing grant and for a row that
// fails a row-level security check
const denied = "42501"

func requireDenied(t *testing.T, err error, what string) {
	t.Helper()
	require.Error(t, err, what)
	require.Equal(t, denied, sqlState(err), "%s: %v", what, err)
}
