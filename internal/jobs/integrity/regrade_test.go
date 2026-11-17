package integrity_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	rules "github.com/justmert/veristar/internal/integrity"
	"github.com/justmert/veristar/internal/jobs/integrity"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/registry"
)

// a backfill of days before the regrade window runs after their network
// keys were pruned. a closed day keeps the network count and rule version it
// was graded with, and a day never graded is not graded on zero networks.
func TestRegradeOutsideWindow(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	ns := jobstest.NATS(t)
	leader, _ := jobstest.Leader(t, ns, jobs, "integrity-regrade-test")

	tenant, site := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, err = c.Exec(ctx, `insert into cp.tenants (id, slug, name, status) values ($1, 'p-regrade', 'Regrade', 'active')`, tenant)
	require.NoError(t, err)

	now := time.Date(2031, 6, 20, 15, 0, 0, 0, time.UTC)
	today := integrity.UTCDay(now)
	graded, never := today.AddDate(0, 0, -10), today.AddDate(0, 0, -11)
	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, jobs.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		}))
	}
	for _, d := range []time.Time{graded, never} {
		exec(`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
		      values ($1, $2, $3, 'primary', 900, 400, 400, 0, 0)`, site, tenant, d)
		exec(`insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count) values ($1, $2, $3, 'accepted', 1000)`,
			site, tenant, d.Add(10*time.Hour))
		exec(`insert into cp.web_evidence_daily (site_id, tenant_id, day, class, count) values ($1, $2, $3, $4, 1000)`,
			site, tenant, d, string(rules.NetAccess))
		// framed views lower a day under rule version 2, not under 1
		exec(`insert into cp.web_signals_daily (site_id, tenant_id, day, signal, count) values
		      ($1, $2, $3, $4, 900), ($1, $2, $3, $5, 600)`, site, tenant, d, rules.SignalViews, rules.SignalFramed)
	}
	// graded under rule version 1 while its 80 network keys still existed
	exec(`insert into agg.web_integrity_daily (site_id, tenant_id, day, rule_version, grade, closed, events, quarantined,
	        proof_failures, net_access, net_organisation, net_hosting, net_anonymiser, net_unknown, networks, visitors,
	        pageviews, baseline_days, signal_views, framed, never_visible, reasons)
	      values ($1, $2, $3, 1, 'A', true, 1000, 0, 0, 1000, 0, 0, 0, 0, 80, 400, 900, 0, 900, 600, 0, '[]')`,
		site, tenant, graded)

	sites := fakeSites{site.String(): registry.Site{ID: site.String(), TenantID: tenant.String(), Status: registry.StatusActive,
		Origins: []string{"https://x.example"}, QuarantineWebsiteID: uuid.NewString()}}
	g, err := integrity.New(sites, func() time.Time { return now }, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	_, err = g.Backfill(ctx, leader.Tx, never, graded)
	require.NoError(t, err)

	var version int32
	var grade *string
	var networks int64
	var reasons []byte
	require.NoError(t, jobs.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select rule_version, grade, networks, reasons from agg.web_integrity_daily
		  where site_id = $1 and day = $2`, site, graded).Scan(&version, &grade, &networks, &reasons)
	}))
	require.Equal(t, int32(1), version, "the rule version of a closed day is kept")
	require.Equal(t, int64(80), networks, "the network count of a pruned day is kept")
	require.NotNil(t, grade)
	require.Equal(t, rules.GradeA, *grade)
	var rs []rules.Reason
	require.NoError(t, json.Unmarshal(reasons, &rs))
	require.Empty(t, rs, "no few_networks from pruned keys")

	require.NoError(t, jobs.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `select 1 from agg.web_integrity_daily where site_id = $1 and day = $2`, site, never).Scan(new(int))
		require.True(t, errors.Is(err, pgx.ErrNoRows), "a day never graded is not graded on pruned network keys: %v", err)
		return nil
	}))
}
