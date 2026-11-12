package obs_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/obs"
)

func get(t *testing.T, h http.Handler) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/edgez", nil))
	return rec.Code, rec.Body.String()
}

func TestCompositeHandler(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	ok := obs.Check{Name: "ok", Fn: func(context.Context) error { return nil }}
	bad := obs.Check{Name: "bad", Fn: func(context.Context) error { return errors.New("down") }}

	code, body := get(t, obs.CompositeHandler(log, nil, ok))
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "healthy\n", body)

	code, body = get(t, obs.CompositeHandler(log, nil, ok, bad))
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, "unhealthy: bad\n", body)

	// missing configuration wins over passing checks
	code, body = get(t, obs.CompositeHandler(log, []string{"EDGE_HEALTH_NATS_URL"}, ok))
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Contains(t, body, "EDGE_HEALTH_NATS_URL")
}

func TestHTTPCheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL + "/healthz")
	require.NoError(t, err)
	c := obs.HTTPCheck("nats", u, srv.Client())
	require.NoError(t, c.Fn(t.Context()))
	status = http.StatusServiceUnavailable
	require.ErrorContains(t, c.Fn(t.Context()), "503")
	srv.Close()
	require.Error(t, c.Fn(t.Context()))
}
