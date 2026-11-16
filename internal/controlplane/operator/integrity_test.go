package operator

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/integrityview"
	"github.com/justmert/veristar/internal/integrity"
)

// seedIntegrity writes graded site days as the integrity task would: aurora
// clean every day, flare B on three of ten days, borealis C on most days.
func seedIntegrity(t *testing.T, f *globalFixture) map[string]uuid.UUID {
	t.Helper()
	c, err := f.pg.Superuser(t.Context(), "platform")
	require.NoError(t, err)
	defer func() { _ = c.Close(context.Background()) }()
	sites := map[string]uuid.UUID{}
	for _, name := range []string{"aurora", "borealis", "flare"} {
		var site uuid.UUID
		require.NoError(t, c.QueryRow(t.Context(), `select id from cp.sites where tenant_id = $1 order by id limit 1`, f.tenants[name]).Scan(&site))
		sites[name] = site
		for i := range fixtureDays {
			day := f.today.AddDate(0, 0, -i)
			grade, reasons := "A", `[]`
			switch {
			case name == "flare" && i < 3:
				grade, reasons = "B", `[{"code":"hosting_share","level":"B","value":0.3,"limit":0.25}]`
			case name == "borealis" && i < 8:
				grade, reasons = "C", `[{"code":"quarantine_share","level":"C","value":0.4,"limit":0.3}]`
			}
			_, err := c.Exec(t.Context(), `insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed,
			    events, quarantined, proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown,
			    networks, visitors, pageviews, baseline_visitors, baseline_days, reasons)
			  values ($1, $2, $3, 1, $4, $5, 200, 10, 5, 150, 10, 40, 0, 0, 30, 90, 180, 85, 20, $6)`,
				site, f.tenants[name], day, grade, i > 0, reasons)
			require.NoError(t, err)
		}
	}
	// a day with too little traffic is listed, never graded
	_, err = c.Exec(t.Context(), `insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed,
	    events, quarantined, proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown,
	    networks, visitors, pageviews, baseline_visitors, baseline_days, reasons)
	  values ($1, $2, $3, 1, null, true, 3, 0, 0, 3, 0, 0, 0, 0, 1, 1, 3, null, 0,
	    '[{"code":"too_little_traffic","level":"none","value":3,"limit":50}]')`,
		uuid.Must(uuid.NewV7()), f.tenants["aurora"], f.today.AddDate(0, 0, -1))
	require.NoError(t, err)
	return sites
}

