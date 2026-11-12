package apicapacity_test

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/jobsq"
	"github.com/justmert/stellar-analytics/internal/jobs/apicapacity"
	"github.com/justmert/stellar-analytics/internal/jobs/apirollup"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
)

// the steady state of the design: about 2,000 active series, each
// reporting every minute
const (
	tenants        = 10
	servicesPer    = 3 // 30 services in all, as in the this stage volume run
	seriesPerSvc   = 67
	measuredMinute = 60
)

var methods = []string{"GET", "GET", "GET", "POST", "PUT", "DELETE"}
var statuses = []int16{200, 200, 200, 201, 204, 400, 404, 500}

// series is one row key without its time.
type series struct {
	tenant  uuid.UUID
	service string
	method  string
	route   string
	status  int16
	source  string
}

func buildSeries(rng *rand.Rand) []series {
	var out []series
	for range tenants {
		tn := uuid.New()
		for s := range servicesPer {
			svc := fmt.Sprintf("svc-%s-%d", tn.String()[:8], s)
			for i := range seriesPerSvc {
				// normalized routes of the length real apis have
				route := fmt.Sprintf("/v1/accounts/{id}/operations/%d/{param}", i/len(statuses))
				src := "metric"
				if rng.IntN(4) == 0 {
					src = "span"
				}
				out = append(out, series{tn, svc, methods[rng.IntN(len(methods))], route, statuses[i%len(statuses)], src})
			}
		}
	}
	return out
}

func literal(c [15]int64) string {
	b := []byte{'{'}
	for i, v := range c {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, v, 10)
	}
	return string(append(b, '}'))
}

// TestMeasure writes an hour of minute rows for about 2,000 series, rolls
// them up with the real rollup, vacuums, and measures bytes per row with
// apicapacity.Measure. the logged figures are the ones in
// docs/reference/capacity.md; run with -v to read them.
func TestMeasure(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, _ := jobstest.Leader(t, srv, d, "capacity-test")

	rng := rand.New(rand.NewPCG(1, 2))
	all := buildSeries(rng)
	start := time.Date(2031, 3, 2, 10, 0, 0, 0, time.UTC)
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		if _, err := q.CreateMinutePartition(t.Context(), time.Date(2031, 3, 2, 0, 0, 0, 0, time.UTC)); err != nil {
			return err
		}
		_, err := q.CreateHourlyPartition(t.Context(), time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC))
		return err
	}))

	for m := range measuredMinute {
		at := start.Add(time.Duration(m) * time.Minute)
		n := len(all)
		ten, svc, meth, route, src, counts := make([]uuid.UUID, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		status, sum, count := make([]int16, n), make([]float64, n), make([]int64, n)
		for i, s := range all {
			var c [15]int64
			var total int64
			// a few busy buckets around the median, as latency histograms are
			for range 1 + rng.IntN(4) {
				b := 2 + rng.IntN(7)
				v := int64(1 + rng.IntN(40))
				c[b] += v
				total += v
			}
			ten[i], svc[i], meth[i], route[i], status[i], src[i] = s.tenant, s.service, s.method, s.route, s.status, s.source
			counts[i], sum[i], count[i] = literal(c), float64(total)*0.03*rng.Float64(), total
		}
		require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `
insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
select x.t, $2, x.s, x.m, x.r, x.st, x.so, x.c::bigint[], x.su, x.co
from unnest($1::uuid[], $3::text[], $4::text[], $5::text[], $6::smallint[], $7::text[], $8::text[], $9::float8[], $10::bigint[])
  as x(t, s, m, r, st, so, c, su, co)
on conflict do nothing`, ten, at, svc, meth, route, status, src, counts, sum, count)
			if err != nil {
				return err
			}
			_, err = tx.Exec(t.Context(), `insert into api.rollup_pending (tenant_id, hour)
select distinct t, date_trunc('hour', $2::timestamptz, 'UTC') from unnest($1::uuid[]) t on conflict do nothing`, ten, at)
			return err
		}))
	}

	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	_, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)

	// sizes after vacuum, so dead tuples of the upserts do not count
	su, err := pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer su.Close(t.Context())
	_, err = su.Exec(t.Context(), `vacuum (analyze) api.metrics_minute, api.metrics_hourly, api.metrics_daily`)
	require.NoError(t, err)

	var measured []apicapacity.Table
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		var err error
		measured, err = apicapacity.Measure(t.Context(), tx)
		return err
	}))
	activeSeries := 0
	seen := map[series]bool{}
	for _, s := range all {
		if !seen[s] {
			seen[s] = true
			activeSeries++
		}
	}
	for _, m := range measured {
		t.Logf("%s: %d rows, %d bytes in %d partitions, %.1f bytes per row; %d empty partitions, %d bytes",
			m.Name, m.Rows, m.Bytes, m.Partitions, m.BytesPerRow(), m.EmptyPartitions, m.EmptyBytes)
		require.Positive(t, m.Rows, m.Name)
		// a row holds 15 bigints alone (120 bytes); anything far outside
		// this band means the measurement read the wrong thing
		require.InDelta(t, 400, m.BytesPerRow(), 300, m.Name)
	}
	require.Equal(t, int64(activeSeries*measuredMinute), measured[0].Rows)
	require.Equal(t, int64(activeSeries), measured[1].Rows)

	load := apicapacity.Load{
		MinuteRowsPerDay: 2000 * 1440,
		HourlyRowsPerDay: 2000 * 24,
		DailyRowsPerDay:  2000,
	}
	p, err := apicapacity.Project(measured, load, 30, 36)
	require.NoError(t, err)
	t.Logf("active series measured: %d", activeSeries)
	t.Logf("projection at 2,000 series: minute 30 days %.2f GiB, hourly 36 months %.2f GiB, daily %.3f GiB per year",
		p.MinuteBytes/(1<<30), p.HourlyBytes/(1<<30), p.DailyBytesPerYear/(1<<30))
}
