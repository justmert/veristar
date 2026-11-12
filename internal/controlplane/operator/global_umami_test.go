package operator

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/aggregateweb"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/umamictr"
)

// TestGlobalWebMatchesUmami feeds a real umami through /api/send, runs the
// aggregation task, and checks that the operator views of each project
// equal umami's own /stats and /metrics over the same utc days.
func TestGlobalWebMatchesUmami(t *testing.T) {
	um := umamictr.Start(t)
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPOperator, pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "operator-umami-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	ns := jobstest.NATS(t)
	_, js := jobstest.Connect(t, ns.URL)
	leader, _ := jobstest.Leader(t, ns, jobs, "operator-umami-test")

	tenantA, sitesA := jobstest.NewWebTenant(t, pg, um, js, "Single", 1)
	tenantB, sitesB := jobstest.NewWebTenant(t, pg, um, js, "Double", 2)
	today := aggregateweb.UTCDay(time.Now())
	from := today.AddDate(0, 0, -4)
	var days []time.Time
	for x := from; !x.After(today); x = x.AddDate(0, 0, 1) {
		days = append(days, x)
	}
	for i, s := range append(append([]jobstest.WebSite{}, sitesA...), sitesB...) {
		jobstest.SendWebTraffic(t, um, s.Site.ID, s.Domain, days, uint64(40+i), 7)
		jobstest.SendWebTraffic(t, um, s.Quarantine, s.Domain, days, uint64(80+i), 2)
	}

	watcher := registry.NewSiteWatcher(js, slog.New(slog.DiscardHandler))
	wctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = watcher.Run(wctx) }()
	require.Eventually(t, func() bool { return watcher.Loaded() && watcher.Len() == 3 }, 10*time.Second, 50*time.Millisecond)
	a, err := aggregateweb.New(um.Admin, watcher, time.Now, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	_, err = a.Backfill(t.Context(), leader.Tx, from, today)
	require.NoError(t, err)

	g, err := NewGlobal(d)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Route("/api/operator", g.Routes)
	rng := umami.Range{Start: from, End: aggregateweb.DayRange(today).End}
	rq := "from=" + from.Format(time.DateOnly) + "&to=" + today.Format(time.DateOnly)

	code, body := call(t, r, "/api/operator/web/compare?"+rq+"&breakdown_limit=500&tenants="+tenantA.String()+","+tenantB.String())
	require.Equal(t, http.StatusOK, code, string(body))
	c := decode[WebCompare](t, body)
	code, body = call(t, r, "/api/operator/web/projects?"+rq+"&sort=name&order=asc")
	require.Equal(t, http.StatusOK, code, string(body))
	list := decode[GlobalList[WebProject]](t, body)
	require.Len(t, list.Items, 2)

	for i, sites := range [][]jobstest.WebSite{sitesA, sitesB} {
		p := c.Projects[i]
		// the project is the sum of its websites in umami, each band apart
		var want, wantQ umami.StatsValues
		wantDims := map[string]map[string]int64{}
		for _, s := range sites {
			st, err := um.Admin.Stats(t.Context(), s.Site.ID, rng)
			require.NoError(t, err)
			want = addStats(want, st.StatsValues)
			qs, err := um.Admin.Stats(t.Context(), s.Quarantine, rng)
			require.NoError(t, err)
			wantQ = addStats(wantQ, qs.StatsValues)
			for _, dim := range webDims {
				rows, err := um.Admin.Metrics(t.Context(), s.Site.ID, umami.MetricsQuery{Range: rng, Type: dim, Limit: 1000})
				require.NoError(t, err)
				if wantDims[dim] == nil {
					wantDims[dim] = map[string]int64{}
				}
				for _, row := range rows {
					wantDims[dim][row.X] += row.Y
				}
			}
		}
		require.Positive(t, want.Pageviews)
		require.Equal(t, want, umami.StatsValues{Pageviews: p.Totals.Pageviews, Visitors: p.Totals.Visitors,
			Visits: p.Totals.Visits, Bounces: p.Totals.Bounces, Totaltime: p.Totals.Totaltime}, p.Tenant.Name)
		require.Equal(t, wantQ.Pageviews, p.Totals.QuarantinePageviews)
		require.Equal(t, wantQ.Visitors, p.Totals.QuarantineVisitors)
		for _, dim := range webDims {
			got := map[string]int64{}
			for _, kv := range p.Breakdowns[dim].Items {
				got[kv.Key] = kv.Value
			}
			require.Equal(t, wantDims[dim], got, "%s %s", p.Tenant.Name, dim)
			require.Equal(t, int64(len(wantDims[dim])), p.Breakdowns[dim].TotalKeys)
		}
		// the list row holds the same numbers and the five largest values
		row := list.Items[slices.IndexFunc(list.Items, func(w WebProject) bool { return w.Tenant.ID == p.Tenant.ID })]
		require.Equal(t, want, umami.StatsValues{Pageviews: row.Pageviews, Visitors: row.Visitors,
			Visits: row.Visits, Bounces: row.Bounces, Totaltime: row.Totaltime})
		for _, dim := range listTopDims {
			var top []KeyValue
			for k, v := range wantDims[dim] {
				top = append(top, KeyValue{Key: k, Value: v})
			}
			slices.SortFunc(top, func(a, b KeyValue) int {
				if a.Value != b.Value {
					return int(b.Value - a.Value)
				}
				return strings.Compare(a.Key, b.Key)
			})
			require.Equal(t, top[:min(len(top), listTopN)], row.Top[dim], "%s %s", p.Tenant.Name, dim)
		}
		// each day of the series equals umami's day
		for j, day := range days {
			var dayWant umami.StatsValues
			for _, s := range sites {
				st, err := um.Admin.Stats(t.Context(), s.Site.ID, aggregateweb.DayRange(day))
				require.NoError(t, err)
				dayWant = addStats(dayWant, st.StatsValues)
			}
			pt := p.Series[j]
			require.True(t, pt.Aggregated)
			require.Equal(t, dayWant, umami.StatsValues{Pageviews: *pt.Pageviews, Visitors: *pt.Visitors,
				Visits: *pt.Visits, Bounces: *pt.Bounces, Totaltime: *pt.Totaltime}, pt.Day)
		}
	}
}

