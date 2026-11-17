//go:build isolation

package consoleisolation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/reports"
)

// the console part of the tenant isolation gate: every route the
// control plane serves has a case here, and the coverage check walks the
// real router, so a route added without a case turns the gate red.

// kind is what a route's isolation case asserts.
type kind int

const (
	// a tenant route: a member of another tenant gets 404 not_found for
	// real, guessed, malformed and mixed ids, and nothing changes
	kindTenant kind = iota
	// an /api/operator route: 403 operator_required for a non-operator
	kindOperator
	// a step-up or passkey route of the operator area (contract 5.15):
	// 403 operator_required for a non-operator; an operator reaches it
	// without a step-up, because it is how a session gets one
	kindStepUp
	// a machine metrics route: only a valid operator api key, within its
	// scope (contract 9.3)
	kindMachine
	// the mcp endpoint and its oauth server (contract 12): bearer or no
	// credential, no session. TestMCP runs every tool across tenants; the
	// gate checks that each refuses a request without a credential or a
	// session cookie in place of one
	kindMCP
	// a route that names no tenant: sign-in, the session's own user, the
	// terms, and the tenant list and enrollment, which act on the caller
	// only. covered by TestConsole (Unauthenticated, Enrollment,
	// TenantListAndPatch, Terms) and TestAccountDeletion; here the gate
	// checks that bob's tenant list never holds tenant a
	kindOwn
	// a public route that needs no account: the shared report link and the
	// report keys (contract 20.4). PublicReports checks that a link answers
	// its own report and nothing of another tenant
	kindPublic
)

// route is one registered route with the request its case sends. path
// holds chi parameters ({tenant_id}, ...) that the case fills.
type route struct {
	method, path string
	kind         kind
	body         func(ids gateIDs) any
}

func (r route) key() string { return r.method + " " + strings.SplitN(r.path, "?", 2)[0] }

// gateIDs fills a route's parameters.
type gateIDs struct {
	tenant, user, invite, site, token, request, key, credential, chain, report, rule, entry, passkey string
	// domain is the site's domain, for bodies that must repeat it
	domain string
}

func (r route) fill(ids gateIDs) string {
	return strings.NewReplacer(
		"{tenant_id}", ids.tenant, "{user_id}", ids.user, "{invite_id}", ids.invite,
		"{site_id}", ids.site, "{token_id}", ids.token, "{request_id}", ids.request, "{key_id}", ids.key,
		"{credential_id}", ids.credential, "{chain_id}", ids.chain, "{report_id}", ids.report, "{rule_id}", ids.rule,
		"{entry_id}", ids.entry, "{passkey_id}", ids.passkey,
	).Replace(r.path)
}

func fixed(v any) func(gateIDs) any { return func(gateIDs) any { return v } }

