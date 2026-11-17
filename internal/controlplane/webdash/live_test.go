package webdash_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/umamiread"
	"github.com/justmert/veristar/internal/controlplane/webdash"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/aggregateweb"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

// the parity test of the web dashboard (contract 18): one real postgresql,
// nats and umami; tenants, sites, umami teams, users and memberships made by
// the real reconciler; traffic sent to umami the way the replayer sends it;
// the aggregates filled by the real aggregate-web task; and the dashboard
// routes behind the real session middleware. every number a route answers is
// compared with umami's own answer for the same websites and range, read as
// the admin, so the dashboard can neither invent nor lose a count.

type env struct {
	d     *pdb.DB
	um    *umamictr.Stack
	rec   *provision.Reconciler
	srv   *httptest.Server
	gh    int64
	agg   *aggregateweb.Aggregator
	tx    aggregateweb.TxFunc
	watch *registry.Watcher[registry.Site]
}

func start(t *testing.T) *env {
	t.Helper()
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "webdash-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	jobs := jobstest.JobsDB(t, pg)

	ns := jobstest.NATS(t)
	_, js := jobstest.Connect(t, ns.URL)
	pub, err := registry.NewPublisher(ctx, js)
	require.NoError(t, err)
	leader, _ := jobstest.Leader(t, ns, jobs, "webdash-test")

	um := umamictr.Start(t)
	raw := make([]byte, umami.PasswordKeyLen)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	key, err := umami.NewPasswordKey(raw)
	require.NoError(t, err)

	log := slog.New(slog.DiscardHandler)
	e := &env{d: d, um: um, gh: 9_100_000, tx: leader.Tx,
		rec: &provision.Reconciler{Admin: um.Admin, Registry: pub, Key: key, Tx: d.WithSystem, Now: time.Now, Log: log},
	}
	e.watch = registry.NewSiteWatcher(js, log)
	wctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	go func() { _ = e.watch.Run(wctx) }()
	e.agg, err = aggregateweb.New(um.Admin, e.watch, time.Now, log, prometheus.NewRegistry())
	require.NoError(t, err)

	scope, err := tenants.NewScope(d, "test-terms", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
		return errors.New("the web dashboard must not reconcile")
	}), log)
	require.NoError(t, err)
	t.Cleanup(scope.Wait)
	sessions := &umamiread.Sessions{Tx: d.WithSystem, Umami: um.Client, Key: key, Now: time.Now}
	require.NoError(t, sessions.Check())
	reader, err := umamiread.NewReader(sessions, um.Client, time.Now)
	require.NoError(t, err)
	routes, err := webdash.NewRoutes(scope, reader, time.Now)
	require.NoError(t, err)

	sm := session.New(d)
	e.srv = httptest.NewUnstartedServer(http.NotFoundHandler())
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	// the session is the real one; only the github round trip is skipped
	signIn := controlplane.RoutesFunc(func(r chi.Router) {
		r.Method(http.MethodGet, "/test-sign-in", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			id, err := uuid.Parse(r.URL.Query().Get("user"))
			if err != nil {
				return httpx.NotFound()
			}
			if err := sm.SignIn(r.Context(), id); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
	})
	e.srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: e.srv.URL, Sessions: sm,
		Public:  []controlplane.Routes{signIn},
		Private: []controlplane.Routes{routes},
	})
	return e
}

func (e *env) sys(t *testing.T, fn func(pgx.Tx) error) {
	t.Helper()
	require.NoError(t, e.d.WithSystem(t.Context(), fn))
}

func (e *env) user(t *testing.T) uuid.UUID {
	t.Helper()
	e.gh++
	var id uuid.UUID
	e.sys(t, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
			e.gh, "u"+strconv.FormatInt(e.gh, 10)).Scan(&id)
	})
	return id
}

type site struct {
	id, quarantine uuid.UUID
	domain         string
}