func addStats(a, b umami.StatsValues) umami.StatsValues {
	return umami.StatsValues{Pageviews: a.Pageviews + b.Pageviews, Visitors: a.Visitors + b.Visitors,
		Visits: a.Visits + b.Visits, Bounces: a.Bounces + b.Bounces, Totaltime: a.Totaltime + b.Totaltime}
}

// TestGlobalRoutesNeedAnOperator mounts the global routes the way the
// control plane does (sessions, RequireUser, the operator guard) and checks
// that only an operator session gets through.
func TestGlobalRoutesNeedAnOperator(t *testing.T) {
	d, pg := reviewDB(t)
	operatorID, plainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.users (id, github_id, github_login, is_operator) values ($1, 7, 'op', true), ($2, 8, 'plain', false)`,
		operatorID, plainID)
	tenant := uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status) values ($1, 'one', 'One', 'active')`, tenant)

	sessions := session.New(d)
	guard, err := NewGuard(map[int64]bool{7: true})
	require.NoError(t, err)
	g, err := NewGlobal(d)
	require.NoError(t, err)
	// the console origin is the server's own url, known only once it
	// listens, so the handler is stored after the start
	var api atomic.Pointer[http.Handler]
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*api.Load()).ServeHTTP(w, r)
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	h := controlplane.NewHandler(controlplane.Options{
		Log: slog.New(slog.DiscardHandler), ConsoleOrigin: srv.URL, Sessions: sessions,
		// a sign-in shortcut for the test: the session is the real one,
		// only the oauth round trip is skipped
		Public: []controlplane.Routes{controlplane.RoutesFunc(func(r chi.Router) {
			r.Get("/test-sign-in/{id}", func(w http.ResponseWriter, r *http.Request) {
				id := uuid.MustParse(chi.URLParam(r, "id"))
				if err := sessions.SignIn(r.Context(), id); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
		})},
		Private: []controlplane.Routes{controlplane.RoutesFunc(guard.Mount(g))},
	})
	api.Store(&h)

	client := func(user *uuid.UUID) *http.Client {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		hc := *srv.Client()
		hc.Jar = jar
		if user != nil {
			res, err := hc.Get(srv.URL + "/api/test-sign-in/" + user.String())
			require.NoError(t, err)
			_ = res.Body.Close()
			require.Equal(t, http.StatusNoContent, res.StatusCode)
		}
		return &hc
	}
	get := func(hc *http.Client, path string) (int, string) {
		res, err := hc.Get(srv.URL + path)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return res.StatusCode, string(b)
	}
	paths := []string{
		"/api/operator/web/projects", "/api/operator/web/compare?tenants=" + tenant.String(),
		"/api/operator/api/projects", "/api/operator/api/compare?tenants=" + tenant.String(),
	}
	anon, plain, op := client(nil), client(&plainID), client(&operatorID)
	for _, p := range paths {
		code, body := get(anon, p)
		require.Equal(t, http.StatusUnauthorized, code, "%s: %s", p, body)
		code, body = get(plain, p)
		require.Equal(t, http.StatusForbidden, code, "%s: %s", p, body)
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &env))
		require.Equal(t, "operator_required", env.Error.Code, p)
		code, body = get(op, p)
		require.Equal(t, http.StatusOK, code, "%s: %s", p, body)
		require.NotContains(t, body, "session_id", "aggregates only, never session rows")
	}
}
