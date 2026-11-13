package apipartition_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/jobs/apipartition"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func partitions(t *testing.T, d *pdb.DB) (map[time.Time]bool, map[time.Time]bool) {
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

// TestEnsureAhead moves an injected clock across a month and a year end
// and checks that 7 days of minute and 3 months of hourly partitions exist
// ahead of it every time, and that a second run creates nothing.
func TestEnsureAhead(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, _ := jobstest.Leader(t, srv, d, "partition-test")

	now := time.Date(2031, 12, 28, 23, 59, 0, 0, time.UTC)
	p, err := apipartition.New(func() time.Time { return now }, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)

	res, err := p.Ensure(t.Context(), apipartition.TxFunc(leader.Tx))
	require.NoError(t, err)
	require.Len(t, res.MinuteDays, 8, "today and 7 days ahead")
	require.Len(t, res.HourlyMonths, 4, "this month and 3 ahead")
	days, months := partitions(t, d)
	for i := range 8 {
		require.True(t, days[day(2031, 12, 28).AddDate(0, 0, i)], "day +%d", i)
	}
	require.True(t, days[day(2032, 1, 4)])
	require.False(t, days[day(2032, 1, 5)])
	for _, m := range []time.Time{day(2031, 12, 1), day(2032, 1, 1), day(2032, 2, 1), day(2032, 3, 1)} {
		require.True(t, months[m], "month %s", m.Format("2006-01"))
	}
	require.False(t, months[day(2032, 4, 1)])

	// idempotent
	res, err = p.Ensure(t.Context(), apipartition.TxFunc(leader.Tx))
	require.NoError(t, err)
	require.Empty(t, res.MinuteDays)
	require.Empty(t, res.HourlyMonths)

	// a day later one new day and, across the month end, no new month yet
	now = now.Add(24 * time.Hour)
	res, err = p.Ensure(t.Context(), apipartition.TxFunc(leader.Tx))
	require.NoError(t, err)
	require.Equal(t, []time.Time{day(2032, 1, 5)}, res.MinuteDays)
	require.Empty(t, res.HourlyMonths)
	now = time.Date(2032, 1, 1, 0, 0, 0, 0, time.UTC)
	res, err = p.Ensure(t.Context(), apipartition.TxFunc(leader.Tx))
	require.NoError(t, err)
	require.Equal(t, []time.Time{day(2032, 4, 1)}, res.HourlyMonths)

	// the writer can store a point in the last day ahead
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `
insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
values (gen_random_uuid(), '2032-01-08 23:59:00+00', 'api', 'GET', '/', 200, 'metric', '{1,0,0,0,0,0,0,0,0,0,0,0,0,0,0}', 0.001, 1)`)
		return err
	}))

	// a fenced-off leader creates nothing
	jobstest.FenceOff(t, d)
	now = time.Date(2033, 6, 1, 0, 0, 0, 0, time.UTC)
	_, err = p.Ensure(t.Context(), apipartition.TxFunc(leader.Tx))
	require.ErrorIs(t, err, natsx.ErrFenced)
	days, months = partitions(t, d)
	require.False(t, days[day(2033, 6, 1)])
	require.False(t, months[day(2033, 6, 1)])
}

func TestCutoffs(t *testing.T) {
	now := time.Date(2031, 6, 15, 13, 0, 0, 0, time.UTC)
	require.Equal(t, day(2031, 5, 16), apipartition.MinuteCutoff(now, 30))
	require.Equal(t, day(2028, 6, 1), apipartition.HourlyCutoff(now, 36))
	require.Equal(t, day(2029, 6, 1), apipartition.HourlyCutoff(now, 24))
	// a clock in another zone means the same utc day
	ist := time.FixedZone("ist", 3*3600)
	require.Equal(t, day(2031, 5, 16), apipartition.MinuteCutoff(time.Date(2031, 6, 16, 1, 0, 0, 0, ist), 30))
}
