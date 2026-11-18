package operator

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type auditRow struct {
	ID                int64           `json:"id"`
	Actor             string          `json:"actor"`
	ActorLogin        *string         `json:"actor_login"`
	ActorDisplayName  *string         `json:"actor_display_name"`
	Action            string          `json:"action"`
	Target            string          `json:"target"`
	TargetDisplayName *string         `json:"target_display_name"`
	Detail            json.RawMessage `json:"detail"`
}

type auditAnswer struct {
	Items      []auditRow `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

func getAudit(t *testing.T, r http.Handler, query string) auditAnswer {
	t.Helper()
	code, body := call(t, r, "/api/operator/audit?"+query)
	require.Equal(t, http.StatusOK, code, string(body))
	var a auditAnswer
	require.NoError(t, json.Unmarshal(body, &a), string(body))
	return a
}

func actions(a auditAnswer) []string {
	out := []string{}
	for _, it := range a.Items {
		out = append(out, it.Action)
	}
	return out
}

const (
	walletAddr  = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"
	reportAlive = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	reportGone  = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// auditNamesFixture seeds the objects the audit rows name: a github
// account, a wallet account, a deleted account, a project renamed after
// its rows were written, a removed site, a live report and a report that
// no longer exists.
type auditNamesFixture struct {
	r                              http.Handler
	tenants                        *Tenants
	octo, wallet, gone             uuid.UUID
	tenant, other, site, otherSite uuid.UUID
}

func newAuditNamesFixture(t *testing.T) *auditNamesFixture {
	d, pg := reviewDB(t)
	f := &auditNamesFixture{
		octo: uuid.Must(uuid.NewV7()), wallet: uuid.Must(uuid.NewV7()), gone: uuid.Must(uuid.NewV7()),
		tenant: uuid.Must(uuid.NewV7()), other: uuid.Must(uuid.NewV7()),
		site: uuid.Must(uuid.NewV7()), otherSite: uuid.Must(uuid.NewV7()),
	}
	superExec(t, pg, `insert into cp.users (id, github_id, github_login) values ($1, 11, 'octocat')`, f.octo)
	superExec(t, pg, `insert into cp.users (id, stellar_address) values ($1, $2)`, f.wallet, walletAddr)
	superExec(t, pg, `insert into cp.users (id, deleted_at) values ($1, now())`, f.gone)
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status) values ($1, 'acme', 'Acme Old', 'removed'), ($2, 'zeta', 'Zeta', 'active')`,
		f.tenant, f.other)
	superExec(t, pg, `insert into cp.sites (id, tenant_id, name, domain, status, verify_token) values
		($1, $2, 'Acme docs', 'docs.acme.example', 'removed', 'x'), ($3, $4, 'Zeta app', 'app.zeta.example', 'active', 'y')`,
		f.site, f.tenant, f.otherSite, f.other)
	superExec(t, pg, `insert into cp.usage_reports (id, tenant_id, period_from, period_to, body, signature, signer)
		values ($1, $2, '2026-10-01', '2026-10-31', '{}', $3, $4)`, reportAlive, f.other, strings.Repeat("A", 86)+"==", walletAddr)
	superExec(t, pg, `insert into cp.tenant_removals (tenant_id, requested_by, reason, keep_aggregates) values ($1, $2, 'asked to leave', false)`,
		f.tenant, f.octo)
	tid, oid := f.tenant.String(), f.other.String()
	for _, row := range []struct{ actor, action, target, detail string }{
		{"user:" + f.octo.String(), "tenant.create", "tenant:" + tid, `{"tenant_id":"` + tid + `"}`},
		{"user:" + f.wallet.String(), "site.create", "site:" + f.site.String(), `{"tenant_id":"` + tid + `","domain":"docs.acme.example"}`},
		{"user:" + f.gone.String(), "member.add", "user:" + f.wallet.String(), `{"tenant_id":"` + tid + `"}`},
		{"user:" + f.octo.String(), "member.remove", "user:" + f.gone.String(), `{"tenant_id":"` + tid + `"}`},
		{"user:" + f.wallet.String(), "report.create", "report:" + reportAlive, `{"tenant_id":"` + oid + `"}`},
		{"user:" + f.wallet.String(), "report.revoke", "report:" + reportGone, `{"tenant_id":"` + tid + `","note":"50%_off"}`},
		{"system:purge", "purge.complete", "tenant:" + tid, `{"tenant_id":"` + tid + `"}`},
		{"user:" + f.octo.String(), "export.csv", "operator:web_projects", `{"view":"web_projects"}`},
		{"user:" + f.octo.String(), "site.update", "site:" + f.otherSite.String(), `{"tenant_id":"` + oid + `"}`},
	} {
		superExec(t, pg, `insert into cp.audit_log (actor, action, target, detail) values ($1, $2, $3, $4)`,
			row.actor, row.action, row.target, row.detail)
	}
	// renamed after the rows were written: the route shows the current name
	superExec(t, pg, `update cp.tenants set name = 'Acme' where id = $1`, f.tenant)

	auditLog, err := NewAuditLog(d)
	require.NoError(t, err)
	f.tenants, err = NewTenants(d)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Route("/api/operator", func(s chi.Router) {
		auditLog.Routes(s)
		f.tenants.Routes(s)
	})
	f.r = r
	return f
}

