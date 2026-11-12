package aggregateweb_test

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/aggregateweb"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

type dailyKey struct {
	Site uuid.UUID
	Day  string
	Band string
}

type daily struct {
	Tenant uuid.UUID
	umami.StatsValues
}

type dimKey struct {
	dailyKey
	Dimension string
	Key       string
}

// dump reads both agg tables as the jobs role, without read_at.
func dump(t *testing.T, d *pdb.DB) (map[dailyKey]daily, map[dimKey]int64) {
	t.Helper()
	days := map[dailyKey]daily{}
	dims := map[dimKey]int64{}
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		rows, err := tx.Query(t.Context(), `select site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime from agg.web_daily`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k dailyKey
			var v daily
			var day time.Time
			if err := rows.Scan(&k.Site, &v.Tenant, &day, &k.Band, &v.Pageviews, &v.Visitors, &v.Visits, &v.Bounces, &v.Totaltime); err != nil {
				return err
			}
			k.Day = day.Format(time.DateOnly)
			days[k] = v
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(t.Context(), `select site_id, day, band, dimension, key, value from agg.web_daily_dim`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k dimKey
			var day time.Time
			var v int64
			if err := rows.Scan(&k.Site, &day, &k.Band, &k.Dimension, &k.Key, &v); err != nil {
				return err
			}
			k.Day = day.Format(time.DateOnly)
			dims[k] = v
		}
		return rows.Err()
	}))
	return days, dims
}

func jobsExec(t *testing.T, d *pdb.DB, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		tag, err := tx.Exec(t.Context(), sql, args...)
		n = tag.RowsAffected()
		return err
	}))
	return n
}

func metricsMap(t *testing.T, um *umamictr.Stack, website, typ string, r umami.Range) map[string]int64 {
	t.Helper()
	rows, err := um.Admin.Metrics(t.Context(), website, umami.MetricsQuery{Range: r, Type: typ, Limit: 1000})
	require.NoError(t, err)
	require.Less(t, len(rows), 1000)
	out := map[string]int64{}
	for _, row := range rows {
		out[row.X] = row.Y
	}
	return out
}

