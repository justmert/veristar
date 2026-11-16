package operator

import (
	"net/http"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/session"

	"github.com/justmert/veristar/internal/controlplane/integrityview"
)

// seedCoverage writes tracker file counters as edgestats would: aurora with
// a tenth of its requests under global privacy control, flare with too few
// requests for a share.
func seedCoverage(t *testing.T, f *globalFixture, sites map[string]string) {
	t.Helper()
	for i := range fixtureDays {
		day := f.today.AddDate(0, 0, -i)
		superExec(t, f.pg, `insert into agg.web_coverage_daily (site_id, tenant_id, day, script_requests, gpc_requests)
		  values ($1, $2, $3, 100, 10)`, sites["aurora"], f.tenants["aurora"], day)
	}
	superExec(t, f.pg, `insert into agg.web_coverage_daily (site_id, tenant_id, day, script_requests, gpc_requests)
	  values ($1, $2, $3, 40, 20)`, sites["flare"], f.tenants["flare"], f.today)
}

func TestCoverageViews(t *testing.T) {
	f := newGlobalFixture(t)
	ids := seedIntegrity(t, f)
	sites := map[string]string{}
	for k, v := range ids {
		sites[k] = v.String()
	}
	seedCoverage(t, f, sites)
	rq := f.rangeQuery()

	t.Run("the project list carries coverage as a column and sorts by it", func(t *testing.T) {
		all, _ := collect[WebProject](t, f, "/api/operator/web/projects?"+rq+"&sort=unmeasured_share&order=desc")
		require.Equal(t, "Aurora", all[0].Tenant.Name, "the only share sorts first")
		c := all[0].Coverage
		require.Equal(t, int64(100*fixtureDays), c.ScriptRequests)
		require.Equal(t, int64(10*fixtureDays), c.GPCRequests)
		require.InDelta(t, 0.1, *c.UnmeasuredShare, 1e-9)
		for _, p := range all[1:] {
			require.Nil(t, p.Coverage.UnmeasuredShare, p.Tenant.Name)
			if p.Tenant.Name == "Flare" {
				require.Equal(t, int64(40), p.Coverage.ScriptRequests, "counts are shown below the minimum")
			}
		}
	})

	t.Run("the integrity report carries coverage per project, site and day", func(t *testing.T) {
		code, body := f.get(t, "/api/operator/web/integrity?tenant="+f.tenants["aurora"].String()+"&"+rq)
		require.Equal(t, http.StatusOK, code, string(body))
		rep := decode[integrityview.Report](t, body)
		require.InDelta(t, 0.1, *rep.Coverage.UnmeasuredShare, 1e-9)
		i := slices.IndexFunc(rep.Sites, func(s integrityview.Site) bool { return s.SiteID == ids["aurora"] })
		require.GreaterOrEqual(t, i, 0)
		site := rep.Sites[i]
		require.Equal(t, int64(100*fixtureDays), site.Coverage.ScriptRequests)
		day := site.Days[len(site.Days)-1]
		require.Equal(t, int64(100), day.Coverage.ScriptRequests)
		require.InDelta(t, 0.1, *day.Coverage.UnmeasuredShare, 1e-9)
	})

	t.Run("compare carries each project's coverage", func(t *testing.T) {
		code, body := f.get(t, "/api/operator/web/compare?"+rq+"&tenants="+f.tenants["aurora"].String()+","+f.tenants["flare"].String())
		require.Equal(t, http.StatusOK, code, string(body))
		cmp := decode[WebCompare](t, body)
		require.Len(t, cmp.Projects, 2)
		for _, p := range cmp.Projects {
			if p.Tenant.Name == "Aurora" {
				require.InDelta(t, 0.1, *p.Coverage.UnmeasuredShare, 1e-9)
			} else {
				require.Nil(t, p.Coverage.UnmeasuredShare)
				require.Equal(t, int64(20), p.Coverage.GPCRequests)
			}
		}
	})

	t.Run("the list export has the coverage columns", func(t *testing.T) {
		ex, err := NewExports(f.g)
		require.NoError(t, err)
		r := chi.NewRouter()
		// the guard's own tests cover who may enter; here the operator is set
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(session.WithUser(req.Context(), session.User{ID: uuid.Must(uuid.NewV7())})))
			})
		})
		r.Route("/api/operator", ex.Routes)
		get := func(path string) (int, []byte) { return call(t, r, path) }
		code, body := get("/api/operator/web/projects.csv?" + rq + "&rows=200&sort=unmeasured_share&order=desc")
		require.Equal(t, http.StatusOK, code, string(body))
		recs := readCSV(t, body)
		col := func(name string) int { return slices.Index(recs[0], name) }
		for _, name := range []string{"script_requests", "gpc_requests", "unmeasured_share"} {
			require.GreaterOrEqual(t, col(name), 0, name)
		}
		require.Equal(t, f.tenants["aurora"].String(), recs[1][0])
		require.Equal(t, "0.1", recs[1][col("unmeasured_share")])
		code, body = get("/api/operator/web/compare.csv?" + rq + "&tenants=" + f.tenants["aurora"].String())
		require.Equal(t, http.StatusOK, code, string(body))
		recs = readCSV(t, body)
		require.GreaterOrEqual(t, slices.Index(recs[0], "unmeasured_share"), 0)
	})
}
