//go:build isolation

package consoleisolation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/mcpserver"
)

// the mcp server (contract 12) against the real router, database, nats and
// umami, through the real go sdk client over streamable http: access keys,
// the oauth flow of the mcp authorization spec, tenant isolation of every
// tool, the operator tools, input validation and automatic revocation.

// bearerRT adds a bearer credential to every request of the sdk client.
type bearerRT struct {
	base  http.RoundTripper
	token string
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// connect opens an sdk session to /mcp with a bearer credential.
func (e *env) connect(t *testing.T, token string) (*mcp.ClientSession, error) {
	t.Helper()
	hc := &http.Client{Transport: bearerRT{base: e.srv.Client().Transport, token: token}}
	tr := &mcp.StreamableClientTransport{Endpoint: e.srv.URL + mcpserver.Path, HTTPClient: hc, DisableStandaloneSSE: true}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "isolation-test", Version: "1"}, nil).Connect(context.Background(), tr, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}
	return cs, err
}

func (e *env) mustConnect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	cs, err := e.connect(t, token)
	require.NoError(t, err)
	return cs
}

// toolResult is one call's answer: the structured content, or the error.
type toolResult struct {
	raw     string
	data    map[string]any
	isError bool
	code    string
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) toolResult {
	t.Helper()
	var res *mcp.CallToolResult
	for {
		var err error
		res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		// the per-credential limit (5 per second, burst 30, the production
		// default) is waited out; it is the only refusal that is
		if err != nil && strings.Contains(err.Error(), "Too Many Requests") {
			time.Sleep(time.Second)
			continue
		}
		require.NoError(t, err, name)
		break
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	out := toolResult{raw: text, isError: res.IsError}
	if res.IsError {
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(text), &env) == nil {
			out.code = env.Error.Code
		}
		return out
	}
	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	out.raw = string(b)
	_ = json.Unmarshal(b, &out.data)
	return out
}

func (c *client) mcpKey(t *testing.T, tenant uuid.UUID, name string) (id, key string) {
	t.Helper()
	r := c.call(t, http.MethodPost, "/api/tenants/"+tenant.String()+"/mcp-credentials", map[string]any{"name": name})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	m := r.json(t)
	require.Equal(t, "key", m["kind"])
	require.Equal(t, "tenant", m["scope"])
	require.Equal(t, "active", m["status"])
	return m["id"].(string), m["key"].(string)
}

// waitWeb waits until web tools answer for a site: the reconciler must
// have made the umami website and the user's membership first.
func waitWeb(t *testing.T, cs *mcp.ClientSession, site uuid.UUID) {
	t.Helper()
	require.Eventually(t, func() bool {
		r := call(t, cs, "get_web_stats", map[string]any{"site_id": site.String()})
		return !r.isError
	}, 2*time.Minute, time.Second, "web tools never answered for site %s", site)
}

