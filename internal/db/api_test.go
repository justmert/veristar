package db_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/db/writerq"
	"github.com/justmert/veristar/internal/histo"
)

// every api relation, for the "reads nothing else" checks
var apiRelations = []string{
	"api.metrics_minute", "api.metrics_hourly", "api.metrics_daily", "api.series_state",
	"api.resource_seen", "api.ingest_dedup", "api.route_registry", "api.ingest_warnings",
	"api.rollup_pending",
}

func TestAPIBoundaries(t *testing.T) {
	readable := map[pdb.Role]map[string]bool{
		pdb.RoleCPOperator: {"api.metrics_hourly": true, "api.metrics_daily": true},
		pdb.RoleCPSystem:   {},
	}
	for role, ok := range readable {
		for _, rel := range apiRelations {
			_, err := count(t, role, nil, `select count(*) from `+rel)
			if ok[rel] {
				require.NoError(t, err, "%s reading %s", role, rel)
			} else {
				requireDenied(t, err, string(role)+" reading "+rel)
			}
		}
	}
	// operators see the aggregates of every tenant, never minutes
	for _, rel := range []string{"api.metrics_hourly", "api.metrics_daily"} {
		n, err := count(t, pdb.RoleCPOperator, nil, `select count(*) from `+rel+` where tenant_id in ($1, $2)`, seed.tenantA, seed.tenantB)
		require.NoError(t, err)
		require.Equal(t, int64(2), n, rel)
	}

	// the writer produces minutes, never hours or days, and deletes nothing
	for _, rel := range []string{"api.metrics_hourly", "api.metrics_daily"} {
		_, err := count(t, pdb.RoleMetricsWriter, nil, `select count(*) from `+rel)
		requireDenied(t, err, "metrics_writer reading "+rel)
	}
	for _, rel := range apiRelations {
		_, err := attempt(t, pdb.RoleMetricsWriter, nil, `delete from `+rel)
		requireDenied(t, err, "metrics_writer deleting from "+rel)
	}

	// partitions are the jobs role's alone
	for _, role := range []pdb.Role{pdb.RoleCPTenant, pdb.RoleCPOperator, pdb.RoleMetricsWriter, pdb.RoleCPSystem} {
		_, err := attempt(t, role, &seed.tenantA, `select api.create_minute_partition('2033-01-01')`)
		requireDenied(t, err, string(role)+" creating a partition")
		_, err = attempt(t, role, &seed.tenantA, `select api.drop_minute_partition(current_date)`)
		requireDenied(t, err, string(role)+" dropping a partition")
	}
	_, err := attempt(t, pdb.RoleJobs, nil, `select api.protect_partition('metrics_minute')`)
	requireDenied(t, err, "jobs calling the internal partition helper")
	_, err = attempt(t, pdb.RoleJobs, nil, `drop table api.metrics_minute`)
	requireDenied(t, err, "jobs dropping a metric table")

	// a tenant reads its percentiles with the shared functions
	var p50 float64
	require.NoError(t, func() error {
		p, err := conn.Pool(pdb.RoleCPTenant)
		require.NoError(t, err)
		tx, err := p.Begin(context.Background())
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(context.Background()) }()
		_, err = tx.Exec(context.Background(), `select set_config('app.tenant_id', $1, true)`, seed.tenantA.String())
		require.NoError(t, err)
		return tx.QueryRow(context.Background(),
			`select api.histogram_quantile(0.5, api.sum_buckets(counts)) from api.metrics_minute`).Scan(&p50)
	}())
	require.InDelta(t, 0.01+(0.025-0.01)*0.5, p50, 1e-12, "rank 2 of 4 lies half way through bucket 3")
}

func minuteParams(tenant uuid.UUID, at time.Time, route, source string, counts histo.Counts, sum float64, lossy bool) writerq.AddMinuteParams {
	return writerq.AddMinuteParams{
		TenantID: tenant, BucketStart: at, Service: "checkout", Method: "POST", Route: route,
		StatusCode: 201, Source: source, TrafficClass: "app", Counts: counts.Slice(), Sum: sum, Count: counts.Total(), Lossy: lossy,
	}
}

