//go:build isolation

package consoleisolation

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/demo"
	"github.com/justmert/veristar/internal/controlplane/session"
)

// demoCatalogue is one case per route of the public demo: every route its
// groups register, allowed or not, with the body the console's own route
// takes, so a refused write is refused with a well-formed body too.
func demoCatalogue(cat []route, e *env) []route {
	console := map[string]route{}
	for _, r := range cat {
		console[r.key()] = r
	}
	var out []route
	for _, mp := range e.demo.Patterns() {
		method, pattern, _ := strings.Cut(mp, " ")
		r := route{method: method, path: demo.Path + pattern, kind: kindDemo}
		// the console's case, with its query and body
		if c, ok := console[method+" /api"+pattern]; ok {
			r.path, r.body = demo.Path+strings.TrimPrefix(c.path, "/api"), c.body
		}
		out = append(out, r)
	}
	return out
}

// testDemo is the public demo's part of the gate (contract 21). tenant b
// becomes the demo project; tenant a, which holds one of every entity, is
// a real project the demo must never show. every demo route is sent
// anonymously with the demo's ids, with tenant a's ids, and with a
// signed-in member's cookie; nothing of tenant a may come back, no person
// may be named, nothing may change, and no cookie may be set.
func testDemo(t *testing.T, e *env, w *gateWorld, cat []route) {
	ctx := context.Background()
	// one of each operator view's rows for the demo project, and a confirmed
	// cohort, so the views have something of b to show beside a's rows
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(ctx, `update cp.tenants set demo = (id = $1)`, w.tb)
		require.NoError(t, err)
		_, err = c.Exec(ctx, `insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
			values ($1, $2, date_trunc('month', current_date) - interval '1 month', 'primary', 30, 20, 20, 0, 0)`, w.sb, w.tb)
		require.NoError(t, err)
		_, err = c.Exec(ctx, `insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count)
			values ($1, $2, date_trunc('hour', now()) - interval '2 hours', 'accepted', 9)`, w.sb, w.tb)
		require.NoError(t, err)
		_, err = c.Exec(ctx, `insert into cp.tenant_cohorts (tenant_id, program, round, confirmed_at) values ($1, 'Demo Award', 'Demo Round', now())
			on conflict (tenant_id) do update set program = excluded.program, round = excluded.round, confirmed_at = now()`, w.tb)
		require.NoError(t, err)
	})
	t.Cleanup(func() {
		e.superuser(t, func(c *pgx.Conn) {
			_, err := c.Exec(context.Background(), `update cp.tenants set demo = false`)
			require.NoError(t, err)
		})
	})
	before := e.snapshot(t, w)

	// the people of the gate: no demo answer may name any of them, the
	// demo project's own owner included
	people := []string{w.alice.login, w.bob.login, w.op.login, w.alice.userID.String(), w.bob.userID.String(),
		w.op.userID.String()}
	named := func(body []byte) []string {
		var hit []string
		for _, p := range people {
			if p != "" && strings.Contains(string(body), p) {
				hit = append(hit, p)
			}
		}
		return hit
	}
	var allowed, refused int
	demoIDs := gateIDs{tenant: w.tb.String(), site: w.sb.String(), report: w.reportB, rule: w.ruleID,
		chain: uuid.NewString(), user: w.bob.userID.String(), invite: uuid.NewString(), token: uuid.NewString(),
		request: uuid.NewString(), credential: uuid.NewString(), key: uuid.NewString(), entry: uuid.NewString(),
		passkey: uuid.NewString(), domain: "b-gate.example.org"}
	aIDs := w.aIDs()
	// the demo project with tenant a's site, report and chain id
	mixed := demoIDs
	mixed.site, mixed.report, mixed.chain = aIDs.site, aIDs.report, aIDs.chain
	// alice's cookie goes along on the third pass: the demo loads no session,
	// so a member's sign-in neither widens nor changes what it shows
	clients := []struct {
		name string
		c    *client
	}{{"anonymous", e.anonymous(t)}, {"anonymous", e.anonymous(t)}, {"alice's session", w.alice}, {"anonymous", e.anonymous(t)}}
	for _, rt := range cat {
		if rt.kind != kindDemo {
			continue
		}
		pattern := strings.TrimPrefix(strings.SplitN(rt.path, "?", 2)[0], demo.Path)
		ok := rt.method == http.MethodGet && e.demo.Allowed(pattern)
		for i, ids := range []gateIDs{demoIDs, aIDs, demoIDs, mixed} {
			var body any
			if rt.body != nil {
				body = rt.body(ids)
			}
			cl := clients[i]
			res := cl.c.call(t, rt.method, rt.fill(ids), body)
			what := cl.name + " " + rt.method + " " + rt.fill(ids)
			require.Empty(t, w.leaked(res.body), "%s shows tenant a: %s", what, res.body)
			require.Empty(t, named(res.body), "%s names a person: %s", what, res.body)
			require.Empty(t, res.header.Values("Set-Cookie"), "%s sets a cookie", what)
			if !ok {
				require.Equal(t, http.StatusForbidden, res.status, "%s: %s", what, res.body)
				require.Equal(t, "demo_read_only", res.code(t), what)
				refused++
				continue
			}
			allowed++
			naming := strings.Contains(pattern, "{tenant_id}") || strings.Contains(pattern, "{site_id}") ||
				strings.Contains(pattern, "{report_id}")
			switch {
			case i == 1 && naming:
				// tenant a's ids: the same 404 as an id that does not exist
				require.Equal(t, http.StatusNotFound, res.status, "%s: %s", what, res.body)
			case i == 3 && (strings.Contains(rt.path, "{site_id}") || strings.Contains(rt.path, "{report_id}")):
				// another tenant's site or report under the demo project
				require.Equal(t, http.StatusNotFound, res.status, "%s: %s", what, res.body)
			case i == 1 && strings.Contains(rt.path, "={tenant_id}"):
				// a comparison asked for tenant a: refused or empty, never
				// a's numbers (checked above)
				require.NotEqual(t, http.StatusInternalServerError, res.status, "%s: %s", what, res.body)
			case strings.Contains(pattern, "{chain_id}"):
				// no chain id of the demo project exists in the gate
				require.Equal(t, http.StatusNotFound, res.status, "%s: %s", what, res.body)
			case strings.HasPrefix(pattern, "/tenants/{tenant_id}/web/"):
				// the gate's umami holds no event of the demo site; the read
				// goes through as the demo project's owner and answers
				require.Contains(t, []int{http.StatusOK, http.StatusConflict}, res.status, "%s: %s", what, res.body)
			default:
				require.Equal(t, http.StatusOK, res.status, "%s: %s", what, res.body)
			}
		}
	}
	require.Positive(t, allowed)
	require.Positive(t, refused)
	t.Logf("%d allowed reads and %d refusals checked across %d demo routes", allowed, refused, len(demo.Allowlist))

	// the demo's own answers name the demo project and only it
	anon := e.anonymous(t)
	me := anon.call(t, http.MethodGet, demo.Path+"/me", nil)
	require.Equal(t, http.StatusOK, me.status, string(me.body))
	m := me.json(t)
	require.Equal(t, true, m["demo"])
	require.Equal(t, session.DemoUserID.String(), m["user"].(map[string]any)["id"])
	ts := m["tenants"].([]any)
	require.Len(t, ts, 1)
	require.Equal(t, w.tb.String(), ts[0].(map[string]any)["id"])
	require.Equal(t, "viewer", ts[0].(map[string]any)["role"])
	for _, p := range []string{"/tenants", "/operator/tenants", "/operator/web/projects", "/operator/api/projects",
		"/operator/cohorts", "/operator/rules/" + w.ruleID + "/matches", "/operator/web/compare?tenants=" + w.ta.String() + "," + w.tb.String()} {
		res := anon.call(t, http.MethodGet, demo.Path+p, nil)
		require.Empty(t, w.leaked(res.body), "%s: %s", p, res.body)
		switch {
		case strings.Contains(p, "compare"):
		case p == "/operator/cohorts":
			// cohorts are counts by value: the demo's confirmed cohort, and
			// not tenant a's proposal
			require.Equal(t, http.StatusOK, res.status, "%s: %s", p, res.body)
			require.Contains(t, string(res.body), "Demo Award")
			require.NotContains(t, string(res.body), "Gate Award")
		default:
			require.Equal(t, http.StatusOK, res.status, "%s: %s", p, res.body)
			require.Contains(t, string(res.body), w.tb.String(), "%s shows the demo project", p)
		}
	}

	// the gate's rule matches tenant a outside the demo; inside it, a is
	// not even evaluated
	res := w.op.call(t, http.MethodGet, "/api/operator/rules/"+w.ruleID+"/matches", nil)
	require.Equal(t, http.StatusOK, res.status, string(res.body))
	require.Contains(t, string(res.body), w.ta.String(), "the gate's rule matches tenant a for operators")

	// paths the demo does not serve and methods it never takes
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, demo.Path + "/nothing-here"}, {http.MethodGet, demo.Path},
		{http.MethodHead, demo.Path + "/tenants"}, {http.MethodOptions, demo.Path + "/tenants"},
		{http.MethodGet, demo.Path + "/operator/audit"}, {http.MethodGet, demo.Path + "/operator/alerts"},
		{http.MethodGet, demo.Path + "/operator/roster"}, {http.MethodGet, demo.Path + "/../operator/audit"},
	} {
		res := anon.call(t, r.method, r.path, nil)
		require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, res.status, "%s %s: %s", r.method, r.path, res.body)
		require.Empty(t, w.leaked(res.body))
	}

	requireUnchanged(t, before, e.snapshot(t, w), "the demo")

	// a project that loses the flag leaves the demo at the next request
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(ctx, `update cp.tenants set demo = false where id = $1`, w.tb)
		require.NoError(t, err)
	})
	for _, p := range []string{"/tenants/" + w.tb.String(), "/tenants/" + w.tb.String() + "/sites", "/operator/tenants/" + w.tb.String()} {
		res := anon.call(t, http.MethodGet, demo.Path+p, nil)
		require.Equal(t, http.StatusNotFound, res.status, "%s: %s", p, res.body)
	}
	res = anon.call(t, http.MethodGet, demo.Path+"/operator/tenants", nil)
	require.Equal(t, http.StatusOK, res.status, string(res.body))
	require.NotContains(t, string(res.body), w.tb.String())
}
