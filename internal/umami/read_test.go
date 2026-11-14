package umami

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadWebsiteAllowlist(t *testing.T) {
	for _, ok := range []string{"/stats", "/pageviews", "/metrics", "/active", "/events/series", "/event-data/values",
		"/sessions", "/funnels", "/goals", "/funnels/0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10/stats",
		"/goals/0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10/stats"} {
		require.True(t, readable(ok), ok)
	}
	for _, bad := range []string{"", "/", "/reset", "/transfer", "/export", "/shares", "/sessions/x/activity",
		"/funnels/0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10", "/funnels/../reset/stats", "/funnels/X/stats",
		"/goals/0193A6F2-7C1E-7D40-9F55-2B1C2A5E8A10/stats", "/reports/funnel", "/stats/../reset"} {
		require.False(t, readable(bad), bad)
	}
}

func TestReadWebsite(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"visitors":3}`))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := New(u, nil)
	require.NoError(t, err)
	var out struct {
		Visitors int `json:"visitors"`
	}
	err = c.ReadWebsite(context.Background(), Token{v: "tok"}, "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10", "/active",
		url.Values{"x": {"1"}}, &out)
	require.NoError(t, err)
	require.Equal(t, 3, out.Visitors)
	require.Equal(t, http.MethodGet, got.Method)
	require.Equal(t, "/api/websites/0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10/active", got.URL.Path)
	require.Equal(t, "Bearer tok", got.Header.Get("Authorization"))
	require.Equal(t, "1", got.URL.Query().Get("x"))

	require.Error(t, c.ReadWebsite(context.Background(), Token{}, "w", "/active", nil, &out), "no token")
	require.Error(t, c.ReadWebsite(context.Background(), Token{v: "t"}, "w", "/reset", nil, &out), "not a read route")
}
