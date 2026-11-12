package operator

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/writerq"
	"github.com/justmert/stellar-analytics/internal/jobs/apirollup"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
)

// freshChosen is the independent expectation: the tenant's minute rows of
// [$2, $3) after the source rule of the design, read past row-level security
const freshChosen = `with r as (
  select * from api.metrics_minute where tenant_id = $1 and bucket_start >= $2 and bucket_start < $3
), svc as (
  select service, case when bool_or(source = 'metric') then 'metric' else 'span' end as src from r group by service
), c as (
  select r.* from r join svc on svc.service = r.service and svc.src = r.source
)
`

// the operator global api views and the machine api read daily and
// hourly rows; before api-rollup has run they must already equal the
// minute rows, and the rollup must not change them (control-plane-api 7.2,
// migration 00022).
func TestOperatorAPIFreshness(t *testing.T) {
	e := newMachineEnv(t)
	f := e.f
	ctx := t.Context()
	today := f.today
	from := today.AddDate(0, 0, -(fixtureDays - 1))
	rq := "from=" + from.Format(time.DateOnly) + "&to=" + today.Format(time.DateOnly)
	hour := time.Now().UTC().Truncate(time.Hour)
	hq := "from=" + url.QueryEscape(httpTime(hour.Add(-30*time.Hour))) + "&to=" + url.QueryEscape(httpTime(hour))

	tenant := uuid.Must(uuid.NewV7())
	superExec(t, f.pg, `insert into cp.tenants (id, slug, name, status) values ($1, 'fresh', 'Fresh', 'active')`, tenant)

	// minute rows through the metrics writer's own queries, as its batch
	// transaction sends them; their hours stay marked until the rollup
	pcfg, err := pdb.LoadPoolConfig(f.pg.Config("platform"), pdb.RoleMetricsWriter, pdb.RoleJobs)
	require.NoError(t, err)
	wd, err := pdb.Open(ctx, pcfg, "operator-fresh-test")
	require.NoError(t, err)
	t.Cleanup(wd.Close)
	type pt struct {
		at                  time.Time
		service, route, src string
		status              int16
		bucket              int
		n                   int64
	}
	points := []pt{
		{hour.Add(time.Minute), "api", "/a", "metric", 200, 3, 9},
		{hour.Add(2 * time.Minute), "api", "/a", "metric", 503, 14, 2},
		{hour.Add(2 * time.Minute), "api", "/a", "span", 200, 1, 40},
		{hour.Add(-55 * time.Minute), "api", "/b/{id}", "metric", 404, 0, 6},
		{hour.Add(-50 * time.Minute), "jobs", "/run", "span", 200, 5, 4},
		{hour.Add(-24 * time.Hour), "api", "/a", "metric", 200, 2, 11},
		{today.AddDate(0, 0, -3).Add(7 * time.Hour), "jobs", "/run", "span", 500, 6, 3},
	}
	require.NoError(t, wd.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgx.Tx) error {
		q := writerq.New(tx)
		var mins []writerq.AddMinuteParams
		var marks []writerq.MarkRollupPendingParams
		for _, p := range points {
			counts := make([]int64, 15)
			counts[p.bucket] = p.n
			mins = append(mins, writerq.AddMinuteParams{TenantID: tenant, BucketStart: p.at, Service: p.service,
				Method: "GET", Route: p.route, StatusCode: p.status, Source: p.src, Counts: counts,
				Sum: float64(p.n) * 0.1, Count: p.n})
			marks = append(marks, writerq.MarkRollupPendingParams{TenantID: tenant, BucketStart: p.at})
		}
		var berr error
		q.AddMinute(ctx, mins).Exec(func(_ int, err error) { berr = errors.Join(berr, err) })
		q.MarkRollupPending(ctx, marks).Exec(func(_ int, err error) { berr = errors.Join(berr, err) })
		return berr
	}))

	key := e.createKey(t, `{"name":"fresh","scope":"metrics:read"}`).Key
	tq := "tenant=" + tenant.String()
	type views struct {
		project APIProject
		compare APICompareProject
		daily   []MachineAPIDay
		routes  []MachineAPIRoute
		hourly  []MachineAPIHour
	}
	fetch := func() views {
		var v views
		code, body := f.get(t, "/api/operator/api/projects?"+rq+"&limit=200")
		require.Equal(t, http.StatusOK, code, string(body))
		found := false
		for _, p := range decode[GlobalList[APIProject]](t, body).Items {
			if p.Tenant.ID == tenant {
				v.project, found = p, true
			}
		}
		require.True(t, found)
		code, body = f.get(t, "/api/operator/api/compare?"+rq+"&tenants="+tenant.String())
		require.Equal(t, http.StatusOK, code, string(body))
		v.compare = decode[APICompare](t, body).Projects[0]
		v.daily = decode[MachineRangeList[MachineAPIDay]](t, e.get(t, key, "/api/daily?"+tq+"&"+rq, 200).body).Items
		v.routes = decode[MachineRangeList[MachineAPIRoute]](t, e.get(t, key, "/api/routes/daily?"+tq+"&"+rq+"&limit=500", 200).body).Items
		v.hourly = decode[MachineRangeList[MachineAPIHour]](t, e.get(t, key, "/api/hourly?"+tq+"&"+hq, 200).body).Items
		return v
	}

	su, err := f.pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = su.Close(context.Background()) })
	_, err = su.Exec(ctx, `set timezone = 'UTC'`)
	require.NoError(t, err)
	end := today.AddDate(0, 0, 1)
	numbers := func(sql string, args ...any) map[string]APINumbers {
		rows, err := su.Query(ctx, sql, args...)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]APINumbers{}
		for rows.Next() {
			var k string
			var n APINumbers
			require.NoError(t, scanNumbers(&n, rows.Scan, &k))
			out[k] = n
		}
		require.NoError(t, rows.Err())
		return out
	}
	total := numbers(freshChosen+`select 'all',`+numbersCols+` from c`, tenant, from, end)["all"]
	perDay := numbers(freshChosen+`select to_char(bucket_start, 'YYYY-MM-DD'),`+numbersCols+` from c group by 1`, tenant, from, end)
	perService := numbers(freshChosen+`select service,`+numbersCols+` from c group by 1`, tenant, from, end)
	perRoute := numbers(freshChosen+`select to_char(bucket_start, 'YYYY-MM-DD')||' '||service||' '||route,`+numbersCols+
		` from c group by 1`, tenant, from, end)
	perHour := numbers(freshChosen+`select to_char(date_trunc('hour', bucket_start), 'YYYY-MM-DD"T"HH24'),`+numbersCols+
		` from c group by 1`, tenant, hour.Add(-30*time.Hour), hour.Add(time.Hour))

	check := func(v views) {
		t.Helper()
		// by hand: 9 + 2 + 6 + 4 + 11 + 3; the 40 span requests of api are left out
		require.Equal(t, int64(35), total.Requests)
		require.Equal(t, total, v.project.APINumbers)
		require.Equal(t, total, v.compare.Totals.APINumbers)
		for _, p := range v.compare.Series {
			want := perDay[p.Day]
			require.Equal(t, want, p.APINumbers, p.Day)
		}
		require.Len(t, v.compare.Services, len(perService))
		for _, s := range v.compare.Services {
			require.Equal(t, perService[s.Service], s.APINumbers, s.Service)
		}
		require.Len(t, v.daily, len(perDay))
		for _, d := range v.daily {
			require.Equal(t, perDay[d.Day], d.APINumbers, d.Day)
		}
		require.Len(t, v.routes, len(perRoute))
		for _, r := range v.routes {
			k := r.Day + " " + r.Service + " " + r.Route
			require.Equal(t, perRoute[k], r.APINumbers, k)
		}
		require.Len(t, v.hourly, len(perHour))
		for _, h := range v.hourly {
			k := time.Time(h.Hour).UTC().Format("2006-01-02T15")
			require.Equal(t, perHour[k], h.APINumbers, k)
		}
	}

	var pending int
	require.NoError(t, su.QueryRow(ctx, `select count(*) from api.rollup_pending where tenant_id = $1`, tenant).Scan(&pending))
	require.Positive(t, pending)
	before := fetch()
	check(before)

	srv := jobstest.NATS(t)
	leader, _ := jobstest.Leader(t, srv, wd, "operator-fresh-test")
	r, err := apirollup.New(slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	_, err = r.Run(ctx, leader.Tx)
	require.NoError(t, err)
	require.NoError(t, su.QueryRow(ctx, `select count(*) from api.rollup_pending where tenant_id = $1`, tenant).Scan(&pending))
	require.Zero(t, pending)

	after := fetch()
	require.Equal(t, before, after, "the rollup does not change any answer")
	check(after)
}