func TestIntegrityViews(t *testing.T) {
	f := newGlobalFixture(t)
	sites := seedIntegrity(t, f)
	rq := f.rangeQuery()

	t.Run("the project list carries the range grade and sorts by it", func(t *testing.T) {
		all, _ := collect[WebProject](t, f, "/api/operator/web/projects?"+rq+"&sort=integrity&order=asc")
		var order []string
		for _, p := range all {
			g := "-"
			if p.Integrity.Grade != nil {
				g = *p.Integrity.Grade
			}
			order = append(order, p.Tenant.Name+":"+g)
		}
		// best first, ungraded projects last
		require.Equal(t, []string{"Aurora:A", "Flare:B", "Borealis:C"}, order[:3])
		for _, s := range order[3:] {
			require.Contains(t, s, ":-")
		}
		for _, p := range all {
			if p.Tenant.Name == "Flare" {
				require.Equal(t, int64(10), p.Integrity.GradedDays)
				require.Equal(t, int64(3), p.Integrity.DaysB)
			}
		}
	})

	t.Run("the reasons panel lists sites, days and reasons with text", func(t *testing.T) {
		code, body := f.get(t, "/api/operator/web/integrity?tenant="+f.tenants["borealis"].String()+"&"+rq)
		require.Equal(t, http.StatusOK, code, string(body))
		rep := decode[integrityview.Report](t, body)
		require.Equal(t, "C", *rep.Grade)
		require.Equal(t, 8, rep.DaysC)
		require.Len(t, rep.Sites, 1)
		require.Equal(t, sites["borealis"], rep.Sites[0].SiteID)
		require.Len(t, rep.Sites[0].Days, fixtureDays)
		require.Equal(t, []integrityview.RangeReason{{Code: "quarantine_share", Level: "C", Days: 8,
			Text: integrity.RangeText("quarantine_share")}}, rep.Reasons)
		last := rep.Sites[0].Days[len(rep.Sites[0].Days)-1]
		require.Equal(t, f.today.Format(time.DateOnly), last.Day)
		require.False(t, last.Closed)
		require.Contains(t, last.Reasons[0].Text, "40% of accepted events went to the quarantine band")
		require.InDelta(t, 0.2, *last.Evidence.HostingShare, 1e-9)
		require.InDelta(t, 0.05, *last.Evidence.QuarantineShare, 1e-9)

		// the ungraded site day of aurora is shown with its reason, not graded
		code, body = f.get(t, "/api/operator/web/integrity?tenant="+f.tenants["aurora"].String()+"&"+rq)
		require.Equal(t, http.StatusOK, code, string(body))
		rep = decode[integrityview.Report](t, body)
		require.Equal(t, "A", *rep.Grade)
		require.Equal(t, 10, rep.GradedDays)
		require.Len(t, rep.Sites, 2)

		// a project without rows has an empty report, not an error
		code, body = f.get(t, "/api/operator/web/integrity?tenant="+f.tenants["dune"].String()+"&"+rq)
		require.Equal(t, http.StatusOK, code, string(body))
		rep = decode[integrityview.Report](t, body)
		require.Nil(t, rep.Grade)
		require.Empty(t, rep.Sites)
	})

	t.Run("the route refuses what it cannot answer", func(t *testing.T) {
		for path, want := range map[string]int{
			"/api/operator/web/integrity?" + rq:                                                     http.StatusBadRequest,
			"/api/operator/web/integrity?tenant=nope&" + rq:                                         http.StatusBadRequest,
			"/api/operator/web/integrity?tenant=" + uuid.NewString() + "&" + rq:                     http.StatusNotFound,
			"/api/operator/web/integrity?tenant=" + f.tenants["probe"].String() + "&" + rq:          http.StatusNotFound,
			"/api/operator/web/integrity?tenant=" + f.tenants["aurora"].String() + "&extra=1":       http.StatusBadRequest,
			"/api/operator/web/integrity?tenant=" + f.tenants["aurora"].String() + "&to=2999-01-01": http.StatusBadRequest,
		} {
			code, body := f.get(t, path)
			require.Equal(t, want, code, "%s: %s", path, body)
		}
	})

	t.Run("compare carries each project's integrity", func(t *testing.T) {
		code, body := f.get(t, "/api/operator/web/compare?"+rq+"&tenants="+f.tenants["flare"].String()+","+f.tenants["borealis"].String())
		require.Equal(t, http.StatusOK, code, string(body))
		cmp := decode[WebCompare](t, body)
		require.Len(t, cmp.Projects, 2)
		require.Equal(t, "B", *cmp.Projects[0].Integrity.Grade)
		require.Equal(t, "C", *cmp.Projects[1].Integrity.Grade)
	})
}

// the sql rank of the list and integrity.RangeOf must agree on every mix
// of day counts, or sorting and the shown grade would disagree.
func TestIntegrityRankSQL(t *testing.T) {
	f := newGlobalFixture(t)
	rows := f.superQuery(t, `
		select days_a, days_b, days_c, `+integritySQL+`
		from generate_series(0, 9) days_a, generate_series(0, 9) days_b, generate_series(0, 9) days_c`)
	defer rows.Close()
	n := 0
	for rows.Next() {
		var a, b, c int
		var rank *int
		require.NoError(t, rows.Scan(&a, &b, &c, &rank))
		want := integrity.Rank(integrity.RangeOf(a, b, c))
		if want == 0 {
			require.Nil(t, rank, "%d %d %d", a, b, c)
		} else {
			require.NotNil(t, rank, "%d %d %d", a, b, c)
			require.Equal(t, want, *rank, "%d %d %d", a, b, c)
		}
		n++
	}
	require.NoError(t, rows.Err())
	require.Equal(t, 1000, n)
}
