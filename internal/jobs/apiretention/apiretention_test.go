package apiretention_test

import (
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/jobs/apipartition"
	"github.com/justmert/veristar/internal/jobs/apiretention"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

const oneBucket = `{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0}`

func exec(t *testing.T, d *pdb.DB, sql string, args ...any) {
	t.Helper()
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), sql, args...)
		return err
	}))
}

func count(t *testing.T, d *pdb.DB, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), sql, args...).Scan(&n)
	}))
	return n
}

func metricRow(t *testing.T, d *pdb.DB, table string, tenant uuid.UUID, at time.Time) {
	t.Helper()
	exec(t, d, `insert into `+pgx.Identifier{"api", table}.Sanitize()+`
(tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values ($1, $2, 'api', 'GET', '/', 200, 'metric', $3, 0.001, 1)`, tenant, at, oneBucket)
}

func newRetention(t *testing.T, cfg config.APIRetention, now *time.Time) *apiretention.Retention {
	t.Helper()
	clock := func() time.Time { return *now }
	p, err := apipartition.New(clock, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	r, err := apiretention.New(cfg, p, clock, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	return r
}

// TestRetention runs retention with an injected clock over partitions and
// rows laid out around the horizons: minute partitions older than 30 days
// go, hourly rows 30 months old stay, daily rows are never dropped, and the
// bookkeeping tables are pruned by age.
func TestRetention(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, _ := jobstest.Leader(t, srv, d, "retention-test")

	now := time.Date(2031, 6, 15, 12, 0, 0, 0, time.UTC)
	r := newRetention(t, config.APIRetention{MinuteDays: 30, HourlyMonths: 36}, &now)
	tenant := uuid.New()

	// minute partitions for every day from 45 days back to today
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		for x := day(2031, 5, 1); !x.After(day(2031, 6, 15)); x = x.AddDate(0, 0, 1) {
			if _, err := q.CreateMinutePartition(t.Context(), x); err != nil {
				return err
			}
		}
		for x := day(2028, 1, 1); !x.After(day(2031, 6, 1)); x = x.AddDate(0, 1, 0) {
			if _, err := q.CreateHourlyPartition(t.Context(), x); err != nil {
				return err
			}
		}
		return nil
	}))
	metricRow(t, d, "metrics_minute", tenant, time.Date(2031, 5, 15, 23, 59, 0, 0, time.UTC)) // 31 days old: goes
	metricRow(t, d, "metrics_minute", tenant, time.Date(2031, 5, 16, 0, 0, 0, 0, time.UTC))   // within 30 days: stays
	metricRow(t, d, "metrics_minute", tenant, time.Date(2031, 6, 15, 11, 0, 0, 0, time.UTC))
	metricRow(t, d, "metrics_hourly", tenant, time.Date(2028, 5, 31, 23, 0, 0, 0, time.UTC)) // 36 months and more: goes
	metricRow(t, d, "metrics_hourly", tenant, time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC))   // just inside 36 months
	metricRow(t, d, "metrics_hourly", tenant, time.Date(2028, 12, 15, 0, 0, 0, 0, time.UTC)) // 30 months old: stays
	metricRow(t, d, "metrics_daily", tenant, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))    // daily is kept forever
	metricRow(t, d, "metrics_daily", tenant, time.Date(2028, 5, 31, 0, 0, 0, 0, time.UTC))
	// an expired day that still waits for the rollup is not dropped yet
	metricRow(t, d, "metrics_minute", tenant, time.Date(2031, 5, 10, 8, 0, 0, 0, time.UTC))
	exec(t, d, `insert into api.rollup_pending (tenant_id, hour) values ($1, '2031-05-10 08:00:00+00')`, tenant)

	// bookkeeping rows on both sides of their cutoffs
	exec(t, d, `insert into api.ingest_dedup (msg_id, tenant_id, applied_at) values
  ('old', $1, $2), ('edge', $1, $3), ('new', $1, $4)`,
		tenant, now.Add(-apiretention.DedupMaxAge-time.Second), now.Add(-apiretention.DedupMaxAge), now.Add(-time.Hour))
	hash := func(b byte) []byte { h := make([]byte, 32); h[0] = b; return h }
	exec(t, d, `insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano, last_count, last_sum, last_counts, last_seen_at)
values ($1, $2, 1, 2, 1, 0.1, $4, $5), ($1, $3, 1, 2, 1, 0.1, $4, $6)`,
		tenant, hash(1), hash(2), oneBucket, now.Add(-apiretention.IdleMaxAge-time.Second), now.Add(-time.Hour))
	exec(t, d, `insert into api.resource_seen (tenant_id, resource_hash, start_unix_nano, first_seen_at, last_seen_at)
values ($1, $2, 1, $4, $4), ($1, $3, 1, $5, $5)`,
		tenant, hash(1), hash(2), now.Add(-apiretention.IdleMaxAge-time.Second), now.Add(-6*24*time.Hour))
	exec(t, d, `insert into api.route_registry (tenant_id, day, service, route) values
  ($1, '2031-06-07', 'api', '/old'), ($1, '2031-06-08', 'api', '/kept'), ($1, '2031-06-15', 'api', '/today')`, tenant)

	res, err := r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)

	days, months := lists(t, d)
	for x := day(2031, 5, 1); x.Before(day(2031, 5, 16)); x = x.AddDate(0, 0, 1) {
		if x.Equal(day(2031, 5, 10)) {
			require.True(t, days[x], "a day with a pending rollup waits")
			continue
		}
		require.False(t, days[x], "minute partition %s is older than 30 days", x.Format(time.DateOnly))
	}
	for x := day(2031, 5, 16); !x.After(day(2031, 6, 15)); x = x.AddDate(0, 0, 1) {
		require.True(t, days[x], "minute partition %s is within 30 days", x.Format(time.DateOnly))
	}
	require.Equal(t, []time.Time{day(2031, 5, 10)}, res.Dropped.Waiting)
	for x := day(2028, 1, 1); x.Before(day(2028, 6, 1)); x = x.AddDate(0, 1, 0) {
		require.False(t, months[x], "hourly partition %s is older than 36 months", x.Format("2006-01"))
	}
	for x := day(2028, 6, 1); !x.After(day(2031, 6, 1)); x = x.AddDate(0, 1, 0) {
		require.True(t, months[x], "hourly partition %s is within 36 months", x.Format("2006-01"))
	}
	// the five months laid out above, plus the ones the migration created
	// around the real date, which this clock sees as years old
	require.Subset(t, res.Dropped.HourlyMonths,
		[]time.Time{day(2028, 1, 1), day(2028, 2, 1), day(2028, 3, 1), day(2028, 4, 1), day(2028, 5, 1)})
	for _, m := range res.Dropped.HourlyMonths {
		require.True(t, m.Before(day(2028, 6, 1)), "dropped %s", m.Format("2006-01"))
	}

	require.Equal(t, 3, count(t, d, `select count(*) from api.metrics_minute`), "the 31-day-old minute went with its partition")
	require.Equal(t, 1, count(t, d, `select count(*) from api.metrics_minute where bucket_start = '2031-05-16 00:00:00+00'`))
	require.Equal(t, 2, count(t, d, `select count(*) from api.metrics_hourly`))
	require.Equal(t, 1, count(t, d, `select count(*) from api.metrics_hourly where bucket_start = '2028-12-15 00:00:00+00'`),
		"hourly rows 30 months old are kept")
	require.Equal(t, 2, count(t, d, `select count(*) from api.metrics_daily`), "daily rows are never dropped")

	require.Equal(t, int64(1), res.IngestDedup)
	require.Equal(t, 2, count(t, d, `select count(*) from api.ingest_dedup where msg_id in ('edge', 'new')`))
	require.Equal(t, int64(1), res.SeriesState)
	require.Equal(t, 1, count(t, d, `select count(*) from api.series_state where series_hash = $1`, hash(2)))
	require.Equal(t, int64(1), res.ResourceSeen)
	require.Equal(t, 1, count(t, d, `select count(*) from api.resource_seen where resource_hash = $1`, hash(2)))
	require.Equal(t, int64(1), res.RouteRegistry)
	require.Equal(t, 2, count(t, d, `select count(*) from api.route_registry`))

	// once the rollup has taken the hour, the expired day goes
	exec(t, d, `delete from api.rollup_pending`)
	res, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, []time.Time{day(2031, 5, 10)}, res.Dropped.MinuteDays)
	require.Empty(t, res.Dropped.Waiting)

	// hourly rows survive 30 months as the clock moves: at 2031-06-15 plus
	// six months the 2028-12 row is 36 months old and its partition is still
	// inside the horizon until the month after
	now = time.Date(2031, 12, 31, 23, 0, 0, 0, time.UTC)
	_, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, 1, count(t, d, `select count(*) from api.metrics_hourly where bucket_start = '2028-12-15 00:00:00+00'`))
	now = time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Zero(t, count(t, d, `select count(*) from api.metrics_hourly where bucket_start = '2028-12-15 00:00:00+00'`))
	require.Equal(t, 2, count(t, d, `select count(*) from api.metrics_daily`), "daily rows are never dropped")

	// a fenced-off leader drops and deletes nothing
	before, _ := lists(t, d)
	exec(t, d, `insert into api.ingest_dedup (msg_id, tenant_id, applied_at) values ('fenced', $1, '2020-01-01')`, tenant)
	jobstest.FenceOff(t, d)
	now = time.Date(2032, 3, 1, 0, 0, 0, 0, time.UTC)
	_, err = r.Run(t.Context(), leader.Tx)
	require.ErrorIs(t, err, natsx.ErrFenced)
	after, _ := lists(t, d)
	require.Equal(t, before, after)
	require.Equal(t, 1, count(t, d, `select count(*) from api.ingest_dedup where msg_id = 'fenced'`))
}

