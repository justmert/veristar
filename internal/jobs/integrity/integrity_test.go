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

func TestMedian(t *testing.T) {
	require.Equal(t, int64(0), integrity.Median(nil))
	require.Equal(t, int64(5), integrity.Median([]int64{9, 5, 1}))
	// the lower middle of an even count, so one busy day cannot lift it
	require.Equal(t, int64(3), integrity.Median([]int64{10, 3, 1, 100}))
}

type fakeSites map[string]registry.Site

func (f fakeSites) Snapshot() map[string]registry.Site { return f }
func (f fakeSites) Loaded() bool                       { return true }

type row struct {
	Grade      *string
	Closed     bool
	Events     int64
	Networks   int64
	Baseline   *int64
	BaseDays   int32
	Reasons    []rules.Reason
	Visitors   int64
	NetHosting int64
}

// TestGrader writes evidence, outcomes and aggregates as edgestats and
// aggregate-web would, runs the task and reads the stored grades.
func TestGrader(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	jobs := jobstest.JobsDB(t, pg)
	ns := jobstest.NATS(t)
	leader, _ := jobstest.Leader(t, ns, jobs, "integrity-test")

	tenant, clean, hosting, removedTenant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	removedSite, framedSite := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	c, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, err = c.Exec(ctx, `insert into cp.tenants (id, slug, name, status) values ($1, 'p-int', 'Int', 'active'), ($2, 'p-gone', 'Gone', 'removed')`, tenant, removedTenant)
	require.NoError(t, err)

	now := time.Date(2031, 6, 20, 15, 0, 0, 0, time.UTC)
	today := integrity.UTCDay(now)
	exec := func(sql string, args ...any) {
		t.Helper()
		require.NoError(t, jobs.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		}))
	}
	web := func(site, tn uuid.UUID, day time.Time, visitors, pageviews int64) {
		exec(`insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime)
		      values ($1, $2, $3, 'primary', $4, $5, $5, 0, 0)`, site, tn, day, pageviews, visitors)
	}
	outcome := func(site, tn uuid.UUID, day time.Time, name string, n int64) {
		exec(`insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count) values ($1, $2, $3, $4, $5)`,
			site, tn, day.Add(10*time.Hour), name, n)
	}
	evidence := func(site, tn uuid.UUID, day time.Time, class rules.NetClass, n int64) {
		exec(`insert into cp.web_evidence_daily (site_id, tenant_id, day, class, count) values ($1, $2, $3, $4, $5)`,
			site, tn, day, string(class), n)
	}
	networks := func(site, tn uuid.UUID, day time.Time, n int) {
		for i := range n {
			exec(`insert into cp.web_networks_daily (site_id, tenant_id, day, network) values ($1, $2, $3, lpad(to_hex($4::int), 16, '0'))`,
				site, tn, day, i+1)
		}
	}

	// two weeks of steady history for both sites
	for i := 1; i <= 14; i++ {
		web(clean, tenant, today.AddDate(0, 0, -i), 400, 900)
		web(hosting, tenant, today.AddDate(0, 0, -i), 40, 90)
	}
	// clean site, yesterday: access networks, a normal spread
	y := today.AddDate(0, 0, -1)
	outcome(clean, tenant, y, "accepted", 1000)
	evidence(clean, tenant, y, rules.NetAccess, 940)
	evidence(clean, tenant, y, rules.NetHosting, 60)
	networks(clean, tenant, y, 80)
	// hosting site, today: a spike of visitors from a few hosting networks,
	// much of it quarantined; the network rows of an old day are pruned
	web(hosting, tenant, today, 900, 1800)
	outcome(hosting, tenant, today, "accepted", 600)
	outcome(hosting, tenant, today, "quarantined", 400)
	outcome(hosting, tenant, today, "rejected_proof_work", 20)
	evidence(hosting, tenant, today, rules.NetHosting, 800)
	evidence(hosting, tenant, today, rules.NetAccess, 200)
	networks(hosting, tenant, today, 3)
	networks(hosting, tenant, today.AddDate(0, 0, -10), 2)
	// a day with events but no web aggregate yet waits
	outcome(clean, tenant, today, "accepted", 300)
	// a removed tenant's site is never written again
	web(removedSite, removedTenant, y, 500, 500)
	outcome(removedSite, removedTenant, y, "accepted", 500)

	// a site whose tracker 1.3.0 pages report most views framed and some
	// loads never visible: rule version 2 lowers an otherwise clean day
	signal := func(site, tn uuid.UUID, day time.Time, name string, n int64) {
		exec(`insert into cp.web_signals_daily (site_id, tenant_id, day, signal, count) values ($1, $2, $3, $4, $5)`,
			site, tn, day, name, n)
	}
	web(framedSite, tenant, y, 400, 900)
	outcome(framedSite, tenant, y, "accepted", 1000)
	evidence(framedSite, tenant, y, rules.NetAccess, 1000)
	networks(framedSite, tenant, y, 80)
	signal(framedSite, tenant, y, rules.SignalViews, 900)
	signal(framedSite, tenant, y, rules.SignalFramed, 600)
	signal(framedSite, tenant, y, rules.SignalNeverVisible, 50)

	sites := fakeSites{}
	for _, s := range []struct{ id, tn uuid.UUID }{{clean, tenant}, {hosting, tenant}, {removedSite, removedTenant}, {framedSite, tenant}} {
		sites[s.id.String()] = registry.Site{ID: s.id.String(), TenantID: s.tn.String(), Status: registry.StatusActive,
			Origins: []string{"https://x.example"}, QuarantineWebsiteID: uuid.NewString()}
	}
	reg := prometheus.NewRegistry()
	g, err := integrity.New(sites, func() time.Time { return now }, slog.New(slog.DiscardHandler), reg)
	require.NoError(t, err)
	res, err := g.Run(ctx, leader.Tx)
	require.NoError(t, err)
	require.Equal(t, 4, res.Sites)
	require.Equal(t, 1, res.Waiting, "the clean site's today has events but no aggregate")
	require.Equal(t, int64(2), res.Pruned)

	read := func(site uuid.UUID, day time.Time) (row, bool) {
		t.Helper()
		var r row
		var reasons []byte
		var found bool
		require.NoError(t, jobs.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `select grade, closed, events, networks, baseline_visitors, baseline_days, reasons, visitors, net_hosting
			  from agg.web_integrity_daily where site_id = $1 and day = $2`, site, day).
				Scan(&r.Grade, &r.Closed, &r.Events, &r.Networks, &r.Baseline, &r.BaseDays, &reasons, &r.Visitors, &r.NetHosting)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			found = true
			if err != nil {
				return err
			}
			return json.Unmarshal(reasons, &r.Reasons)
		}))
		return r, found
	}

	cleanDay, ok := read(clean, y)
	require.True(t, ok)
	require.NotNil(t, cleanDay.Grade)
	require.Equal(t, rules.GradeA, *cleanDay.Grade)
	require.True(t, cleanDay.Closed)
	require.Equal(t, int64(80), cleanDay.Networks)
	require.Equal(t, int64(400), *cleanDay.Baseline)
	require.Equal(t, int32(13), cleanDay.BaseDays, "the days before yesterday within the history")
	require.Empty(t, cleanDay.Reasons)

	hot, ok := read(hosting, today)
	require.True(t, ok)
	require.Equal(t, rules.GradeC, *hot.Grade)
	require.False(t, hot.Closed, "today is still open")
	var got []string
	for _, r := range hot.Reasons {
		got = append(got, r.Code+":"+r.Level)
	}
	require.ElementsMatch(t, []string{"hosting_share:C", "quarantine_share:C", "few_networks:B", "spike:B"}, got)

	fr, ok := read(framedSite, y)
	require.True(t, ok)
	require.Equal(t, rules.GradeC, *fr.Grade)
	require.Len(t, fr.Reasons, 1)
	require.Equal(t, rules.ReasonFramedShare, fr.Reasons[0].Code)
	var rv int32
	var views, framed, nv int64
	require.NoError(t, jobs.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select rule_version, signal_views, framed, never_visible
		  from agg.web_integrity_daily where site_id = $1 and day = $2`, framedSite, y).Scan(&rv, &views, &framed, &nv)
	}))
	require.Equal(t, int32(rules.RuleVersion), rv)
	require.Equal(t, []int64{900, 600, 50}, []int64{views, framed, nv})

	// an older day without events has a row that says it is not graded
	old, ok := read(clean, today.AddDate(0, 0, -3))
	require.True(t, ok)
	require.Nil(t, old.Grade)
	require.Equal(t, rules.ReasonTooLittleTraffic, old.Reasons[0].Code)

	_, ok = read(clean, today)
	require.False(t, ok, "a waiting day has no row")
	_, ok = read(removedSite, y)
	require.False(t, ok, "a removed tenant is never written")

	// a later run replaces the row, it never adds a second one
	_, err = g.Run(ctx, leader.Tx)
	require.NoError(t, err)
	var n int
	require.NoError(t, jobs.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from agg.web_integrity_daily where site_id = $1`, hosting).Scan(&n)
	}))
	require.Equal(t, 4, n, "today and three days before, once each")

	_, err = g.Backfill(ctx, leader.Tx, today, today.AddDate(0, 0, 1))
	require.Error(t, err, "a day after today is refused")
}