func execBatch(exec func(func(int, error))) error {
	var first error
	exec(func(_ int, err error) {
		if err != nil && first == nil {
			first = err
		}
	})
	return first
}

func TestAPIWriterQueries(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	now := time.Now().UTC()
	minute := now.Truncate(time.Minute)
	day := now.Truncate(24 * time.Hour)
	c1 := histo.Counts{0, 1, 2, 3}
	c2 := histo.Counts{14: 2, 0: 1}
	hash := make([]byte, 32)
	hash[0] = 7

	require.NoError(t, conn.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgxTx) error {
		q := writerq.New(tx)
		n, err := q.RecordIngestMessage(ctx, writerq.RecordIngestMessageParams{MsgID: "msg-" + tenant.String(), TenantID: tenant, AppliedAt: now})
		require.NoError(t, err)
		require.Equal(t, int64(1), n)
		n, err = q.RecordIngestMessage(ctx, writerq.RecordIngestMessageParams{MsgID: "msg-" + tenant.String(), TenantID: tenant, AppliedAt: now})
		require.NoError(t, err)
		require.Zero(t, n, "a redelivered message id is refused")

		require.NoError(t, execBatch(q.AddMinute(ctx, []writerq.AddMinuteParams{
			minuteParams(tenant, minute, "/orders", "metric", c1, 0.5, false),
			minuteParams(tenant, minute, "/orders", "metric", c2, 30.25, true),
			minuteParams(tenant, minute, "/orders", "span", c1, 0.5, false),
		}).Exec))
		// a probe of the same route is a row of its own (00037)
		probe := minuteParams(tenant, minute, "/orders", "metric", c1, 0.5, false)
		probe.TrafficClass = "probe"
		require.NoError(t, execBatch(q.AddMinute(ctx, []writerq.AddMinuteParams{probe}).Exec))
		require.NoError(t, execBatch(q.MarkRollupPending(ctx, []writerq.MarkRollupPendingParams{
			{TenantID: tenant, BucketStart: minute}, {TenantID: tenant, BucketStart: minute.Add(time.Second)},
		}).Exec))

		require.NoError(t, execBatch(q.UpsertSeriesState(ctx, []writerq.UpsertSeriesStateParams{
			{TenantID: tenant, SeriesHash: hash, StartUnixNano: 100, LastUnixNano: 200, LastCount: 6, LastSum: 1, LastCounts: c1.Slice(), SeenAt: now},
			{TenantID: tenant, SeriesHash: hash, StartUnixNano: 100, LastUnixNano: 300, LastCount: 9, LastSum: 2, LastCounts: c1.Slice(), SeenAt: now},
			{TenantID: tenant, SeriesHash: hash, StartUnixNano: 101, LastUnixNano: 250, LastCount: 3, LastSum: 3, LastCounts: c2.Slice(), SeenAt: now},
		}).Exec))
		states, err := q.GetSeriesStates(ctx, writerq.GetSeriesStatesParams{TenantID: tenant, SeriesHashes: [][]byte{hash}})
		require.NoError(t, err)
		require.Len(t, states, 2, "two start times are two streams")
		for _, s := range states {
			if s.StartUnixNano == 100 {
				require.Equal(t, int64(300), s.LastUnixNano)
				require.Equal(t, int64(9), s.LastCount)
			}
		}
		other, err := q.GetSeriesStates(ctx, writerq.GetSeriesStatesParams{TenantID: uuid.New(), SeriesHashes: [][]byte{hash}})
		require.NoError(t, err)
		require.Empty(t, other, "series are per tenant")

		require.NoError(t, execBatch(q.TouchResource(ctx, []writerq.TouchResourceParams{
			{TenantID: tenant, ResourceHash: hash, StartUnixNano: 100, SeenAt: now},
			{TenantID: tenant, ResourceHash: hash, StartUnixNano: 100, SeenAt: now.Add(time.Minute)},
		}).Exec))
		seen, err := q.GetResourcesSeen(ctx, writerq.GetResourcesSeenParams{TenantID: tenant, ResourceHashes: [][]byte{hash}})
		require.NoError(t, err)
		require.Len(t, seen, 1)
		require.WithinDuration(t, now, seen[0].FirstSeenAt, time.Millisecond)
		require.WithinDuration(t, now.Add(time.Minute), seen[0].LastSeenAt, time.Millisecond)

		for i, want := range []int64{1, 0} {
			n, err := q.RegisterRoute(ctx, writerq.RegisterRouteParams{TenantID: tenant, Day: day, Service: "checkout", Route: "/orders"})
			require.NoError(t, err)
			require.Equal(t, want, n, "registration %d", i)
		}
		routes, err := q.ListRoutes(ctx, writerq.ListRoutesParams{TenantID: tenant, Day: day})
		require.NoError(t, err)
		require.Equal(t, []writerq.ListRoutesRow{{Service: "checkout", Route: "/orders"}}, routes)

		for _, n := range []int64{1, 2} {
			require.NoError(t, q.AddIngestWarning(ctx, writerq.AddIngestWarningParams{TenantID: tenant, Day: day, Kind: "secret_in_path", Count: n}))
		}
		return nil
	}))

	c := superuser(t)
	var counts []int64
	var sum float64
	var total int64
	var lossy bool
	require.NoError(t, c.QueryRow(ctx, `select counts, sum, count, lossy from api.metrics_minute
		where tenant_id = $1 and source = 'metric' and traffic_class = 'app'`, tenant).Scan(&counts, &sum, &total, &lossy))
	require.Equal(t, []int64{1, 1, 2, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}, counts, "a second point adds its buckets")
	require.Equal(t, 30.75, sum)
	require.Equal(t, int64(9), total)
	require.True(t, lossy, "one lossy contribution makes the row lossy")
	var rows, pending, warnings int64
	require.NoError(t, c.QueryRow(ctx, `select count(*) from api.metrics_minute where tenant_id = $1`, tenant).Scan(&rows))
	require.Equal(t, int64(3), rows, "metric and span rows stay apart, and so do application and probe rows")
	require.NoError(t, c.QueryRow(ctx, `select count(*) from api.rollup_pending where tenant_id = $1 and hour = $2`,
		tenant, minute.Truncate(time.Hour)).Scan(&pending))
	require.Equal(t, int64(1), pending, "both marks name the same hour")
	require.NoError(t, c.QueryRow(ctx, `select count from api.ingest_warnings where tenant_id = $1`, tenant).Scan(&warnings))
	require.Equal(t, int64(3), warnings)

	// rows the table refuses: the writer must not get a silently changed row
	bad := []struct {
		name  string
		p     writerq.AddMinuteParams
		state string
	}{
		{"count differs from the buckets", func() writerq.AddMinuteParams {
			p := minuteParams(tenant, minute, "/x", "metric", c1, 1, false)
			p.Count++
			return p
		}(), "23514"},
		{"not the start of a minute", minuteParams(tenant, minute.Add(time.Second), "/x", "metric", c1, 1, false), "23514"},
		{"unknown source", minuteParams(tenant, minute, "/x", "log", c1, 1, false), "23514"},
		{"unknown traffic class", func() writerq.AddMinuteParams {
			p := minuteParams(tenant, minute, "/x", "metric", c1, 1, false)
			p.TrafficClass = "bot"
			return p
		}(), "23514"},
		{"wrong bucket count", func() writerq.AddMinuteParams {
			p := minuteParams(tenant, minute, "/x", "metric", c1, 1, false)
			p.Counts = []int64{6}
			p.Count = 6
			return p
		}(), "23514"},
		{"no partition for the time", minuteParams(tenant, time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), "/x", "metric", c1, 1, false), "23514"},
	}
	for _, b := range bad {
		err := conn.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgxTx) error {
			return execBatch(writerq.New(tx).AddMinute(ctx, []writerq.AddMinuteParams{b.p}).Exec)
		})
		require.Error(t, err, b.name)
		require.Equal(t, b.state, sqlState(err), "%s: %v", b.name, err)
	}
	// a stored row and a new point of different shape never meet in
	// api.add_buckets: the table's check refuses the new row first
	err := conn.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgxTx) error {
		p := minuteParams(tenant, minute, "/orders", "metric", c1, 1, false)
		p.Counts = append(p.Counts, 0)
		return execBatch(writerq.New(tx).AddMinute(ctx, []writerq.AddMinuteParams{p}).Exec)
	})
	require.Equal(t, "23514", sqlState(err), "%v", err)
}

