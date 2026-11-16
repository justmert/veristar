package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// listTools builds a tool set the way New does and lists it through a real
// sdk client over in-memory transports.
func listTools(t *testing.T, add func(*Server, *mcp.Server)) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	s := &Server{}
	srv := mcp.NewServer(&mcp.Implementation{Name: "veristar", Version: "test"}, nil)
	add(s, srv)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	return res.Tools
}

func TestToolSets(t *testing.T) {
	tenant := listTools(t, (*Server).addTenantTools)
	operator := listTools(t, (*Server).addOperatorTools)
	names := func(ts []*mcp.Tool) map[string]bool {
		m := map[string]bool{}
		for _, tl := range ts {
			m[tl.Name] = true
		}
		return m
	}
	tn, on := names(tenant), names(operator)
	for _, want := range []string{"list_projects", "list_sites", "get_site", "get_web_stats", "get_web_traffic",
		"get_web_metrics", "get_web_realtime", "get_event_stats", "get_event_series", "get_event_properties",
		"get_sessions", "get_session_stats", "list_funnels", "run_funnel", "run_journey", "run_retention", "get_goals",
		"list_api_services", "get_api_overview", "get_api_timeseries", "get_api_status_codes", "list_api_routes",
		"get_api_top_error_routes", "get_api_warnings", "get_onchain_activity", "get_scf_view"} {
		require.True(t, tn[want], want)
	}
	for _, want := range []string{"list_tenants", "get_tenant", "get_global_web_overview", "get_global_api_overview",
		"compare_tenants_web", "compare_tenants_api", "get_tenant_onchain"} {
		require.True(t, on[want], want)
		require.False(t, tn[want], "operator tool %s in the tenant set", want)
	}
	for name := range tn {
		require.False(t, on[name], "tenant tool %s in the operator set", name)
	}
	for _, tl := range append(tenant, operator...) {
		require.NotNil(t, tl.Annotations, tl.Name)
		require.True(t, tl.Annotations.ReadOnlyHint, tl.Name)
		require.NotEmpty(t, tl.Description, tl.Name)
		b, err := json.Marshal(tl.InputSchema)
		require.NoError(t, err)
		var sch map[string]any
		require.NoError(t, json.Unmarshal(b, &sch))
		require.Equal(t, "object", sch["type"], tl.Name)
		require.Equal(t, false, sch["additionalProperties"], "%s accepts unknown arguments", tl.Name)
	}
}