func TestAuditDisplayNames(t *testing.T) {
	f := newAuditNamesFixture(t)
	a := getAudit(t, f.r, "")
	require.Len(t, a.Items, 9)
	byAction := map[string]auditRow{}
	for _, it := range a.Items {
		byAction[it.Action] = it
	}
	str := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	for _, c := range []struct {
		action            string
		actor, actorLogin any
		target            any
	}{
		{"tenant.create", "octocat", "octocat", "Acme"},
		{"site.create", "GBRP…OX2H", nil, "Acme docs"},
		{"member.add", DeletedAccountName, nil, "GBRP…OX2H"},
		{"member.remove", "octocat", "octocat", DeletedAccountName},
		{"report.create", "GBRP…OX2H", nil, "Report 2026-10-01 to 2026-10-31"},
		{"report.revoke", "GBRP…OX2H", nil, DeletedReportName},
		{"purge.complete", nil, nil, "Acme"},
		{"export.csv", "octocat", "octocat", nil},
		{"site.update", "octocat", "octocat", "Zeta app"},
	} {
		row := byAction[c.action]
		require.Equal(t, c.actor, str(row.ActorDisplayName), c.action)
		require.Equal(t, c.actorLogin, str(row.ActorLogin), c.action)
		require.Equal(t, c.target, str(row.TargetDisplayName), c.action)
	}

	// the tenant detail's recent audit carries the same names, and the
	// removal names who asked for it
	code, body := call(t, f.r, "/api/operator/tenants/"+f.tenant.String())
	require.Equal(t, http.StatusOK, code, string(body))
	var det struct {
		Removal struct {
			RequestedBy            uuid.UUID `json:"requested_by"`
			RequestedByLogin       *string   `json:"requested_by_login"`
			RequestedByDisplayName *string   `json:"requested_by_display_name"`
		} `json:"removal"`
		RecentAudit []auditRow `json:"recent_audit"`
	}
	require.NoError(t, json.Unmarshal(body, &det))
	require.Equal(t, f.octo, det.Removal.RequestedBy)
	require.Equal(t, "octocat", str(det.Removal.RequestedByLogin))
	require.Equal(t, "octocat", str(det.Removal.RequestedByDisplayName))
	require.NotEmpty(t, det.RecentAudit)
	for _, it := range det.RecentAudit {
		require.NotNil(t, it.TargetDisplayName, it.Action)
		if it.Action == "tenant.create" {
			require.Equal(t, "Acme", *it.TargetDisplayName)
		}
	}
}

// a removal requested by an account that was deleted since keeps the id
// and names the placeholder; a wallet requester has no login
func TestRemovalRequesterNames(t *testing.T) {
	d, pg := reviewDB(t)
	h, err := NewTenants(d)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Route("/api/operator", h.Routes)
	wallet, gone := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.users (id, stellar_address) values ($1, $2)`, wallet, walletAddr)
	superExec(t, pg, `insert into cp.users (id, deleted_at) values ($1, now())`, gone)
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status) values ($1, 'a', 'A', 'removed'), ($2, 'b', 'B', 'removed'), ($3, 'c', 'C', 'removed')`, a, b, c)
	superExec(t, pg, `insert into cp.tenant_removals (tenant_id, requested_by, reason, keep_aggregates) values
		($1, $2, 'r', false), ($3, $4, 'r', false), ($5, null, 'r', false)`, a, wallet, b, gone, c)

	type removal struct {
		RequestedBy            *uuid.UUID `json:"requested_by"`
		RequestedByLogin       *string    `json:"requested_by_login"`
		RequestedByDisplayName *string    `json:"requested_by_display_name"`
	}
	get := func(id uuid.UUID) removal {
		code, body := call(t, r, "/api/operator/tenants/"+id.String())
		require.Equal(t, http.StatusOK, code, string(body))
		var det struct {
			Removal removal `json:"removal"`
		}
		require.NoError(t, json.Unmarshal(body, &det))
		return det.Removal
	}
	rm := get(a)
	require.Equal(t, wallet, *rm.RequestedBy)
	require.Nil(t, rm.RequestedByLogin)
	require.Equal(t, "GBRP…OX2H", *rm.RequestedByDisplayName)
	rm = get(b)
	require.Equal(t, gone, *rm.RequestedBy)
	require.Nil(t, rm.RequestedByLogin)
	require.Equal(t, DeletedAccountName, *rm.RequestedByDisplayName)
	rm = get(c)
	require.Nil(t, rm.RequestedBy)
	require.Nil(t, rm.RequestedByLogin)
	require.Nil(t, rm.RequestedByDisplayName)
}