type apiRow struct {
	Service, Method, Route, Source, Class string
	Status                         int16
	Counts                         []int64
	Sum                            float64
	Count                          int64
	Lossy                          bool
}

func apiRows(t *testing.T, tx pgx.Tx, table string, tenant uuid.UUID, from, to time.Time) []apiRow {
	t.Helper()
	rows, err := tx.Query(context.Background(), `
		select service, method, route, source, traffic_class, status_code, counts, sum, count, lossy from `+table+`
		where tenant_id = $1 and bucket_start >= $2 and bucket_start < $3
		order by service, method, route, source, traffic_class, status_code, bucket_start`, tenant, from, to)
	require.NoError(t, err)
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (apiRow, error) {
		var x apiRow
		return x, r.Scan(&x.Service, &x.Method, &x.Route, &x.Source, &x.Class, &x.Status, &x.Counts, &x.Sum, &x.Count, &x.Lossy)
	})
	require.NoError(t, err)
	return out
}

func TestAPIRollupQueries(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	hour := time.Now().UTC().Truncate(time.Hour)
	day := hour.Truncate(24 * time.Hour)

	// minute rows written by the writer in two minutes of the hour
	require.NoError(t, conn.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgxTx) error {
		q := writerq.New(tx)
		var ps []writerq.AddMinuteParams
		var marks []writerq.MarkRollupPendingParams
		for i, m := range []time.Duration{time.Minute, 2 * time.Minute} {
			at := hour.Add(m)
			ps = append(ps,
				minuteParams(tenant, at, "/orders", "metric", histo.Counts{1, 2, 3, int64(i)}, 0.1+float64(i)/3, false),
				minuteParams(tenant, at, "/orders", "span", histo.Counts{5: 1, 14: int64(i + 1)}, 11.5, i == 1),
				minuteParams(tenant, at, "/items", "metric", histo.Counts{9: 4}, 3.3, false),
			)
			// a load balancer's probe of the same route stays a row of its own
			probe := minuteParams(tenant, at, "/orders", "metric", histo.Counts{0: 50}, 0.2, false)
			probe.TrafficClass = "probe"
			ps = append(ps, probe)
			marks = append(marks, writerq.MarkRollupPendingParams{TenantID: tenant, BucketStart: at})
		}
		if err := execBatch(q.AddMinute(ctx, ps).Exec); err != nil {
			return err
		}
		return execBatch(q.MarkRollupPending(ctx, marks).Exec)
	}))

	// the rollup runs in one jobs transaction, rolled back at the end so the
	// shared seed's pending marks stay for the isolation tests
	p, err := conn.Pool(pdb.RoleJobs)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	q := jobsq.New(tx)

	claimed, err := q.ClaimRollupHours(ctx, 1000)
	require.NoError(t, err)
	var mine []time.Time
	for _, c := range claimed {
		if c.TenantID == tenant {
			mine = append(mine, c.Hour.UTC())
		}
	}
	require.Equal(t, []time.Time{hour}, mine, "both minutes marked the one hour")
	again, err := q.ClaimRollupHours(ctx, 1000)
	require.NoError(t, err)
	require.Empty(t, again, "claimed marks are gone")

	rollup := func() {
		_, err := q.DeleteHourlyRows(ctx, jobsq.DeleteHourlyRowsParams{TenantID: tenant, Hour: hour})
		require.NoError(t, err)
		n, err := q.RollupHour(ctx, jobsq.RollupHourParams{Hour: hour, TenantID: tenant})
		require.NoError(t, err)
		require.Equal(t, int64(4), n, "one hourly row per dimension, source and traffic class")
		_, err = q.DeleteDailyRows(ctx, jobsq.DeleteDailyRowsParams{TenantID: tenant, Day: day})
		require.NoError(t, err)
		n, err = q.RollupDay(ctx, jobsq.RollupDayParams{Day: day, TenantID: tenant})
		require.NoError(t, err)
		require.Equal(t, int64(4), n)
	}
	rollup()

	minutes := apiRows(t, tx, "api.metrics_minute", tenant, hour, hour.Add(time.Hour))
	hours := apiRows(t, tx, "api.metrics_hourly", tenant, hour, hour.Add(time.Hour))
	days := apiRows(t, tx, "api.metrics_daily", tenant, day, day.Add(24*time.Hour))
	require.Len(t, minutes, 8)

	// hourly rows are the sums of their minutes, dimension by dimension
	type key struct{ route, source, class string }
	want := map[key]apiRow{}
	for _, m := range minutes {
		k := key{m.Route, m.Source, m.Class}
		w, ok := want[k]
		if !ok {
			w = apiRow{Service: m.Service, Method: m.Method, Route: m.Route, Source: m.Source, Class: m.Class, Status: m.Status, Counts: make([]int64, histo.Buckets)}
		}
		for i := range m.Counts {
			w.Counts[i] += m.Counts[i]
		}
		w.Sum += m.Sum
		w.Count += m.Count
		w.Lossy = w.Lossy || m.Lossy
		want[k] = w
	}
	require.Len(t, hours, len(want))
	for _, h := range hours {
		require.Equal(t, want[key{h.Route, h.Source, h.Class}], h)
		var sqlQ float64
		require.NoError(t, tx.QueryRow(ctx, `select api.histogram_quantile(0.95, $1)`, h.Counts).Scan(&sqlQ))
		goQ, err := histo.Quantile(0.95, h.Counts)
		require.NoError(t, err)
		require.Equal(t, math.Float64bits(goQ), math.Float64bits(sqlQ), "p95 of %v", h.Counts)
	}
	require.Equal(t, hours, days, "one hour of data makes days equal to hours")

	// a re-run changes nothing, to the bit
	rollup()
	require.Equal(t, hours, apiRows(t, tx, "api.metrics_hourly", tenant, hour, hour.Add(time.Hour)))
	require.Equal(t, days, apiRows(t, tx, "api.metrics_daily", tenant, day, day.Add(24*time.Hour)))
}

