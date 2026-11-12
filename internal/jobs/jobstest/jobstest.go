// Package jobstest starts what the jobs tests run against: a real postgres
// with the platform migrations and role passwords applied, and a real nats
// server with the topology applied. only test files import it.
package jobstest

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/db"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

// Postgres starts postgres, migrates the platform database and gives every
// application role its password.
func Postgres(t testing.TB) *dbtest.Postgres {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pg, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	cfg, err := pdb.LoadAdminConfig(pg.Config("platform"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pdb.Migrate(ctx, cfg, db.Migrations(), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	rcfg, err := pdb.LoadRolesConfig(pg.Config("platform"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pdb.SetRolePasswords(ctx, rcfg); err != nil {
		t.Fatal(err)
	}
	return pg
}

// JobsDB opens a pool as the jobs role, the role the jobs daemon uses.
func JobsDB(t testing.TB, pg *dbtest.Postgres) *pdb.DB {
	t.Helper()
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleJobs)
	if err != nil {
		t.Fatal(err)
	}
	d, err := pdb.Open(context.Background(), pcfg, "jobs-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

// NATS starts one server and applies the topology with one replica.
func NATS(t testing.TB, opts ...natsctr.Option) *natsctr.Server {
	t.Helper()
	srv := natsctr.StartSingle(t, opts...)
	_, js := Connect(t, srv.URL)
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := natsx.Apply(context.Background(), js, spec); err != nil {
		t.Fatal(err)
	}
	return srv
}

// Connect opens an unauthenticated connection that reconnects forever.
func Connect(t testing.TB, url string) (*nats.Conn, jetstream.JetStream) {
	t.Helper()
	nc, err := nats.Connect(url, nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return nc, js
}

// PutSite registers an active site of tenant in registry_sites.
func PutSite(t testing.TB, js jetstream.JetStream, site, tenant uuid.UUID) {
	t.Helper()
	p, err := registry.NewPublisher(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	err = p.PutSite(context.Background(), registry.Site{
		ID: site.String(), TenantID: tenant.String(), Status: registry.StatusActive,
		Origins: []string{"https://example.org"}, QuarantineWebsiteID: uuid.New().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Row is one cp.web_ingest_hourly row.
type Row struct {
	Site    uuid.UUID
	Tenant  uuid.UUID
	Hour    time.Time
	Outcome string
	Count   int64
}

// Rows reads every cp.web_ingest_hourly row as the jobs role.
func Rows(t testing.TB, d *pdb.DB) []Row {
	t.Helper()
	var out []Row
	err := d.WithRole(context.Background(), pdb.RoleJobs, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(),
			`select site_id, tenant_id, hour, outcome, count from cp.web_ingest_hourly order by site_id, hour, outcome`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Row, error) {
			var x Row
			err := r.Scan(&x.Site, &x.Tenant, &x.Hour, &x.Outcome, &x.Count)
			return x, err
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Totals sums Rows by site and outcome, for comparisons that do not care
// which hour a count landed in.
func Totals(t testing.TB, d *pdb.DB) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, r := range Rows(t, d) {
		out[r.Site.String()+"/"+r.Outcome] += r.Count
	}
	return out
}

// DedupIDs reads every message id in cp.stats_dedup.
func DedupIDs(t testing.TB, d *pdb.DB) []string {
	t.Helper()
	var out []string
	err := d.WithRole(context.Background(), pdb.RoleJobs, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `select message_id from cp.stats_dedup order by message_id`)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
