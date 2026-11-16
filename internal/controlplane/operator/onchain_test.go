package operator

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
)

// chain ids of the fixture: aurora declares an account read on every day
// of the range but one, and a contract never read; borealis an account
const (
	auroraAccount  = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	auroraContract = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	borealisAcct   = "GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

// seedChain writes the ids and the daily rows as enrollment and the
// chain-activity task would, and returns the operations per tenant over
// the fixture range.
func seedChain(t *testing.T, f *globalFixture) map[uuid.UUID]int64 {
	t.Helper()
	a, b := f.tenants["aurora"], f.tenants["borealis"]
	superExec(t, f.pg, `insert into cp.chain_ids (tenant_id, strkey, kind, added_by, verify_method, verified_at)
		values ($1, $2, 'account', 'enrollment', 'sep53', now()), ($1, $3, 'contract', 'console', null, null),
		       ($4, $5, 'account', 'console', null, null)`, a, auroraAccount, auroraContract, b, borealisAcct)
	ops := map[uuid.UUID]int64{}
	first := f.today.AddDate(0, 0, -(fixtureDays - 1))
	for i := range fixtureDays - 1 {
		day := first.AddDate(0, 0, i)
		if i == 4 {
			continue
		}
		superExec(t, f.pg, `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
			values ($1, $2, $3, 'horizon', $4, $5, 1)`, a, auroraAccount, day, int64(i*3), int64(i))
		ops[a] += int64(i * 3)
		superExec(t, f.pg, `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
			values ($1, $2, $3, 'horizon', 2, 1, 1)`, b, borealisAcct, day)
		ops[b] += 2
	}
	superExec(t, f.pg, `insert into agg.chain_reads (tenant_id, strkey, covered_from, covered_to, last_attempt_at, last_success_at,
		home_domain) values ($1, $2, $3, $4, now(), now(), 's0.aurora.example')`, a, auroraAccount, first, f.today.AddDate(0, 0, -1))
	superExec(t, f.pg, `insert into agg.chain_reads (tenant_id, strkey, last_attempt_at, last_error, last_error_at)
		values ($1, $2, now(), 'source_unavailable', now())`, a, auroraContract)
	return ops
}

func readCSV(t *testing.T, body []byte) [][]string {
	t.Helper()
	recs, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	require.NoError(t, err, string(body))
	return recs
}

func colOf(t *testing.T, header []string, name string) int {
	t.Helper()
	for i, h := range header {
		if h == name {
			return i
		}
	}
	t.Fatalf("no column %s in %v", name, header)
	return -1
}

func TestOnchainOperatorViews(t *testing.T) {
	f := newGlobalFixture(t)
	ops := seedChain(t, f)
	opUser := uuid.Must(uuid.NewV7())
	ex, err := NewExports(f.g)
	require.NoError(t, err)
	oc, err := NewOnchain(f.g.db, time.Now)
	require.NoError(t, err)
	r := chi.NewRouter()
	// the guard's own tests cover who may enter; here the operator is set
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(session.WithUser(req.Context(), session.User{ID: opUser})))
		})
	})
	r.Route("/api/operator", func(sub chi.Router) {
		f.g.Routes(sub)
		ex.Routes(sub)
		oc.Routes(sub)
	})
	get := func(path string) (int, []byte) { return call(t, r, path) }
	rq := f.rangeQuery()
	aurora, borealis := f.tenants["aurora"], f.tenants["borealis"]

	t.Run("list column", func(t *testing.T) {
		code, body := get("/api/operator/web/projects?" + rq + "&limit=200")
		require.Equal(t, http.StatusOK, code, string(body))
		list := decode[GlobalList[WebProject]](t, body)
		for _, p := range list.Items {
			switch p.Tenant.ID {
			case aurora:
				require.Equal(t, onchain.Summary{IDs: 2, IDsVerified: 1, Operations: ops[aurora], Transactions: 0 + 1 + 2 + 3 + 5 + 6 + 7 + 8,
					DaysMissing: 1 + int64(fixtureDays-1), IDsFailing: 1}, p.Onchain)
			case borealis:
				require.Equal(t, int64(1), p.Onchain.IDs)
				require.Equal(t, ops[borealis], p.Onchain.Operations)
			default:
				require.Equal(t, onchain.Summary{}, p.Onchain, p.Tenant.Name)
			}
		}
		code, body = get("/api/operator/api/projects?" + rq + "&limit=200")
		require.Equal(t, http.StatusOK, code)
		for _, p := range decode[GlobalList[APIProject]](t, body).Items {
			if p.Tenant.ID == aurora {
				require.Equal(t, ops[aurora], p.Onchain.Operations)
			}
		}
	})

	t.Run("list exports are the rows on screen", func(t *testing.T) {
		for _, tc := range []struct{ view, sort string }{
			{"web", "visitors"}, {"web", "name"}, {"api", "requests"}, {"api", "p95"},
		} {
			q := rq + "&status=active,suspended&sort=" + tc.sort + "&order=desc"
			code, body := get("/api/operator/" + tc.view + "/projects?" + q + "&limit=3")
			require.Equal(t, http.StatusOK, code, string(body))
			var want []string
			if tc.view == "web" {
				for _, p := range decode[GlobalList[WebProject]](t, body).Items {
					want = append(want, p.Tenant.ID.String())
				}
			} else {
				for _, p := range decode[GlobalList[APIProject]](t, body).Items {
					want = append(want, p.Tenant.ID.String())
				}
			}
			code, body = get("/api/operator/" + tc.view + "/projects.csv?" + q + "&rows=3")
			require.Equal(t, http.StatusOK, code, string(body))
			recs := readCSV(t, body)
			require.Equal(t, "tenant_id", recs[0][0])
			var got []string
			for _, rec := range recs[1:] {
				got = append(got, rec[0])
			}
			require.Equal(t, want, got, "%s sorted by %s", tc.view, tc.sort)
		}
		// the numbers equal the json row
		code, body := get("/api/operator/web/projects.csv?" + rq + "&rows=200")
		require.Equal(t, http.StatusOK, code)
		recs := readCSV(t, body)
		h := recs[0]
		code, jbody := get("/api/operator/web/projects?" + rq + "&limit=200")
		require.Equal(t, http.StatusOK, code)
		items := decode[GlobalList[WebProject]](t, jbody).Items
		require.Len(t, recs, len(items)+1)
		for i, p := range items {
			require.Equal(t, strconv.FormatInt(p.Pageviews, 10), recs[i+1][colOf(t, h, "pageviews")])
			require.Equal(t, strconv.FormatInt(p.Onchain.Operations, 10), recs[i+1][colOf(t, h, "onchain_operations")])
		}
		var n int
		require.NoError(t, superRow(t, f, `select count(*) from cp.audit_log where action = 'export.csv' and actor = $1`,
			"user:"+opUser.String()).Scan(&n))
		require.Equal(t, 5, n, "one audit row per export")
		for _, bad := range []string{"&rows=0", "&rows=5001", "&limit=3", "&cursor=abc"} {
			code, _ := get("/api/operator/web/projects.csv?" + rq + bad)
			require.Equal(t, http.StatusBadRequest, code, bad)
		}
	})

	t.Run("compare exports and series", func(t *testing.T) {
		q := rq + "&tenants=" + aurora.String() + "," + borealis.String()
		code, body := get("/api/operator/web/compare?" + q)
		require.Equal(t, http.StatusOK, code, string(body))
		cmp := decode[WebCompare](t, body)
		require.Len(t, cmp.Projects[0].Onchain.Series, fixtureDays)
		require.Nil(t, cmp.Projects[0].Onchain.Series[4].Operations, "a day without a row is null")
		require.Equal(t, ops[aurora], cmp.Projects[0].Onchain.Totals.Operations)
		for _, view := range []string{"web", "api"} {
			code, body = get("/api/operator/" + view + "/compare.csv?" + q)
			require.Equal(t, http.StatusOK, code, string(body))
			recs := readCSV(t, body)
			require.Len(t, recs, 1+2*fixtureDays)
			h := recs[0]
			require.Equal(t, aurora.String(), recs[1][0])
			require.Equal(t, "", recs[5][colOf(t, h, "onchain_operations")])
			require.Equal(t, "3", recs[2][colOf(t, h, "onchain_operations")])
		}
	})

	t.Run("tenant detail", func(t *testing.T) {
		code, body := get("/api/operator/tenants/" + aurora.String() + "/onchain?" + rq)
		require.Equal(t, http.StatusOK, code, string(body))
		v := decode[onchain.View](t, body)
		require.Len(t, v.IDs, 2)
		require.Equal(t, "verified", v.IDs[0].Verification.Status)
		require.Equal(t, "source_unavailable", *v.IDs[1].Series[0].Missing)
		require.Len(t, v.Link.Sites, 2)
		code, _ = get("/api/operator/tenants/" + f.tenants["probe"].String() + "/onchain")
		require.Equal(t, http.StatusNotFound, code, "internal tenants are not shown")
	})
}