func TestAPIPartitionAndRetentionQueries(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, conn.WithRole(ctx, pdb.RoleJobs, func(tx pgxTx) error {
		q := jobsq.New(tx)
		day := time.Date(2032, 3, 4, 0, 0, 0, 0, time.UTC)
		month := time.Date(2032, 3, 1, 0, 0, 0, 0, time.UTC)
		for i, want := range []bool{true, false} {
			got, err := q.CreateMinutePartition(ctx, day)
			require.NoError(t, err)
			require.Equal(t, want, got, "minute create %d", i)
			got, err = q.CreateHourlyPartition(ctx, month)
			require.NoError(t, err)
			require.Equal(t, want, got, "hourly create %d", i)
		}
		days, err := q.ListMinutePartitions(ctx)
		require.NoError(t, err)
		require.Contains(t, days, day)
		today := time.Now().UTC().Truncate(24 * time.Hour)
		require.Contains(t, days, today.AddDate(0, 0, -7), "the migration created a week back")
		require.Contains(t, days, today.AddDate(0, 0, 7), "and a week ahead")
		months, err := q.ListHourlyPartitions(ctx)
		require.NoError(t, err)
		require.Contains(t, months, month)
		require.Contains(t, months, time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC))

		for i, want := range []bool{true, false} {
			got, err := q.DropMinutePartition(ctx, day)
			require.NoError(t, err)
			require.Equal(t, want, got, "minute drop %d", i)
			got, err = q.DropHourlyPartition(ctx, month)
			require.NoError(t, err)
			require.Equal(t, want, got, "hourly drop %d", i)
		}
		return nil
	}))
	_, err := attempt(t, pdb.RoleJobs, nil, `select api.create_hourly_partition('2032-03-05')`)
	require.Equal(t, "22023", sqlState(err), "an hourly partition starts on the first of a month: %v", err)

	// retention, in a transaction that is rolled back
	p, err := conn.Pool(pdb.RoleJobs)
	require.NoError(t, err)
	tx, err := p.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	tenant := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -9)
	zero := make([]int64, histo.Buckets)
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`insert into api.ingest_dedup (msg_id, tenant_id, applied_at) values ('old-' || $4, $1, $2), ('new-' || $4, $1, $3)`, []any{tenant, old, now, tenant.String()}},
		{`insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano, last_count, last_sum, last_counts, last_seen_at)
		  values ($1, sha256('a'), 1, 1, 0, 0, $4, $2), ($1, sha256('b'), 1, 1, 0, 0, $4, $3)`, []any{tenant, old, now, zero}},
		{`insert into api.resource_seen (tenant_id, resource_hash, start_unix_nano, first_seen_at, last_seen_at)
		  values ($1, sha256('a'), 1, $2, $2), ($1, sha256('b'), 1, $2, $3)`, []any{tenant, old, now}},
		{`insert into api.route_registry (tenant_id, day, service, route) values ($1, $2::date, 's', '/'), ($1, $3::date, 's', '/')`, []any{tenant, old, now}},
	} {
		_, err := tx.Exec(ctx, s.sql, s.args...)
		require.NoError(t, err, s.sql)
	}
	q := jobsq.New(tx)
	cutoff := now.AddDate(0, 0, -8)
	for name, prune := range map[string]func() (int64, error){
		"dedup":    func() (int64, error) { return q.PruneIngestDedup(ctx, cutoff) },
		"series":   func() (int64, error) { return q.PruneSeriesState(ctx, cutoff) },
		"resource": func() (int64, error) { return q.PruneResourceSeen(ctx, cutoff) },
		"routes":   func() (int64, error) { return q.PruneRouteRegistry(ctx, cutoff.Truncate(24*time.Hour)) },
	} {
		n, err := prune()
		require.NoError(t, err, name)
		require.Equal(t, int64(1), n, "%s removes only the old row", name)
	}
}

