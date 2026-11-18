package apimetrics_test

import (
	"math"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/apimetrics"
)

// TestRangeSumsPastBigint: a tenant whose request counts over a range add
// up past bigint still reads every route. the counts saturate at the
// largest bigint instead of failing the query with 22003, and the
// percentiles keep their bucket.
func TestRangeSumsPastBigint(t *testing.T) {
	e := start(t)
	user, tenant := e.member(t, "active")
	const big = int64(6_000_000_000_000_000_000)
	var ps []point
	for _, day := range []string{"2031-06-01T10:00:00Z", "2031-06-02T10:00:00Z"} {
		ps = append(ps, point{tenant: tenant, at: at(day), service: "svc", method: "GET", route: "/a",
			status: 200, source: "metric", counts: h(map[int]int64{3: big})})
	}
	e.write(t, ps, nil)
	c := e.client(t, user)
	base := "/tenants/" + tenant.String() + "/api-metrics/"
	qs := "?range=custom&from=2031-05-01T00:00:00Z&to=2031-06-10T00:00:00Z"

	var ov apimetrics.Overview
	c.ok(t, base+"overview"+qs, &ov)
	require.Equal(t, apimetrics.Day, ov.Current.Range.Resolution)
	require.Equal(t, int64(math.MaxInt64), ov.Current.Summary.Requests)
	p50 := ov.Current.Summary.P50
	require.True(t, p50.Valid)
	require.Greater(t, p50.Seconds, 0.025)
	require.LessOrEqual(t, p50.Seconds, 0.05)
	for _, route := range []string{"timeseries", "routes", "status", "services", "warnings"} {
		code, raw := c.get(t, base+route+qs)
		require.Equal(t, http.StatusOK, code, "%s: %s", route, raw)
	}
}
