package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/docs/reference/openapi"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/umami"
	"github.com/justmert/veristar/tests/fixtures/umamictr"
)

// machineEnv is the control plane as cmd/controlplane wires it:
// the session api with the operator guard and the key routes, and the
// machine api mounted beside it, over the operator views' fixture database and a
// real umami.
type machineEnv struct {
	f      *globalFixture
	um     *umamictr.Stack
	srv    *httptest.Server
	limSrv *httptest.Server
	router routers.Router
	opID   uuid.UUID
	op     *http.Client
	plain  *http.Client
	anon   *http.Client
	// the tenant with real umami websites, for /web/hourly
	hourly      uuid.UUID
	hourlySites []jobstest.WebSite
}

func newMachineEnv(t *testing.T) *machineEnv {
	t.Helper()
	f := newGlobalFixture(t)
	um := umamictr.Start(t)
	ns := jobstest.NATS(t)
	_, js := jobstest.Connect(t, ns.URL)
	e := &machineEnv{f: f, um: um, opID: uuid.Must(uuid.NewV7())}
	e.hourly, e.hourlySites = jobstest.NewWebTenant(t, f.pg, um, js, "Hourly", 2)
	plainID := uuid.Must(uuid.NewV7())
	superExec(t, f.pg, `insert into cp.users (id, github_id, github_login, is_operator) values ($1, 7, 'op', true), ($2, 8, 'plain', false)`,
		e.opID, plainID)

	d := f.g.db
	sessions := session.New(d)
	guard, err := NewGuard(map[int64]bool{7: true})
	require.NoError(t, err)
	keys, err := NewAPIKeys(d)
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)
	// a bucket large enough that the many requests of this test never meet
	// it; the limit itself is tested on limSrv
	machine, err := NewMachine(MachineConfig{DB: d, Umami: um.Admin, Log: log, Rate: 100000, Burst: 100000})
	require.NoError(t, err)
	limited, err := NewMachine(MachineConfig{DB: d, Umami: um.Admin, Log: log, Rate: 1, Burst: 2})
	require.NoError(t, err)

	var api atomic.Pointer[http.Handler]
	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*api.Load()).ServeHTTP(w, r)
	}))
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	h := controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: e.srv.URL, Sessions: sessions,
		// a sign-in shortcut for the test: the session is the real one,
		// only the oauth round trip is skipped
		Public: []controlplane.Routes{controlplane.RoutesFunc(func(r chi.Router) {
			r.Get("/test-sign-in/{id}", func(w http.ResponseWriter, r *http.Request) {
				if err := sessions.SignIn(r.Context(), uuid.MustParse(chi.URLParam(r, "id"))); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
		})},
		Private: []controlplane.Routes{controlplane.RoutesFunc(guard.Mount(f.g, keys))},
		Mounts:  []controlplane.Mount{{Path: MachinePath, Handler: machine.Handler()}},
	})
	api.Store(&h)
	limRoot := chi.NewRouter()
	limRoot.Mount(MachinePath, limited.Handler())
	e.limSrv = httptest.NewServer(limRoot)
	t.Cleanup(e.limSrv.Close)

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(openapi.MetricsV1)
	require.NoError(t, err)
	require.NoError(t, doc.Validate(loader.Context))
	doc.Servers = openapi3.Servers{{URL: e.srv.URL + MachinePath}, {URL: e.limSrv.URL + MachinePath}}
	e.router, err = gorillamux.NewRouter(doc)
	require.NoError(t, err)

	client := func(user *uuid.UUID) *http.Client {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		hc := *e.srv.Client()
		hc.Jar = jar
		if user != nil {
			res, err := hc.Get(e.srv.URL + "/api/test-sign-in/" + user.String())
			require.NoError(t, err)
			_ = res.Body.Close()
			require.Equal(t, http.StatusNoContent, res.StatusCode)
		}
		return &hc
	}
	e.anon, e.plain, e.op = client(nil), client(&plainID), client(&e.opID)
	return e
}

type answer struct {
	code   int
	header http.Header
	body   []byte
}

func (a answer) errCode(t *testing.T) string {
	t.Helper()
	var env struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(a.body, &env), string(a.body))
	return env.Error.Code
}

// do sends a request with the client's cookies (if any) and an optional key.
func do(t *testing.T, hc *http.Client, method, u, key, body string) (*http.Request, answer) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, u, rd)
	require.NoError(t, err)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", strings.TrimSuffix(u[:strings.Index(u, "/api/")], "/"))
	}
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return req, answer{code: res.StatusCode, header: res.Header, body: b}
}

// machine calls the machine api with key and checks the answer against the
// openapi document: the status must be one it defines for the route, and
// headers and body must match its schema. a request expected to succeed is
// validated against the document too.
func (e *machineEnv) machine(t *testing.T, base, key, path string, want int) answer {
	t.Helper()
	req, a := do(t, e.anon, http.MethodGet, base+MachinePath+path, key, "")
	require.Equal(t, want, a.code, "%s: %s", path, a.body)
	require.Equal(t, "no-store", a.header.Get("Cache-Control"), path)
	route, params, err := e.router.FindRoute(req)
	require.NoError(t, err, path)
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, IncludeResponseStatus: true}
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: opts}
	if want == http.StatusOK {
		require.NoError(t, openapi3filter.ValidateRequest(t.Context(), in), path)
	}
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: a.code, Header: a.header, Options: opts}
	out.SetBodyBytes(a.body)
	require.NoError(t, openapi3filter.ValidateResponse(t.Context(), out), "%s: %s", path, a.body)
	return a
}

