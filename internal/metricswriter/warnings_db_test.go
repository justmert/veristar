package metricswriter

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
)

// review: a cumulative series is re-exported every interval for the life
// of its process. warnings must count requests, so exports without a new
// request add nothing; before, every export of a resource with a redacted
// seed and every lossy point added one, so the numbers grew by the minute
// with no traffic. the coarse_buckets kind of review counts the same way.
// runs the writer's own transaction code against a real postgresql.
func TestWarningsCountRequestsNotExports(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	super, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = super.Close(context.Background()) })
	var user, tenant, token uuid.UUID
	require.NoError(t, super.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (4444, 'warn') returning id`).Scan(&user))
	require.NoError(t, super.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ('warn', 'warn', 'active', $1) returning id`, user).Scan(&tenant))
	h := sha256.Sum256([]byte("warn"))
	require.NoError(t, super.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'it', 'sa_otlp_xxxx', $2, $3) returning id`,
		tenant, h[:], user).Scan(&token))

	wcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleMetricsWriter)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, wcfg, "warn-writer")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	w := &Writer{
		set: Settings{NodeID: "warn", Batch: 64, FirstPointThreshold: DefaultFirstPointThreshold, RouteCap: 300, ServiceCap: 50},
		db:  d, log: slog.New(slog.DiscardHandler), now: time.Now, maxAge: 7 * 24 * time.Hour,
	}

	// a fresh process: start 30 s ago, exports every 10 s. its generic
	// default bounds in seconds are both lossy and coarse, and its resource
	// had a seed redacted from a route.
	now := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	start := now.Add(-30 * time.Second)
	generic := []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
	export := func(i int, requests uint64) *item {
		at := now.Add(time.Duration(i) * 10 * time.Second)
		src := make([]uint64, len(generic)+1)
		src[1] = requests
		var counts Buckets
		counts[11] = requests // (2.5, 5]
		p := Decoded{
			Point: Point{Tenant: tenant.String(), ResourceHash: strings.Repeat("ef", 32), Metric: metricDuration,
				Attrs: []Attr{{attrRoute, "6:/keys/{redacted-secret}"}}, Temporality: Cumulative,
				Start: start.UnixNano(), Time: at.UnixNano(), Count: requests, Sum: 0.01 * float64(requests), Counts: counts, Lossy: true},
			TokenID: token, Source: SourceMetric, Service: "api", Method: "GET", Route: "/keys/{redacted-secret}", Status: 200,
			SecretSeed: true, Coarse: true,
		}
		return &item{id: uuid.NewString(), tenant: tenant, meta: &jetstream.MsgMetadata{Timestamp: at.Add(time.Second)},
			decoded: Message{Tenant: tenant, Points: []Decoded{p}, Resources: 1, SecretSeeds: 1}}
	}
	// 6 requests before the first export, then three exports with no new one
	var items []*item
	for i, n := range []uint64{6, 6, 6, 6} {
		items = append(items, export(i, n))
	}
	require.NoError(t, d.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgx.Tx) error {
		_, err := w.applyTx(ctx, tx, items)
		return err
	}))

	got := map[string]int64{}
	rows, err := super.Query(ctx, `select kind, count from api.ingest_warnings where tenant_id = $1`, tenant)
	require.NoError(t, err)
	for rows.Next() {
		var k string
		var n int64
		require.NoError(t, rows.Scan(&k, &n))
		got[k] = n
	}
	require.NoError(t, rows.Err())
	var stored int64
	require.NoError(t, super.QueryRow(ctx, `select coalesce(sum(count), 0)::bigint from api.metrics_minute where tenant_id = $1`, tenant).Scan(&stored))
	require.Equal(t, int64(6), stored)
	require.Equal(t, map[string]int64{"secret_in_path": 6, "lossy_histogram": 6, "coarse_buckets": 6}, got,
		"warnings must count the 6 requests, not the 4 exports or points")
}
