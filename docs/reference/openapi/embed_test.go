package openapi

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

// the document is the contract of the machine api and of its tests, so it
// must load and validate as openapi 3.1 on its own.
func TestMetricsV1Validates(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(MetricsV1)
	require.NoError(t, err)
	require.True(t, doc.IsOpenAPI31OrLater(), "the document is openapi 3.1")
	require.NoError(t, doc.Validate(loader.Context))
	for _, p := range []string{"/tenants", "/web/daily", "/web/dimensions", "/web/hourly",
		"/api/daily", "/api/routes/daily", "/api/hourly", "/openapi.yaml"} {
		item := doc.Paths.Value(p)
		require.NotNil(t, item, p)
		require.NotNil(t, item.Get, p)
		require.Len(t, item.Operations(), 1, "%s defines GET only", p)
	}
}

// the schemas are strict: the tests that validate answers against them
// would prove nothing if an unknown field or a wrong type passed.
func TestMetricsV1SchemasAreStrict(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(MetricsV1)
	require.NoError(t, err)
	day := doc.Components.Schemas["WebDaily"].Value
	visit := func(v any) error { return day.VisitJSON(v, openapi3.EnableJSONSchema2020()) }
	ok := map[string]any{
		"tenant_id": "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10", "day": "2026-11-12", "band": "primary",
		"sites": 1.0, "pageviews": 3.0, "visitors": 2.0, "visits": 2.0, "bounces": 1.0, "totaltime": 40.0,
	}
	require.NoError(t, visit(ok))
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range ok {
			m[a] = b
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	require.Error(t, visit(with("session_id", "x")), "unknown field")
	require.Error(t, visit(with("pageviews", nil)), "missing field")
	require.Error(t, visit(with("pageviews", -1.0)), "negative count")
	require.Error(t, visit(with("band", "both")), "unknown band")
	hour := doc.Components.Schemas["APIHour"].Value
	require.Error(t, hour.VisitJSON(map[string]any{"tenant_id": "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10"}, openapi3.EnableJSONSchema2020()))
}