// rawMCP posts one json-rpc message with the given headers and returns the status.
func (e *env) rawMCP(t *testing.T, headers map[string]string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+mcpserver.Path,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func TestMCP(t *testing.T) {
	e := start(t)
	alice := e.signIn(t, "mcp-alice")
	bob := e.signIn(t, "mcp-bob")
	ta := alice.enroll(t, "MCP Alpha")
	tb := bob.enroll(t, "MCP Beta")
	sa := alice.addSite(t, ta, "mcp-a.example.org")
	sb := bob.addSite(t, tb, "mcp-b.example.org")
	// api rows of both tenants, so a leak of tenant b into tenant a's
	// answers would show b's service name
	e.superuser(t, func(c *pgx.Conn) {
		counts := []int64{0, 1, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		for tenant, svc := range map[uuid.UUID]string{ta: "alpha-svc", tb: "beta-secret-svc"} {
			for _, q := range []string{
				`insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
				 values ($1, date_trunc('hour', now(), 'UTC') - interval '1 hour', $2, 'GET', '/v1/x/{id}', 500, 'metric', $3, 11.5, 4)`,
				`insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count)
				 values ($1, date_trunc('day', now() - interval '1 hour', 'UTC'), $2, 'GET', '/v1/x/{id}', 500, 'metric', $3, 11.5, 4)`,
			} {
				_, err := c.Exec(context.Background(), q, tenant, svc, counts)
				require.NoError(t, err)
			}
		}
	})
	t.Run("Keys", func(t *testing.T) { testMCPKeys(t, e, alice, bob, ta, tb, sa, sb) })
	t.Run("Isolation", func(t *testing.T) { testMCPIsolation(t, e, alice, bob, ta, tb, sa, sb) })
	t.Run("Validation", func(t *testing.T) { testMCPValidation(t, e, alice, ta, sa) })
	t.Run("Transport", func(t *testing.T) { testMCPTransport(t, e, alice, ta) })
	t.Run("Operator", func(t *testing.T) { testMCPOperator(t, e, alice, ta, tb) })
	t.Run("OAuthSDKClient", func(t *testing.T) { testMCPOAuthSDK(t, e, alice, ta, sa, sb) })
	t.Run("OAuthFlow", func(t *testing.T) { testMCPOAuthFlow(t, e, alice, bob, ta, tb) })
	t.Run("Revocation", func(t *testing.T) { testMCPRevocation(t, e) })
}

func testMCPKeys(t *testing.T, e *env, alice, bob *client, ta, tb, sa, sb uuid.UUID) {
	base := "/api/tenants/" + ta.String() + "/mcp-credentials"
	// field rules
	r := alice.call(t, http.MethodPost, base, map[string]any{"name": " "})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "required", r.fields(t)["name"])
	r = alice.call(t, http.MethodPost, base, map[string]any{"name": "x", "expires_in_days": 366})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, "invalid_value", r.fields(t)["expires_in_days"])
	r = alice.call(t, http.MethodPost, base, map[string]any{"name": "x", "scope": "operator"})
	require.Equal(t, http.StatusBadRequest, r.status, "unknown fields are refused")

	id, key := alice.mcpKey(t, ta, "laptop")
	require.True(t, strings.HasPrefix(key, mcpserver.KeyPrefix))
	require.Len(t, key, len(mcpserver.KeyPrefix)+40)
	require.Equal(t, 1, e.auditCount(t, "mcp_credential.create", "mcp_credential:"+id))
	var hash []byte
	e.superuser(t, func(c *pgx.Conn) {
		require.NoError(t, c.QueryRow(context.Background(), `select hash from cp.mcp_credentials where id = $1`, id).Scan(&hash))
	})
	sum := sha256.Sum256([]byte(key))
	require.Equal(t, sum[:], hash, "only the sha-256 is stored")

	// the list never shows the key, and another tenant's member gets 404
	r = alice.call(t, http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, r.status)
	require.NotContains(t, string(r.body), key)
	require.Contains(t, string(r.body), key[:11])
	require.Equal(t, http.StatusNotFound, bob.call(t, http.MethodGet, base, nil).status)
	require.Equal(t, http.StatusNotFound, bob.call(t, http.MethodDelete, base+"/"+id, nil).status)
	require.Equal(t, http.StatusNotFound, bob.call(t, http.MethodDelete, "/api/tenants/"+tb.String()+"/mcp-credentials/"+id, nil).status)

	cs := e.mustConnect(t, key)
	tools, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
		require.True(t, tl.Annotations.ReadOnlyHint, tl.Name)
	}
	require.True(t, names["get_web_stats"] && names["list_api_routes"])
	require.False(t, names["list_tenants"], "a tenant key lists no operator tool")

	res := call(t, cs, "list_projects", nil)
	require.False(t, res.isError, res.raw)
	require.Contains(t, res.raw, ta.String())
	waitWeb(t, cs, sa)
	res = call(t, cs, "get_web_stats", map[string]any{"site_id": sa.String(), "from": time.Now().UTC().Format(time.DateOnly)})
	require.False(t, res.isError, res.raw)
	require.Equal(t, "primary", res.data["site"].(map[string]any)["view"])
	res = call(t, cs, "list_api_services", map[string]any{"range": "24h"})
	require.False(t, res.isError, res.raw)
	require.Contains(t, res.raw, "alpha-svc")
	require.Contains(t, res.raw, "estimated from histogram buckets")

	// every call is audited without its result
	n := e.auditCount(t, "mcp.tool_call", "tenant:"+ta.String())
	require.GreaterOrEqual(t, n, 3)
	e.superuser(t, func(c *pgx.Conn) {
		var detail string
		require.NoError(t, c.QueryRow(context.Background(), `select detail::text from cp.audit_log
			where action = 'mcp.tool_call' and detail->>'tool' = 'list_api_services' order by id desc limit 1`).Scan(&detail))
		require.Contains(t, detail, `"outcome": "ok"`)
		require.NotContains(t, detail, "alpha-svc", "the audit row holds no result data")
		var seen *time.Time
		require.NoError(t, c.QueryRow(context.Background(), `select last_seen_at from cp.mcp_credentials where id = $1`, id).Scan(&seen))
		require.NotNil(t, seen)
	})

	// revoked: the very next request is refused
	require.Equal(t, http.StatusNoContent, alice.call(t, http.MethodDelete, base+"/"+id, nil).status)
	require.Equal(t, http.StatusNoContent, alice.call(t, http.MethodDelete, base+"/"+id, nil).status, "revoking twice changes nothing")
	require.Equal(t, 1, e.auditCount(t, "mcp_credential.revoke", "mcp_credential:"+id))
	status, hdr, _ := e.rawMCP(t, map[string]string{"Authorization": "Bearer " + key})
	require.Equal(t, http.StatusUnauthorized, status)
	require.Contains(t, hdr.Get("WWW-Authenticate"), `error="invalid_token"`)
	_, err = e.connect(t, key)
	require.Error(t, err)
}