// project makes an active tenant owned by owner with one site per domain
// and lets the real reconciler create its umami team, websites, user and
// membership.
func (e *env) project(t *testing.T, owner uuid.UUID, domains ...string) (uuid.UUID, []site) {
	t.Helper()
	var tenant uuid.UUID
	var sites []site
	e.sys(t, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(),
			`insert into cp.tenants (slug, name, status, created_by) values ($1, 'Project', 'active', $2) returning id`,
			"p-"+uuid.NewString()[:8], owner).Scan(&tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, tenant, owner); err != nil {
			return err
		}
		for _, dom := range domains {
			s := site{id: uuid.Must(uuid.NewV7()), quarantine: uuid.Must(uuid.NewV7()), domain: dom}
			if _, err := tx.Exec(t.Context(), `
				insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
				values ($1, $2, $3, $3, $4, $5, 'active', 'unused')`,
				s.id, tenant, dom, []string{"https://" + dom}, s.quarantine); err != nil {
				return err
			}
			sites = append(sites, s)
		}
		return nil
	})
	_, err := e.rec.Changes(t.Context(), tenant)
	require.NoError(t, err)
	return tenant, sites
}

type client struct {
	e  *env
	hc *http.Client
}

func (e *env) client(t *testing.T, user uuid.UUID) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	res, err := hc.Get(e.srv.URL + "/api/test-sign-in?user=" + user.String())
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	return &client{e: e, hc: &hc}
}

func (c *client) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	res, err := c.hc.Get(c.e.srv.URL + "/api" + path)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, b
}

func (c *client) ok(t *testing.T, path string, v any) {
	t.Helper()
	st, b := c.get(t, path)
	require.Equal(t, http.StatusOK, st, "%s: %s", path, b)
	require.NoError(t, json.Unmarshal(b, v), string(b))
}

func (c *client) fail(t *testing.T, path string) (int, string) {
	t.Helper()
	st, b := c.get(t, path)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(b, &env), string(b))
	return st, env.Error.Code
}

// send stores one event in umami as the replayer would
func (e *env) send(t *testing.T, website, domain, path, salt, name string, at time.Time) {
	t.Helper()
	_, err := e.um.Client.Send(t.Context(), umami.SendRequest{
		Event: umami.Event{Website: website, Hostname: domain, URL: path, Title: "page", Screen: "1440x900",
			Language: "en-US", Name: name, Timestamp: at.Unix(), Salt: salt},
		UserAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
		Country:   "DE",
	})
	require.NoError(t, err)
}

func metricsMap(t *testing.T, um *umamictr.Stack, website, typ string, r umami.Range) map[string]int64 {
	t.Helper()
	rows, err := um.Admin.Metrics(t.Context(), website, umami.MetricsQuery{Range: r, Type: typ, Limit: 5000})
	require.NoError(t, err)
	require.Less(t, len(rows), 5000)
	out := map[string]int64{}
	for _, row := range rows {
		if row.X != "" {
			out[row.X] += row.Y
		}
	}
	return out
}

