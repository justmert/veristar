package apirollup_test

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/jobs/apirollup"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
)

// dims is every dimension of a metric row except its time.
type dims struct {
	Tenant  uuid.UUID
	Service string
	Method  string
	Route   string
	Status  int16
	Source  string
}

// agg is the measure of a row.
type agg struct {
	Counts [15]int64
	Sum    float64
	Count  int64
	Lossy  bool
}

func (a *agg) add(b agg) {
	for i := range a.Counts {
		a.Counts[i] += b.Counts[i]
	}
	// left to right in time order, as the rollup sums
	a.Sum += b.Sum
	a.Count += b.Count
	a.Lossy = a.Lossy || b.Lossy
}

type key struct {
	D     dims
	Start time.Time
}

type minuteRow struct {
	key
	agg
}

// genMinutes makes a random sparse set of minute rows over every
// combination of the dimension values below, between from and to.
func genMinutes(rng *rand.Rand, tenants []uuid.UUID, from, to time.Time) []minuteRow {
	var out []minuteRow
	for m := from; m.Before(to); m = m.Add(time.Minute) {
		for _, tn := range tenants {
			for _, svc := range []string{"api", ""} {
				for _, meth := range []string{"GET", "POST"} {
					for _, route := range []string{"/a", "/b/{id}", ""} {
						for _, st := range []int16{200, 500, 0} {
							for _, src := range []string{"metric", "span"} {
								if rng.IntN(9) != 0 {
									continue
								}
								var a agg
								for i := range a.Counts {
									if rng.IntN(3) == 0 {
										a.Counts[i] = int64(rng.IntN(50))
									}
									a.Count += a.Counts[i]
								}
								a.Sum = rng.Float64() * float64(a.Count+1)
								a.Lossy = rng.IntN(10) == 0
								out = append(out, minuteRow{key{dims{tn, svc, meth, route, st, src}, m}, a})
							}
						}
					}
				}
			}
		}
	}
	return out
}

