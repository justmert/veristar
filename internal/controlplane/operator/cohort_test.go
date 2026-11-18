package operator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/session"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
)

// cohortFixture is a platform database with five projects whose web,
// api and on-chain numbers are known exactly for two months, so growth,
// percentiles and rule evaluations can be checked to the digit.
type cohortFixture struct {
	pg      *dbtest.Postgres
	d       *pdb.DB
	h       http.Handler
	op      uuid.UUID
	now     time.Time
	tenants map[string]uuid.UUID
	sites   map[string]uuid.UUID
}

// the fixture's clock: 2026-11-16, so the previous complete months are
// september and october
var cohortNow = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func newCohortFixture(t *testing.T) *cohortFixture {
	t.Helper()
	d, pg := reviewDB(t)
	f := &cohortFixture{pg: pg, d: d, op: uuid.Must(uuid.NewV7()), now: cohortNow,
		tenants: map[string]uuid.UUID{}, sites: map[string]uuid.UUID{}}
	superExec(t, pg, `insert into cp.users (id, github_id, github_login, is_operator) values ($1, 7, 'op', true)`, f.op)
	for _, n := range []struct {
		name, status string
		internal     bool
	}{
		{"alpha", "active", false}, {"beta", "active", false}, {"gamma", "active", false},
		{"delta", "pending", false}, {"probe", "active", true},
	} {
		id, site := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		f.tenants[n.name], f.sites[n.name] = id, site
		superExec(t, pg, `insert into cp.tenants (id, slug, name, status, is_internal) values ($1, $2, $2, $3, $4)`,
			id, n.name, n.status, n.internal)
		superExec(t, pg, `insert into cp.sites (id, tenant_id, name, domain, verify_token) values ($1, $2, $3, $3, 'x')`,
			site, id, n.name+".example")
	}
	g, err := NewGlobal(d)
	require.NoError(t, err)
	g.now = func() time.Time { return f.now }
	ex, err := NewExports(g)
	require.NoError(t, err)
	co, err := NewCohorts(d, func() time.Time { return f.now })
	require.NoError(t, err)
	ru, err := NewRules(d, func() time.Time { return f.now })
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(session.WithUser(req.Context(), session.User{ID: f.op})))
		})
	})
	r.Route("/api/operator", func(sub chi.Router) {
		g.Routes(sub)
		ex.Routes(sub)
		co.Routes(sub)
		ru.Routes(sub)
	})
	f.h = r
	return f
}

// web writes primary visitors (and twice as many page views) for a
// project on each day.
func (f *cohortFixture) web(t *testing.T, name string, visitors map[string]int64) {
	t.Helper()
	for day, v := range visitors {
		superExec(t, f.pg, `insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
			values ($1, $2, $3, 'primary', $4, $5, $5, 0, 0)`, f.sites[name], f.tenants[name], day, 2*v, v)
	}
}

func (f *cohortFixture) grade(t *testing.T, name, day, grade string) {
	t.Helper()
	superExec(t, f.pg, `insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed,
	    events, quarantined, proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown,
	    networks, visitors, pageviews, baseline_visitors, baseline_days, reasons)
	  values ($1, $2, $3, 1, $4, true, 1, 0, 0, 1, 0, 0, 0, 0, 1, 1, 1, null, 0, '[]')`, f.sites[name], f.tenants[name], day, grade)
}

func (f *cohortFixture) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func (f *cohortFixture) audit(t *testing.T, action string) []map[string]any {
	t.Helper()
	c, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(t.Context()) }()
	rows, err := c.Query(t.Context(), `select actor, target, detail from cp.audit_log where action = $1 order by id`, action)
	require.NoError(t, err)
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var actor, target string
		var detail map[string]any
		require.NoError(t, rows.Scan(&actor, &target, &detail))
		out = append(out, map[string]any{"actor": actor, "target": target, "detail": detail})
	}
	return out
}

