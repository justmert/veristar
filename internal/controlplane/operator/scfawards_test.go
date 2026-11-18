package operator

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/scfawards"
	"github.com/justmert/veristar/internal/controlplane/session"
)

// the scf award routes (contract 19.4): only an operator with a fresh
// step-up reads them; the list matches, measures and counts the imported
// awards; the csv is the same rows and writes an audit row.
func TestSCFAwardRoutes(t *testing.T) {
	d, pg := reviewDB(t)
	ctx := t.Context()
	now := time.Now().UTC()
	today := utcDay(now)

	opID, memberID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.operators (github_id, role) values (61, 'operator')`)
	superExec(t, pg, `insert into cp.users (id, github_id, github_login) values ($1, 61, 'scf-op'), ($2, 62, 'scf-member')`,
		opID, memberID)

	alpha, beta, site := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status, enrollment_answers) values
		($1, 'alpha', 'Alpha', 'active', '{"website": "https://alpha.example"}'),
		($2, 'beta', 'Beta', 'active', '{"website": "https://www.beta.example/"}')`, alpha, beta)
	superExec(t, pg, `insert into cp.sites (id, tenant_id, name, domain, origins, verify_token, verified_at, verify_method, status)
		values ($1, $2, 'alpha', 'alpha.example', '{https://alpha.example}', 'x', now(), 'dns', 'active')`, site, alpha)
	superExec(t, pg, `insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
		values ($1, $2, $3, 'primary', 10, 4, 4, 1, 60)`, site, alpha, today.AddDate(0, 0, -2))
	superExec(t, pg, `insert into cp.tenant_cohorts (tenant_id, program, round, category, award_date, confirmed_at)
		values ($1, 'Build Award', 'SCF #30', 'DeFi', '2025-01-01', now())`, beta)

	file := "Project,Round,Category,Award Type,Award Amount,Award Date,Website,Contact\n" +
		"Alpha,SCF #45,Developer Tooling,Build,\"$100,000.00\",9/1/2026,https://alpha.example,Pat Placeholder\n" +
		"Beta,SCF #44,Financial Protocols,Build,\"$50,000.00\",,beta.example,Pat Placeholder\n" +
		"Gamma,SCF #44,Applications,Build,$25000,,https://gamma.example,Pat Placeholder\n" +
		"Delta,SCF #44,Applications,Build,lots,,https://delta.example,Pat Placeholder\n"
	rep, err := scfawards.Import(ctx, d, []byte(file), scfawards.ActorPlatformctl, now)
	require.NoError(t, err)
	require.Equal(t, 3, rep.Inserted)
	require.Len(t, rep.Rejected, 1)

	sessions := session.New(d)
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.StartTLS()
	t.Cleanup(srv.Close)
	guard, stepUp := testGuard(t, d, sessions, srv.URL)
	routes, err := NewSCFAwards(d, time.Now)
	require.NoError(t, err)
	srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: slog.New(slog.DiscardHandler), ConsoleOrigin: srv.URL, Sessions: sessions,
		Public: []controlplane.Routes{controlplane.RoutesFunc(func(r chi.Router) {
			r.Get("/test-sign-in/{id}", func(w http.ResponseWriter, r *http.Request) {
				id := uuid.MustParse(chi.URLParam(r, "id"))
				if err := sessions.SignIn(r.Context(), id); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if r.URL.Query().Get("step_up") == "1" {
					if _, err := stepUp.Record(r.Context(), id); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				w.WriteHeader(http.StatusNoContent)
			})
		})},
		Private: []controlplane.Routes{controlplane.RoutesFunc(guard.Mount(routes))},
	})
	signIn := func(id uuid.UUID, stepped bool) *http.Client {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		hc := *srv.Client()
		hc.Jar = jar
		q := ""
		if stepped {
			q = "?step_up=1"
		}
		res, err := hc.Get(srv.URL + "/api/test-sign-in/" + id.String() + q)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		return &hc
	}
	get := func(hc *http.Client, path string) (int, string, http.Header) {
		res, err := hc.Get(srv.URL + "/api/operator" + path)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		return res.StatusCode, string(b), res.Header
	}
	codeOf := func(body string) string {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &m))
		return errCode(m)
	}

	// who may read: an operator with a fresh step-up, nobody else
	member, unstepped, op := signIn(memberID, true), signIn(opID, false), signIn(opID, true)
	for _, p := range []string{"/scf/awards", "/scf/awards.csv", "/scf/imports"} {
		st, body, _ := get(member, p)
		require.Equal(t, http.StatusForbidden, st, p)
		require.Equal(t, "operator_required", codeOf(body))
		st, body, _ = get(unstepped, p)
		require.Equal(t, http.StatusUnauthorized, st, p)
		require.Equal(t, "step_up_required", codeOf(body))
	}

	st, body, _ := get(op, "/scf/awards")
	require.Equal(t, http.StatusOK, st, body)
	var list SCFAwardList
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	require.Equal(t, today.AddDate(0, 0, -29).Format(time.DateOnly), list.From)
	require.Equal(t, rep.ImportID, list.LastImport.ID)
	require.EqualValues(t, 1, list.LastImport.Rejected)
	require.Len(t, list.Items, 3)
	byName := map[string]SCFAward{}
	for _, a := range list.Items {
		byName[a.Project] = a
	}
	a := byName["Alpha"]
	require.Equal(t, scfawards.StateMatched, a.Match.State)
	require.Equal(t, alpha, a.Match.Tenant.ID)
	require.Equal(t, "100000.00", a.AwardAmountUSD)
	require.True(t, a.Measured.Measured && a.Measured.HasWebData)
	require.False(t, a.Measured.HasAPIData)
	require.Equal(t, today.AddDate(0, 0, -2).Format(time.DateOnly), *a.Measured.LastActivity)
	require.EqualValues(t, 1, a.Measured.SitesVerified)
	require.Equal(t, scfawards.CohortProposed, a.Cohort.State)
	require.Equal(t, scfawards.SourceSCFImport, *a.Cohort.ProposalSource)
	b := byName["Beta"]
	require.Equal(t, beta, b.Match.Tenant.ID)
	require.Equal(t, scfawards.EvidenceEnrollmentWebsite, b.Match.Evidence[0].Kind)
	require.False(t, b.Measured.Measured)
	require.Equal(t, scfawards.CohortConfirmedDiffers, b.Cohort.State)
	require.Equal(t, "SCF #30", *b.Cohort.Confirmed.Round)
	g := byName["Gamma"]
	require.Equal(t, scfawards.StateNotEnrolled, g.Match.State)
	require.Nil(t, g.Measured)
	require.Nil(t, g.Cohort)
	require.NotContains(t, body, "Placeholder")

	s := list.Summary
	require.Equal(t, SCFCount{Awards: 3, AwardAmountUSD: "175000.00", Matched: 2, NotEnrolled: 1, Measured: 1}, s.Total)
	require.Equal(t, []string{"SCF #45", "SCF #44"}, []string{s.ByRound[0].Key, s.ByRound[1].Key})
	require.EqualValues(t, 2, s.ByRound[1].Awards)
	require.Len(t, s.ByCategory, 3)
	require.Equal(t, map[string]int64{scfawards.CohortProposed: 1, scfawards.CohortConfirmedDiffers: 1}, s.CohortStates)

	// filters narrow the items, never the summary
	for q, want := range map[string]int{
		"?state=matched": 2, "?state=not_enrolled": 1, "?measured=true": 1, "?measured=false": 2,
		"?round=SCF+%2344": 2, "?category=Applications": 1, "?q=alp": 1,
	} {
		st, body, _ := get(op, "/scf/awards"+q)
		require.Equal(t, http.StatusOK, st, q)
		var l SCFAwardList
		require.NoError(t, json.Unmarshal([]byte(body), &l))
		require.Len(t, l.Items, want, q)
		require.EqualValues(t, 3, l.Summary.Total.Awards)
	}
	for _, q := range []string{"?state=lost", "?measured=yes", "?nope=1", "?from=2020-01-01&to=2026-01-01", "?q="} {
		st, body, _ := get(op, "/scf/awards"+q)
		require.Equal(t, http.StatusBadRequest, st, q)
		require.Equal(t, "invalid_query", codeOf(body), q)
	}

	// the csv holds the same rows, and the export is audited
	st, body, hdr := get(op, "/scf/awards.csv?state=matched")
	require.Equal(t, http.StatusOK, st, body)
	require.Contains(t, hdr.Get("Content-Type"), "text/csv")
	recs, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	require.NoError(t, err)
	require.Len(t, recs, 3)
	require.Equal(t, scfCSVHeader, recs[0])
	require.Equal(t, 1, superCount(t, pg, `select count(*) from cp.audit_log where action = 'export.csv' and target = 'operator:scf_awards'
		and actor = $1 and (detail ->> 'rows')::int = 2`, "user:"+opID.String()))

	st, body, _ = get(op, "/scf/imports")
	require.Equal(t, http.StatusOK, st, body)
	require.Contains(t, body, rep.FileSHA256)
	st, _, _ = get(op, "/scf/imports?x=1")
	require.Equal(t, http.StatusBadRequest, st)
}