func hourOf(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }
func dayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// insertMinutes writes rows and marks their hours pending, as the writer
// does in one transaction.
func insertMinutes(t *testing.T, d *pdb.DB, rows []minuteRow) {
	t.Helper()
	err := d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		// copy is refused under row-level security, so the rows go in as
		// parallel arrays; counts travel as array literals because unnest
		// would flatten a two-dimensional array
		n := len(rows)
		ten, svc, meth, route, src := make([]uuid.UUID, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		start, counts := make([]time.Time, n), make([]string, n)
		status, sum, count, lossy := make([]int16, n), make([]float64, n), make([]int64, n), make([]bool, n)
		marks := map[[2]any]bool{}
		for i, r := range rows {
			ten[i], start[i], svc[i], meth[i], route[i], status[i], src[i] = r.D.Tenant, r.Start, r.D.Service, r.D.Method, r.D.Route, r.D.Status, r.D.Source
			counts[i] = arrayLiteral(r.Counts)
			sum[i], count[i], lossy[i] = r.Sum, r.Count, r.Lossy
			marks[[2]any{r.D.Tenant, hourOf(r.Start)}] = true
		}
		_, err := tx.Exec(t.Context(), `
insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy)
select t, b, s, m, r, st, so, c::bigint[], su, co, l
from unnest($1::uuid[], $2::timestamptz[], $3::text[], $4::text[], $5::text[], $6::smallint[], $7::text[], $8::text[],
            $9::float8[], $10::bigint[], $11::boolean[]) as x(t, b, s, m, r, st, so, c, su, co, l)`,
			ten, start, svc, meth, route, status, src, counts, sum, count, lossy)
		if err != nil {
			return err
		}
		for m := range marks {
			if _, err := tx.Exec(t.Context(), `insert into api.rollup_pending (tenant_id, hour) values ($1, $2) on conflict do nothing`, m[0], m[1]); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// addMinutes adds to existing minute rows the way the writer's upsert does.
func addMinutes(t *testing.T, d *pdb.DB, rows []minuteRow) {
	t.Helper()
	err := d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		for _, r := range rows {
			_, err := tx.Exec(t.Context(), `
insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
on conflict (tenant_id, bucket_start, service, method, route, status_code, source, traffic_class) do update
  set counts = api.add_buckets(api.metrics_minute.counts, excluded.counts),
      sum = api.metrics_minute.sum + excluded.sum, count = api.metrics_minute.count + excluded.count,
      lossy = api.metrics_minute.lossy or excluded.lossy`,
				r.D.Tenant, r.Start, r.D.Service, r.D.Method, r.D.Route, r.D.Status, r.D.Source, r.Counts[:], r.Sum, r.Count, r.Lossy)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(t.Context(), `insert into api.rollup_pending (tenant_id, hour) values ($1, $2) on conflict do nothing`,
				r.D.Tenant, hourOf(r.Start)); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// readRows reads a whole metric table, in a stable order.
func readRows(t *testing.T, d *pdb.DB, table string) map[key]agg {
	t.Helper()
	out := map[key]agg{}
	err := d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		rows, err := tx.Query(t.Context(), `select tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy from `+
			pgx.Identifier{"api", table}.Sanitize())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k key
			var a agg
			var counts []int64
			if err := rows.Scan(&k.D.Tenant, &k.Start, &k.D.Service, &k.D.Method, &k.D.Route, &k.D.Status, &k.D.Source,
				&counts, &a.Sum, &a.Count, &a.Lossy); err != nil {
				return err
			}
			if len(counts) != 15 {
				return fmt.Errorf("%d buckets", len(counts))
			}
			copy(a.Counts[:], counts)
			k.Start = k.Start.UTC()
			out[k] = a
		}
		return rows.Err()
	})
	require.NoError(t, err)
	return out
}

// rollUp computes in go what the hourly or daily table must hold, folding
// rows in time order.
func rollUp(rows map[key]agg, trunc func(time.Time) time.Time) map[key]agg {
	keys := make([]key, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Start.Before(keys[j].Start) })
	out := map[key]agg{}
	for _, k := range keys {
		ok := key{k.D, trunc(k.Start)}
		a := out[ok]
		a.add(rows[k])
		out[ok] = a
	}
	return out
}

func pendingCount(t *testing.T, d *pdb.DB) int {
	t.Helper()
	var n int
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select count(*) from api.rollup_pending`).Scan(&n)
	}))
	return n
}

func ensurePartitions(t *testing.T, d *pdb.DB, days []time.Time, months []time.Time) {
	t.Helper()
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		q := jobsq.New(tx)
		for _, day := range days {
			if _, err := q.CreateMinutePartition(t.Context(), day); err != nil {
				return err
			}
		}
		for _, m := range months {
			if _, err := q.CreateHourlyPartition(t.Context(), m); err != nil {
				return err
			}
		}
		return nil
	}))
}

func TestRollup(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, lock := jobstest.Leader(t, srv, d, "rollup-test")
	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)

	// three hours either side of a utc midnight, so hours of two days are
	// rolled up and both days are recomputed
	from := time.Date(2031, 3, 1, 22, 0, 0, 0, time.UTC)
	to := from.Add(4 * time.Hour)
	ensurePartitions(t, d,
		[]time.Time{time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2031, 3, 2, 0, 0, 0, 0, time.UTC)},
		[]time.Time{time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC)})
	rng := rand.New(rand.NewPCG(9, 9))
	tenants := []uuid.UUID{uuid.New(), uuid.New()}
	minutes := genMinutes(rng, tenants, from, to)
	require.Greater(t, len(minutes), 1000)
	insertMinutes(t, d, minutes)

	check := func(t *testing.T) (map[key]agg, map[key]agg) {
		t.Helper()
		minute := readRows(t, d, "metrics_minute")
		hourly := readRows(t, d, "metrics_hourly")
		daily := readRows(t, d, "metrics_daily")
		// hourly sums equal minute sums for every dimension combination,
		// and daily sums equal hourly sums; bucket arrays, counts and the
		// lossy flag exactly, float sums to the bit because both fold in
		// time order
		require.Equal(t, rollUp(minute, hourOf), hourly)
		require.Equal(t, rollUp(hourly, dayOf), daily)
		return hourly, daily
	}

	res, err := r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, 2*4, res.Hours, "4 hours of 2 tenants")
	require.Equal(t, 2*2, res.Days, "2 days of 2 tenants")
	require.Zero(t, pendingCount(t, d))
	hourly, daily := check(t)
	require.Len(t, daily, len(rollUp(hourly, dayOf)))

	// metric and span rows of the same dimensions stay separate rows
	both := 0
	for k := range hourly {
		if k.D.Source != "metric" {
			continue
		}
		span := k
		span.D.Source = "span"
		if _, ok := hourly[span]; ok {
			both++
		}
	}
	require.Positive(t, both, "some dimensions have rows of both sources")
	sources := map[string]int{}
	for k := range daily {
		sources[k.D.Source]++
	}
	require.Positive(t, sources["metric"])
	require.Positive(t, sources["span"])

	// a run with nothing pending changes nothing
	res, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Zero(t, res.Hours)
	h2, d2 := check(t)
	require.Equal(t, hourly, h2)
	require.Equal(t, daily, d2)

	// recomputing every hour again over unchanged minutes changes nothing:
	// a claimed hour is replaced, never added twice
	insertPending := func(hours ...time.Time) {
		require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
			for _, tn := range tenants {
				for _, h := range hours {
					if _, err := tx.Exec(t.Context(), `insert into api.rollup_pending (tenant_id, hour) values ($1, $2) on conflict do nothing`, tn, h); err != nil {
						return err
					}
				}
			}
			return nil
		}))
	}
	var all []time.Time
	for h := from; h.Before(to); h = h.Add(time.Hour) {
		all = append(all, h)
	}
	insertPending(all...)
	res, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, 8, res.Hours)
	h3, d3 := check(t)
	require.Equal(t, hourly, h3)
	require.Equal(t, daily, d3)

	// late data for an early hour changes that hour and its day only
	late := minuteRow{key{dims{tenants[0], "api", "GET", "/a", 200, "metric"}, from.Add(17 * time.Minute)},
		agg{Counts: [15]int64{0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}, Sum: 21.5, Count: 5}}
	addMinutes(t, d, []minuteRow{late})
	res, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Hours)
	require.Equal(t, 1, res.Days)
	h4, d4 := check(t)
	changed := 0
	for k, v := range h4 {
		if hourly[k] != v {
			changed++
			require.Equal(t, key{late.D, hourOf(late.Start)}, k)
		}
	}
	require.Equal(t, 1, changed)
	for k, v := range d4 {
		if daily[k] != v {
			require.Equal(t, key{late.D, dayOf(late.Start)}, k)
		}
	}
	require.Equal(t, hourly[key{late.D, hourOf(late.Start)}].Count+5, h4[key{late.D, hourOf(late.Start)}].Count)

	// a fenced-off leader commits nothing and leaves the marks for its
	// successor
	insertPending(all[0])
	jobstest.FenceOff(t, d)
	_, err = r.Run(t.Context(), leader.Tx)
	require.ErrorIs(t, err, natsx.ErrFenced)
	require.Equal(t, 2, pendingCount(t, d), "the fenced run's claim rolled back")
	h5, d5 := check(t)
	require.Equal(t, h4, h5)
	require.Equal(t, d4, d5)

	// the successor takes the lock and finishes the work
	require.NoError(t, lock.Release(context.Background()))
	next, _ := jobstest.Leader(t, srv, d, "rollup-test-2")
	res, err = r.Run(t.Context(), next.Tx)
	require.NoError(t, err)
	require.Equal(t, 2, res.Hours)
	check(t)
}

// TestRollupSkipsHourWithoutMinutes keeps hourly rows whose minutes are
// gone, and does not let that one mark stop the rollup of every other
// tenant: the hour is skipped and counted, the others are
// recomputed.
func TestRollupSkipsHourWithoutMinutes(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, _ := jobstest.Leader(t, srv, d, "rollup-gone")
	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)

	tenant, other := uuid.New(), uuid.New()
	hour := time.Date(2031, 3, 1, 5, 0, 0, 0, time.UTC)
	later := time.Date(2031, 3, 2, 7, 0, 0, 0, time.UTC)
	ensurePartitions(t, d, []time.Time{dayOf(hour), dayOf(later)}, []time.Time{time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC)})
	insertMinutes(t, d, []minuteRow{{key{dims{tenant, "api", "GET", "/a", 200, "metric"}, hour}, agg{Counts: [15]int64{1}, Sum: 0.001, Count: 1}}})
	_, err = r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	before := readRows(t, d, "metrics_hourly")
	require.Len(t, before, 1)

	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		if _, err := jobsq.New(tx).DropMinutePartition(t.Context(), dayOf(hour)); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `insert into api.rollup_pending (tenant_id, hour) values ($1, $2)`, tenant, hour)
		return err
	}))
	// another tenant's hour, claimed after the orphaned one (oldest first)
	insertMinutes(t, d, []minuteRow{{key{dims{other, "api", "GET", "/b", 200, "metric"}, later}, agg{Counts: [15]int64{2}, Sum: 0.002, Count: 2}}})
	res, err := r.Run(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Orphaned)
	require.Equal(t, 1, res.Hours, "the other tenant's hour is rolled up")
	after := readRows(t, d, "metrics_hourly")
	require.Equal(t, before[key{dims{tenant, "api", "GET", "/a", 200, "metric"}, hour}], after[key{dims{tenant, "api", "GET", "/a", 200, "metric"}, hour}],
		"the orphaned hour keeps its hourly rows")
	require.Equal(t, int64(2), after[key{dims{other, "api", "GET", "/b", 200, "metric"}, later}].Count)
	require.Zero(t, pendingCount(t, d))
}

// TestRollupDayIsTwentyFourHoursInAnyTimeZone runs the rollup in a session
// whose time zone has a daylight saving change on the day: the daily row
// must still hold all 24 utc hours.
func TestRollupDayIsTwentyFourHoursInAnyTimeZone(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, _ := jobstest.Leader(t, srv, d, "rollup-tz")
	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)

	// europe moves its clocks forward on sunday 2031-03-30
	day := time.Date(2031, 3, 30, 0, 0, 0, 0, time.UTC)
	ensurePartitions(t, d, []time.Time{day}, []time.Time{time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC)})
	tenant := uuid.New()
	var rows []minuteRow
	for h := range 24 {
		rows = append(rows, minuteRow{key{dims{tenant, "api", "GET", "/a", 200, "metric"}, day.Add(time.Duration(h)*time.Hour + 30*time.Minute)},
			agg{Counts: [15]int64{1}, Sum: 0.001, Count: 1}})
	}
	insertMinutes(t, d, rows)
	berlin := func(ctx context.Context, fn func(pgx.Tx) error) error {
		return leader.Tx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `set local timezone = 'Europe/Berlin'`); err != nil {
				return err
			}
			return fn(tx)
		})
	}
	res, err := r.Run(t.Context(), berlin)
	require.NoError(t, err)
	require.Equal(t, 24, res.Hours)
	daily := readRows(t, d, "metrics_daily")
	require.Equal(t, int64(24), daily[key{dims{tenant, "api", "GET", "/a", 200, "metric"}, day}].Count,
		"the daily row must cover 24 utc hours, not a calendar day of the session time zone")
}

func arrayLiteral(c [15]int64) string {
	b := []byte{'{'}
	for i, v := range c {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, v, 10)
	}
	return string(append(b, '}'))
}

// a full batch of marks whose hours a writer still holds made no
// progress, and since busy marks go back as the oldest, Run claimed and
// restored the same batch back to back until the writer let go. now a batch
// without progress ends the run. separately, the 24 hour consistency check
// ran in every 5 minute run; it now runs at most once per DriftEvery.
func TestRollupStopsOnBusyBatchesAndChecksDriftHourly(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	leader, _ := jobstest.Leader(t, srv, d, "rollup-busy")
	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	var txs int
	counted := func(ctx context.Context, fn func(pgx.Tx) error) error {
		txs++
		return leader.Tx(ctx, fn)
	}

	// an empty run: one batch and the consistency check
	_, err = r.Run(t.Context(), counted)
	require.NoError(t, err)
	require.Equal(t, 2, txs, "batch and consistency check")
	// the next run within the hour skips the check
	txs = 0
	_, err = r.Run(t.Context(), counted)
	require.NoError(t, err)
	require.Equal(t, 1, txs, "the consistency check runs at most once per DriftEvery")

	// more marks than one batch, every hour held by an open writer
	// transaction, as a writer's batch holds its tenants' hours to commit
	tenant := uuid.New()
	first := time.Date(2031, 4, 1, 0, 0, 0, 0, time.UTC)
	const hours = apirollup.ClaimBatch + 100
	var days []time.Time
	for h := 0; h < hours; h += 24 {
		days = append(days, dayOf(first.Add(time.Duration(h)*time.Hour)))
	}
	ensurePartitions(t, d, days, []time.Time{first, first.AddDate(0, 1, 0)})
	writer, err := d.Pool(pdb.RoleJobs)
	require.NoError(t, err)
	wtx, err := writer.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = wtx.Rollback(context.Background()) })
	for h := range hours {
		hour := first.Add(time.Duration(h) * time.Hour)
		_, err := wtx.Exec(t.Context(), `select pg_advisory_xact_lock_shared(api.rollup_lock_key($1, $2))`, tenant, hour)
		require.NoError(t, err)
	}
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `insert into api.rollup_pending (tenant_id, hour)
			select $1, $2::timestamptz + make_interval(hours => g) from generate_series(0, $3::int - 1) g`, tenant, first, hours)
		return err
	}))
	txs = 0
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := r.Run(ctx, counted)
	require.NoError(t, err, "the run must end while the writer holds the hours")
	require.Equal(t, 1, txs, "one batch without progress ends the run")
	require.Equal(t, apirollup.ClaimBatch, res.Busy)
	require.Equal(t, hours, pendingCount(t, d), "every mark is kept for a later run")

	// once the writer committed, the next run rolls everything up
	require.NoError(t, wtx.Rollback(t.Context()))
	res, err = r.Run(t.Context(), counted)
	require.NoError(t, err)
	require.Equal(t, hours, res.Hours)
	require.Zero(t, pendingCount(t, d))
}