func testMCPIsolation(t *testing.T, e *env, alice, bob *client, ta, tb, sa, sb uuid.UUID) {
	_, keyA := alice.mcpKey(t, ta, "iso-a")
	_, keyB := bob.mcpKey(t, tb, "iso-b")
	a, b := e.mustConnect(t, keyA), e.mustConnect(t, keyB)
	waitWeb(t, b, sb)
	markersB := []string{tb.String(), sb.String(), "mcp-b.example.org", "beta-secret-svc", "MCP Beta"}
	leaks := func(raw string) []string {
		var hit []string
		for _, m := range markersB {
			if strings.Contains(raw, m) {
				hit = append(hit, m)
			}
		}
		return hit
	}
	// every tool with a site id, given tenant b's site through tenant a's key
	siteTools := []struct {
		name  string
		extra map[string]any
	}{
		{"get_site", nil}, {"get_web_stats", nil}, {"get_web_traffic", nil},
		{"get_web_metrics", map[string]any{"type": "path"}}, {"get_web_realtime", nil},
		{"get_event_stats", nil}, {"get_event_series", nil}, {"get_event_properties", nil},
		{"get_sessions", nil}, {"get_session_stats", nil}, {"list_funnels", nil},
		{"run_funnel", map[string]any{"steps": []map[string]any{{"type": "path", "value": "/"}, {"type": "path", "value": "/b"}}}},
		{"run_journey", nil}, {"run_retention", nil}, {"get_goals", nil},
	}
	for _, tl := range siteTools {
		for _, view := range []string{"", "quarantine"} {
			args := map[string]any{"site_id": sb.String()}
			if view != "" && tl.name != "get_site" {
				args["view"] = view
			}
			for k, v := range tl.extra {
				args[k] = v
			}
			r := call(t, a, tl.name, args)
			require.True(t, r.isError, "%s with tenant b's site: %s", tl.name, r.raw)
			require.Equal(t, "not_found", r.code, "%s: %s", tl.name, r.raw)
			require.Empty(t, leaks(r.raw), "%s: %s", tl.name, r.raw)
			// a guessed id answers the same
			args["site_id"] = uuid.NewString()
			r = call(t, a, tl.name, args)
			require.Equal(t, "not_found", r.code, "%s guessed: %s", tl.name, r.raw)
		}
	}
	// the tenant-wide tools answer only with tenant a
	for _, tl := range []struct {
		name string
		args map[string]any
	}{
		{"list_projects", nil}, {"list_sites", nil},
		{"list_api_services", map[string]any{"range": "7d"}}, {"get_api_overview", map[string]any{"range": "7d"}},
		{"get_api_timeseries", map[string]any{"range": "7d"}}, {"get_api_status_codes", map[string]any{"range": "7d"}},
		{"list_api_routes", map[string]any{"range": "7d"}}, {"get_api_top_error_routes", map[string]any{"range": "7d"}},
		{"get_api_warnings", map[string]any{"range": "7d"}},
		// a filter naming tenant b's service matches nothing of tenant a
		{"get_api_overview", map[string]any{"range": "7d", "service": "beta-secret-svc"}},
	} {
		r := call(t, a, tl.name, tl.args)
		require.False(t, r.isError, "%s: %s", tl.name, r.raw)
		require.Empty(t, leaks(r.raw), "%s leaked tenant b: %s", tl.name, r.raw)
	}
	r := call(t, a, "get_api_overview", map[string]any{"range": "7d", "service": "beta-secret-svc"})
	require.EqualValues(t, 0, r.data["current"].(map[string]any)["summary"].(map[string]any)["requests"])
	// control: tenant b's own key sees its data
	r = call(t, b, "list_api_services", map[string]any{"range": "7d"})
	require.Contains(t, r.raw, "beta-secret-svc")
	r = call(t, b, "get_web_stats", map[string]any{"site_id": sb.String()})
	require.False(t, r.isError, r.raw)

	// operator tools do not exist for a tenant key, also for an operator's
	_, err := a.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_tenants", Arguments: map[string]any{}})
	require.Error(t, err)
	op := e.signInAs(t, operatorGHID, "mcp-operator")
	tOp := op.enroll(t, "MCP Operator Project")
	_, keyOp := op.mcpKey(t, tOp, "operator's tenant key")
	opTenant := e.mustConnect(t, keyOp)
	_, err = opTenant.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_global_web_overview", Arguments: map[string]any{}})
	require.Error(t, err, "an operator's tenant key reads one tenant only")
	r = call(t, opTenant, "list_projects", nil)
	require.NotContains(t, r.raw, ta.String())
}