func TestAuditTextSearch(t *testing.T) {
	f := newAuditNamesFixture(t)
	for q, want := range map[string][]string{
		// the row's own text: action, actor, target and detail
		"REVOKE":         {"report.revoke"},
		"docs.acme":      {"site.create"},
		"operator:web":   {"export.csv"},
		"system:pu":      {"purge.complete"},
		reportAlive[:10]: {"report.create"},
		// like wildcards in q are literal characters
		"50%_off": {"report.revoke"},
		"%":       {"report.revoke"},
		"_":       {"tenant.create", "site.create", "member.add", "member.remove", "report.create", "report.revoke", "purge.complete", "export.csv", "site.update"},
		// the actor's login and address, never a deleted account's
		"octo":   {"tenant.create", "member.remove", "export.csv", "site.update"},
		"gbrpyh": {"site.create", "member.add", "report.create", "report.revoke"},
		// a project's current name finds its rows, by target or tenant_id
		"acme":             {"tenant.create", "site.create", "member.add", "member.remove", "report.revoke", "purge.complete"},
		"Zeta":             {"report.create", "site.update"},
		"app.zeta.example": {"site.update"},
		"no such thing":    {},
	} {
		a := getAudit(t, f.r, "q="+url.QueryEscape(q))
		got := actions(a)
		require.ElementsMatch(t, want, got, q)
	}
	// q combines with the other filters and pages like them
	a := getAudit(t, f.r, "q=acme&action=member.")
	require.ElementsMatch(t, []string{"member.add", "member.remove"}, actions(a))
	a = getAudit(t, f.r, "q=acme&actor=user:"+f.octo.String()+"&limit=1")
	require.Len(t, a.Items, 1)
	require.NotNil(t, a.NextCursor)
	a = getAudit(t, f.r, "q=acme&actor=user:"+f.octo.String()+"&limit=1&cursor="+url.QueryEscape(*a.NextCursor))
	require.Len(t, a.Items, 1)
	require.Nil(t, a.NextCursor)
	a = getAudit(t, f.r, "q=acme&tenant_id="+f.other.String())
	require.Empty(t, a.Items)

	for _, bad := range []string{"q=", "q=a&q=b", "q=" + strings.Repeat("x", 201), "q=%00"} {
		code, body := call(t, f.r, "/api/operator/audit?"+bad)
		require.Equal(t, http.StatusBadRequest, code, bad)
		require.Contains(t, string(body), `"invalid_query"`, bad)
	}
}

// the trigram index of 00052 serves the search expression of the audit
// query: the two are written separately, so a change to one alone shows
// here as a plan without the index
func TestAuditSearchUsesTheIndex(t *testing.T) {
	d, _ := reviewDB(t)
	require.NoError(t, d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `set local enable_seqscan = off`); err != nil {
			return err
		}
		rows, err := tx.Query(t.Context(), `explain select id from cp.audit_log a
			where (a.action || ' ' || a.actor || ' ' || a.target || ' ' || a.detail::text) ilike '%octocat%'`)
		if err != nil {
			return err
		}
		plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		require.Contains(t, strings.Join(plan, "\n"), "audit_log_search_idx")
		return nil
	}))
	sql, err := os.ReadFile("../../../db/queries/system/audit.sql")
	require.NoError(t, err)
	require.Contains(t, string(sql), `(a.action || ' ' || a.actor || ' ' || a.target || ' ' || a.detail::text) ilike`)
}
