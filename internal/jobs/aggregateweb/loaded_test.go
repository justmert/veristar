package aggregateweb_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/jobs/aggregateweb"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
)

// unloadedSites is a registry that has not built its first snapshot yet.
type unloadedSites struct{}

func (unloadedSites) Snapshot() map[string]registry.Site { return map[string]registry.Site{} }
func (unloadedSites) Loaded() bool                       { return false }

// noUmami fails the test if the task reads umami at all.
type noUmami struct{ t *testing.T }

func (u noUmami) GetWebsite(context.Context, string) (umami.Website, bool, error) {
	u.t.Fatal("umami read before the registry loaded")
	return umami.Website{}, false, nil
}

func (u noUmami) Stats(context.Context, string, umami.Range) (umami.Stats, error) {
	u.t.Fatal("umami read before the registry loaded")
	return umami.Stats{}, nil
}

func (u noUmami) Metrics(context.Context, string, umami.MetricsQuery) ([]umami.MetricRow, error) {
	u.t.Fatal("umami read before the registry loaded")
	return nil, nil
}

// a daemon's first run can start before the registry watch delivered its
// snapshot; that run must fail (and be retried) instead of succeeding over
// zero sites, and a site lookup must not report every site as removed
func TestRegistryNotLoaded(t *testing.T) {
	a, err := aggregateweb.New(noUmami{t}, unloadedSites{}, time.Now, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	tx := func(context.Context, func(pgx.Tx) error) error {
		t.Fatal("transaction opened before the registry loaded")
		return nil
	}

	res, err := a.Run(t.Context(), tx)
	require.True(t, errors.Is(err, aggregateweb.ErrRegistryNotLoaded), "run: %v", err)
	require.Zero(t, res.Sites)

	_, ok, err := a.Site("01a1d53a-0001-72c5-a1b5-11755853fc1e")
	require.True(t, errors.Is(err, aggregateweb.ErrRegistryNotLoaded), "site: %v", err)
	require.False(t, ok)
}