func testMCPValidation(t *testing.T, e *env, alice *client, ta, sa uuid.UUID) {
	_, key := alice.mcpKey(t, ta, "validation")
	cs := e.mustConnect(t, key)
	bad := []struct {
		name string
		args map[string]any
	}{
		{"get_web_metrics", map[string]any{"site_id": sa.String(), "type": "bogus"}},
		{"get_web_metrics", map[string]any{"site_id": sa.String(), "type": "path", "limit": 501}},
		{"get_web_stats", map[string]any{"site_id": sa.String(), "from": "yesterday"}},
		{"get_web_stats", map[string]any{"site_id": sa.String(), "from": "2024-01-01", "to": "2026-01-01"}},
		{"get_web_stats", map[string]any{"site_id": sa.String(), "unknown": 1}},
		{"get_web_stats", map[string]any{"site_id": "not-a-uuid"}},
		{"get_web_stats", map[string]any{"site_id": strings.ToUpper(sa.String())}},
		{"get_web_stats", map[string]any{"site_id": sa.String(), "view": "everything"}},
		{"get_web_stats", map[string]any{"site_id": sa.String(), "timezone": "Mars/Base"}},
		{"get_web_stats", map[string]any{"site_id": sa.String(), "filters": map[string]any{"event": "x"}}},
		{"get_web_traffic", map[string]any{"site_id": sa.String(), "unit": "minute", "from": "2026-01-01", "to": "2026-03-01"}},
		{"get_sessions", map[string]any{"site_id": sa.String(), "page_size": 101}},
		{"run_funnel", map[string]any{"site_id": sa.String()}},
		{"run_funnel", map[string]any{"site_id": sa.String(), "steps": []map[string]any{{"type": "path", "value": "/"}}}},
		{"run_funnel", map[string]any{"site_id": sa.String(), "funnel_id": uuid.NewString(),
			"steps": []map[string]any{{"type": "path", "value": "/"}, {"type": "path", "value": "/b"}}}},
		{"get_api_overview", map[string]any{"range": "custom"}},
		{"get_api_overview", map[string]any{"range": "5y"}},
		{"get_api_overview", map[string]any{"range": "24h", "from": "2026-01-01"}},
		{"list_api_routes", map[string]any{"sort": "password"}},
		{"list_api_routes", map[string]any{"cursor": "forged"}},
		{"get_api_overview", map[string]any{"service": "a\u0001b"}},
	}
	for _, c := range bad {
		r := call(t, cs, c.name, c.args)
		require.True(t, r.isError, "%s %v: %s", c.name, c.args, r.raw)
		require.NotContains(t, r.raw, "goroutine", "no stack traces")
		require.NotContains(t, r.raw, ".go:", "no source locations")
	}
	// a saved funnel id that is not the site's is refused by the control plane
	r := call(t, cs, "run_funnel", map[string]any{"site_id": sa.String(), "funnel_id": uuid.NewString()})
	require.Equal(t, "not_found", r.code, r.raw)
}

func testMCPTransport(t *testing.T, e *env, alice *client, ta uuid.UUID) {
	_, key := alice.mcpKey(t, ta, "transport")
	for name, h := range map[string]map[string]string{
		"no credential":    {},
		"session cookie":   {"Cookie": "sa_session=whatever"},
		"wrong scheme":     {"Authorization": "Basic " + key},
		"short key":        {"Authorization": "Bearer sa_mcp_short"},
		"guessed key":      {"Authorization": "Bearer sa_mcp_" + strings.Repeat("A", 40)},
		"operator api key": {"Authorization": "Bearer sa_op_" + strings.Repeat("A", 40)},
		"key plus char":    {"Authorization": "Bearer " + key + "x"},
	} {
		status, hdr, body := e.rawMCP(t, h)
		require.Equal(t, http.StatusUnauthorized, status, "%s: %s", name, body)
		require.Contains(t, hdr.Get("WWW-Authenticate"), `resource_metadata="`+e.srv.URL+`/.well-known/oauth-protected-resource/mcp"`, name)
	}
	// a browser origin is refused even with a valid key (dns rebinding)
	status, _, _ := e.rawMCP(t, map[string]string{"Authorization": "Bearer " + key, "Origin": "https://evil.example"})
	require.Equal(t, http.StatusForbidden, status)
	status, _, body := e.rawMCP(t, map[string]string{"Authorization": "Bearer " + key})
	require.Equal(t, http.StatusOK, status, body)
	// the key authenticates nothing outside /mcp
	for _, p := range []string{"/api/tenants", "/api/me", "/api/metrics/v1/tenants"} {
		req, err := http.NewRequest(http.MethodGet, e.srv.URL+p, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := e.srv.Client().Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, p)
	}
	// get and delete: the stateless server offers no stream and no session
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req, err := http.NewRequest(m, e.srv.URL+mcpserver.Path, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := e.srv.Client().Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode, m)
	}
	// metadata documents
	var prm map[string]any
	getJSON(t, e, "/.well-known/oauth-protected-resource/mcp", &prm)
	require.Equal(t, e.mcp.Resource(), prm["resource"])
	require.Equal(t, []any{e.srv.URL}, prm["authorization_servers"])
	var asm map[string]any
	getJSON(t, e, "/.well-known/oauth-authorization-server", &asm)
	require.Equal(t, e.srv.URL, asm["issuer"])
	require.Equal(t, []any{"S256"}, asm["code_challenge_methods_supported"])
	require.Equal(t, []any{"none"}, asm["token_endpoint_auth_methods_supported"])
}

func getJSON(t *testing.T, e *env, path string, out any) {
	t.Helper()
	res, err := e.srv.Client().Get(e.srv.URL + path)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, http.StatusOK, res.StatusCode, path)
	require.NoError(t, json.NewDecoder(res.Body).Decode(out))
}