func days(from, to time.Time) []time.Time {
	var out []time.Time
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

//nolint:contextcheck // the helpers take the test and use its context, t.Context()
func TestWebDashboardParity(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	owner, outsider := e.user(t), e.user(t)
	tenant, sites := e.project(t, owner, "alpha.example", "beta.example")
	otherTenant, otherSites := e.project(t, outsider, "gamma.example")
	// utm values are kept for the first site only
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update cp.sites set collect_utm = true where id = $1`, sites[0].id)
		return err
	})

	today := webdash.UTCDay(time.Now())
	first := today.AddDate(0, 0, -9)
	all := days(first, today)
	for i, s := range append(append([]site{}, sites...), otherSites...) {
		require.Positive(t, jobstest.SendWebTraffic(t, e.um, s.id.String(), s.domain, all, uint64(40+i), 5))
		require.Positive(t, jobstest.SendWebTraffic(t, e.um, s.quarantine.String(), s.domain, all, uint64(80+i), 2))
	}
	// utm values reach umami in the url query, as the gateway forwards them
	// for a site that collects them
	for d, day := range all[6:] {
		e.send(t, sites[0].id.String(), sites[0].domain, "/landing?utm_source=newsletter&utm_medium=email&utm_campaign=launch",
			fmt.Sprintf("utm-%d", d), "", day.Add(3*time.Hour))
	}
	// two visitors right now, for the realtime answer
	now := time.Now()
	e.send(t, sites[0].id.String(), sites[0].domain, "/now", "now-1", "", now.Add(-30*time.Second))
	e.send(t, sites[1].id.String(), sites[1].domain, "/now", "now-2", "", now.Add(-20*time.Second))

	require.Eventually(t, func() bool { return e.watch.Loaded() && e.watch.Len() == 3 }, 30*time.Second, 50*time.Millisecond)
	_, err := e.agg.Backfill(ctx, e.tx, first, today)
	require.NoError(t, err)

	c := e.client(t, owner)
	// a range of five days with a previous period of five days, both full
	from, to := today.AddDate(0, 0, -4), today
	rg := webdash.Days{From: from, To: to}
	q := func(extra string) string {
		return "from=" + from.Format(time.DateOnly) + "&to=" + to.Format(time.DateOnly) + extra
	}
	base := "/tenants/" + tenant.String() + "/web"

	// umami's own totals for websites over a range
	stats := func(websites []string, r umami.Range) umami.StatsValues {
		var sum umami.StatsValues
		for _, w := range websites {
			st, err := e.um.Admin.Stats(ctx, w, r)
			require.NoError(t, err)
			sum.Pageviews += st.Pageviews
			sum.Visitors += st.Visitors
			sum.Visits += st.Visits
			sum.Bounces += st.Bounces
			sum.Totaltime += st.Totaltime
		}
		return sum
	}
	primary := func(ss ...site) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.id.String())
		}
		return out
	}
	quarantine := func(ss ...site) []string {
		var out []string
		for _, s := range ss {
			out = append(out, s.quarantine.String())
		}
		return out
	}
	asStats := func(tt webdash.Totals) umami.StatsValues {
		return umami.StatsValues{Pageviews: tt.Pageviews, Visitors: tt.Visitors, Visits: tt.Visits, Bounces: tt.Bounces, Totaltime: tt.Totaltime}
	}

	for _, sc := range []struct {
		name    string
		extra   string
		sites   []site
		band    func(...site) []string
		bandArg string
	}{
		{"project", "", sites, primary, "primary"},
		{"one site", "&site=" + sites[1].id.String(), sites[1:], primary, "primary"},
		{"quarantine band", "&band=quarantine", sites, quarantine, "quarantine"},
		{"quarantine band of one site", "&site=" + sites[0].id.String() + "&band=quarantine", sites[:1], quarantine, "quarantine"},
	} {
		t.Run("summary equals umami: "+sc.name, func(t *testing.T) {
			var s webdash.Summary
			c.ok(t, base+"/summary?"+q(sc.extra), &s)
			require.Equal(t, sc.bandArg, s.Band)
			require.Equal(t, webdash.SourceAggregate, s.Source)
			require.Equal(t, stats(sc.band(sc.sites...), rg.Umami()), asStats(s.Current.Totals))
			require.Equal(t, stats(sc.band(sc.sites...), rg.Previous().Umami()), asStats(s.Previous.Totals))
			require.Equal(t, 5, s.Current.Totals.AggregatedDays)
			require.NotNil(t, s.ReadAt)
			require.Len(t, s.Current.Series, 5)
			for i, d := range rg.List() {
				p := s.Current.Series[i]
				require.True(t, p.Aggregated)
				want := stats(sc.band(sc.sites...), webdash.Days{From: d, To: d}.Umami())
				require.Equal(t, want, umami.StatsValues{Pageviews: *p.Pageviews, Visitors: *p.Visitors,
					Visits: *p.Visits, Bounces: *p.Bounces, Totaltime: *p.Totaltime}, p.Day)
			}
			// the dashboard's formulas
			tt := s.Current.Totals
			require.InDelta(t, float64(tt.Bounces)/float64(tt.Visits), *tt.BounceRate, 1e-12)
			require.InDelta(t, float64(tt.Totaltime)/float64(tt.Visits), *tt.AvgVisitDuration, 1e-12)
			// the quarantine share is quarantined over all page views
			pv := stats(primary(sc.sites...), rg.Umami()).Pageviews
			qv := stats(quarantine(sc.sites...), rg.Umami()).Pageviews
			require.InDelta(t, float64(qv)/float64(pv+qv), *s.QuarantineShare, 1e-12)
		})

		for _, dim := range webdash.DimensionNames {
			t.Run("breakdown equals umami: "+sc.name+" "+dim, func(t *testing.T) {
				var b webdash.Breakdown
				if strings.HasPrefix(dim, "utm") && len(sc.sites) == 1 && sc.sites[0] != sites[0] {
					// asking one site that keeps no utm values is refused
					st, code := c.fail(t, base+"/breakdown?"+q(sc.extra+"&dimension="+dim))
					require.Equal(t, http.StatusBadRequest, st)
					require.Equal(t, httpx.CodeInvalidQuery, code)
					return
				}
				c.ok(t, base+"/breakdown?"+q(sc.extra+"&dimension="+dim+"&limit=100"), &b)
				typ := map[string]string{
					"page": "path", "entry_page": "entry", "exit_page": "exit", "event": "event",
					"utm_source": "utmSource", "utm_medium": "utmMedium", "utm_campaign": "utmCampaign",
					"utm_content": "utmContent", "utm_term": "utmTerm",
				}[dim]
				if typ == "" {
					typ = dim
					require.Equal(t, webdash.SourceAggregate, b.Source)
				} else {
					require.Equal(t, webdash.SourceEngine, b.Source)
				}
				ss := sc.sites
				if strings.HasPrefix(dim, "utm") {
					ss = nil
					for _, s := range sc.sites {
						if s == sites[0] {
							ss = append(ss, s)
						}
					}
				}
				perSite := dim == "page" || dim == "entry_page" || dim == "exit_page"
				want := map[string]int64{}
				for i, w := range sc.band(ss...) {
					for k, v := range metricsMap(t, e.um, w, typ, rg.Umami()) {
						if perSite {
							k = ss[i].id.String() + " " + k
						}
						want[k] += v
					}
				}
				got := map[string]int64{}
				for _, it := range b.Items {
					k := it.Key
					if perSite {
						require.NotNil(t, it.Site)
						k = it.Site.String() + " " + k
					}
					got[k] = it.Value
				}
				require.Less(t, len(want), 100, "the limit holds every value")
				require.Equal(t, want, got)
				require.Equal(t, int64(len(want)), b.TotalKeys)
				if dim == "utm_source" && sc.bandArg == "primary" && (sc.extra == "" || len(ss) > 0) {
					require.Positive(t, got["newsletter"])
				}
			})
		}
	}

	t.Run("a limit keeps the largest values", func(t *testing.T) {
		var full, two webdash.Breakdown
		c.ok(t, base+"/breakdown?"+q("&dimension=page&limit=100"), &full)
		c.ok(t, base+"/breakdown?"+q("&dimension=page&limit=2"), &two)
		require.Equal(t, full.Items[:2], two.Items)
		require.Equal(t, full.TotalKeys, two.TotalKeys)
	})

	t.Run("hourly equals umami", func(t *testing.T) {
		for _, d := range []time.Time{today, today.AddDate(0, 0, -2)} {
			var h webdash.Hourly
			c.ok(t, base+"/hourly?day="+d.Format(time.DateOnly), &h)
			require.Len(t, h.Points, 24)
			r := webdash.Days{From: d, To: d}.Umami()
			want := map[time.Time][2]int64{}
			for _, s := range sites {
				pv, err := e.um.Admin.Pageviews(ctx, s.id.String(), umami.PageviewsQuery{Range: r, Unit: umami.UnitHour, Timezone: "UTC"})
				require.NoError(t, err)
				for _, p := range pv.Pageviews {
					at := bucket(t, p.X)
					v := want[at]
					v[0] += p.Y
					want[at] = v
				}
				for _, p := range pv.Sessions {
					at := bucket(t, p.X)
					v := want[at]
					v[1] += p.Y
					want[at] = v
				}
			}
			var pageviews int64
			for _, p := range h.Points {
				at := time.Time(p.At)
				require.Equal(t, want[at], [2]int64{p.Pageviews, p.Visitors}, at)
				delete(want, at)
				pageviews += p.Pageviews
			}
			require.Empty(t, want, "every umami bucket is on the grid")
			// the hours of a day add up to the day's aggregate
			require.Equal(t, stats(primary(sites...), r).Pageviews, pageviews)
		}
	})

	t.Run("realtime equals umami", func(t *testing.T) {
		var rt webdash.Realtime
		c.ok(t, base+"/realtime", &rt)
		var want int64
		for _, s := range sites {
			var active struct {
				Visitors json.Number `json:"visitors"`
			}
			tok := adminToken(t, e)
			require.NoError(t, e.um.Client.ReadWebsite(ctx, tok, s.id.String(), "/active", nil, &active))
			n, err := strconv.ParseInt(active.Visitors.String(), 10, 64)
			require.NoError(t, err)
			want += n
		}
		require.Positive(t, want)
		require.Equal(t, want, rt.Visitors)
		require.Len(t, rt.Sites, 2)
		require.Len(t, rt.Minutes, webdash.MinutesWindow)
		var pv int64
		for _, m := range rt.Minutes {
			pv += m.Pageviews
		}
		require.GreaterOrEqual(t, pv, int64(2), "the two events of the last minute")
	})

	t.Run("sessions equal umami", func(t *testing.T) {
		d := today.AddDate(0, 0, -1)
		r := webdash.Days{From: d, To: d}.Umami()
		tok := adminToken(t, e)
		for _, band := range []struct {
			arg, website string
		}{{"", sites[0].id.String()}, {"&band=quarantine", sites[0].quarantine.String()}} {
			var want struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
				Count json.Number `json:"count"`
			}
			q := url.Values{"startAt": {strconv.FormatInt(r.Start.UnixMilli(), 10)}, "endAt": {strconv.FormatInt(r.End.UnixMilli(), 10)},
				"page": {"1"}, "pageSize": {strconv.Itoa(webdash.SessionsPageSize)}}
			require.NoError(t, e.um.Client.ReadWebsite(ctx, tok, band.website, "/sessions", q, &want))
			var got webdash.Sessions
			c.ok(t, base+"/sessions?site="+sites[0].id.String()+"&day="+d.Format(time.DateOnly)+band.arg, &got)
			require.NotEmpty(t, got.Items)
			var wantIDs, gotIDs []string
			for _, x := range want.Data {
				wantIDs = append(wantIDs, x.ID)
			}
			for _, x := range got.Items {
				gotIDs = append(gotIDs, x.ID)
				require.Positive(t, x.Views+x.Visits)
				require.False(t, time.Time(x.FirstAt).After(time.Time(x.LastAt)))
			}
			require.Equal(t, wantIDs, gotIDs)
			require.Equal(t, want.Count.String(), strconv.FormatInt(got.Total, 10))
		}
		st, code := c.fail(t, base+"/sessions")
		require.Equal(t, http.StatusBadRequest, st)
		require.Equal(t, httpx.CodeInvalidQuery, code)
	})

	t.Run("isolation", func(t *testing.T) {
		oc := e.client(t, outsider)
		for _, sub := range []string{"/summary", "/breakdown?dimension=page", "/breakdown?dimension=country", "/hourly", "/realtime", "/sessions?day=" + today.Format(time.DateOnly)} {
			// another project's routes are not found
			st, code := oc.fail(t, base+sub)
			require.Equal(t, http.StatusNotFound, st, sub)
			require.Equal(t, httpx.CodeNotFound, code, sub)
			// a site of another project inside one's own project is not found
			sep := "?"
			if strings.Contains(sub, "?") {
				sep = "&"
			}
			st, code = oc.fail(t, "/tenants/"+otherTenant.String()+"/web"+sub+sep+"site="+sites[0].id.String())
			require.Equal(t, http.StatusNotFound, st, sub)
			require.Equal(t, httpx.CodeNotFound, code, sub)
		}
		// the outsider's own project shows only its own numbers
		var s webdash.Summary
		oc.ok(t, "/tenants/"+otherTenant.String()+"/web/summary?"+q(""), &s)
		require.Equal(t, stats(primary(otherSites...), rg.Umami()), asStats(s.Current.Totals))
		require.Len(t, s.Sites, 1)
	})

	t.Run("refusals", func(t *testing.T) {
		for _, p := range []string{
			"/summary?from=2026-01-01&to=2025-01-01", "/summary?site=nope", "/summary?band=all", "/summary?x=1",
			"/breakdown?dimension=title", "/breakdown?dimension=page&limit=0", "/breakdown?dimension=page&limit=101",
			"/hourly?day=" + today.AddDate(0, 0, 1).Format(time.DateOnly), "/realtime?band=primary",
			"/breakdown?dimension=utm_source&site=" + sites[1].id.String(),
		} {
			st, code := c.fail(t, base+p)
			require.Equal(t, http.StatusBadRequest, st, p)
			require.Equal(t, httpx.CodeInvalidQuery, code, p)
		}
		st, _ := c.fail(t, base+"/summary?site="+uuid.NewString())
		require.Equal(t, http.StatusNotFound, st)
	})

	t.Run("a site in review answers provisioning_pending on engine routes", func(t *testing.T) {
		e.sys(t, func(tx pgx.Tx) error {
			// as cp_system, the way the operator review sets it
			_, err := tx.Exec(ctx, `update cp.sites set status = 'pending' where id = $1`, sites[1].id)
			return err
		})
		defer e.sys(t, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `update cp.sites set status = 'active' where id = $1`, sites[1].id)
			return err
		})
		// a site in review is not in umami's reads of the project
		var rt webdash.Realtime
		c.ok(t, base+"/realtime", &rt)
		require.Len(t, rt.Sites, 1)
		// and has nothing to read on its own
		var h webdash.Hourly
		c.ok(t, base+"/hourly?site="+sites[1].id.String(), &h)
		require.Empty(t, h.Sites)
	})

	t.Run("a member without a umami user yet gets provisioning_pending", func(t *testing.T) {
		late := e.user(t)
		e.sys(t, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, tenant, late)
			return err
		})
		lc := e.client(t, late)
		st, code := lc.fail(t, base+"/realtime")
		require.Equal(t, http.StatusConflict, st)
		require.Equal(t, webdash.CodeProvisioningPending, code)
		// the aggregates need no umami user
		var s webdash.Summary
		lc.ok(t, base+"/summary?"+q(""), &s)
		require.Equal(t, stats(primary(sites...), rg.Umami()), asStats(s.Current.Totals))
		// after reconcile the engine routes answer
		_, err := e.rec.Changes(ctx, tenant)
		require.NoError(t, err)
		lc.ok(t, base+"/realtime", &webdash.Realtime{})
	})
}

func bucket(t *testing.T, x string) time.Time {
	t.Helper()
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if v, err := time.Parse(layout, x); err == nil {
			return v.UTC()
		}
	}
	t.Fatalf("bucket %q", x)
	return time.Time{}
}

func adminToken(t *testing.T, e *env) umami.Token {
	t.Helper()
	s, err := e.um.Client.Login(t.Context(), umami.SeededUsername, e.um.AdminPassword)
	require.NoError(t, err)
	return s.Token
}
