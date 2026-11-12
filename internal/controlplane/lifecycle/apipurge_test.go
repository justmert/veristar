package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane/lifecycle"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// apiTables are the api tables holding a tenant's rows, and whether each is
// an aggregate that keep_aggregates keeps.
var apiTables = map[string]bool{
	"metrics_minute": false, "series_state": false, "resource_seen": false, "route_registry": false,
	"rollup_pending": false, "metrics_hourly": true, "metrics_daily": true, "ingest_warnings": true,
}

// seedAPI writes one row of tenant into every api table, as the jobs role.
func seedAPI(t *testing.T, jobs *pdb.DB, tenant uuid.UUID, b byte) {
	t.Helper()
	now := time.Now().UTC()
	minute := now.Truncate(time.Minute)
	hour := now.Truncate(time.Hour)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	hash := make([]byte, 32)
	hash[0] = b
	const counts = `{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0}`
	require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		for _, s := range []struct {
			sql  string
			args []any
		}{
			{`insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values ($1, $2, 'api', 'GET', '/', 200, 'metric', $3, 0.001, 1)`, []any{tenant, minute, counts}},
			{`insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values ($1, $2, 'api', 'GET', '/', 200, 'metric', $3, 0.001, 1)`, []any{tenant, hour, counts}},
			{`insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values ($1, $2, 'api', 'GET', '/', 200, 'metric', $3, 0.001, 1)`, []any{tenant, dayStart, counts}},
			{`insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano, last_count, last_sum, last_counts, last_seen_at)
values ($1, $2, 1, 2, 1, 0.1, $3, now())`, []any{tenant, hash, counts}},
			{`insert into api.resource_seen (tenant_id, resource_hash, start_unix_nano, first_seen_at, last_seen_at)
values ($1, $2, 1, now(), now())`, []any{tenant, hash}},
			{`insert into api.ingest_dedup (msg_id, tenant_id) values ($1, $2)`, []any{tenant.String(), tenant}},
			{`insert into api.route_registry (tenant_id, day, service, route) values ($1, $2, 'api', '/')`, []any{tenant, dayStart}},
			{`insert into api.ingest_warnings (tenant_id, day, kind, count) values ($1, $2, 'lossy_histogram', 3)`, []any{tenant, dayStart}},
			{`insert into api.rollup_pending (tenant_id, hour) values ($1, $2)`, []any{tenant, hour}},
		} {
			if _, err := tx.Exec(t.Context(), s.sql, s.args...); err != nil {
				return err
			}
		}
		return nil
	}))
}

func apiCounts(t *testing.T, jobs *pdb.DB, tenant uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		for table := range apiTables {
			var n int
			if err := tx.QueryRow(t.Context(), `select count(*) from `+pgx.Identifier{"api", table}.Sanitize()+` where tenant_id = $1`, tenant).Scan(&n); err != nil {
				return err
			}
			out[table] = n
		}
		var n int
		err := tx.QueryRow(t.Context(), `select count(*) from api.ingest_dedup where tenant_id = $1`, tenant).Scan(&n)
		out["ingest_dedup"] = n
		return err
	}))
	return out
}

// TestAPIPurger runs the api purger in a cp_system transaction, as the
// reconciler does, for a removal that deletes aggregates and one that keeps
// them, and checks that it refuses a tenant that is not removed.
func TestAPIPurger(t *testing.T) {
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	cfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem)
	require.NoError(t, err)
	sys, err := pdb.Open(t.Context(), cfg, "api-purge-test")
	require.NoError(t, err)
	t.Cleanup(sys.Close)

	newTenant := func(slug, status string) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, sys.WithSystem(t.Context(), func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), `insert into cp.tenants (slug, name, status) values ($1, $1, $2) returning id`, slug, status).Scan(&id)
		}))
		return id
	}
	purge := func(tenant uuid.UUID, keep bool) (map[string]any, error) {
		var detail map[string]any
		err := sys.WithSystem(t.Context(), func(tx pgx.Tx) error {
			var err error
			detail, err = lifecycle.APIPurger{}.Purge(context.Background(), lifecycle.Removal{TenantID: tenant, KeepAggregates: keep, Tx: tx})
			return err
		})
		return detail, err
	}

	gone, kept, live := newTenant("gone", "removed"), newTenant("kept", "removed"), newTenant("live", "active")
	for i, id := range []uuid.UUID{gone, kept, live} {
		seedAPI(t, jobs, id, byte(i+1))
	}
	full := apiCounts(t, jobs, live)
	for table, n := range full {
		require.Equal(t, 1, n, table)
	}

	// keep_aggregates false: every row of the tenant goes, except the dedup
	// ids that stop a queued message from being applied again
	detail, err := purge(gone, false)
	require.NoError(t, err)
	after := apiCounts(t, jobs, gone)
	for table := range apiTables {
		require.Zero(t, after[table], table)
	}
	require.Equal(t, 1, after["ingest_dedup"])
	deleted := detail["rows_deleted"].(map[string]int64)
	require.Len(t, deleted, len(apiTables))
	for table := range apiTables {
		require.Equal(t, int64(1), deleted[table], table)
	}

	// keep_aggregates true: minutes and writer state go, hourly, daily and
	// warnings stay
	detail, err = purge(kept, true)
	require.NoError(t, err)
	after = apiCounts(t, jobs, kept)
	for table, aggregate := range apiTables {
		if aggregate {
			require.Equal(t, 1, after[table], "%s is kept", table)
		} else {
			require.Zero(t, after[table], "%s is deleted", table)
		}
	}
	require.NotContains(t, detail["rows_deleted"], "metrics_hourly")
	require.NotNil(t, detail["kept"])

	// purging again is a no-op, as a retried step must be
	_, err = purge(gone, false)
	require.NoError(t, err)

	// a tenant that is not removed is refused and keeps every row
	_, err = purge(live, false)
	require.ErrorContains(t, err, "is not removed")
	require.Equal(t, full, apiCounts(t, jobs, live))

	// cp_system reaches api only through the function
	err = sys.WithSystem(t.Context(), func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `delete from api.metrics_minute where tenant_id = $1`, live)
		return err
	})
	require.ErrorContains(t, err, "permission denied")
}

func TestPurgersIncludeAPI(t *testing.T) {
	// the list is only inspected here, so the stores behind it are not
	// reached; the api step must come after otlp_tokens, so collector
	// exports have stopped before the rows go
	purgers, err := lifecycle.Purgers(&registry.Publisher{}, &umami.Admin{})
	require.NoError(t, err)
	var names []string
	for _, p := range purgers {
		names = append(names, p.Name())
	}
	require.Equal(t, []string{"registry", "otlp_tokens", "umami", "web_ingest", "api", "agg"}, names)
}