func (e *machineEnv) get(t *testing.T, key, path string, want int) answer {
	t.Helper()
	return e.machine(t, e.srv.URL, key, path, want)
}

// list fetches every page of a machine list with a small page size and
// checks that the pages join up to the answer of one large page.
func list[T any](t *testing.T, e *machineEnv, key, path string, pageSize int) []T {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	type page struct {
		Items      []T     `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	whole := decode[page](t, e.get(t, key, path, http.StatusOK).body)
	require.Less(t, len(whole.Items), machineMaxLimit, "%s: the fixture fits one page of 500", path)
	require.Nil(t, whole.NextCursor)
	var all []T
	cursor := ""
	for n := 0; ; n++ {
		require.Less(t, n, 1000, path)
		q := sep + "limit=" + fmt.Sprint(pageSize)
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		p := decode[page](t, e.get(t, key, path+q, http.StatusOK).body)
		require.LessOrEqual(t, len(p.Items), pageSize)
		all = append(all, p.Items...)
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	require.Equal(t, whole.Items, all, "%s: pages of %d join up to the whole list", path, pageSize)
	return all
}

func (e *machineEnv) createKey(t *testing.T, body string) CreatedAPIKey {
	t.Helper()
	_, a := do(t, e.op, http.MethodPost, e.srv.URL+"/api/operator/api-keys", "", body)
	require.Equal(t, http.StatusCreated, a.code, string(a.body))
	k := decode[CreatedAPIKey](t, a.body)
	require.Equal(t, "/api/operator/api-keys/"+k.ID.String(), a.header.Get("Location"))
	return k
}

func TestMachineAPI(t *testing.T) {
	e := newMachineEnv(t)
	f := e.f
	ids := f.tenants
	today := f.today
	from := today.AddDate(0, 0, -(fixtureDays - 1))
	rq := "from=" + from.Format(time.DateOnly) + "&to=" + today.Format(time.DateOnly)

	// hourly api rows for the current and the previous hour, written as the
	// rollup writes them
	hour := time.Now().UTC().Truncate(time.Hour)
	for _, name := range []string{"aurora", "borealis", "probe"} {
		for i, h := range []time.Time{hour.Add(-time.Hour), hour} {
			for j, svc := range []string{"api", "worker"} {
				src := "metric"
				if name == "borealis" {
					src = "span"
				}
				counts := make([]int64, 15)
				counts[2+i+j] = int64(5 + i + 3*j)
				counts[14] = int64(j)
				var total int64
				for _, c := range counts {
					total += c
				}
				superExec(t, f.pg, `insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy)
					values ($1, $2, $3, 'GET', '/a', 200, $4, $5, 1, $6, false)`, ids[name], h, svc, src, counts, total)
			}
		}
	}
	// page views in the current and the previous hour on both websites of
	// both sites of the hourly tenant
	for i, s := range e.hourlySites {
		for j, at := range []time.Time{hour.Add(-50 * time.Minute), time.Now()} {
			for k, website := range []string{s.Site.ID, s.Quarantine} {
				for v := range 2 + i + j {
					_, err := e.um.Client.Send(t.Context(), umami.SendRequest{
						Event: umami.Event{Website: website, Hostname: s.Domain, URL: fmt.Sprintf("/p%d", v), Title: "t",
							Timestamp: at.Unix(), Salt: fmt.Sprintf("vt-%d-%d-%d-%d", i, j, k, v)},
						UserAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
						Country:   "DE",
					})
					require.NoError(t, err)
				}
			}
		}
	}

	global := e.createKey(t, `{"name":"warehouse","scope":"metrics:read"}`)
	require.Nil(t, global.TenantID)
	require.Equal(t, KeyActive, global.Status)
	require.Equal(t, ScopeMetricsRead, global.Scope)
	require.True(t, strings.HasPrefix(global.Key, "sa_op_"))
	require.Len(t, global.Key, len("sa_op_")+40)
	require.Equal(t, global.Key[:10], global.Prefix)
	expiry := time.Time(global.ExpiresAt).Sub(time.Time(global.CreatedAt))
	require.Equal(t, 90*24*time.Hour, expiry.Round(time.Second), "default lifetime is 90 days")
	aurora := e.createKey(t, fmt.Sprintf(`{"name":"aurora feed","scope":"metrics:read","tenant_id":%q,"expires_in_days":365}`, ids["aurora"]))
	require.Equal(t, ids["aurora"], *aurora.TenantID)
	probe := e.createKey(t, fmt.Sprintf(`{"name":"prober","scope":"metrics:read","tenant_id":%q,"expires_in_days":30}`, ids["probe"]))
	hourly := e.createKey(t, fmt.Sprintf(`{"name":"hourly","scope":"metrics:read","tenant_id":%q}`, e.hourly))
	visible := []uuid.UUID{ids["aurora"], ids["borealis"], ids["comet"], ids["dune"], ids["eclipse"], ids["flare"], e.hourly}

	t.Run("every route answers as the document says", func(t *testing.T) {
		paths := []string{
			"/tenants",
			"/web/daily?" + rq, "/web/daily?band=quarantine&tenant=" + ids["aurora"].String(),
			"/web/dimensions?" + rq, "/web/dimensions?dimension=country&band=primary&" + rq,
			"/web/hourly?tenant=" + e.hourly.String(),
			"/api/daily?" + rq, "/api/daily?service=api&" + rq, "/api/daily?service=&" + rq,
			"/api/routes/daily?" + rq, "/api/routes/daily?service=worker&tenant=" + ids["flare"].String(),
			"/api/hourly", "/api/hourly?service=worker&from=" + url.QueryEscape(httpTime(hour.Add(-5*time.Hour))),
		}
		for _, p := range paths {
			e.get(t, global.Key, p, http.StatusOK)
		}
		for _, p := range []string{"/tenants", "/web/daily", "/web/dimensions", "/api/daily", "/api/routes/daily", "/api/hourly"} {
			e.get(t, aurora.Key, p, http.StatusOK)
		}
		e.get(t, hourly.Key, "/web/hourly", http.StatusOK)
		// the served document is the embedded file, byte for byte
		a := e.get(t, global.Key, "/openapi.yaml", http.StatusOK)
		require.Equal(t, openapi.MetricsV1, a.body)
		require.Equal(t, "application/yaml", a.header.Get("Content-Type"))
	})

	t.Run("errors answer as the document says", func(t *testing.T) {
		for _, c := range []struct {
			key, path string
			code      int
			errCode   string
		}{
			{"", "/tenants", 401, "unauthenticated"},
			{"sa_op_short", "/tenants", 401, "unauthenticated"},
			{"sa_op_" + strings.Repeat("A", 40), "/web/daily", 401, "unauthenticated"},
			{global.Key, "/tenants?bogus=1", 400, "invalid_query"},
			{global.Key, "/web/daily?from=yesterday", 400, "invalid_query"},
			{global.Key, "/web/daily?to=" + today.AddDate(0, 0, 1).Format(time.DateOnly), 400, "invalid_query"},
			{global.Key, "/web/daily?from=2020-01-01&to=2021-06-01", 400, "invalid_query"},
			{global.Key, "/web/daily?band=both", 400, "invalid_query"},
			{global.Key, "/web/daily?tenant=AB", 400, "invalid_query"},
			{global.Key, "/web/daily?tenant=" + ids["aurora"].String() + "&tenant=" + ids["aurora"].String(), 400, "invalid_query"},
			{global.Key, "/web/dimensions?dimension=city", 400, "invalid_query"},
			{global.Key, "/web/hourly", 400, "invalid_query"},
			{global.Key, "/web/hourly?tenant=" + e.hourly.String() + "&from=" + url.QueryEscape(httpTime(hour.Add(-24*time.Hour))), 400, "invalid_query"},
			{global.Key, "/web/hourly?tenant=" + e.hourly.String() + "&to=" + url.QueryEscape(httpTime(hour.Add(time.Hour))), 400, "invalid_query"},
			{global.Key, "/api/hourly?from=2026-11-12T09:30:00Z", 400, "invalid_query"},
			{global.Key, "/api/daily?service=" + url.QueryEscape("a\x01"), 400, "invalid_query"},
			{global.Key, "/api/routes/daily?limit=501", 400, "invalid_query"},
			{global.Key, "/api/routes/daily?cursor=nope", 400, "invalid_cursor"},
			{global.Key, "/web/daily?tenant=" + ids["probe"].String(), 404, "not_found"},
			{global.Key, "/web/daily?tenant=" + uuid.Must(uuid.NewV7()).String(), 404, "not_found"},
			{aurora.Key, "/web/daily?tenant=" + ids["borealis"].String(), 403, "scope_forbidden"},
			{aurora.Key, "/api/daily?tenant=" + uuid.Must(uuid.NewV7()).String(), 403, "scope_forbidden"},
		} {
			a := e.get(t, c.key, c.path, c.code)
			require.Equal(t, c.errCode, a.errCode(t), c.path)
			if c.code == 401 {
				require.Equal(t, `Bearer realm="metrics"`, a.header.Get("WWW-Authenticate"))
			}
		}
		// a cursor is bound to the query and to the key it was made for
		p := decode[MachineRangeList[MachineAPIRoute]](t, e.get(t, global.Key, "/api/routes/daily?limit=3&"+rq, 200).body)
		require.NotNil(t, p.NextCursor)
		c := url.QueryEscape(*p.NextCursor)
		e.get(t, global.Key, "/api/routes/daily?limit=3&cursor="+c+"&"+rq, 200)
		e.get(t, global.Key, "/api/routes/daily?limit=3&cursor="+c+"&service=api&"+rq, 400)
		e.get(t, global.Key, "/api/daily?limit=3&cursor="+c+"&"+rq, 400)
		e.get(t, probe.Key, "/api/routes/daily?limit=3&cursor="+c+"&"+rq, 400)
		// only GET exists
		_, a := do(t, e.anon, http.MethodPost, e.srv.URL+MachinePath+"/tenants", global.Key, `{}`)
		require.Equal(t, http.StatusMethodNotAllowed, a.code, string(a.body))
		_, a = do(t, e.anon, http.MethodGet, e.srv.URL+MachinePath+"/nothing", global.Key, "")
		require.Equal(t, http.StatusNotFound, a.code, string(a.body))
	})

	t.Run("a key for all tenants sees every tenant but internal ones", func(t *testing.T) {
		ts := list[MachineTenant](t, e, global.Key, "/tenants", 2)
		var got []uuid.UUID
		for _, x := range ts {
			got = append(got, x.ID)
			if x.ID == ids["aurora"] {
				require.Equal(t, int64(2), x.Sites)
				require.Equal(t, int64(1), x.SitesVerified)
				require.True(t, x.Verified)
				require.True(t, x.StellarToml)
			}
			if x.ID == ids["borealis"] {
				require.False(t, x.Verified, "a failed check is not verified (5.8)")
			}
		}
		require.ElementsMatch(t, visible, got)
		for _, p := range []string{"/web/daily?" + rq, "/api/daily?" + rq, "/api/hourly"} {
			for _, it := range decode[MachineRangeList[struct {
				TenantID uuid.UUID `json:"tenant_id"`
			}]](t, e.get(t, global.Key, p, 200).body).Items {
				require.NotEqual(t, ids["probe"], it.TenantID, p)
			}
		}
	})

	t.Run("a scoped key sees only its tenant", func(t *testing.T) {
		ts := decode[MachineList[MachineTenant]](t, e.get(t, aurora.Key, "/tenants", 200).body)
		require.Len(t, ts.Items, 1)
		require.Equal(t, ids["aurora"], ts.Items[0].ID)
		for _, p := range []string{"/web/daily?" + rq, "/web/dimensions?" + rq, "/api/daily?" + rq, "/api/routes/daily?" + rq, "/api/hourly",
			"/web/daily?tenant=" + ids["aurora"].String()} {
			items := decode[MachineRangeList[struct {
				TenantID uuid.UUID `json:"tenant_id"`
			}]](t, e.get(t, aurora.Key, p, 200).body).Items
			require.NotEmpty(t, items, p)
			for _, it := range items {
				require.Equal(t, ids["aurora"], it.TenantID, p)
			}
		}
		for _, other := range []string{"borealis", "probe"} {
			for _, p := range []string{"/web/daily", "/web/dimensions", "/web/hourly", "/api/daily", "/api/routes/daily", "/api/hourly"} {
				e.get(t, aurora.Key, p+"?tenant="+ids[other].String(), 403)
			}
		}
		// the probe's key reads the internal probe tenant, which a key for
		// all tenants never sees
		pt := decode[MachineList[MachineTenant]](t, e.get(t, probe.Key, "/tenants", 200).body)
		require.Len(t, pt.Items, 1)
		require.Equal(t, ids["probe"], pt.Items[0].ID)
		ph := decode[MachineRangeList[MachineAPIHour]](t, e.get(t, probe.Key, "/api/hourly", 200).body)
		require.Len(t, ph.Items, 2)
		for _, it := range ph.Items {
			require.Equal(t, ids["probe"], it.TenantID)
		}
		e.get(t, global.Key, "/api/hourly?tenant="+ids["probe"].String(), 404)
	})

	t.Run("numbers equal the operator views", func(t *testing.T) {
		opGet := func(path string) []byte {
			_, a := do(t, e.op, http.MethodGet, e.srv.URL+"/api/operator"+path, "", "")
			require.Equal(t, http.StatusOK, a.code, string(a.body))
			return a.body
		}
		days := list[MachineWebDay](t, e, global.Key, "/web/daily?"+rq, 7)
		type sums struct{ pv, vis, visits, bounces, tt, qpv, qvis int64 }
		web := map[uuid.UUID]*sums{}
		for _, d := range days {
			s := web[d.TenantID]
			if s == nil {
				s = &sums{}
				web[d.TenantID] = s
			}
			if d.Band == "primary" {
				s.pv += d.Pageviews
				s.vis += d.Visitors
				s.visits += d.Visits
				s.bounces += d.Bounces
				s.tt += d.Totaltime
			} else {
				s.qpv += d.Pageviews
				s.qvis += d.Visitors
			}
		}
		projects := decode[GlobalList[WebProject]](t, opGet("/web/projects?limit=200&"+rq))
		require.Len(t, projects.Items, len(visible))
		for _, p := range projects.Items {
			s := web[p.Tenant.ID]
			if s == nil {
				s = &sums{}
			}
			require.Equal(t, sums{p.Pageviews, p.Visitors, p.Visits, p.Bounces, p.Totaltime, p.QuarantinePageviews, p.QuarantineVisitors}, *s, p.Tenant.Name)
		}

		// one dimension per request keeps each list within one page of 500
		var dims []MachineWebDimension
		for _, dim := range webDims {
			dims = append(dims, list[MachineWebDimension](t, e, global.Key, "/web/dimensions?band=primary&dimension="+dim+"&"+rq, 97)...)
		}
		cmpIDs := []uuid.UUID{ids["aurora"], ids["borealis"], ids["eclipse"], ids["flare"]}
		var idStrs []string
		for _, id := range cmpIDs {
			idStrs = append(idStrs, id.String())
		}
		cmp := decode[WebCompare](t, opGet("/web/compare?breakdown_limit=500&tenants="+strings.Join(idStrs, ",")+"&"+rq))
		for _, p := range cmp.Projects {
			for _, dim := range webDims {
				want := map[string]int64{}
				for _, d := range dims {
					if d.TenantID == p.Tenant.ID && d.Dimension == dim {
						want[d.Key] += d.Value
					}
				}
				got := map[string]int64{}
				for _, kv := range p.Breakdowns[dim].Items {
					got[kv.Key] = kv.Value
				}
				require.Equal(t, want, got, "%s %s", p.Tenant.Name, dim)
				require.Equal(t, int64(len(want)), p.Breakdowns[dim].TotalKeys)
			}
			for _, pt := range p.Series {
				var pv *int64
				for _, d := range days {
					if d.TenantID == p.Tenant.ID && d.Day == pt.Day && d.Band == "primary" {
						v := d.Pageviews
						pv = &v
					}
				}
				require.Equal(t, pt.Aggregated, pv != nil, "%s %s", p.Tenant.Name, pt.Day)
				require.Equal(t, pt.Pageviews, pv, "%s %s", p.Tenant.Name, pt.Day)
			}
		}

		apiDays := list[MachineAPIDay](t, e, global.Key, "/api/daily?"+rq, 6)
		routes := list[MachineAPIRoute](t, e, global.Key, "/api/routes/daily?"+rq, 50)
		apiProjects := decode[GlobalList[APIProject]](t, opGet("/api/projects?limit=200&"+rq))
		for _, p := range apiProjects.Items {
			var req, r4, r5 int64
			for _, d := range apiDays {
				if d.TenantID == p.Tenant.ID {
					req, r4, r5 = req+d.Requests, r4+d.Requests4xx, r5+d.Requests5xx
				}
			}
			require.Equal(t, [3]int64{p.Requests, p.Requests4xx, p.Requests5xx}, [3]int64{req, r4, r5}, p.Tenant.Name)
			services, pairs := map[string]bool{}, map[string]bool{}
			var rreq int64
			for _, rt := range routes {
				if rt.TenantID != p.Tenant.ID {
					continue
				}
				rreq += rt.Requests
				services[rt.Service] = true
				if rt.Route != "" {
					pairs[rt.Service+"\x00"+rt.Route] = true
				}
				if p.Source != nil && *p.Source != "mixed" {
					require.Equal(t, *p.Source, rt.Source, p.Tenant.Name)
				}
			}
			require.Equal(t, p.Requests, rreq, p.Tenant.Name)
			require.Equal(t, p.Services, int64(len(services)), p.Tenant.Name)
			require.Equal(t, p.Routes, int64(len(pairs)), p.Tenant.Name)
		}
		apiCmp := decode[APICompare](t, opGet("/api/compare?tenants="+ids["aurora"].String()+","+ids["comet"].String()+","+ids["flare"].String()+"&"+rq))
		for _, p := range apiCmp.Projects {
			for _, pt := range p.Series {
				var got *MachineAPIDay
				for i := range apiDays {
					if apiDays[i].TenantID == p.Tenant.ID && apiDays[i].Day == pt.Day {
						got = &apiDays[i]
					}
				}
				if pt.Requests == 0 {
					require.Nil(t, got, "%s %s", p.Tenant.Name, pt.Day)
					continue
				}
				require.NotNil(t, got, "%s %s", p.Tenant.Name, pt.Day)
				require.Equal(t, pt.APINumbers, got.APINumbers, "%s %s: percentiles from the same summed buckets", p.Tenant.Name, pt.Day)
			}
			// one service: the compare's per-service numbers over the range
			for _, s := range p.Services {
				sd := decode[MachineRangeList[MachineAPIDay]](t, e.get(t, global.Key,
					"/api/daily?tenant="+p.Tenant.ID.String()+"&service="+url.QueryEscape(s.Service)+"&"+rq, 200).body)
				var n int64
				for _, d := range sd.Items {
					n += d.Requests
					require.Equal(t, s.Source, d.Source)
				}
				require.Equal(t, s.Requests, n, "%s service %q", p.Tenant.Name, s.Service)
			}
		}
	})

	t.Run("api hourly reads the hourly rollup", func(t *testing.T) {
		h := decode[MachineRangeList[MachineAPIHour]](t, e.get(t, global.Key, "/api/hourly", 200).body)
		require.Equal(t, httpTime(hour), h.To)
		require.Equal(t, httpTime(hour.Add(-23*time.Hour)), h.From)
		got := map[string]MachineAPIHour{}
		for _, it := range h.Items {
			got[it.TenantID.String()+" "+httpTime(time.Time(it.Hour))] = it
		}
		require.Len(t, got, 4, "aurora and borealis, two hours each")
		for _, name := range []string{"aurora", "borealis"} {
			for i, at := range []time.Time{hour.Add(-time.Hour), hour} {
				it := got[ids[name].String()+" "+httpTime(at)]
				require.Equal(t, int64(5+i)+int64(5+i+3)+1, it.Requests, name)
				require.Equal(t, int64(2), it.Services)
				want := "metric"
				if name == "borealis" {
					want = "span"
				}
				require.Equal(t, want, it.Source)
			}
		}
		one := decode[MachineRangeList[MachineAPIHour]](t, e.get(t, global.Key,
			"/api/hourly?service=worker&tenant="+ids["aurora"].String()+"&from="+url.QueryEscape(httpTime(hour)), 200).body)
		require.Len(t, one.Items, 1)
		require.Equal(t, int64(5+1+3+1), one.Items[0].Requests)
		require.True(t, one.Items[0].P99Over10s, "one request of 10 is above 10 s")
		require.Nil(t, one.Items[0].P99Seconds)
	})

	t.Run("web hourly reads umami live", func(t *testing.T) {
		for _, key := range []string{hourly.Key, global.Key} {
			a := e.get(t, key, "/web/hourly?tenant="+e.hourly.String()+"&from="+url.QueryEscape(httpTime(hour.Add(-2*time.Hour))), 200)
			wh := decode[MachineWebHourly](t, a.body)
			require.Equal(t, e.hourly, wh.TenantID)
			require.Len(t, wh.Items, 6, "three hours, two bands")
			var total int64
			for _, it := range wh.Items {
				h := time.Time(it.Hour)
				require.Equal(t, int64(2), it.Sites)
				require.Equal(t, h.Before(hour), it.Complete, httpTime(h))
				var want umami.StatsValues
				for _, s := range e.hourlySites {
					website := s.Site.ID
					if it.Band == "quarantine" {
						website = s.Quarantine
					}
					st, err := e.um.Admin.Stats(t.Context(), website, umami.Range{Start: h, End: h.Add(time.Hour - time.Millisecond)})
					require.NoError(t, err)
					want.Pageviews += st.Pageviews
					want.Visitors += st.Visitors
					want.Visits += st.Visits
					want.Bounces += st.Bounces
					want.Totaltime += st.Totaltime
				}
				require.Equal(t, want, umami.StatsValues{Pageviews: it.Pageviews, Visitors: it.Visitors, Visits: it.Visits,
					Bounces: it.Bounces, Totaltime: it.Totaltime}, "%s %s", httpTime(h), it.Band)
				total += it.Pageviews
			}
			// 2+3 views per site and band in the earlier hour, 3+4 in the current one
			require.Equal(t, int64(2*(5+7)), total)
		}
	})

	t.Run("operators manage keys and nobody else does", func(t *testing.T) {
		for _, hc := range []*http.Client{e.plain, e.anon} {
			want := http.StatusForbidden
			if hc == e.anon {
				want = http.StatusUnauthorized
			}
			for _, c := range []struct{ method, path, body string }{
				{http.MethodGet, "/api/operator/api-keys", ""},
				{http.MethodGet, "/api/operator/api-keys/" + global.ID.String(), ""},
				{http.MethodGet, "/api/operator/api-keys/" + global.ID.String() + "/usage", ""},
				{http.MethodPost, "/api/operator/api-keys", `{"name":"x","scope":"metrics:read"}`},
				{http.MethodDelete, "/api/operator/api-keys/" + global.ID.String(), ""},
			} {
				_, a := do(t, hc, c.method, e.srv.URL+c.path, "", c.body)
				require.Equal(t, want, a.code, "%s %s: %s", c.method, c.path, a.body)
				if want == http.StatusForbidden {
					require.Equal(t, "operator_required", a.errCode(t))
				}
			}
		}
		// the global key still works: nothing above revoked it
		e.get(t, global.Key, "/tenants", 200)

		for _, c := range []struct{ body, field, code string }{
			{`{"scope":"metrics:read"}`, "name", "required"},
			{`{"name":" ","scope":"metrics:read"}`, "name", "required"},
			{`{"name":"x"}`, "scope", "required"},
			{`{"name":"x","scope":"metrics:write"}`, "scope", "invalid_value"},
			{`{"name":"x","scope":"metrics:read","expires_in_days":366}`, "expires_in_days", "invalid_value"},
			{`{"name":"x","scope":"metrics:read","expires_in_days":0}`, "expires_in_days", "invalid_value"},
			{`{"name":"x","scope":"metrics:read","tenant_id":"nope"}`, "tenant_id", "invalid_format"},
			{fmt.Sprintf(`{"name":"x","scope":"metrics:read","tenant_id":%q}`, uuid.Must(uuid.NewV7())), "tenant_id", FieldUnknownTenant},
			{fmt.Sprintf(`{"name":"x","scope":"metrics:read","tenant_id":%q}`, ids["eclipse"]), "tenant_id", FieldUnknownTenant},
		} {
			_, a := do(t, e.op, http.MethodPost, e.srv.URL+"/api/operator/api-keys", "", c.body)
			require.Equal(t, http.StatusUnprocessableEntity, a.code, c.body)
			var env struct {
				Error struct{ Fields map[string]string } `json:"error"`
			}
			require.NoError(t, json.Unmarshal(a.body, &env))
			require.Equal(t, c.code, env.Error.Fields[c.field], c.body)
		}
		_, a := do(t, e.op, http.MethodPost, e.srv.URL+"/api/operator/api-keys", "", `{"name":"x","scope":"metrics:read","extra":1}`)
		require.Equal(t, http.StatusBadRequest, a.code)

		// the list pages newest first and never shows a key
		var seen []uuid.UUID
		cursor := ""
		for {
			p := "/api/operator/api-keys?limit=2"
			if cursor != "" {
				p += "&cursor=" + url.QueryEscape(cursor)
			}
			_, a := do(t, e.op, http.MethodGet, e.srv.URL+p, "", "")
			require.Equal(t, http.StatusOK, a.code, string(a.body))
			for _, k := range []string{global.Key, aurora.Key, probe.Key, hourly.Key} {
				require.NotContains(t, string(a.body), k)
				require.NotContains(t, string(a.body), k[10:])
			}
			page := decode[MachineList[APIKey]](t, a.body)
			for _, k := range page.Items {
				seen = append(seen, k.ID)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		require.Equal(t, []uuid.UUID{hourly.ID, probe.ID, aurora.ID, global.ID}, seen)

		// usage: every request of the global key so far, none rejected,
		// and last_used_at set
		_, a = do(t, e.op, http.MethodGet, e.srv.URL+"/api/operator/api-keys/"+global.ID.String(), "", "")
		require.Equal(t, http.StatusOK, a.code)
		k := decode[APIKey](t, a.body)
		require.NotNil(t, k.LastUsedAt)
		_, a = do(t, e.op, http.MethodGet, e.srv.URL+"/api/operator/api-keys/"+aurora.ID.String()+"/usage", "", "")
		require.Equal(t, http.StatusOK, a.code, string(a.body))
		u := decode[Usage](t, a.body)
		require.Positive(t, u.Totals.Requests)
		require.Positive(t, u.Totals.Rejected, "the scope refusals above")
		var direct [2]int64
		require.NoError(t, superRow(t, f, `select coalesce(sum(requests), 0), coalesce(sum(rejected), 0) from cp.api_key_usage_hourly where key_id = $1`,
			aurora.ID).Scan(&direct[0], &direct[1]))
		require.Equal(t, direct, [2]int64{u.Totals.Requests, u.Totals.Rejected})
	})

	t.Run("a revoked key is refused on the very next request", func(t *testing.T) {
		k := e.createKey(t, `{"name":"short lived","scope":"metrics:read"}`)
		e.get(t, k.Key, "/tenants", 200)
		_, a := do(t, e.op, http.MethodDelete, e.srv.URL+"/api/operator/api-keys/"+k.ID.String(), "", "")
		require.Equal(t, http.StatusNoContent, a.code, string(a.body))
		a = e.get(t, k.Key, "/tenants", 401)
		require.Contains(t, string(a.body), "revoked")
		// revoking twice changes nothing and writes one audit row
		_, a = do(t, e.op, http.MethodDelete, e.srv.URL+"/api/operator/api-keys/"+k.ID.String(), "", "")
		require.Equal(t, http.StatusNoContent, a.code)
		_, a = do(t, e.op, http.MethodDelete, e.srv.URL+"/api/operator/api-keys/"+uuid.Must(uuid.NewV7()).String(), "", "")
		require.Equal(t, http.StatusNotFound, a.code)
		var n int64
		require.NoError(t, superRow(t, f, `select count(*) from cp.audit_log where action = 'operator_api_key.revoke' and target = $1
			and actor = $2 and detail ->> 'prefix' = $3`, APIKeyTarget(k.ID), "user:"+e.opID.String(), k.Prefix).Scan(&n))
		require.Equal(t, int64(1), n)
		require.NoError(t, superRow(t, f, `select count(*) from cp.audit_log where action = 'operator_api_key.create' and target = $1`,
			APIKeyTarget(k.ID)).Scan(&n))
		require.Equal(t, int64(1), n)
		_, a = do(t, e.op, http.MethodGet, e.srv.URL+"/api/operator/api-keys/"+k.ID.String(), "", "")
		require.Equal(t, KeyRevoked, decode[APIKey](t, a.body).Status)
		var rejected int64
		require.NoError(t, superRow(t, f, `select sum(rejected) from cp.api_key_usage_hourly where key_id = $1`, k.ID).Scan(&rejected))
		require.Equal(t, int64(1), rejected)
	})

	t.Run("an expired key is refused", func(t *testing.T) {
		k := e.createKey(t, `{"name":"expiring","scope":"metrics:read","expires_in_days":1}`)
		e.get(t, k.Key, "/tenants", 200)
		superExec(t, f.pg, `update cp.operator_api_keys set created_at = now() - interval '2 days', expires_at = now() - interval '1 second' where id = $1`, k.ID)
		a := e.get(t, k.Key, "/web/daily", 401)
		require.Contains(t, string(a.body), "expired")
		_, b := do(t, e.op, http.MethodGet, e.srv.URL+"/api/operator/api-keys/"+k.ID.String(), "", "")
		require.Equal(t, KeyExpired, decode[APIKey](t, b.body).Status)
	})

	t.Run("a key works nowhere else and a cookie not here", func(t *testing.T) {
		for _, p := range []string{"/api/operator/web/projects", "/api/operator/api-keys", "/api/operator/tenants",
			"/api/tenants", "/api/auth/me", "/api/metrics/v2/tenants", "/api/metrics/tenants"} {
			_, a := do(t, e.anon, http.MethodGet, e.srv.URL+p, global.Key, "")
			require.Contains(t, []int{http.StatusUnauthorized, http.StatusNotFound}, a.code, "%s: %s", p, a.body)
		}
		_, a := do(t, e.anon, http.MethodPost, e.srv.URL+"/api/operator/api-keys", global.Key, `{"name":"x","scope":"metrics:read"}`)
		require.Equal(t, http.StatusUnauthorized, a.code)
		// the operator's own session cookie opens nothing under the machine api
		for _, p := range []string{"/tenants", "/web/daily", "/api/daily", "/openapi.yaml"} {
			_, a := do(t, e.op, http.MethodGet, e.srv.URL+MachinePath+p, "", "")
			require.Equal(t, http.StatusUnauthorized, a.code, p)
		}
		// and the operator routes still take it
		_, a = do(t, e.op, http.MethodGet, e.srv.URL+"/api/operator/api-keys", "", "")
		require.Equal(t, http.StatusOK, a.code)
	})

	t.Run("rate limit answers 429 with Retry-After", func(t *testing.T) {
		k := e.createKey(t, `{"name":"busy","scope":"metrics:read"}`)
		e.machine(t, e.limSrv.URL, k.Key, "/tenants", 200)
		e.machine(t, e.limSrv.URL, k.Key, "/tenants", 200)
		a := e.machine(t, e.limSrv.URL, k.Key, "/tenants", 429)
		require.Equal(t, "rate_limited", a.errCode(t))
		require.Equal(t, "1", a.header.Get("Retry-After"))
		// another key has its own bucket
		e.machine(t, e.limSrv.URL, hourly.Key, "/tenants", 200)
		time.Sleep(1100 * time.Millisecond)
		e.machine(t, e.limSrv.URL, k.Key, "/tenants", 200)
		var req, rej int64
		require.NoError(t, superRow(t, f, `select sum(requests), sum(rejected) from cp.api_key_usage_hourly where key_id = $1`, k.ID).Scan(&req, &rej))
		require.Equal(t, [2]int64{4, 1}, [2]int64{req, rej})
	})

	t.Run("no plaintext key is stored anywhere", func(t *testing.T) {
		rows := f.superQuery(t, `select format('%I.%I', schemaname, tablename) from pg_tables
			where schemaname in ('cp', 'api', 'agg') order by 1`)
		var tables []string
		for rows.Next() {
			var s string
			require.NoError(t, rows.Scan(&s))
			tables = append(tables, s)
		}
		require.NoError(t, rows.Err())
		require.Contains(t, tables, "cp.operator_api_keys")
		require.Contains(t, tables, "cp.audit_log")
		for _, k := range []string{global.Key, aurora.Key, probe.Key, hourly.Key} {
			for _, tb := range tables {
				var n int64
				// the random part alone, so neither the whole key nor
				// anything holding its secret part survives
				require.NoError(t, superRow(t, f, `select count(*) from `+tb+` x where strpos(x::text, $1) > 0`, k[len(KeyPrefix):]).Scan(&n), tb)
				require.Zero(t, n, "%s holds a plaintext key", tb)
			}
		}
		// what is stored is the sha-256 of the whole key
		var n int64
		require.NoError(t, superRow(t, f, `select count(*) from cp.operator_api_keys where hash = sha256($1::bytea)`, []byte(global.Key)).Scan(&n))
		require.Equal(t, int64(1), n)
	})
}

func httpTime(t time.Time) string { return t.UTC().Format(hourLayout) }

// superRow runs one independent query past row-level security on its own
// connection, closed once the row is scanned.
func superRow(t *testing.T, f *globalFixture, sql string, args ...any) superRowT {
	t.Helper()
	c, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	return superRowT{c: c, row: c.QueryRow(t.Context(), sql, args...)}
}

type superRowT struct {
	c   *pgx.Conn
	row pgx.Row
}

func (r superRowT) Scan(dest ...any) error {
	defer func() { _ = r.c.Close(context.Background()) }()
	return r.row.Scan(dest...)
}

func TestKeyFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		k, err := GenerateKey()
		require.NoError(t, err)
		require.Len(t, k.Plain, 46)
		require.False(t, seen[k.Plain])
		seen[k.Plain] = true
		require.Equal(t, k.Plain[:10], k.Display)
		h, err := ParseBearer("Bearer " + k.Plain)
		require.NoError(t, err)
		require.Equal(t, k.Hash, h)
		_, err = ParseBearer("bearer " + k.Plain)
		require.NoError(t, err, "the scheme is case-insensitive")
	}
	k, err := GenerateKey()
	require.NoError(t, err)
	for _, bad := range []string{"", "Bearer", "Bearer ", k.Plain, "Basic " + k.Plain, "Bearer  " + k.Plain,
		"Bearer " + k.Plain + " ", "Bearer " + k.Plain[:45], "Bearer " + k.Plain + "a", "Bearer sa_otlp_" + k.Plain[6:] + "ab",
		"Bearer " + strings.Replace(k.Plain, k.Plain[20:21], "-", 1), "Bearer SA_OP_" + k.Plain[6:]} {
		_, err := ParseBearer(bad)
		require.Error(t, err, bad)
	}
}

func TestKeyLimiter(t *testing.T) {
	l := &keyLimiter{rate: 2, burst: 3, buckets: map[uuid.UUID]*tokenBucket{}}
	a, b := uuid.New(), uuid.New()
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for range 3 {
		ok, _ := l.take(a, at)
		require.True(t, ok)
	}
	ok, wait := l.take(a, at)
	require.False(t, ok)
	require.Equal(t, 500*time.Millisecond, wait)
	ok, _ = l.take(b, at)
	require.True(t, ok, "buckets are per key")
	ok, _ = l.take(a, at.Add(500*time.Millisecond))
	require.True(t, ok)
	// refilled buckets are forgotten after a minute, and a forgotten bucket
	// starts full, which is what it would hold anyway
	ok, _ = l.take(uuid.New(), at.Add(2*time.Minute))
	require.True(t, ok)
	require.Len(t, l.buckets, 1)
}