func TestSCFView(t *testing.T) {
	f := newGlobalFixture(t)
	seedChain(t, f)
	aurora := f.tenants["aurora"]
	member := uuid.Must(uuid.NewV7())
	outsider := uuid.Must(uuid.NewV7())
	superExec(t, f.pg, `insert into cp.users (id, github_id, github_login) values ($1, 91, 'm'), ($2, 92, 'o')`, member, outsider)
	superExec(t, f.pg, `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, aurora, member)
	scope, err := tenants.NewScope(f.g.db, "t", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
		return errors.New("the scf view must not reconcile")
	}), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	v, err := NewSCFView(scope, f.g.db, time.Now)
	require.NoError(t, err)
	as := func(u uuid.UUID, path string) (int, []byte) {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(session.WithUser(req.Context(), session.User{ID: u})))
			})
		})
		r.Route("/api", v.Routes)
		return call(t, r, path)
	}
	rq := f.rangeQuery()
	code, body := as(member, "/api/tenants/"+aurora.String()+"/scf-view?"+rq)
	require.Equal(t, http.StatusOK, code, string(body))
	got := decode[SCFViewAnswer](t, body)
	require.True(t, got.InOperatorViews)

	// the operator's own rows for the same range
	code, body = call(t, f.h, "/api/operator/web/projects?"+rq+"&limit=200")
	require.Equal(t, http.StatusOK, code)
	var web *WebProject
	for _, p := range decode[GlobalList[WebProject]](t, body).Items {
		if p.Tenant.ID == aurora {
			web = &p
		}
	}
	require.NotNil(t, web)
	require.Equal(t, SCFWebRow{
		Pageviews: web.Pageviews, Visitors: web.Visitors, Visits: web.Visits, Bounces: web.Bounces, Totaltime: web.Totaltime,
		BounceRate: web.BounceRate, AvgVisitDuration: web.AvgVisitDuration, QuarantinePageviews: web.QuarantinePageviews,
		QuarantineVisitors: web.QuarantineVisitors, QuarantineShare: web.QuarantineShare, IngestRequests: web.IngestRequests,
		IngestRejected: web.IngestRejected, RejectedShare: web.RejectedShare,
	}, *got.Web)
	require.Equal(t, web.SiteCounts, *got.Sites)
	require.Equal(t, web.Onchain, *got.Onchain)
	code, body = call(t, f.h, "/api/operator/api/projects?"+rq+"&limit=200")
	require.Equal(t, http.StatusOK, code)
	for _, p := range decode[GlobalList[APIProject]](t, body).Items {
		if p.Tenant.ID == aurora {
			require.Equal(t, p.APINumbers, got.API.APINumbers)
			require.Equal(t, p.Services, got.API.Services)
		}
	}
	code, body = call(t, f.h, "/api/operator/web/compare?"+rq+"&tenants="+aurora.String())
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, decode[WebCompare](t, body).Projects[0].Series, got.WebSeries)

	// a non-member, and a member asking for another project, get 404
	code, _ = as(outsider, "/api/tenants/"+aurora.String()+"/scf-view")
	require.Equal(t, http.StatusNotFound, code)
	code, _ = as(member, "/api/tenants/"+f.tenants["borealis"].String()+"/scf-view")
	require.Equal(t, http.StatusNotFound, code)
	// a range the operator views refuse is refused here too
	code, _ = as(member, "/api/tenants/"+aurora.String()+"/scf-view?from=2020-01-01&to=2026-12-31")
	require.Equal(t, http.StatusBadRequest, code)
}