func TestCohortsGrowthAndPercentiles(t *testing.T) {
	f := newCohortFixture(t)
	// range 2026-11-01..2026-11-10 against 2026-10-22..2026-10-31
	f.web(t, "alpha", map[string]int64{"2026-11-02": 30, "2026-10-25": 20})
	f.web(t, "beta", map[string]int64{"2026-11-03": 10, "2026-10-23": 40})
	f.web(t, "gamma", map[string]int64{"2026-11-04": 50})
	f.web(t, "delta", map[string]int64{"2026-11-04": 70})
	f.web(t, "probe", map[string]int64{"2026-11-04": 999})
	rq := "from=2026-11-01&to=2026-11-10"

	list := func(q string) map[string]WebProject {
		t.Helper()
		code, body := f.do(t, http.MethodGet, "/api/operator/web/projects?"+rq+"&limit=200"+q, nil)
		require.Equal(t, http.StatusOK, code, string(body))
		out := map[string]WebProject{}
		for _, p := range decode[GlobalList[WebProject]](t, body).Items {
			out[p.Tenant.Slug] = p
		}
		return out
	}

	t.Run("growth against the previous period of equal length", func(t *testing.T) {
		rows := list("")
		require.Len(t, rows, 4, "the internal project is never listed")
		a := rows["alpha"].KPIs.Visitors
		require.Equal(t, 30.0, *a.Value)
		require.Equal(t, 20.0, *a.Previous)
		require.InDelta(t, 0.5, *a.Change, 1e-9)
		require.InDelta(t, -0.75, *rows["beta"].KPIs.Visitors.Change, 1e-9)
		require.Nil(t, rows["gamma"].KPIs.Visitors.Change, "no previous value, no change")
		require.InDelta(t, 0.5, *rows["alpha"].KPIs.Pageviews.Change, 1e-9)
	})

	t.Run("percentiles over the active and suspended projects", func(t *testing.T) {
		rows := list("")
		// alpha 30, beta 10, gamma 50; delta is pending and outside the population
		require.Equal(t, int64(3), rows["alpha"].CohortSize)
		require.InDelta(t, 2.0/3, *rows["alpha"].KPIs.Visitors.Percentile, 1e-9)
		require.InDelta(t, 1.0/3, *rows["beta"].KPIs.Visitors.Percentile, 1e-9)
		require.InDelta(t, 1.0, *rows["gamma"].KPIs.Visitors.Percentile, 1e-9)
		require.Nil(t, rows["delta"].KPIs.Visitors.Percentile)
		require.Nil(t, rows["alpha"].KPIs.Requests.Percentile, "no requests, no percentile")
		require.Nil(t, rows["alpha"].KPIs.ErrorRate.Value)
	})

	t.Run("cohorts are set, proposed, confirmed and audited", func(t *testing.T) {
		code, body := f.do(t, http.MethodPut, "/api/operator/tenants/"+f.tenants["alpha"].String()+"/cohort",
			map[string]any{"program": "Build Award", "round": "SCF #38", "category": "DeFi", "award_date": "2026-11-03"})
		require.Equal(t, http.StatusOK, code, string(body))
		rec := decode[CohortRecord](t, body)
		require.Equal(t, "Build Award", *rec.Confirmed.Program)
		require.Equal(t, "2026-11-03", *rec.Confirmed.AwardDate)

		// a proposal written as the project, then confirmed by the operator
		superExec(t, f.pg, `insert into cp.tenant_cohorts (tenant_id, proposed_program, proposed_round, proposed_by, proposed_at, proposed_source)
			values ($1, 'Build Award', 'SCF #38', $2, now(), 'member')`, f.tenants["gamma"], f.op)
		code, body = f.do(t, http.MethodGet, "/api/operator/cohorts", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		cl := decode[CohortList](t, body)
		require.Len(t, cl.Proposals, 1)
		require.Equal(t, "gamma", cl.Proposals[0].Tenant.Slug)
		code, body = f.do(t, http.MethodPost, "/api/operator/tenants/"+f.tenants["gamma"].String()+"/cohort/confirm", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		rec = decode[CohortRecord](t, body)
		require.Equal(t, "SCF #38", *rec.Confirmed.Round)
		require.Nil(t, rec.Proposal)
		code, _ = f.do(t, http.MethodPost, "/api/operator/tenants/"+f.tenants["gamma"].String()+"/cohort/confirm", nil)
		require.Equal(t, http.StatusConflict, code)

		// a dismissed proposal leaves the confirmed values
		superExec(t, f.pg, `update cp.tenant_cohorts set proposed_category = 'Wallets', proposed_by = $2, proposed_at = now(),
			proposed_source = 'member'
			where tenant_id = $1`, f.tenants["gamma"], f.op)
		code, body = f.do(t, http.MethodDelete, "/api/operator/tenants/"+f.tenants["gamma"].String()+"/cohort/proposal", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		rec = decode[CohortRecord](t, body)
		require.Nil(t, rec.Proposal)
		require.Equal(t, "SCF #38", *rec.Confirmed.Round)

		code, body = f.do(t, http.MethodGet, "/api/operator/cohorts", nil)
		require.Equal(t, http.StatusOK, code)
		cl = decode[CohortList](t, body)
		require.Equal(t, []CohortValue{{Value: "Build Award", Projects: 2}}, cl.Programs)
		require.Empty(t, cl.Proposals)

		for _, a := range []string{ActionCohortSet, ActionCohortConfirm, ActionCohortProposalDismiss} {
			rows := f.audit(t, a)
			require.Len(t, rows, 1, a)
			require.Equal(t, "user:"+f.op.String(), rows[0]["actor"])
		}

		// validation and unknown or internal projects
		code, _ = f.do(t, http.MethodPut, "/api/operator/tenants/"+f.tenants["alpha"].String()+"/cohort",
			map[string]any{"award_date": "2030-01-01"})
		require.Equal(t, http.StatusUnprocessableEntity, code)
		code, _ = f.do(t, http.MethodGet, "/api/operator/tenants/"+f.tenants["probe"].String()+"/cohort", nil)
		require.Equal(t, http.StatusNotFound, code)
	})

	t.Run("cohort filters narrow the list and the percentile population", func(t *testing.T) {
		rows := list("&program=Build+Award&round=SCF+%2338")
		require.Len(t, rows, 2)
		require.Equal(t, int64(2), rows["alpha"].CohortSize)
		require.InDelta(t, 0.5, *rows["alpha"].KPIs.Visitors.Percentile, 1e-9)
		require.InDelta(t, 1.0, *rows["gamma"].KPIs.Visitors.Percentile, 1e-9)
		require.Equal(t, "2026-11-03", *rows["alpha"].Cohort.AwardDate)
		// a name search does not change the population
		rows = list("&program=Build+Award&q=alpha")
		require.Len(t, rows, 1)
		require.InDelta(t, 0.5, *rows["alpha"].KPIs.Visitors.Percentile, 1e-9)
		require.Len(t, list("&award_from=2026-11-04"), 0)
		require.Len(t, list("&category=DeFi"), 1)
		code, _ := f.do(t, http.MethodGet, "/api/operator/web/projects?award_from=2026-11-05&award_to=2026-11-01", nil)
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("growth sorts", func(t *testing.T) {
		code, body := f.do(t, http.MethodGet, "/api/operator/web/projects?"+rq+"&sort=visitors_growth&order=desc", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		var order []string
		for _, p := range decode[GlobalList[WebProject]](t, body).Items {
			order = append(order, p.Tenant.Slug)
		}
		require.Equal(t, []string{"alpha", "beta"}, order[:2], "projects without a change come last")
	})

	t.Run("compare carries the cohort and the csv its columns", func(t *testing.T) {
		code, body := f.do(t, http.MethodGet, "/api/operator/web/compare?"+rq+"&tenants="+f.tenants["alpha"].String(), nil)
		require.Equal(t, http.StatusOK, code, string(body))
		cmp := decode[WebCompare](t, body)
		require.Equal(t, "2026-11-03", *cmp.Projects[0].Cohort.AwardDate)

		code, body = f.do(t, http.MethodGet, "/api/operator/web/compare.csv?"+rq+"&tenants="+f.tenants["alpha"].String(), nil)
		require.Equal(t, http.StatusOK, code, string(body))
		recs := readCSV(t, body)
		after := colOf(t, recs[0], "after_award")
		require.Equal(t, "false", recs[2][after], recs[2])
		require.Equal(t, "true", recs[3][after], recs[3])

		code, body = f.do(t, http.MethodGet, "/api/operator/web/projects.csv?"+rq+"&sort=name&order=asc", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		recs = readCSV(t, body)
		require.Equal(t, "alpha", recs[1][colOf(t, recs[0], "slug")])
		require.Equal(t, "0.5", recs[1][colOf(t, recs[0], "visitors_growth")])
		require.Equal(t, "Build Award", recs[1][colOf(t, recs[0], "cohort_program")])
	})
}

func TestThresholdRules(t *testing.T) {
	f := newCohortFixture(t)
	// september and october: alpha has 3 days of >= 100 in each month,
	// beta 3 in september and 1 in october, gamma 3 and 3 but grade C
	days := func(month string, n int, v int64) map[string]int64 {
		out := map[string]int64{}
		for i := 1; i <= n; i++ {
			out[month+"-"+[]string{"01", "02", "03", "04", "05"}[i-1]] = v
		}
		return out
	}
	f.web(t, "alpha", days("2026-09", 3, 120))
	f.web(t, "alpha", days("2026-10", 4, 100))
	f.web(t, "alpha", map[string]int64{"2026-10-20": 99})
	f.web(t, "beta", days("2026-09", 3, 500))
	f.web(t, "beta", days("2026-10", 1, 500))
	f.web(t, "gamma", days("2026-09", 3, 200))
	f.web(t, "gamma", days("2026-10", 3, 200))
	for _, d := range []string{"2026-09-01", "2026-10-01"} {
		f.grade(t, "alpha", d, "A")
		f.grade(t, "gamma", d, "C")
	}

	rule := map[string]any{"name": "steady web", "metric": "visitors", "min_per_day": 100, "min_days": 3, "months": 2}
	code, body := f.do(t, http.MethodPost, "/api/operator/rules", rule)
	require.Equal(t, http.StatusCreated, code, string(body))
	r := decode[Rule](t, body)
	path := "/api/operator/rules/" + r.ID.String()

	matches := func() RuleMatches {
		t.Helper()
		code, body := f.do(t, http.MethodGet, path+"/matches", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		return decode[RuleMatches](t, body)
	}
	t.Run("matches with evidence", func(t *testing.T) {
		m := matches()
		require.Equal(t, []string{"2026-09", "2026-10"}, m.Months)
		require.Equal(t, "2026-09-01", m.From)
		require.Equal(t, "2026-10-31", m.To)
		require.Equal(t, 3, m.Evaluated, "active projects only")
		var met []string
		for _, it := range m.Items {
			met = append(met, it.Tenant.Slug)
		}
		require.Equal(t, []string{"alpha", "gamma"}, met)
		a := m.Items[0]
		require.Equal(t, 3, a.Months[0].QualifyingDays)
		require.Equal(t, 4, a.Months[1].QualifyingDays)
		require.Equal(t, RuleDay{Day: "2026-10-01", Value: 100}, a.Months[1].Days[0])
		require.Equal(t, "A", *a.Integrity.Grade)

		// the list carries what the matches route answers today
		code, body := f.do(t, http.MethodGet, "/api/operator/rules", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		list := decode[RuleList](t, body)
		require.Len(t, list.Items, 1)
		require.Equal(t, r.ID, list.Items[0].ID)
		require.Equal(t, 2, list.Items[0].MatchCount)
		require.Equal(t, 3, list.Items[0].Evaluated)
		require.Equal(t, m.AsOf, list.Items[0].MatchesAsOf)
		require.Contains(t, string(body), `"match_count":2`)
		require.Contains(t, string(body), `"min_per_day":100`, "the rule's own fields stay at the top level")
	})

	t.Run("a minimum grade and an update", func(t *testing.T) {
		rule["min_grade"] = "B"
		code, body := f.do(t, http.MethodPut, path, rule)
		require.Equal(t, http.StatusOK, code, string(body))
		m := matches()
		require.Len(t, m.Items, 1)
		require.Equal(t, "alpha", m.Items[0].Tenant.Slug)
		code, body = f.do(t, http.MethodGet, "/api/operator/rules", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		require.Equal(t, 1, decode[RuleList](t, body).Items[0].MatchCount)
	})

	t.Run("an earlier evaluation day moves the window", func(t *testing.T) {
		code, body := f.do(t, http.MethodGet, path+"/matches?as_of=2026-10-15", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		m := decode[RuleMatches](t, body)
		require.Equal(t, []string{"2026-08", "2026-09"}, m.Months)
		require.Empty(t, m.Items, "nobody had data in august")
		code, _ = f.do(t, http.MethodGet, path+"/matches?as_of=2027-01-01", nil)
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("csv export", func(t *testing.T) {
		code, body := f.do(t, http.MethodGet, path+"/matches.csv", nil)
		require.Equal(t, http.StatusOK, code, string(body))
		recs := readCSV(t, body)
		require.Len(t, recs, 3, "one row per month of the one match")
		require.Equal(t, "2026-09", recs[1][colOf(t, recs[0], "month")])
		require.Equal(t, "3", recs[1][colOf(t, recs[0], "qualifying_days")])
	})

	t.Run("validation, delete and audit", func(t *testing.T) {
		for _, bad := range []map[string]any{
			{"name": "x", "metric": "pageviews", "min_per_day": 1, "min_days": 1, "months": 1},
			{"name": "x", "metric": "visitors", "min_per_day": 0, "min_days": 1, "months": 1},
			{"name": "x", "metric": "visitors", "min_per_day": 1, "min_days": 29, "months": 1},
			{"name": "x", "metric": "visitors", "min_per_day": 1, "min_days": 1, "months": 13},
			{"name": "x", "metric": "visitors", "min_per_day": 1, "min_days": 1, "months": 1, "min_grade": "D"},
			{"metric": "visitors", "min_per_day": 1, "min_days": 1, "months": 1},
		} {
			code, body := f.do(t, http.MethodPost, "/api/operator/rules", bad)
			require.Equal(t, http.StatusUnprocessableEntity, code, string(body))
		}
		code, body := f.do(t, http.MethodGet, "/api/operator/rules", nil)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, decode[RuleList](t, body).Items, 1)
		code, _ = f.do(t, http.MethodDelete, path, nil)
		require.Equal(t, http.StatusNoContent, code)
		code, _ = f.do(t, http.MethodGet, path, nil)
		require.Equal(t, http.StatusNotFound, code)
		code, _ = f.do(t, http.MethodGet, path+"/matches", nil)
		require.Equal(t, http.StatusNotFound, code)
		for _, a := range []string{ActionRuleCreate, ActionRuleUpdate, ActionRuleDelete} {
			require.Len(t, f.audit(t, a), 1, a)
		}
	})

	t.Run("requests and on-chain operations", func(t *testing.T) {
		for i := 1; i <= 2; i++ {
			day := time.Date(2026, 10, i, 0, 0, 0, 0, time.UTC)
			superExec(t, f.pg, `insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy)
				values ($1, $2, 'svc', 'GET', '/x', 200, 'metric', $3, 1, 60, false),
				       ($1, $2, 'svc', 'GET', '/x', 200, 'span', $4, 1, 999, false)`,
				f.tenants["beta"], day, buckets(60), buckets(999))
		}
		account := "GBXFXNDLV4LSWA4VB7YIL5GBD7BVNR22SGBTDKMO2SBZZHDXSKZYCP7L"
		superExec(t, f.pg, `insert into cp.chain_ids (tenant_id, strkey, kind, added_by) values ($1, $2, 'account', 'console')`,
			f.tenants["gamma"], account)
		superExec(t, f.pg, `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
			values ($1, $2, '2026-10-05', 'horizon', 7, 1, 1)`, f.tenants["gamma"], account)
		matches := func(metric string) RuleMatches {
			code, body := f.do(t, http.MethodPost, "/api/operator/rules",
				map[string]any{"name": metric, "metric": metric, "min_per_day": 5, "min_days": 1, "months": 1})
			require.Equal(t, http.StatusCreated, code, string(body))
			id := decode[Rule](t, body).ID
			code, body = f.do(t, http.MethodGet, "/api/operator/rules/"+id.String()+"/matches", nil)
			require.Equal(t, http.StatusOK, code, string(body))
			return decode[RuleMatches](t, body)
		}
		m := matches("requests")
		require.Len(t, m.Items, 1)
		require.Equal(t, "beta", m.Items[0].Tenant.Slug)
		// the metric rows win over the span rows of the same service
		require.Equal(t, int64(60), m.Items[0].Months[0].Days[0].Value)

		// an id the project only declared does not count: anyone can
		// declare any public account
		require.Empty(t, matches("onchain_operations").Items, "an unverified id meets no rule")
		superExec(t, f.pg, `update cp.chain_ids set verify_method = 'sep53', verified_at = now() where strkey = $1`, account)
		m = matches("onchain_operations")
		require.Len(t, m.Items, 1)
		require.Equal(t, "gamma", m.Items[0].Tenant.Slug)
	})
}

// buckets is a histogram with every request in the first bucket
func buckets(n int64) []int64 {
	b := make([]int64, 15)
	b[0] = n
	return b
}

func TestRuleWindow(t *testing.T) {
	from, to, names := RuleWindow(3, time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	require.Equal(t, time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC), from)
	require.Equal(t, time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), to)
	require.Equal(t, []string{"2025-10", "2025-11", "2025-12"}, names)
}
