package metricswriter

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/db/writerq"
	"github.com/justmert/veristar/internal/jobs/apirollup"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// a writer transaction adds minute rows to an hour whose mark is
// already committed, and the rollup claims that mark while the writer is
// still open. the rollup must not recompute the hour from minutes it cannot
// see yet, or those requests never reach the hourly and daily rows. two real
// sessions on one postgresql: the writer's own writeRows in one, the
// api-rollup task in the other.
func TestRollupNeverRecomputesAnHourAWriterHasOpen(t *testing.T) {
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	wcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleMetricsWriter)
	require.NoError(t, err)
	writer, err := pdb.Open(t.Context(), wcfg, "race-writer")
	require.NoError(t, err)
	t.Cleanup(writer.Close)

	tenant := uuid.New()
	hour := time.Date(2031, 4, 2, 10, 0, 0, 0, time.UTC)
	require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		if _, err := q.CreateMinutePartition(t.Context(), time.Date(2031, 4, 2, 0, 0, 0, 0, time.UTC)); err != nil {
			return err
		}
		if _, err := q.CreateHourlyPartition(t.Context(), time.Date(2031, 4, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			return err
		}
		// an earlier batch: one request at 10:05, committed with its mark
		if _, err := tx.Exec(t.Context(), `insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values ($1, $2, 'api', 'GET', '/a', 200, 'metric', '{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.001, 1)`, tenant, hour.Add(5*time.Minute)); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `insert into api.rollup_pending (tenant_id, hour) values ($1, $2)`, tenant, hour)
		return err
	}))

	rollup, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	jobsTx := func(ctx context.Context, fn func(pgx.Tx) error) error { return jobs.WithRole(ctx, pdb.RoleJobs, fn) }

	// the writer's batch: five requests at 10:58. the rollup runs while the
	// writer's transaction is open, then the writer commits.
	var counts Buckets
	counts[0] = 5
	rows := map[rowKey]*row{{
		tenant: tenant, minute: hour.Add(58 * time.Minute).Unix(), service: "api", method: "GET", route: "/a", status: 200, source: SourceMetric,
		class: "app",
	}: {count: 5, sum: 0.005, counts: counts, points: 1}}
	var during apirollup.Result
	require.NoError(t, writer.WithRole(t.Context(), pdb.RoleMetricsWriter, func(tx pgx.Tx) error {
		if err := writeRows(t.Context(), tx, writerq.New(tx), rows, &result{}, slog.New(slog.DiscardHandler)); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		var err error
		during, err = rollup.Run(ctx, jobsTx)
		return err
	}))

	// the next scheduled run
	_, err = rollup.Run(t.Context(), jobsTx)
	require.NoError(t, err)

	var minute, hourly, daily int64
	require.NoError(t, jobs.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(), `select coalesce(sum(count), 0) from api.metrics_minute where tenant_id = $1`, tenant).Scan(&minute); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `select coalesce(sum(count), 0) from api.metrics_hourly where tenant_id = $1`, tenant).Scan(&hourly); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `select coalesce(sum(count), 0) from api.metrics_daily where tenant_id = $1`, tenant).Scan(&daily)
	}))
	require.Equal(t, int64(6), minute)
	require.Equal(t, minute, hourly, "hourly rows miss the requests of the writer that was open during the rollup")
	require.Equal(t, minute, daily, "daily rows miss the requests of the writer that was open during the rollup")
	require.Zero(t, during.Hours, "the rollup recomputed an hour while a writer had it open")
	require.Zero(t, during.Drifted, "the consistency check, not the lock, repaired the hour")
}
