package apirollup_test

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
	"github.com/justmert/veristar/internal/jobs/apirollup"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// during the rolling deploy of migration 00040 the rollup of the release
// before it may still run on another node. it groups without the traffic
// class, so an hour with app and health minute rows gets one hourly row
// under 'app' holding both, and its day carries that on. the totals agree
// with the minutes, so only a per-class check finds the hour; the first
// check of a new process reaches back to when the rule set took effect, so
// an hour older than the regular window is recomputed as well.
func TestRollupRecomputesHoursMergedAcrossClasses(t *testing.T) {
	ctx := t.Context()
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, _ := jobstest.Leader(t, srv, d, "rollup-class")

	now := time.Now().UTC()
	recent := now.Truncate(time.Hour).Add(-2 * time.Hour)
	old := now.Truncate(time.Hour).AddDate(0, 0, -3)
	var days, months []time.Time
	for _, h := range []time.Time{recent, old} {
		days = append(days, dayOf(h))
		months = append(months, time.Date(h.Year(), h.Month(), 1, 0, 0, 0, 0, time.UTC))
	}
	ensurePartitions(t, d, days, months)

	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, err = c.Exec(ctx, `update api.traffic_class_rules set applied_from = $1 where version = 1`, now.AddDate(0, 0, -4))
	require.NoError(t, err)

	tenant := uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		}))
	}
	for _, h := range []time.Time{recent, old} {
		// what the new writer stored, with every mark already claimed
		exec(`insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source,
		        traffic_class, counts, sum, count, lossy) values
		      ($1, $2, 'api', 'GET', '/a', 200, 'metric', 'app', '{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.001, 1, false),
		      ($1, $2, 'api', 'GET', '/a', 200, 'metric', 'health', '{5,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.005, 5, false)`,
			tenant, h.Add(10*time.Minute))
		// what the older rollup made of it: both classes under 'app'
		exec(`insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source,
		        counts, sum, count, lossy)
		      values ($1, $2, 'api', 'GET', '/a', 200, 'metric', '{6,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.006, 6, false)`, tenant, h)
		exec(`insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source,
		        counts, sum, count, lossy)
		      values ($1, $2, 'api', 'GET', '/a', 200, 'metric', '{6,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.006, 6, false)
		      on conflict (tenant_id, bucket_start, service, method, route, status_code, source, traffic_class)
		      do update set count = api.metrics_daily.count + excluded.count`, tenant, dayOf(h))
	}
	require.Zero(t, pendingCount(t, d))

	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	res, err := r.Run(ctx, leader.Tx)
	require.NoError(t, err)
	require.Equal(t, 2, res.Drifted, "both merged hours are marked again")
	require.Equal(t, 2, res.Hours)
	require.Zero(t, pendingCount(t, d))

	byClass := func(table string, start time.Time) map[string]int64 {
		t.Helper()
		out := map[string]int64{}
		require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `select traffic_class, sum(count)::bigint from `+pgx.Identifier{"api", table}.Sanitize()+
				` where tenant_id = $1 and bucket_start = $2 group by 1`, tenant, start)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var class string
				var n int64
				if err := rows.Scan(&class, &n); err != nil {
					return err
				}
				out[class] = n
			}
			return rows.Err()
		}))
		return out
	}
	for _, h := range []time.Time{recent, old} {
		require.Equal(t, map[string]int64{"app": 1, "health": 5}, byClass("metrics_hourly", h), "hour %s", h)
	}
	if dayOf(recent).Equal(dayOf(old)) {
		t.Fatal("the two hours must fall on different days")
	}
	for _, h := range []time.Time{recent, old} {
		require.Equal(t, map[string]int64{"app": 1, "health": 5}, byClass("metrics_daily", dayOf(h)), "day %s", dayOf(h))
	}

	// a later run finds nothing: the check agrees per class now
	r2, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	res, err = r2.Run(ctx, leader.Tx)
	require.NoError(t, err)
	require.Zero(t, res.Drifted)
}
