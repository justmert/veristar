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
)

type loadedSites map[string]registry.Site

func (s loadedSites) Snapshot() map[string]registry.Site { return s }
func (loadedSites) Loaded() bool                         { return true }

// retain-web deletes raw umami rows older than WEB_RAW_RETENTION_MONTHS, and
// umami then answers zeros for those days. a backfill must not read them:
// the zeros would replace aggregates that are kept for the life of the site
func TestBackfillStopsAtRawCutoff(t *testing.T) {
	now := time.Date(2031, 6, 20, 15, 0, 0, 0, time.UTC)
	site := registry.Site{ID: "01a1d53a-0001-72c5-a1b5-11755853fc1e", TenantID: "01a1d53a-0002-72c5-a1b5-11755853fc1e",
		Status: registry.StatusActive, Origins: []string{"https://x.example"}, QuarantineWebsiteID: "01a1d53a-0003-72c5-a1b5-11755853fc1e"}
	a, err := aggregateweb.New(noUmami{t}, loadedSites{site.ID: site}, func() time.Time { return now },
		slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	require.NoError(t, a.SetRawRetentionMonths(25))
	require.Error(t, a.SetRawRetentionMonths(0))
	cutoff := time.Date(2029, 5, 20, 0, 0, 0, 0, time.UTC)
	require.Equal(t, cutoff, a.RawCutoff())

	tx := func(context.Context, func(pgx.Tx) error) error {
		t.Fatal("transaction opened for a day before the raw cutoff")
		return nil
	}
	_, err = a.Backfill(t.Context(), tx, cutoff.AddDate(0, 0, -1), cutoff.AddDate(0, 0, 5))
	require.True(t, errors.Is(err, aggregateweb.ErrBeforeRawCutoff), "backfill: %v", err)

	// the erasure follow-up reads the days it can and leaves older ones alone
	n, err := a.BackfillSite(t.Context(), tx, site, cutoff.AddDate(0, -3, 0), cutoff.AddDate(0, 0, -1))
	require.NoError(t, err)
	require.Zero(t, n)
}