func lists(t *testing.T, d *pdb.DB) (map[time.Time]bool, map[time.Time]bool) {
	t.Helper()
	days, months := map[time.Time]bool{}, map[time.Time]bool{}
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		ds, err := q.ListMinutePartitions(t.Context())
		if err != nil {
			return err
		}
		for _, x := range ds {
			days[x.UTC()] = true
		}
		ms, err := q.ListHourlyPartitions(t.Context())
		for _, x := range ms {
			months[x.UTC()] = true
		}
		return err
	}))
	return days, months
}

// TestRefusesBelowFloor: the rfp keeps api aggregates 24 months, so a
// shorter hourly horizon is refused by the constructor as well as by the
// config loader, and never clamped.
func TestRefusesBelowFloor(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	p, err := apipartition.New(clock, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	for _, months := range []int{23, 12, 0, -1} {
		_, err := apiretention.New(config.APIRetention{MinuteDays: 30, HourlyMonths: months}, p, clock,
			slog.New(slog.DiscardHandler), prometheus.NewRegistry())
		require.ErrorIs(t, err, apiretention.ErrBelowFloor, "%d months", months)
	}
	_, err = apiretention.New(config.APIRetention{MinuteDays: 30, HourlyMonths: 24}, p, clock,
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	_, err = config.LoadAPIRetention(config.FromMap(map[string]string{
		"API_MINUTE_RETENTION_DAYS": "30", "API_HOURLY_RETENTION_MONTHS": "23",
	}))
	require.ErrorContains(t, err, "API_HOURLY_RETENTION_MONTHS")
}

// TestRefusesMinuteHorizonBelowLateDataWindow: the writer accepts points as
// old as API_METRICS keeps messages (7 days), so a minute horizon that drops
// their partitions earlier would discard a backlog the queue kept safe
//. 8 days is the floor, in the constructor and the loader.
func TestRefusesMinuteHorizonBelowLateDataWindow(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	p, err := apipartition.New(clock, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	for _, days := range []int{7, 2} {
		_, err := apiretention.New(config.APIRetention{MinuteDays: days, HourlyMonths: 36}, p, clock,
			slog.New(slog.DiscardHandler), prometheus.NewRegistry())
		require.Error(t, err, "%d days", days)
		_, err = config.LoadAPIRetention(config.FromMap(map[string]string{
			"API_MINUTE_RETENTION_DAYS": strconv.Itoa(days), "API_HOURLY_RETENTION_MONTHS": "36",
		}))
		require.ErrorContains(t, err, "API_MINUTE_RETENTION_DAYS", "%d days", days)
	}
	_, err = apiretention.New(config.APIRetention{MinuteDays: 8, HourlyMonths: 36}, p, clock,
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
}