// the security definer functions that give cp_operator
// hour and day grain rows return only the tenants their caller names, so a
// query that forgets the tenant join can no longer read every tenant (the
// internal probe tenant included). no call returns all tenants at once.
func TestOperatorAPIFunctionsReadOnlyNamedTenants(t *testing.T) {
	for _, fn := range []string{"api.operator_hourly", "api.operator_daily"} {
		args := `(now() - interval '2 days', now() + interval '1 day', array[$1]::uuid[])`
		mine, err := count(t, pdb.RoleCPOperator, nil, `select count(*) from `+fn+args+` where tenant_id = $1`, seed.tenantA)
		require.NoError(t, err, fn)
		require.Positive(t, mine, "%s returns the named tenant's rows", fn)
		other, err := count(t, pdb.RoleCPOperator, nil, `select count(*) from `+fn+args+` where tenant_id <> $1`, seed.tenantA)
		require.NoError(t, err, fn)
		require.Zero(t, other, "%s returns no other tenant", fn)
		none, err := count(t, pdb.RoleCPOperator, nil,
			`select count(*) from `+fn+`(now() - interval '2 days', now() + interval '1 day', '{}'::uuid[])`)
		require.NoError(t, err, fn)
		require.Zero(t, none, "%s with no tenant named returns nothing", fn)
		_, err = count(t, pdb.RoleCPOperator, nil, `select count(*) from `+fn+`(now() - interval '2 days', now() + interval '1 day')`)
		require.Error(t, err, "%s has no form that reads every tenant", fn)
	}
}