// catalogue lists every route of the api with its case. the bodies would
// succeed for a member (or an operator), so a refusal can only come from
// the tenant check (or the guard).
func catalogue(terms string) []route {
	var out []route
	add := func(k kind, method, path string, body func(gateIDs) any) {
		out = append(out, route{method: method, path: path, kind: k, body: body})
	}
	tp := "/api/tenants/{tenant_id}"
	sp := "/api/sites/{site_id}"

	// names no tenant
	for _, r := range []struct{ m, p string }{
		{http.MethodGet, "/api/auth/github/start"}, {http.MethodGet, "/api/auth/github/callback"},
		{http.MethodPost, "/api/auth/logout"}, {http.MethodGet, "/api/me"}, {http.MethodDelete, "/api/me"},
		{http.MethodGet, "/api/terms"},
		// wallet sign-in and linked sign-in methods (contract 4.1.7, 4.1.8):
		// they act on the caller's own account only. covered by
		// TestWalletIsolation
		{http.MethodPost, "/api/auth/stellar/challenge"}, {http.MethodPost, "/api/auth/stellar/verify"},
		{http.MethodPost, "/api/me/identities/stellar/challenge"}, {http.MethodPost, "/api/me/identities/stellar"},
		{http.MethodDelete, "/api/me/identities/stellar"}, {http.MethodDelete, "/api/me/identities/github"},
		// passkey sign-in and linking (contract 4.1.9): the caller's own
		// account only. covered by TestPasskeyIsolation
		{http.MethodPost, "/api/auth/passkey/challenge"}, {http.MethodPost, "/api/auth/passkey/verify"},
		{http.MethodPost, "/api/me/identities/passkey/challenge"}, {http.MethodPost, "/api/me/identities/passkey"},
		{http.MethodDelete, "/api/me/identities/passkey"},
		{http.MethodGet, "/api/tenants"}, {http.MethodPost, "/api/tenants"},
	} {
		add(kindOwn, r.m, r.p, nil)
	}

	// tenants, members, invites
	add(kindTenant, http.MethodGet, tp, nil)
	add(kindTenant, http.MethodPatch, tp, fixed(map[string]any{"name": "hijacked"}))
	add(kindTenant, http.MethodPost, tp+"/terms", fixed(map[string]any{"version": terms}))
	add(kindTenant, http.MethodPut, tp+"/enrollment", fixed(map[string]any{
		"enrollment": map[string]any{"website": "https://intruder.example.org", "stellar_usage": "intruder"},
	}))
	add(kindTenant, http.MethodGet, tp+"/members", nil)
	add(kindTenant, http.MethodPost, tp+"/members", fixed(map[string]any{"github_login": "intruder-friend", "role": "owner"}))
	add(kindTenant, http.MethodPatch, tp+"/members/{user_id}", fixed(map[string]any{"role": "member"}))
	add(kindTenant, http.MethodDelete, tp+"/members/{user_id}", nil)
	add(kindTenant, http.MethodDelete, tp+"/invites/{invite_id}", nil)
	// sites
	add(kindTenant, http.MethodPost, tp+"/sites", fixed(map[string]any{
		"name": "x", "domain": "intruder.example.org", "origins": []string{"https://intruder.example.org"},
	}))
	add(kindTenant, http.MethodGet, tp+"/sites", nil)
	add(kindTenant, http.MethodGet, tp+"/sites/{site_id}", nil)
	// the web dashboard (contract 18): the project, and one site of it,
	// whose id must belong to the tenant in the path
	add(kindTenant, http.MethodGet, tp+"/web/summary?site={site_id}", nil)
	add(kindTenant, http.MethodGet, tp+"/web/breakdown?dimension=page&site={site_id}", nil)
	add(kindTenant, http.MethodGet, tp+"/web/hourly?site={site_id}", nil)
	add(kindTenant, http.MethodGet, tp+"/web/realtime?site={site_id}", nil)
	add(kindTenant, http.MethodGet, tp+"/web/sessions?site={site_id}", nil)
	// integrity grade (contract 17.2)
	add(kindTenant, http.MethodGet, tp+"/integrity", nil)
	add(kindTenant, http.MethodPatch, tp+"/sites/{site_id}", fixed(map[string]any{"origins": []string{"https://intruder.example.org"}}))
	add(kindTenant, http.MethodDelete, tp+"/sites/{site_id}", nil)
	// otlp tokens
	add(kindTenant, http.MethodPost, tp+"/otlp-tokens", fixed(map[string]any{"name": "intruder", "environment": "production"}))
	add(kindTenant, http.MethodGet, tp+"/otlp-tokens", nil)
	add(kindTenant, http.MethodGet, tp+"/otlp-tokens/{token_id}", nil)
	add(kindTenant, http.MethodDelete, tp+"/otlp-tokens/{token_id}", nil)
	// api metrics
	for _, m := range []string{"overview", "timeseries", "routes", "status", "services", "warnings"} {
		add(kindTenant, http.MethodGet, tp+"/api-metrics/"+m, nil)
	}
	// erasure: the body names the site and repeats its domain,
	// so for the real ids it is exactly the request that resets site a
	add(kindTenant, http.MethodPost, tp+"/erasure", func(ids gateIDs) any {
		return map[string]any{"kind": "site_reset", "site_id": ids.site, "confirm_domain": ids.domain}
	})
	// the on-chain witness, the overview export and the scf view (contract
	// 15 and 16); the add body names an account no project declared
	add(kindTenant, http.MethodGet, tp+"/onchain", nil)
	add(kindTenant, http.MethodPost, tp+"/onchain/ids", fixed(map[string]any{"strkey": gateFreshAccount}))
	add(kindTenant, http.MethodDelete, tp+"/onchain/ids/{chain_id}", nil)
	add(kindTenant, http.MethodPost, tp+"/onchain/ids/{chain_id}/challenge", nil)
	add(kindTenant, http.MethodPost, tp+"/onchain/ids/{chain_id}/verify", fixed(map[string]any{
		"method": "sep53", "signature": "c2lnbmF0dXJl",
	}))
	add(kindTenant, http.MethodGet, tp+"/onchain/ids/{chain_id}/functions", nil)
	add(kindTenant, http.MethodGet, tp+"/onchain/ids/{chain_id}/functions.csv", nil)
	add(kindTenant, http.MethodGet, tp+"/overview.csv", nil)
	add(kindTenant, http.MethodGet, tp+"/scf-view", nil)
	add(kindTenant, http.MethodGet, tp+"/erasure", nil)
	add(kindTenant, http.MethodGet, tp+"/erasure/{request_id}", nil)
	// the cohort proposal and signed usage reports (contract 19.1, 20)
	add(kindTenant, http.MethodGet, tp+"/cohort", nil)
	add(kindTenant, http.MethodPut, tp+"/cohort/proposal", fixed(map[string]any{"program": "Intruder Award"}))
	add(kindTenant, http.MethodGet, tp+"/reports", nil)
	add(kindTenant, http.MethodPost, tp+"/reports", func(gateIDs) any {
		y := time.Now().UTC().AddDate(0, 0, -reports.FinalDays).Format(time.DateOnly)
		return map[string]any{"from": y, "to": y}
	})
	add(kindTenant, http.MethodGet, tp+"/reports/{report_id}", nil)
	add(kindTenant, http.MethodPost, tp+"/reports/{report_id}/revoke", nil)
	add(kindPublic, http.MethodGet, "/api/public/reports/{report_id}", nil)
	add(kindPublic, http.MethodGet, "/api/public/report-keys", nil)
	// site routes
	add(kindTenant, http.MethodGet, sp+"/snippet", nil)
	add(kindTenant, http.MethodGet, sp+"/ingest", nil)
	add(kindTenant, http.MethodGet, sp+"/verification", nil)
	add(kindTenant, http.MethodPost, sp+"/verify", fixed(map[string]any{}))

	// operator area
	op := "/api/operator"
	reason := fixed(map[string]any{"reason": "isolation gate"})
	add(kindOperator, http.MethodGet, op+"/settings/enrollment-mode", nil)
	add(kindOperator, http.MethodPut, op+"/settings/enrollment-mode", fixed(map[string]any{"mode": "approval"}))
	add(kindOperator, http.MethodGet, op+"/settings/removal-keep-aggregates", nil)
	add(kindOperator, http.MethodPut, op+"/settings/removal-keep-aggregates", fixed(map[string]any{"keep": true}))
	add(kindOperator, http.MethodGet, op+"/audit", nil)
	for _, a := range []string{"approve", "reject", "suspend", "resume", "remove"} {
		add(kindOperator, http.MethodPost, op+"/tenants/{tenant_id}/"+a, reason)
	}
	add(kindOperator, http.MethodPost, op+"/sites/{site_id}/approve", reason)
	add(kindOperator, http.MethodPost, op+"/sites/{site_id}/reject", reason)
	add(kindOperator, http.MethodGet, op+"/tenants", nil)
	add(kindOperator, http.MethodGet, op+"/tenants/{tenant_id}", nil)
	add(kindOperator, http.MethodGet, op+"/web/projects", nil)
	add(kindOperator, http.MethodGet, op+"/web/compare?tenants={tenant_id}", nil)
	add(kindOperator, http.MethodGet, op+"/web/integrity?tenant={tenant_id}", nil)
	add(kindOperator, http.MethodGet, op+"/api/projects", nil)
	add(kindOperator, http.MethodGet, op+"/api/compare?tenants={tenant_id}", nil)
	add(kindOperator, http.MethodGet, op+"/tenants/{tenant_id}/onchain", nil)
	add(kindOperator, http.MethodGet, op+"/tenants/{tenant_id}/onchain/ids/{chain_id}/functions", nil)
	add(kindOperator, http.MethodGet, op+"/tenants/{tenant_id}/onchain/ids/{chain_id}/functions.csv", nil)
	add(kindOperator, http.MethodGet, op+"/web/projects.csv", nil)
	add(kindOperator, http.MethodGet, op+"/api/projects.csv", nil)
	add(kindOperator, http.MethodGet, op+"/web/compare.csv?tenants={tenant_id}", nil)
	add(kindOperator, http.MethodGet, op+"/api/compare.csv?tenants={tenant_id}", nil)
	add(kindOperator, http.MethodPost, op+"/api-keys", fixed(map[string]any{"name": "intruder", "scope": "metrics:read"}))
	add(kindOperator, http.MethodGet, op+"/api-keys", nil)
	add(kindOperator, http.MethodGet, op+"/api-keys/{key_id}", nil)
	add(kindOperator, http.MethodGet, op+"/api-keys/{key_id}/usage", nil)
	add(kindOperator, http.MethodDelete, op+"/api-keys/{key_id}", nil)
	// operator mcp credentials (contract 12.8)
	add(kindOperator, http.MethodPost, op+"/mcp-credentials", fixed(map[string]any{"name": "intruder"}))
	add(kindOperator, http.MethodGet, op+"/mcp-credentials", nil)
	add(kindOperator, http.MethodDelete, op+"/mcp-credentials/{credential_id}", nil)
	// cohorts and threshold rules (contract 19)
	add(kindOperator, http.MethodGet, op+"/cohorts", nil)
	add(kindOperator, http.MethodGet, op+"/tenants/{tenant_id}/cohort", nil)
	add(kindOperator, http.MethodPut, op+"/tenants/{tenant_id}/cohort", fixed(map[string]any{"program": "Intruder Award"}))
	add(kindOperator, http.MethodPost, op+"/tenants/{tenant_id}/cohort/confirm", nil)
	add(kindOperator, http.MethodDelete, op+"/tenants/{tenant_id}/cohort/proposal", nil)
	rule := fixed(map[string]any{"name": "intruder", "metric": "visitors", "min_per_day": 1, "min_days": 1, "months": 1})
	add(kindOperator, http.MethodGet, op+"/rules", nil)
	add(kindOperator, http.MethodPost, op+"/rules", rule)
	add(kindOperator, http.MethodGet, op+"/rules/{rule_id}", nil)
	add(kindOperator, http.MethodPut, op+"/rules/{rule_id}", rule)
	add(kindOperator, http.MethodDelete, op+"/rules/{rule_id}", nil)
	add(kindOperator, http.MethodGet, op+"/rules/{rule_id}/matches", nil)
	add(kindOperator, http.MethodGet, op+"/rules/{rule_id}/matches.csv", nil)
	// the operator roster (contract 5.16)
	add(kindOperator, http.MethodGet, op+"/roster", nil)
	add(kindOperator, http.MethodPost, op+"/roster", fixed(map[string]any{
		"github_login": "intruder-friend", "role": "operator_admin", "reason": "isolation gate"}))
	for _, a := range []string{"suspend", "resume", "remove", "reset-passkeys"} {
		add(kindOperator, http.MethodPost, op+"/roster/{entry_id}/"+a, reason)
	}
	add(kindOperator, http.MethodPost, op+"/roster/{entry_id}/role", fixed(map[string]any{"role": "operator", "reason": "isolation gate"}))
	// the step-up and the passkeys (contract 5.15)
	add(kindStepUp, http.MethodGet, op+"/step-up", nil)
	add(kindStepUp, http.MethodPost, op+"/step-up/challenge", fixed(map[string]any{}))
	add(kindStepUp, http.MethodPost, op+"/step-up", fixed(map[string]any{"credential_id": "AQID"}))
	add(kindStepUp, http.MethodGet, op+"/passkeys", nil)
	add(kindStepUp, http.MethodPost, op+"/passkeys/challenge", fixed(map[string]any{}))
	add(kindStepUp, http.MethodPost, op+"/passkeys", fixed(map[string]any{"name": "intruder", "credential_id": "AQID"}))
	add(kindStepUp, http.MethodDelete, op+"/passkeys/{passkey_id}", nil)

	// mcp access keys of a project and the consent page (contract 12.8);
	// the consent routes act on the caller's own memberships only
	add(kindTenant, http.MethodPost, tp+"/mcp-credentials", fixed(map[string]any{"name": "intruder"}))
	add(kindTenant, http.MethodGet, tp+"/mcp-credentials", nil)
	add(kindTenant, http.MethodDelete, tp+"/mcp-credentials/{credential_id}", nil)
	for _, a := range []string{"inspect", "approve", "deny"} {
		add(kindOwn, http.MethodPost, "/api/mcp/authorize/"+a, nil)
	}
	// the mcp endpoint, oauth endpoints and discovery documents
	add(kindMCP, http.MethodPost, "/mcp/", nil)
	add(kindMCP, http.MethodGet, "/oauth/authorize", nil)
	add(kindMCP, http.MethodPost, "/oauth/token", nil)
	add(kindMCP, http.MethodPost, "/oauth/register", nil)
	add(kindMCP, http.MethodPost, "/oauth/revoke", nil)
	add(kindMCP, http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	add(kindMCP, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil)
	add(kindMCP, http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	// the report keys for sep-1 readers (contract 20.2); public by design
	add(kindPublic, http.MethodGet, "/.well-known/stellar.toml", nil)

	// machine metrics api
	for _, p := range []string{"/tenants", "/web/daily", "/web/dimensions", "/web/hourly", "/web/integrity", "/api/daily", "/api/routes/daily",
		"/api/hourly", "/onchain/ids", "/onchain/daily", "/onchain/weekly", "/onchain/functions", "/rules", "/rules/{rule_id}/matches", "/openapi.yaml"} {
		add(kindMachine, http.MethodGet, operator.MachinePath+p, nil)
	}
	return out
}

// the accounts of the gate: tenant a declares the first; the add case
// names the second, which no project declares
const (
	gateAccountA     = "GDWQ7YV27AVTL25BDOLZD6E44VCQLCEBJXBOOWA2R3VLKCUIZHSXZLDE"
	gateFreshAccount = "GABZATVFOHSGTZNO2VUY2JDXW3R76URMFO2MUQVXBRBIDRCJXBEZG5QH"
)

// walked returns "METHOD /path" for every route the router serves.
func walked(t *testing.T, r chi.Routes) []string {
	t.Helper()
	var out []string
	require.NoError(t, chi.Walk(r, func(method, rt string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi keeps the trailing slash of a mounted subrouter's root and
		// writes a mount's own path with /*; neither occurs in this api
		if strings.HasSuffix(rt, "/*") {
			return fmt.Errorf("wildcard route %s %s: give it an explicit case", method, rt)
		}
		out = append(out, method+" "+rt)
		return nil
	}))
	sort.Strings(out)
	return out
}

// gateWorld is the state the cases run against: alice owns tenant a with
// one of every entity a route can name, bob owns tenant b, and an operator.
type gateWorld struct {
	alice, bob, op *client
	// opFresh is a second session of the operator that never stepped up
	opFresh      *client
	ta, tb       uuid.UUID
	sa, sb       uuid.UUID
	domainA      string
	inviteA      string
	tokenA       string // id
	tokenAPlain  string // the otlp token itself
	credentialA  string // id of alice's mcp access key
	chainA       string // id of alice's declared account
	chainKeyA    string // its strkey
	requestA     string
	reportA      string // id of a signed usage report of tenant a
	reportB      string // and of tenant b
	ruleID       string // a threshold rule tenant a meets
	slugA, nameA string
}

func (w *gateWorld) aIDs() gateIDs {
	return gateIDs{tenant: w.ta.String(), user: w.alice.userID.String(), invite: w.inviteA, site: w.sa.String(),
		token: w.tokenA, request: w.requestA, domain: w.domainA, credential: w.credentialA, chain: w.chainA,
		report: w.reportA, rule: w.ruleID}
}

// markers are strings only tenant a's data holds.
func (w *gateWorld) markers() []string {
	return []string{w.ta.String(), w.sa.String(), w.domainA, w.slugA, w.nameA, w.inviteA, w.tokenA, w.requestA, w.credentialA,
		w.chainA, w.chainKeyA, w.reportA}
}

func (w *gateWorld) leaked(body []byte) []string {
	var hit []string
	for _, m := range w.markers() {
		if m != "" && strings.Contains(string(body), m) {
			hit = append(hit, m)
		}
	}
	return hit
}

func (e *env) gateWorld(t *testing.T) *gateWorld {
	t.Helper()
	w := &gateWorld{domainA: "a-gate.example.org", nameA: "Gate Tenant Alpha"}
	w.op = e.signInAs(t, operatorGHID, "gate-operator")
	w.op.stepUp(t)
	w.opFresh = e.signInAs(t, operatorGHID, "gate-operator")
	w.alice = e.signIn(t, "gate-alice")
	w.bob = e.signIn(t, "gate-bob")

	r := w.alice.call(t, http.MethodPost, "/api/tenants", map[string]any{
		"name": w.nameA,
		"enrollment": map[string]any{
			"website": "https://example.org", "stellar_usage": "soroban contracts on mainnet",
		},
		"accepted_terms_version": e.terms,
	})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	w.ta = uuid.MustParse(r.json(t)["id"].(string))
	w.slugA = r.json(t)["slug"].(string)
	require.Equal(t, "active", r.json(t)["status"], "the gate runs in open enrollment mode")
	w.tb = w.bob.enroll(t, "Gate Tenant Beta")
	w.sa = w.alice.addSite(t, w.ta, w.domainA)
	w.sb = w.bob.addSite(t, w.tb, "b-gate.example.org")

	r = w.alice.call(t, http.MethodPost, "/api/tenants/"+w.ta.String()+"/members", map[string]any{"github_login": "gate-pending", "role": "member"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	w.inviteA = r.json(t)["invite"].(map[string]any)["id"].(string)

	r = w.alice.call(t, http.MethodPost, "/api/tenants/"+w.ta.String()+"/otlp-tokens", map[string]any{"name": "gate", "environment": "production"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	w.tokenA = r.json(t)["id"].(string)
	w.tokenAPlain = r.json(t)["token"].(string)
	require.True(t, strings.HasPrefix(w.tokenAPlain, "sa_otlp_"))

	w.credentialA, _ = w.alice.mcpKey(t, w.ta, "gate")

	// a declared account of tenant a, with ledger activity and a read state
	w.chainKeyA = gateAccountA
	r = w.alice.call(t, http.MethodPost, "/api/tenants/"+w.ta.String()+"/onchain/ids", map[string]any{"strkey": w.chainKeyA})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	w.chainA = r.json(t)["id"].(string)
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
			values ($1, $2, current_date - 1, 'horizon', 7, 3, 2)`, w.ta, w.chainKeyA)
		require.NoError(t, err)
		_, err = c.Exec(context.Background(), `insert into agg.chain_reads (tenant_id, strkey, covered_from, covered_to, last_attempt_at, home_domain)
			values ($1, $2, current_date - 1, current_date - 1, now(), $3)`, w.ta, w.chainKeyA, w.domainA)
		require.NoError(t, err)
		// a closed week of weekly active accounts
		_, err = c.Exec(context.Background(), `insert into agg.chain_weeks (tenant_id, week, active_accounts, closed_at)
			values ($1, date_trunc('week', current_date - 7)::date, 5, now())`, w.ta)
		require.NoError(t, err)
	})

	// a finished erasure request of site a, written as the control plane
	// writes one at the end of a run; a real run needs the gateway and the
	// replayer, which this harness does not start
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(), `
			insert into cp.erasure_requests (tenant_id, site_id, kind, requested_by, status, outcome, completed_at)
			values ($1, $2, 'site_reset', $3, 'done', 'erased', now()) returning id::text`,
			w.ta, w.sa, w.alice.userID).Scan(&w.requestA))
	})
	r = w.alice.call(t, http.MethodGet, "/api/tenants/"+w.ta.String()+"/erasure/"+w.requestA, nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))

	// a cohort proposal and one day of visitors in the previous month for
	// tenant a, a threshold rule it meets, and a signed report of each
	// tenant (contract 19, 20)
	r = w.alice.call(t, http.MethodPut, "/api/tenants/"+w.ta.String()+"/cohort/proposal", map[string]any{"program": "Gate Award"})
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
			values ($1, $2, date_trunc('month', current_date) - interval '1 month', 'primary', 3, 2, 2, 0, 0)`, w.sa, w.ta)
		require.NoError(t, err)
	})
	r = w.op.call(t, http.MethodPost, "/api/operator/rules", map[string]any{
		"name": "gate rule", "metric": "visitors", "min_per_day": 1, "min_days": 1, "months": 1})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	w.ruleID = r.json(t)["id"].(string)
	y := time.Now().UTC().AddDate(0, 0, -reports.FinalDays).Format(time.DateOnly)
	r = w.alice.call(t, http.MethodPost, "/api/tenants/"+w.ta.String()+"/reports", map[string]any{"from": y, "to": y})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	w.reportA = r.json(t)["id"].(string)
	r = w.bob.call(t, http.MethodPost, "/api/tenants/"+w.tb.String()+"/reports", map[string]any{"from": y, "to": y})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	w.reportB = r.json(t)["id"].(string)

	// the stellar.toml fetches site creation started have written their rows
	e.checks.Wait()
	// both tenants' websites exist in umami before anything is compared:
	// the reconciles that enrollment and site creation started are done
	require.Eventually(t, func() bool {
		for _, s := range []uuid.UUID{w.sa, w.sb} {
			_, ok, err := e.umami.Admin.GetWebsite(context.Background(), s.String())
			if err != nil || !ok {
				return false
			}
		}
		return true
	}, 2*time.Minute, 500*time.Millisecond, "umami websites of both tenants")
	return w
}

// snapshot is everything of tenant a, the platform settings, the operator
// keys and both registries, read past row-level security, plus bob's audit
// trail. a refused request must leave it byte for byte the same.
func (e *env) snapshot(t *testing.T, w *gateWorld) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	ab := []any{w.ta, w.tb}
	queries := []struct {
		name, sql string
		args      []any
	}{
		{"tenants", `select coalesce(json_agg(x order by x.id)::text, '') from cp.tenants x where x.id in ($1, $2)`, ab},
		{"members", `select coalesce(json_agg(x order by x.tenant_id, x.user_id)::text, '') from cp.tenant_members x where x.tenant_id in ($1, $2)`, ab},
		{"invites", `select coalesce(json_agg(x order by x.id)::text, '') from cp.invites x where x.tenant_id in ($1, $2)`, ab},
		{"sites", `select coalesce(json_agg(x order by x.id)::text, '') from cp.sites x where x.tenant_id in ($1, $2)`, ab},
		{"otlp_tokens", `select coalesce(json_agg(x order by x.id)::text, '') from cp.otlp_tokens x where x.tenant_id in ($1, $2)`, ab},
		{"erasure_requests", `select coalesce(json_agg(x order by x.id)::text, '') from cp.erasure_requests x where x.tenant_id in ($1, $2)`, ab},
		{"tenant_removals", `select coalesce(json_agg(x order by x.tenant_id)::text, '') from cp.tenant_removals x where x.tenant_id in ($1, $2)`, ab},
		{"settings", `select coalesce(json_agg(x order by x.key)::text, '') from cp.settings x`, nil},
		{"roster", `select coalesce(json_agg(x order by x.id)::text, '') from cp.operators x`, nil},
		{"passkeys", `select coalesce(json_agg(json_build_array(x.id, x.user_id, x.name) order by x.id)::text, '') from cp.operator_passkeys x`, nil},
		// last_seen_at moves with every accepted mcp request, by design
		{"mcp_credentials", `select coalesce(json_agg(json_build_array(x.id, x.tenant_id, x.user_id, x.name, x.expires_at, x.revoked_at) order by x.id)::text, '')
			from cp.mcp_credentials x`, nil},
		// last_used_at moves with every accepted machine request, by design
		{"operator_keys", `select coalesce(json_agg(json_build_array(x.id, x.name, x.scope, x.scope_tenant_id, x.expires_at, x.revoked_at) order by x.id)::text, '')
			from cp.operator_api_keys x`, nil},
		{"chain_ids", `select coalesce(json_agg(x order by x.id)::text, '') from cp.chain_ids x where x.tenant_id in ($1, $2)`, ab},
		{"chain_daily", `select coalesce(json_agg(x order by x.tenant_id, x.strkey, x.day)::text, '') from agg.chain_daily x where x.tenant_id in ($1, $2)`, ab},
		{"chain_function_daily", `select coalesce(json_agg(x order by x.tenant_id, x.strkey, x.day, x.caller, x.function_name)::text, '')
			from agg.chain_function_daily x where x.tenant_id in ($1, $2)`, ab},
		{"tenant_cohorts", `select coalesce(json_agg(x order by x.tenant_id)::text, '') from cp.tenant_cohorts x where x.tenant_id in ($1, $2)`, ab},
		{"threshold_rules", `select coalesce(json_agg(x order by x.id)::text, '') from cp.threshold_rules x`, nil},
		// view_count moves with every public view, by design
		{"usage_reports", `select coalesce(json_agg(json_build_array(x.id, x.tenant_id, x.body, x.signature, x.revoked_at) order by x.id)::text, '')
			from cp.usage_reports x where x.tenant_id in ($1, $2)`, ab},
		{"audit_tenant_a", `select count(*)::text from cp.audit_log x where x.target in ('tenant:' || $1, 'site:' || $2, 'chain_id:' || $3, 'report:' || $4)
			and x.action <> 'report.view'`, []any{w.ta.String(), w.sa.String(), w.chainA, w.reportA}},
		{"audit_bob", `select count(*)::text from cp.audit_log x where x.actor = 'user:' || $1`, []any{w.bob.userID.String()}},
	}
	e.superuser(t, func(c *pgx.Conn) {
		for _, q := range queries {
			var v string
			require.NoError(t, c.QueryRow(ctx, q.sql, q.args...).Scan(&v), q.name)
			out[q.name] = v
		}
	})
	for name, kv := range map[string]jetstream.KeyValue{"registry_sites": e.sites, "registry_otlp": e.otlp} {
		keys, err := kv.ListKeys(ctx)
		require.NoError(t, err)
		var lines []string
		for k := range keys.Keys() {
			v, err := kv.Get(ctx, k)
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				continue
			}
			require.NoError(t, err)
			lines = append(lines, k+"="+string(v.Value()))
		}
		sort.Strings(lines)
		out[name] = strings.Join(lines, "\n")
	}
	return out
}

func requireUnchanged(t *testing.T, before, after map[string]string, what string) {
	t.Helper()
	for k, v := range before {
		require.Equal(t, v, after[k], "%s changed %s", what, k)
	}
}

func TestIsolationGate(t *testing.T) {
	e := start(t)
	cat := catalogue(e.terms)
	t.Run("RouteCoverage", func(t *testing.T) { testRouteCoverage(t, e, cat) })
	w := e.gateWorld(t)
	t.Run("TenantRoutesCrossTenant", func(t *testing.T) { testTenantRoutes(t, e, w, cat) })
	t.Run("OperatorRoutesNonOperator", func(t *testing.T) { testOperatorRoutes(t, e, w, cat) })
	t.Run("MachineAPIScope", func(t *testing.T) { testMachineAPI(t, e, w, cat) })
	t.Run("MCPRoutes", func(t *testing.T) { testMCPRoutes(t, e, w, cat) })
	t.Run("PublicReports", func(t *testing.T) { testPublicRoutes(t, e, w, cat) })
}

// testPublicRoutes: a report link answers that one report, to anyone,
// and nothing of any other tenant; an unknown, malformed or revoked link
// is the same 404; the key documents hold no tenant data at all.
func testPublicRoutes(t *testing.T, e *env, w *gateWorld, cat []route) {
	var bMarkers []string
	r := w.bob.call(t, http.MethodGet, "/api/tenants/"+w.tb.String(), nil)
	require.Equal(t, http.StatusOK, r.status)
	bMarkers = append(bMarkers, w.tb.String(), w.sb.String(), "b-gate.example.org", r.json(t)["slug"].(string),
		r.json(t)["name"].(string), w.reportB)
	leakedB := func(body []byte) []string {
		var hit []string
		for _, m := range bMarkers {
			if strings.Contains(string(body), m) {
				hit = append(hit, m)
			}
		}
		return hit
	}
	sent := 0
	for _, c := range []*client{e.anonymous(t), w.bob, w.op} {
		// a's link holds a's report and nothing of tenant b
		res := c.call(t, http.MethodGet, "/api/public/reports/"+w.reportA, nil)
		require.Equal(t, http.StatusOK, res.status, "%s: %s", c.login, res.body)
		require.Contains(t, string(res.body), w.ta.String())
		require.Empty(t, leakedB(res.body), "%s: a's link: %s", c.login, res.body)
		// b's link holds b's report and nothing of tenant a
		res = c.call(t, http.MethodGet, "/api/public/reports/"+w.reportB, nil)
		require.Equal(t, http.StatusOK, res.status, "%s: %s", c.login, res.body)
		require.Empty(t, w.leaked(res.body), "%s: b's link: %s", c.login, res.body)
		sent += 2
		for _, id := range []string{"aaaaaaaaaaaaaaaaaaaaaaaaaa", strings.ToUpper(w.reportA), w.reportA + "a", w.ta.String(), "..", "%2e%2e"} {
			res := c.call(t, http.MethodGet, "/api/public/reports/"+id, nil)
			require.Equal(t, http.StatusNotFound, res.status, "%s: %s", id, res.body)
			require.Empty(t, w.leaked(res.body))
			require.Empty(t, leakedB(res.body))
			sent++
		}
	}
	for _, rt := range cat {
		if rt.kind != kindPublic || strings.Contains(rt.path, "{") {
			continue
		}
		res := e.anonymous(t).call(t, rt.method, rt.path, nil)
		require.Equal(t, http.StatusOK, res.status, "%s: %s", rt.path, res.body)
		require.Empty(t, w.leaked(res.body), rt.path)
		require.Empty(t, leakedB(res.body), rt.path)
		sent++
	}
	// a revoked link is gone for everyone, and stays so
	r = w.bob.call(t, http.MethodPost, "/api/tenants/"+w.tb.String()+"/reports/"+w.reportB+"/revoke", nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	res := e.anonymous(t).call(t, http.MethodGet, "/api/public/reports/"+w.reportB, nil)
	require.Equal(t, http.StatusNotFound, res.status)
	require.Empty(t, leakedB(res.body))
	t.Logf("%d public report requests: each link answered its own report only", sent+1)
}

// testMCPRoutes: the mcp endpoint refuses every request without a bearer
// credential, a console session included, and bob's key never reaches
// tenant a through any tool that takes a site id. the oauth endpoints and
// documents need no credential by design; their rules are in TestMCP.
func testMCPRoutes(t *testing.T, e *env, w *gateWorld, cat []route) {
	_, keyB := w.bob.mcpKey(t, w.tb, "gate-b")
	before := e.snapshot(t, w)
	n := 0
	for _, r := range cat {
		if r.kind != kindMCP || r.path != "/mcp/" {
			continue
		}
		for _, c := range []*client{e.anonymous(t), w.bob, w.op} {
			// the client's cookie jar sends its session; no Origin, which the
			// endpoint refuses before it looks at any credential
			req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/mcp",
				strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			res, err := c.hc.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			require.NoError(t, err)
			require.Equal(t, http.StatusUnauthorized, res.StatusCode, "%s: %s", c.login, body)
			require.Empty(t, w.leaked(body))
			n++
		}
	}
	cs := e.mustConnect(t, keyB)
	for _, tool := range []string{"get_site", "get_web_stats", "get_web_traffic", "get_web_realtime", "get_event_stats",
		"get_sessions", "get_session_stats", "list_funnels", "run_journey", "run_retention", "get_goals"} {
		res := call(t, cs, tool, map[string]any{"site_id": w.sa.String()})
		require.Equal(t, "not_found", res.code, "%s: %s", tool, res.raw)
		require.Empty(t, w.leaked([]byte(res.raw)), "%s: %s", tool, res.raw)
		n++
	}
	for _, tool := range []string{"list_projects", "list_sites", "list_api_services", "list_api_routes"} {
		res := call(t, cs, tool, nil)
		require.False(t, res.isError, "%s: %s", tool, res.raw)
		require.Empty(t, w.leaked([]byte(res.raw)), "%s: %s", tool, res.raw)
		n++
	}
	requireUnchanged(t, before, e.snapshot(t, w), "an mcp request")
	t.Logf("%d mcp requests: no credential or a session refused, bob's key never read tenant a", n)
}

// every route the real router serves has a case, and every case names a
// route the router serves: a new route without a case fails here.
func testRouteCoverage(t *testing.T, e *env, cat []route) {
	got := walked(t, e.router)
	want := make([]string, 0, len(cat))
	seen := map[string]bool{}
	for _, r := range cat {
		k := r.key()
		require.False(t, seen[k], "duplicate case %s", k)
		seen[k] = true
		want = append(want, k)
	}
	sort.Strings(want)
	var missing, stale []string
	for _, g := range got {
		if !seen[g] {
			missing = append(missing, g)
		}
	}
	served := map[string]bool{}
	for _, g := range got {
		served[g] = true
	}
	for _, w := range want {
		if !served[w] {
			stale = append(stale, w)
		}
	}
	require.Empty(t, missing, "routes without an isolation case: add one to catalogue in gate_test.go")
	require.Empty(t, stale, "cases for routes the router does not serve")
	counts := map[kind]int{}
	for _, r := range cat {
		counts[r.kind]++
	}
	t.Logf("%d routes walked, all covered: %d tenant, %d operator, %d step-up, %d machine, %d mcp, %d without a tenant",
		len(got), counts[kindTenant], counts[kindOperator], counts[kindStepUp], counts[kindMachine], counts[kindMCP],
		counts[kindOwn])
}

func testTenantRoutes(t *testing.T, e *env, w *gateWorld, cat []route) {
	before := e.snapshot(t, w)
	a := w.aIDs()
	guess := uuid.NewString()
	cases := []struct {
		name string
		ids  gateIDs
	}{
		{"real", a},
		{"guessed", gateIDs{tenant: guess, user: guess, invite: guess, site: guess, token: guess, request: guess, domain: w.domainA, credential: guess,
			report: "aaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		{"malformed", gateIDs{tenant: "not-a-uuid", user: "1", invite: "..", site: "%2e%2e", token: "x'or'1", request: strings.ToUpper(w.requestA), domain: w.domainA, credential: "x",
			report: strings.ToUpper(w.reportA)}},
		// bob's own tenant with tenant a's members, invite, site, token and
		// erasure request
		{"mixed", gateIDs{tenant: w.tb.String(), user: a.user, invite: a.invite, site: a.site, token: a.token, request: a.request, domain: w.domainA, credential: a.credential,
			report: a.report}},
	}
	sent := 0
	for _, c := range cases {
		for _, r := range cat {
			if r.kind != kindTenant {
				continue
			}
			path := r.fill(c.ids)
			if c.name == "mixed" && !strings.Contains(r.path, "{") {
				continue
			}
			if c.name == "mixed" && strings.HasPrefix(r.path, "/api/tenants/{tenant_id}") &&
				strings.Count(r.path, "{") == 1 {
				// bob's own tenant without a foreign id in the path is his to
				// use; the foreign ids in bodies have their own case below
				continue
			}
			var body any
			if r.body != nil {
				body = r.body(c.ids)
			}
			res := w.bob.call(t, r.method, path, body)
			sent++
			require.Equal(t, http.StatusNotFound, res.status, "%s: %s %s: %s", c.name, r.method, path, res.body)
			require.Equal(t, "not_found", res.code(t), "%s: %s %s", c.name, r.method, path)
			require.Empty(t, w.leaked(res.body), "%s: %s %s: %s", c.name, r.method, path, res.body)
		}
	}

	// foreign ids in the body of bob's own tenant routes: an erasure of
	// site a through tenant b is refused on the site id
	res := w.bob.call(t, http.MethodPost, "/api/tenants/"+w.tb.String()+"/erasure",
		map[string]any{"kind": "site_reset", "site_id": w.sa.String(), "confirm_domain": w.domainA})
	require.Equal(t, http.StatusUnprocessableEntity, res.status, string(res.body))
	require.Contains(t, res.fields(t), "site_id")
	sent++

	// bob's lists hold only tenant b
	for _, p := range []string{"/api/tenants", "/api/me", "/api/tenants/" + w.tb.String() + "/sites",
		"/api/tenants/" + w.tb.String() + "/otlp-tokens", "/api/tenants/" + w.tb.String() + "/erasure",
		"/api/tenants/" + w.tb.String() + "/members"} {
		res := w.bob.call(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusOK, res.status, p)
		require.Empty(t, w.leaked(res.body), "%s: %s", p, res.body)
		sent++
	}

	requireUnchanged(t, before, e.snapshot(t, w), "a cross-tenant request")
	// alice still reaches everything of hers
	for _, r := range cat {
		if r.kind == kindTenant && r.method == http.MethodGet {
			res := w.alice.call(t, r.method, r.fill(a), nil)
			require.Equal(t, http.StatusOK, res.status, "control: alice %s %s: %s", r.method, r.fill(a), res.body)
		}
	}
	t.Logf("%d cross-tenant requests refused, tenant a unchanged", sent)
}

func testOperatorRoutes(t *testing.T, e *env, w *gateWorld, cat []route) {
	// an api key of the operator, so the key routes have a real id
	r := w.op.call(t, http.MethodPost, "/api/operator/api-keys", map[string]any{"name": "gate-control", "scope": "metrics:read"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	keyID := r.json(t)["id"].(string)

	before := e.snapshot(t, w)
	sent := 0
	idsets := []gateIDs{
		{tenant: w.ta.String(), site: w.sa.String(), key: keyID, rule: w.ruleID},
		// bob trying to act on his own tenant and site
		{tenant: w.tb.String(), site: w.sb.String(), key: keyID, rule: w.ruleID},
		{tenant: uuid.NewString(), site: uuid.NewString(), key: uuid.NewString(), rule: uuid.NewString()},
	}
	anon := e.anonymous(t)
	for _, r := range cat {
		if r.kind != kindOperator && r.kind != kindStepUp {
			continue
		}
		for _, ids := range idsets {
			var body any
			if r.body != nil {
				body = r.body(ids)
			}
			for _, c := range []*client{w.bob, w.alice} {
				res := c.call(t, r.method, r.fill(ids), body)
				require.Equal(t, http.StatusForbidden, res.status, "%s %s as %s: %s", r.method, r.fill(ids), c.login, res.body)
				require.Equal(t, "operator_required", res.code(t))
				sent++
			}
		}
		res := anon.call(t, r.method, r.fill(idsets[0]), nil)
		require.Equal(t, http.StatusUnauthorized, res.status, "%s %s anonymous", r.method, r.path)
	}
	// unknown paths and methods in the area tell a non-operator nothing
	for _, p := range []struct{ m, p string }{
		{http.MethodGet, "/api/operator/nope"}, {http.MethodDelete, "/api/operator/tenants"},
		{http.MethodGet, "/api/operator"}, {http.MethodPost, "/api/operator/settings/enrollment-mode"},
	} {
		res := w.bob.call(t, p.m, p.p, map[string]any{})
		require.Equal(t, http.StatusForbidden, res.status, "%s %s", p.m, p.p)
		require.Equal(t, "operator_required", res.code(t))
		sent++
	}
	requireUnchanged(t, before, e.snapshot(t, w), "a non-operator request")

	// an operator session without a recent passkey step-up gets 401
	// step_up_required on every route of the area except the step-up and
	// passkey routes, and changes nothing
	entry := rosterEntryOf(t, w.op, operatorGHID)
	for _, r := range cat {
		if r.kind != kindOperator {
			continue
		}
		// every parameter is filled, so each request reaches a route
		guess := uuid.NewString()
		ids := gateIDs{tenant: w.ta.String(), site: w.sa.String(), key: keyID, entry: entry, credential: guess,
			report: guess, rule: w.ruleID, passkey: guess, chain: guess, invite: guess, user: guess, token: guess,
			request: guess}
		var body any
		if r.body != nil {
			body = r.body(ids)
		}
		res := w.opFresh.call(t, r.method, r.fill(ids), body)
		require.Equal(t, http.StatusUnauthorized, res.status, "%s %s without step-up: %s", r.method, r.fill(ids), res.body)
		require.Equal(t, "step_up_required", res.code(t))
		sent++
	}
	// the step-up status answers, the passkey list and a deletion need the
	// step-up, and a registration needs it once the window closed
	for _, c := range []struct {
		m, p   string
		status int
		code   string
	}{
		{http.MethodGet, "/api/operator/step-up", http.StatusOK, ""},
		{http.MethodGet, "/api/operator/passkeys", http.StatusUnauthorized, "step_up_required"},
		{http.MethodDelete, "/api/operator/passkeys/" + uuid.NewString(), http.StatusUnauthorized, "step_up_required"},
		{http.MethodPost, "/api/operator/passkeys/challenge", http.StatusUnauthorized, "step_up_required"},
	} {
		var body any
		if c.m == http.MethodPost {
			body = map[string]any{}
		}
		res := w.opFresh.call(t, c.m, c.p, body)
		require.Equal(t, c.status, res.status, "%s %s without step-up: %s", c.m, c.p, res.body)
		if c.code != "" {
			require.Equal(t, c.code, res.code(t))
		}
	}
	requireUnchanged(t, before, e.snapshot(t, w), "an operator request without a step-up")

	// control: the operator reaches every read, so the 403s above came from
	// the guard and not from a missing route
	for _, r := range cat {
		if (r.kind == kindOperator || r.kind == kindStepUp) && r.method == http.MethodGet {
			p := r.fill(gateIDs{tenant: w.ta.String(), key: keyID, rule: w.ruleID, chain: w.chainA})
			res := w.op.call(t, r.method, p, nil)
			require.Equal(t, http.StatusOK, res.status, "control: operator %s: %s", p, res.body)
		}
	}
	t.Logf("%d non-operator requests refused with 403 operator_required, nothing changed", sent)
}

// rosterEntryOf is the id of the roster row of a github id
func rosterEntryOf(t *testing.T, op *client, gh int64) string {
	t.Helper()
	r := op.call(t, http.MethodGet, "/api/operator/roster", nil)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	for _, it := range r.json(t)["items"].([]any) {
		m := it.(map[string]any)
		if id, ok := m["github_id"].(float64); ok && int64(id) == gh {
			return m["id"].(string)
		}
	}
	t.Fatalf("no roster row for github id %d", gh)
	return ""
}

type machineResp struct {
	status int
	header http.Header
	body   []byte
}

// machine sends one request to the machine api. headers are set as given,
// so a test can repeat Authorization. a 429 of the per-key rate limit
// (10 per second, burst 20, the production default) is waited out and the
// request repeated; it is the only answer that is.
func (c *client) machine(t *testing.T, path string, headers [][2]string) machineResp {
	t.Helper()
	for {
		req, err := http.NewRequest(http.MethodGet, c.e.srv.URL+path, nil)
		require.NoError(t, err)
		for _, h := range headers {
			req.Header.Add(h[0], h[1])
		}
		res, err := c.hc.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		require.NoError(t, err)
		if res.StatusCode == http.StatusTooManyRequests {
			s, err := strconv.Atoi(res.Header.Get("Retry-After"))
			require.NoError(t, err)
			time.Sleep(time.Duration(s)*time.Second + 100*time.Millisecond)
			continue
		}
		return machineResp{status: res.StatusCode, header: res.Header, body: b}
	}
}

func bearer(k string) [][2]string { return [][2]string{{"Authorization", "Bearer " + k}} }

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	var m struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(b, &m), string(b))
	return m.Error.Code
}

func testMachineAPI(t *testing.T, e *env, w *gateWorld, cat []route) {
	newKey := func(name string, tenant *uuid.UUID) (id, plain string) {
		body := map[string]any{"name": name, "scope": "metrics:read"}
		if tenant != nil {
			body["tenant_id"] = tenant.String()
		}
		r := w.op.call(t, http.MethodPost, "/api/operator/api-keys", body)
		require.Equal(t, http.StatusCreated, r.status, string(r.body))
		return r.json(t)["id"].(string), r.json(t)["key"].(string)
	}
	_, global := newKey("gate-global", nil)
	_, keyA := newKey("gate-a", &w.ta)
	_, keyB := newKey("gate-b", &w.tb)
	revokedID, revoked := newKey("gate-revoked", &w.tb)
	require.Equal(t, http.StatusNoContent, w.op.call(t, http.MethodDelete, "/api/operator/api-keys/"+revokedID, nil).status)
	expiredID, expired := newKey("gate-expired", &w.tb)
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.operator_api_keys
			set created_at = now() - interval '2 days', expires_at = now() - interval '1 minute' where id = $1`, expiredID)
		require.NoError(t, err)
		// aggregate rows for both tenants, so an answer about tenant b would
		// show tenant a's if the scope leaked
		for i, tn := range []uuid.UUID{w.ta, w.tb} {
			site := []uuid.UUID{w.sa, w.sb}[i]
			for _, q := range []string{
				`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
				 values ($1, $2, current_date, 'primary', 5, 2, 3, 1, 40)`,
				`insert into agg.web_daily_dim (site_id, tenant_id, day, band, dimension, key, value)
				 values ($1, $2, current_date, 'primary', 'country', 'DE', 2)`,
				`insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed, events,
				   quarantined, proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown,
				   networks, visitors, pageviews, baseline_visitors, baseline_days, reasons)
				 values ($1, $2, current_date, 1, 'A', false, 60, 0, 0, 60, 0, 0, 0, 0, 3, 2, 5, null, 0, '[]')`,
				// coverage rides on the integrity and daily answers (17.5)
				`insert into agg.web_coverage_daily (site_id, tenant_id, day, script_requests, gpc_requests)
				 values ($1, $2, current_date, 200, 20)`,
			} {
				_, err := c.Exec(context.Background(), q, site, tn)
				require.NoError(t, err)
			}
			counts := []int64{0, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
			for _, q := range []string{
				`insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
				 values ($1, date_trunc('hour', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`,
				`insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
				 values ($1, date_trunc('day', now(), 'UTC'), 'svc', 'GET', '/a', 200, 'metric', $2, 11.5, 4)`,
			} {
				_, err := c.Exec(context.Background(), q, tn, counts)
				require.NoError(t, err)
			}
		}
	})
	before := e.snapshot(t, w)

	var dataRoutes []string
	for _, r := range cat {
		if r.kind == kindMachine && !strings.HasSuffix(r.path, "/openapi.yaml") {
			dataRoutes = append(dataRoutes, r.fill(gateIDs{rule: w.ruleID}))
		}
	}
	require.Len(t, dataRoutes, 14)
	sent := 0
	refused := func(r machineResp, status int, code, what string) {
		t.Helper()
		require.Equal(t, status, r.status, "%s: %s", what, r.body)
		require.Equal(t, code, errCode(t, r.body), what)
		// a's own key may name its own tenant in the refusal; every other
		// caller must never see anything of tenant a
		if !strings.HasPrefix(what, "a's key") {
			require.Empty(t, w.leaked(r.body), "%s: %s", what, r.body)
		}
		if status == http.StatusUnauthorized {
			require.Equal(t, `Bearer realm="metrics"`, r.header.Get("WWW-Authenticate"), what)
		}
		sent++
	}
	malformed := map[string][][2]string{
		"no header":         nil,
		"empty bearer":      {{"Authorization", "Bearer "}},
		"short key":         bearer("sa_op_short"),
		"key plus a char":   bearer(global + "x"),
		"key minus a char":  bearer(global[:len(global)-1]),
		"other scheme":      {{"Authorization", "Basic " + global}},
		"no scheme":         {{"Authorization", global}},
		"two headers":       {{"Authorization", "Bearer " + global}, {"Authorization", "Bearer " + keyB}},
		"otlp token as key": bearer(w.tokenAPlain),
		"guessed key":       bearer("sa_op_" + strings.Repeat("A", 40)),
	}
	all := append(append([]string{}, dataRoutes...), operator.MachinePath+"/openapi.yaml")
	for _, p := range all {
		// a key scoped to tenant b naming tenant a; a's key naming b
		if p != operator.MachinePath+"/openapi.yaml" {
			refused(w.bob.machine(t, p+"?tenant="+w.ta.String(), bearer(keyB)), http.StatusForbidden, operator.CodeScopeForbidden, "b's key reading a "+p)
			refused(w.bob.machine(t, p+"?tenant="+w.tb.String(), bearer(keyA)), http.StatusForbidden, operator.CodeScopeForbidden, "a's key reading b "+p)
		}
		refused(w.bob.machine(t, p, bearer(revoked)), http.StatusUnauthorized, "unauthenticated", "revoked key "+p)
		refused(w.bob.machine(t, p, bearer(expired)), http.StatusUnauthorized, "unauthenticated", "expired key "+p)
		// a console session, of a tenant member and of the operator, is no key
		refused(w.bob.machine(t, p, nil), http.StatusUnauthorized, "unauthenticated", "bob's console session "+p)
		refused(w.op.machine(t, p, nil), http.StatusUnauthorized, "unauthenticated", "the operator's console session "+p)
		for name, h := range malformed {
			refused(w.bob.machine(t, p, h), http.StatusUnauthorized, "unauthenticated", name+" "+p)
		}
		// a key in the query is not a key
		refused(e.anonymous(t).machine(t, p+"?access_token="+global, nil), http.StatusUnauthorized, "unauthenticated", "key in the query "+p)
	}

	// within its scope a key answers, and only with its own tenant
	for _, p := range dataRoutes {
		r := w.bob.machine(t, p, bearer(keyB))
		require.Equal(t, http.StatusOK, r.status, "b's key %s: %s", p, r.body)
		require.Empty(t, w.leaked(r.body), "b's key %s: %s", p, r.body)
		sent++
		if p == operator.MachinePath+"/tenants" {
			// /tenants takes no tenant parameter: a scoped key lists its own
			require.Contains(t, string(r.body), w.tb.String())
		} else {
			r = w.bob.machine(t, p+"?tenant="+w.tb.String(), bearer(keyB))
			require.Equal(t, http.StatusOK, r.status, "b's key naming b %s: %s", p, r.body)
			require.Empty(t, w.leaked(r.body), "b's key naming b %s: %s", p, r.body)
			sent++
		}
		// control: tenant a's data is there for a key allowed to read it
		if p == operator.MachinePath+"/tenants" {
			r = w.bob.machine(t, p, bearer(global))
		} else {
			r = w.bob.machine(t, p+"?tenant="+w.ta.String(), bearer(global))
		}
		require.Equal(t, http.StatusOK, r.status, "control: global key %s: %s", p, r.body)
		// the hourly view reads umami live and the rule list holds no tenant
		if !strings.HasSuffix(p, "/web/hourly") && !strings.HasSuffix(p, "/rules") {
			require.Contains(t, string(r.body), w.ta.String(), "control: global key %s holds tenant a", p)
		}
		// a key for every tenant naming a tenant that does not exist
		if p != operator.MachinePath+"/tenants" {
			refused(w.bob.machine(t, p+"?tenant="+uuid.NewString(), bearer(global)), http.StatusNotFound, "not_found", "unknown tenant "+p)
		}
	}

	// a key authenticates nothing outside the machine api
	for _, p := range []string{"/api/tenants", "/api/me", "/api/operator/tenants", "/api/tenants/" + w.ta.String()} {
		r := e.anonymous(t).machine(t, p, bearer(global))
		require.Equal(t, http.StatusUnauthorized, r.status, "key on %s: %s", p, r.body)
		require.Equal(t, "unauthenticated", errCode(t, r.body))
		sent++
	}
	requireUnchanged(t, before, e.snapshot(t, w), "a refused machine request")
	_ = expiredID
	t.Logf("%d machine api requests: wrong scope, revoked, expired, session, otlp token and malformed keys refused; no answer held tenant a", sent)
}