// TestAggregateWeb runs the task against a real umami fed through
// /api/send and a real postgresql, and checks every stored number against
// umami's own answer for the same utc day.
func TestAggregateWeb(t *testing.T) {
	um := umamictr.Start(t)
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	ns := jobstest.NATS(t)
	_, js := jobstest.Connect(t, ns.URL)
	leader, _ := jobstest.Leader(t, ns, jobs, "aggregate-web-test")

	tenantA, sitesA := jobstest.NewWebTenant(t, pg, um, js, "Alpha", 2)
	tenantB, sitesB := jobstest.NewWebTenant(t, pg, um, js, "Bravo", 1)
	all := append(append([]jobstest.WebSite{}, sitesA...), sitesB...)

	today := aggregateweb.UTCDay(time.Now())
	from := today.AddDate(0, 0, -5)
	var days []time.Time
	for d := from; !d.After(today); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}
	for i, s := range all {
		require.Positive(t, jobstest.SendWebTraffic(t, um, s.Site.ID, s.Domain, days, uint64(10+i), 6))
		// quarantined traffic is a fraction of the primary band
		require.Positive(t, jobstest.SendWebTraffic(t, um, s.Quarantine, s.Domain, days, uint64(100+i), 2))
	}

	watcher := registry.NewSiteWatcher(js, slog.New(slog.DiscardHandler))
	wctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = watcher.Run(wctx) }()
	require.Eventually(t, func() bool { return watcher.Loaded() && watcher.Len() == len(all) }, 10*time.Second, 50*time.Millisecond)

	a, err := aggregateweb.New(um.Admin, watcher, time.Now, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)

	res, err := a.Backfill(t.Context(), leader.Tx, from, today)
	require.NoError(t, err)
	require.Equal(t, aggregateweb.Result{Sites: 3, Days: 3 * len(days)}, res)

	rows, dims := dump(t, jobs)
	require.Len(t, rows, len(all)*len(days)*2, "one row per site, day and band, zeros included")

	t.Run("rows equal umami per utc day", func(t *testing.T) {
		nonzero := 0
		for _, s := range all {
			site := uuid.MustParse(s.Site.ID)
			tenant := uuid.MustParse(s.Site.TenantID)
			for _, band := range []struct{ name, website string }{
				{aggregateweb.BandPrimary, s.Site.ID}, {aggregateweb.BandQuarantine, s.Quarantine},
			} {
				for _, d := range days {
					r := aggregateweb.DayRange(d)
					st, err := um.Admin.Stats(t.Context(), band.website, r)
					require.NoError(t, err)
					k := dailyKey{Site: site, Day: d.Format(time.DateOnly), Band: band.name}
					require.Equal(t, daily{Tenant: tenant, StatsValues: st.StatsValues}, rows[k], "%+v", k)
					if st.Pageviews > 0 {
						nonzero++
					}
					for _, dim := range aggregateweb.Dimensions {
						want := metricsMap(t, um, band.website, dim, r)
						got := map[string]int64{}
						for dk, v := range dims {
							if dk.dailyKey == k && dk.Dimension == dim {
								got[dk.Key] = v
							}
						}
						require.Equal(t, want, got, "%+v %s", k, dim)
					}
				}
			}
		}
		require.Equal(t, len(all)*len(days)*2, nonzero, "every site, band and day carries traffic")
	})

	// research items of the design, recorded in docs/reference/web-metrics.md
	t.Run("referrer and domain are the same ranking", func(t *testing.T) {
		for _, s := range all {
			for _, d := range days {
				r := aggregateweb.DayRange(d)
				ref := metricsMap(t, um, s.Site.ID, "referrer", r)
				require.Equal(t, ref, metricsMap(t, um, s.Site.ID, "domain", r))
				for k := range ref {
					require.Equal(t, strings.ToLower(k), k, "referrer domains are lowercase")
					require.False(t, strings.HasPrefix(k, "www."), "www. is stripped")
					require.NotContains(t, k, s.Domain, "the site's own host is not a referrer")
				}
			}
		}
	})
	t.Run("range totals equal the sum of the days", func(t *testing.T) {
		r := umami.Range{Start: from, End: aggregateweb.DayRange(today).End}
		for _, s := range all {
			site := uuid.MustParse(s.Site.ID)
			st, err := um.Admin.Stats(t.Context(), s.Site.ID, r)
			require.NoError(t, err)
			var sum umami.StatsValues
			for _, d := range days {
				v := rows[dailyKey{Site: site, Day: d.Format(time.DateOnly), Band: aggregateweb.BandPrimary}]
				sum.Pageviews += v.Pageviews
				sum.Visitors += v.Visitors
				sum.Visits += v.Visits
				sum.Bounces += v.Bounces
				sum.Totaltime += v.Totaltime
			}
			require.Equal(t, st.StatsValues, sum, "visitors over a range are the sum of daily visitors")
			for _, dim := range aggregateweb.Dimensions {
				sumDim := map[string]int64{}
				for k, v := range dims {
					if k.Site == site && k.Band == aggregateweb.BandPrimary && k.Dimension == dim {
						sumDim[k.Key] += v
					}
				}
				require.Equal(t, metricsMap(t, um, s.Site.ID, dim, r), sumDim, dim)
			}
		}
	})
	t.Run("the next midnight as end counts a 00:00:00 event twice", func(t *testing.T) {
		s := all[0]
		d := days[1]
		inclusive := umami.Range{Start: d, End: d.AddDate(0, 0, 1)}
		wide, err := um.Admin.Stats(t.Context(), s.Site.ID, inclusive)
		require.NoError(t, err)
		exact, err := um.Admin.Stats(t.Context(), s.Site.ID, aggregateweb.DayRange(d))
		require.NoError(t, err)
		// visitor 0 of every day starts at exactly 00:00:00
		require.Greater(t, wide.Pageviews, exact.Pageviews)
	})
	t.Run("umami answers an unknown website with zeros", func(t *testing.T) {
		st, err := um.Admin.Stats(t.Context(), uuid.NewString(), aggregateweb.DayRange(today))
		require.NoError(t, err)
		require.Equal(t, umami.StatsValues{}, st.StatsValues)
	})

	t.Run("a second run changes nothing", func(t *testing.T) {
		_, err := a.Backfill(t.Context(), leader.Tx, from, today)
		require.NoError(t, err)
		r2, d2 := dump(t, jobs)
		require.Equal(t, rows, r2)
		require.Equal(t, dims, d2)
	})

	t.Run("a backfill restores deleted rows", func(t *testing.T) {
		day := days[2]
		require.Positive(t, jobsExec(t, jobs, `delete from agg.web_daily where day = $1`, day))
		require.Positive(t, jobsExec(t, jobs, `delete from agg.web_daily_dim where day = $1`, day))
		// a stale key that umami no longer returns is dropped on the next read
		jobsExec(t, jobs, `insert into agg.web_daily_dim (site_id, tenant_id, day, band, dimension, key, value)
			values ($1, $2, $3, 'primary', 'country', 'ZZ', 9)`, uuid.MustParse(all[0].Site.ID), tenantA, days[3])
		res, err := a.Backfill(t.Context(), leader.Tx, day, days[3])
		require.NoError(t, err)
		require.Equal(t, 3*2, res.Days)
		r2, d2 := dump(t, jobs)
		require.Equal(t, rows, r2)
		require.Equal(t, dims, d2)
	})

	t.Run("a regular run reads today and the three days before", func(t *testing.T) {
		jobsExec(t, jobs, `delete from agg.web_daily`)
		jobsExec(t, jobs, `delete from agg.web_daily_dim`)
		res, err := a.Run(t.Context(), leader.Tx)
		require.NoError(t, err)
		require.Equal(t, 3*(aggregateweb.RereadDays+1), res.Days)
		r2, _ := dump(t, jobs)
		got := map[string]bool{}
		for k, v := range r2 {
			got[k.Day] = true
			require.Equal(t, rows[k], v)
		}
		var want []string
		for d := today.AddDate(0, 0, -aggregateweb.RereadDays); !d.After(today); d = d.AddDate(0, 0, 1) {
			want = append(want, d.Format(time.DateOnly))
		}
		gotDays := sortedKeys(got)
		require.Equal(t, want, gotDays)
		_, err = a.Backfill(t.Context(), leader.Tx, from, today)
		require.NoError(t, err)
	})

	t.Run("bad ranges are refused", func(t *testing.T) {
		_, err := a.Backfill(t.Context(), leader.Tx, today, today.AddDate(0, 0, -1))
		require.ErrorContains(t, err, "before")
		_, err = a.Backfill(t.Context(), leader.Tx, today, today.AddDate(0, 0, 1))
		require.ErrorContains(t, err, "after today")
	})

	t.Run("a removed tenant is not written again", func(t *testing.T) {
		c, err := pg.Superuser(t.Context(), "platform")
		require.NoError(t, err)
		defer func() { _ = c.Close(context.Background()) }()
		_, err = c.Exec(t.Context(), `update cp.tenants set status = 'removed' where id = $1`, tenantB)
		require.NoError(t, err)
		jobsExec(t, jobs, `delete from agg.web_daily where tenant_id = $1`, tenantB)
		jobsExec(t, jobs, `delete from agg.web_daily_dim where tenant_id = $1`, tenantB)
		res, err := a.Backfill(t.Context(), leader.Tx, from, today)
		require.NoError(t, err)
		require.Equal(t, len(days), res.Skipped)
		require.Equal(t, 2*len(days), res.Days)
		r2, _ := dump(t, jobs)
		for k, v := range r2 {
			require.NotEqual(t, tenantB, v.Tenant, "%+v", k)
		}
		_, err = c.Exec(t.Context(), `update cp.tenants set status = 'active' where id = $1`, tenantB)
		require.NoError(t, err)
	})

	t.Run("a missing umami website fails its site only", func(t *testing.T) {
		pub, err := registry.NewPublisher(t.Context(), js)
		require.NoError(t, err)
		ghost := registry.Site{ID: uuid.NewString(), TenantID: tenantA.String(), Status: registry.StatusActive,
			Origins: []string{"https://ghost.example"}, QuarantineWebsiteID: uuid.NewString()}
		require.NoError(t, pub.PutSite(t.Context(), ghost))
		suspended := sitesA[1].Site
		suspended.Status = "suspended"
		require.NoError(t, pub.PutSite(t.Context(), suspended))
		require.Eventually(t, func() bool {
			s, ok := watcher.Get(ghost.ID)
			g, _ := watcher.Get(suspended.ID)
			return ok && s.ID == ghost.ID && g.Status == "suspended"
		}, 10*time.Second, 50*time.Millisecond)
		jobsExec(t, jobs, `delete from agg.web_daily where day = $1`, today)
		res, err := a.Backfill(t.Context(), leader.Tx, today, today)
		require.ErrorContains(t, err, "umami website "+ghost.ID+" does not exist")
		require.Equal(t, 2, res.Days, "the other active sites are still read; the suspended one is not")
		r2, _ := dump(t, jobs)
		for k := range r2 {
			require.NotEqual(t, ghost.ID, k.Site.String())
			if k.Day == today.Format(time.DateOnly) {
				require.NotEqual(t, suspended.ID, k.Site.String(), "a suspended site is not read")
			}
		}
		require.NoError(t, pub.DeleteSite(t.Context(), ghost.ID))
		require.NoError(t, pub.PutSite(t.Context(), sitesA[1].Site))
		require.Eventually(t, func() bool {
			_, ok := watcher.Get(ghost.ID)
			s, _ := watcher.Get(suspended.ID)
			return !ok && s.Active()
		}, 10*time.Second, 50*time.Millisecond)
	})

	t.Run("a fenced-off leader writes nothing", func(t *testing.T) {
		// today's dimension rows are gone, so a write that got through
		// would show
		jobsExec(t, jobs, `delete from agg.web_daily_dim where day = $1`, today)
		before, beforeDims := dump(t, jobs)
		jobstest.FenceOff(t, jobs)
		_, err := a.Backfill(t.Context(), leader.Tx, today, today)
		require.ErrorIs(t, err, natsx.ErrFenced)
		after, afterDims := dump(t, jobs)
		require.Equal(t, before, after)
		require.Equal(t, beforeDims, afterDims)
	})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