func testMCPOperator(t *testing.T, e *env, alice *client, ta, tb uuid.UUID) {
	op := e.signInAs(t, operatorGHID, "mcp-operator")
	// only operators reach the operator key routes
	r := alice.call(t, http.MethodPost, "/api/operator/mcp-credentials", map[string]any{"name": "x"})
	require.Equal(t, http.StatusForbidden, r.status)
	r = op.call(t, http.MethodPost, "/api/operator/mcp-credentials", map[string]any{"name": "global view"})
	require.Equal(t, http.StatusCreated, r.status, string(r.body))
	opID, opKey := r.json(t)["id"].(string), r.json(t)["key"].(string)
	require.Equal(t, "operator", r.json(t)["scope"])
	cs := e.mustConnect(t, opKey)
	tools, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	require.True(t, names["list_tenants"] && names["compare_tenants_api"])
	require.False(t, names["get_web_stats"], "operator keys read aggregates only")

	res := call(t, cs, "list_tenants", map[string]any{"q": "MCP"})
	require.False(t, res.isError, res.raw)
	require.Contains(t, res.raw, ta.String())
	require.Contains(t, res.raw, tb.String())
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"get_tenant", map[string]any{"tenant_id": ta.String()}},
		{"get_global_web_overview", map[string]any{"sort": "pageviews"}},
		{"get_global_api_overview", map[string]any{"sort": "requests", "order": "desc"}},
		{"compare_tenants_web", map[string]any{"tenant_ids": []string{ta.String(), tb.String()}}},
		{"compare_tenants_api", map[string]any{"tenant_ids": []string{ta.String(), tb.String()}}},
	} {
		r := call(t, cs, c.name, c.args)
		require.False(t, r.isError, "%s: %s", c.name, r.raw)
	}
	res = call(t, cs, "compare_tenants_api", map[string]any{"tenant_ids": []string{ta.String(), tb.String()}})
	require.Contains(t, res.raw, "beta-secret-svc")
	res = call(t, cs, "get_tenant", map[string]any{"tenant_id": uuid.NewString()})
	require.Equal(t, "not_found", res.code)
	res = call(t, cs, "compare_tenants_web", map[string]any{"tenant_ids": []string{}})
	require.True(t, res.isError)

	// alice cannot revoke it, the operator can; operator flag loss revokes
	require.Equal(t, http.StatusForbidden, alice.call(t, http.MethodDelete, "/api/operator/mcp-credentials/"+opID, nil).status)
	r = op.call(t, http.MethodPost, "/api/operator/mcp-credentials", map[string]any{"name": "second"})
	require.Equal(t, http.StatusCreated, r.status)
	secondKey := r.json(t)["key"].(string)
	require.Equal(t, http.StatusNoContent, op.call(t, http.MethodDelete, "/api/operator/mcp-credentials/"+opID, nil).status)
	_, err = e.connect(t, opKey)
	require.Error(t, err)
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.users set is_operator = false where id = $1`, op.userID)
		require.NoError(t, err)
		var reason string
		require.NoError(t, c.QueryRow(context.Background(), `select revoked_reason from cp.mcp_credentials
			where scope = 'operator' and user_id = $1 and name = 'second'`, op.userID).Scan(&reason))
		require.Equal(t, "operator_removed", reason)
		_, err = c.Exec(context.Background(), `update cp.users set is_operator = true where id = $1`, op.userID)
		require.NoError(t, err)
	})
	_, err = e.connect(t, secondKey)
	require.Error(t, err, "a restored flag does not restore a revoked credential")
}

// pkce returns a verifier and its s256 challenge.
func pkce(t *testing.T) (string, string) {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	v := base64.RawURLEncoding.EncodeToString(b)
	s := sha256.Sum256([]byte(v))
	return v, base64.RawURLEncoding.EncodeToString(s[:])
}

const testRedirect = "http://127.0.0.1:65530/callback"

func (e *env) registerClient(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	res, err := e.srv.Client().Post(e.srv.URL+mcpserver.RegisterPath, "application/json", strings.NewReader(string(b)))
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	var m map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&m))
	return res.StatusCode, m
}

// authorize runs the authorize endpoint and returns the consent page's
// sealed request, or the redirect for an error.
func (e *env) authorize(t *testing.T, q url.Values) *url.URL {
	t.Helper()
	hc := *e.srv.Client()
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := hc.Get(e.srv.URL + mcpserver.AuthorizePath + "?" + q.Encode())
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusSeeOther, res.StatusCode)
	u, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	return u
}

// consent approves a sealed request as the signed-in client and returns
// the redirect the page would follow.
func (c *client) consent(t *testing.T, request, scope string, tenant *uuid.UUID) resp {
	t.Helper()
	body := map[string]any{"request": request, "scope": scope}
	if tenant != nil {
		body["tenant_id"] = tenant.String()
	}
	return c.call(t, http.MethodPost, "/api/mcp/authorize/approve", body)
}

func (e *env) tokenRequest(t *testing.T, form url.Values) (int, map[string]any) {
	t.Helper()
	res, err := e.srv.Client().PostForm(e.srv.URL+mcpserver.TokenPath, form)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	var m map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&m))
	require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
	return res.StatusCode, m
}

// grant runs the whole flow by hand for c and returns the token answer.
func (e *env) grant(t *testing.T, c *client, clientID, scope string, tenant *uuid.UUID) map[string]any {
	t.Helper()
	verifier, challenge := pkce(t)
	u := e.authorize(t, url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {testRedirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st-1"}, "resource": {e.mcp.Resource()}})
	require.Equal(t, mcpserver.ConsentPath, u.Path)
	r := c.consent(t, u.Query().Get("request"), scope, tenant)
	require.Equal(t, http.StatusOK, r.status, string(r.body))
	back, err := url.Parse(r.json(t)["redirect_to"].(string))
	require.NoError(t, err)
	require.Equal(t, "st-1", back.Query().Get("state"))
	require.Equal(t, e.srv.URL, back.Query().Get("iss"))
	status, tok := e.tokenRequest(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID},
		"code": {back.Query().Get("code")}, "redirect_uri": {testRedirect}, "code_verifier": {verifier}, "resource": {e.mcp.Resource()}})
	require.Equal(t, http.StatusOK, status, tok)
	return tok
}

// testMCPOAuthSDK runs the mcp authorization flow with the go sdk's own
// client handler: discovery from the 401, dynamic registration, pkce, the
// consent page approved by the signed-in user, the token request, and the
// retried tool call.
func testMCPOAuthSDK(t *testing.T, e *env, alice *client, ta, sa, sb uuid.UUID) {
	ctx := context.Background()
	fetched := 0
	h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "sdk test client", RedirectURIs: []string{testRedirect},
			GrantTypes: []string{"authorization_code", "refresh_token"}, TokenEndpointAuthMethod: "none",
		}},
		RedirectURL: testRedirect,
		Client:      e.srv.Client(),
		AuthorizationCodeFetcher: func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			fetched++
			au, err := url.Parse(args.URL)
			require.NoError(t, err)
			require.Equal(t, "S256", au.Query().Get("code_challenge_method"))
			u := e.authorize(t, au.Query())
			require.Equal(t, mcpserver.ConsentPath, u.Path, u.String())
			// the page's inspect call shows the client and alice's projects
			r := alice.call(t, http.MethodPost, "/api/mcp/authorize/inspect", map[string]any{"request": u.Query().Get("request")})
			require.Equal(t, http.StatusOK, r.status, string(r.body))
			require.Equal(t, "sdk test client", r.json(t)["client_name"])
			require.Contains(t, string(r.body), ta.String())
			require.Equal(t, false, r.json(t)["operator"])
			r = alice.consent(t, u.Query().Get("request"), "tenant", &ta)
			require.Equal(t, http.StatusOK, r.status, string(r.body))
			back, err := url.Parse(r.json(t)["redirect_to"].(string))
			require.NoError(t, err)
			return &auth.AuthorizationResult{Code: back.Query().Get("code"), State: back.Query().Get("state"), Iss: back.Query().Get("iss")}, nil
		},
	})
	require.NoError(t, err)
	tr := &mcp.StreamableClientTransport{Endpoint: e.srv.URL + mcpserver.Path, HTTPClient: e.srv.Client(), OAuthHandler: h, DisableStandaloneSSE: true}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "sdk-oauth", Version: "1"}, nil).Connect(ctx, tr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	require.Equal(t, 1, fetched)
	r := call(t, cs, "list_projects", nil)
	require.False(t, r.isError, r.raw)
	require.Contains(t, r.raw, ta.String())
	r = call(t, cs, "get_site", map[string]any{"site_id": sb.String()})
	require.Equal(t, "not_found", r.code, "an oauth token of tenant a never reads tenant b")
	r = call(t, cs, "get_site", map[string]any{"site_id": sa.String()})
	require.False(t, r.isError, r.raw)
	// the grant is listed on the project's page as an oauth credential
	lr := alice.call(t, http.MethodGet, "/api/tenants/"+ta.String()+"/mcp-credentials", nil)
	require.Contains(t, string(lr.body), `"kind":"oauth"`)
	require.Contains(t, string(lr.body), "sdk test client")
}

func testMCPOAuthFlow(t *testing.T, e *env, alice, bob *client, ta, tb uuid.UUID) {
	// registration rules
	status, m := e.registerClient(t, map[string]any{"redirect_uris": []string{"http://evil.example/cb"}})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_redirect_uri", m["error"])
	status, m = e.registerClient(t, map[string]any{"redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "client_secret_basic"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_client_metadata", m["error"])
	status, m = e.registerClient(t, map[string]any{"redirect_uris": []string{testRedirect}, "client_name": "flow client", "software_id": "ignored"})
	require.Equal(t, http.StatusCreated, status, m)
	clientID := m["client_id"].(string)
	require.Equal(t, "none", m["token_endpoint_auth_method"])

	_, challenge := pkce(t)
	base := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {testRedirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"s"}}
	with := func(k, v string) url.Values {
		q := url.Values{}
		for kk, vv := range base {
			q[kk] = vv
		}
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
		return q
	}
	// errors before the client is trusted go to the console page
	u := e.authorize(t, with("client_id", "mcpc_"+strings.Repeat("A", 32)))
	require.Equal(t, mcpserver.ConsentPath, u.Path)
	require.Equal(t, "invalid_client", u.Query().Get("error"))
	u = e.authorize(t, with("redirect_uri", "http://127.0.0.1:1/other"))
	require.Equal(t, "invalid_request", u.Query().Get("error"))
	// errors after go to the client's redirect uri with state and iss
	for k, v := range map[string]string{"code_challenge_method": "plain", "code_challenge": "", "response_type": "token",
		"resource": "https://other.example/mcp", "scope": "admin"} {
		u = e.authorize(t, with(k, v))
		require.Equal(t, "127.0.0.1:65530", u.Host, k)
		require.NotEmpty(t, u.Query().Get("error"), k)
		require.Equal(t, "s", u.Query().Get("state"), k)
	}

	// consent rules: only one's own projects, operator view for operators
	u = e.authorize(t, base)
	request := u.Query().Get("request")
	r := alice.consent(t, request, "tenant", &tb)
	require.Equal(t, http.StatusNotFound, r.status, "a project the user is not a member of")
	r = alice.consent(t, request, "operator", nil)
	require.Equal(t, http.StatusForbidden, r.status)
	r = alice.consent(t, request+"x", "tenant", &ta)
	require.Equal(t, http.StatusUnprocessableEntity, r.status, "a tampered request")
	r = alice.call(t, http.MethodPost, "/api/mcp/authorize/deny", map[string]any{"request": request})
	require.Equal(t, http.StatusOK, r.status)
	require.Contains(t, r.json(t)["redirect_to"], "error=access_denied")
	require.Equal(t, http.StatusUnauthorized, e.anonymous(t).call(t, http.MethodPost, "/api/mcp/authorize/inspect", map[string]any{"request": request}).status)

	// pkce failure, redirect mismatch and code reuse
	verifier, challenge := pkce(t)
	u = e.authorize(t, with("code_challenge", challenge))
	r = alice.consent(t, u.Query().Get("request"), "tenant", &ta)
	back, err := url.Parse(r.json(t)["redirect_to"].(string))
	require.NoError(t, err)
	code := back.Query().Get("code")
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {testRedirect}}
	wrong, _ := pkce(t)
	form.Set("code_verifier", wrong)
	status, m = e.tokenRequest(t, form)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_grant", m["error"])
	require.Contains(t, m["error_description"], "code_verifier")
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", "http://127.0.0.1:1/other")
	_, m = e.tokenRequest(t, form)
	require.Equal(t, "invalid_grant", m["error"])
	form.Set("redirect_uri", testRedirect)
	status, tok := e.tokenRequest(t, form)
	require.Equal(t, http.StatusOK, status, tok)
	require.Equal(t, "Bearer", tok["token_type"])
	require.EqualValues(t, 3600, tok["expires_in"])
	access := tok["access_token"].(string)
	require.True(t, strings.HasPrefix(access, mcpserver.AccessTokenPrefix))
	e.mustConnect(t, access)
	_, m = e.tokenRequest(t, form)
	require.Equal(t, "invalid_grant", m["error"], "a code is used once")
	_, err = e.connect(t, access)
	require.Error(t, err, "a second use of the code revokes what the first issued")

	// refresh rotation and reuse detection
	tok = e.grant(t, alice, clientID, "tenant", &ta)
	refresh := tok["refresh_token"].(string)
	status, next := e.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
	require.Equal(t, http.StatusOK, status, next)
	require.NotEqual(t, refresh, next["refresh_token"])
	e.mustConnect(t, next["access_token"].(string))
	_, m = e.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
	require.Equal(t, "invalid_grant", m["error"])
	_, err = e.connect(t, next["access_token"].(string))
	require.Error(t, err, "a replayed refresh token revokes the grant")
	_, m = e.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {next["refresh_token"].(string)}})
	require.Equal(t, "invalid_grant", m["error"])

	// another client cannot refresh or revoke it
	_, m2 := e.registerClient(t, map[string]any{"redirect_uris": []string{testRedirect}})
	other := m2["client_id"].(string)
	tok = e.grant(t, alice, clientID, "tenant", &ta)
	_, m = e.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {other}, "refresh_token": {tok["refresh_token"].(string)}})
	require.Equal(t, "invalid_grant", m["error"])

	// revocation (rfc 7009) of the refresh token ends the access token too
	res, err := e.srv.Client().PostForm(e.srv.URL+mcpserver.RevokePath, url.Values{"client_id": {clientID}, "token": {tok["refresh_token"].(string)}})
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	_, err = e.connect(t, tok["access_token"].(string))
	require.Error(t, err)

	// an expired access token is refused
	tok = e.grant(t, bob, clientID, "tenant", &tb)
	sum := sha256.Sum256([]byte(tok["access_token"].(string)))
	e.superuser(t, func(c *pgx.Conn) {
		_, err := c.Exec(context.Background(), `update cp.mcp_oauth_tokens set expires_at = now() - interval '1 second' where hash = $1`, sum[:])
		require.NoError(t, err)
	})
	_, err = e.connect(t, tok["access_token"].(string))
	require.Error(t, err)
	status, _ = e.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {tok["refresh_token"].(string)}})
	require.Equal(t, http.StatusOK, status, "the refresh token still works")

	// an operator grant opens the operator tools
	op := e.signInAs(t, operatorGHID, "mcp-operator")
	tok = e.grant(t, op, clientID, "operator", nil)
	cs := e.mustConnect(t, tok["access_token"].(string))
	rr := call(t, cs, "list_tenants", nil)
	require.False(t, rr.isError, rr.raw)
}

// testMCPRevocation: removing a member, suspending, rejecting or removing a
// tenant revokes its keys and grants, in the database and on the next
// request.
func testMCPRevocation(t *testing.T, e *env) {
	op := e.signInAs(t, operatorGHID, "mcp-operator")
	owner := e.signIn(t, "rv-owner")
	member := e.signIn(t, "rv-member")
	tenant := owner.enroll(t, "MCP Revocation")
	tp := "/api/tenants/" + tenant.String()
	require.Equal(t, http.StatusCreated, owner.call(t, http.MethodPost, tp+"/members", map[string]any{"github_login": "rv-member", "role": "member"}).status)
	_, ownerKey := owner.mcpKey(t, tenant, "owner key")
	memberKeyID, memberKey := member.mcpKey(t, tenant, "member key")
	_, m := e.registerClient(t, map[string]any{"redirect_uris": []string{testRedirect}})
	clientID := m["client_id"].(string)
	tok := e.grant(t, member, clientID, "tenant", &tenant)
	e.mustConnect(t, memberKey)
	e.mustConnect(t, tok["access_token"].(string))

	// a member revokes only its own keys; the operator any
	r := member.call(t, http.MethodGet, tp+"/mcp-credentials", nil)
	var ownerKeyID string
	for _, it := range r.json(t)["items"].([]any) {
		if it.(map[string]any)["name"] == "owner key" {
			ownerKeyID = it.(map[string]any)["id"].(string)
		}
	}
	require.NotEmpty(t, ownerKeyID)
	r = member.call(t, http.MethodDelete, tp+"/mcp-credentials/"+ownerKeyID, nil)
	require.Equal(t, http.StatusForbidden, r.status)
	require.Equal(t, "owner_required", r.code(t))

	// member removal
	require.Equal(t, http.StatusNoContent, owner.call(t, http.MethodDelete, tp+"/members/"+member.userID.String(), nil).status)
	_, err := e.connect(t, memberKey)
	require.Error(t, err)
	_, err = e.connect(t, tok["access_token"].(string))
	require.Error(t, err)
	status, mm := e.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {tok["refresh_token"].(string)}})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_grant", mm["error"])
	e.superuser(t, func(c *pgx.Conn) {
		rows, err := c.Query(context.Background(), `select revoked_reason from cp.mcp_credentials where user_id = $1 and tenant_id = $2`,
			member.userID, tenant)
		require.NoError(t, err)
		n := 0
		for rows.Next() {
			var reason *string
			require.NoError(t, rows.Scan(&reason))
			require.NotNil(t, reason)
			require.Equal(t, "member_removed", *reason)
			n++
		}
		require.NoError(t, rows.Err())
		require.Equal(t, 2, n, "the key and the oauth grant")
	})
	require.Equal(t, 1, e.auditCount(t, "mcp_credential.revoke", "mcp_credential:"+memberKeyID))
	e.mustConnect(t, ownerKey)

	// suspension, then a resumed tenant keeps its keys revoked
	reason := map[string]any{"reason": "mcp revocation test"}
	require.Equal(t, http.StatusOK, op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/suspend", reason).status)
	_, err = e.connect(t, ownerKey)
	require.Error(t, err)
	require.Equal(t, http.StatusOK, op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/resume", reason).status)
	_, err = e.connect(t, ownerKey)
	require.Error(t, err, "resuming does not restore revoked credentials")
	// keys cannot be made while suspended
	require.Equal(t, http.StatusOK, op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/suspend", reason).status)
	r = owner.call(t, http.MethodPost, tp+"/mcp-credentials", map[string]any{"name": "while suspended"})
	require.Equal(t, http.StatusConflict, r.status)
	require.Equal(t, "tenant_suspended", r.code(t))
	require.Equal(t, http.StatusOK, op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/resume", reason).status)

	// removal
	_, again := owner.mcpKey(t, tenant, "before removal")
	e.mustConnect(t, again)
	require.Equal(t, http.StatusOK, op.call(t, http.MethodPost, "/api/operator/tenants/"+tenant.String()+"/remove", reason).status)
	_, err = e.connect(t, again)
	require.Error(t, err)
	e.superuser(t, func(c *pgx.Conn) {
		var n int
		require.NoError(t, c.QueryRow(context.Background(), `select count(*) from cp.mcp_credentials
			where tenant_id = $1 and revoked_at is null`, tenant).Scan(&n))
		require.Zero(t, n)
		require.NoError(t, c.QueryRow(context.Background(), `select count(*) from cp.mcp_credentials
			where tenant_id = $1 and revoked_reason in ('tenant_suspended', 'tenant_removed')`, tenant).Scan(&n))
		require.Equal(t, 2, n)
	})
}
