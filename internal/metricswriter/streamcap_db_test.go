package metricswriter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/jobsq"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// a stream is a (series, start time) row, so every process
// restart adds a new set and the old rows stay for 7 idle days. the cap
// counted all of them, so ordinary restarts reached it and every new
// process's data was refused for up to a week. the cap now counts streams
// with a point in the last hour, and api-retention bounds the stored rows
// by evicting the longest idle ones. runs the writer's own transaction code
// and the eviction function against a real postgresql.
func TestStreamCapCountsOnlyActiveStreams(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	super, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = super.Close(context.Background()) })
	var user, tenant, token uuid.UUID
	require.NoError(t, super.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (4545, 'churn') returning id`).Scan(&user))
	require.NoError(t, super.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ('churn', 'churn', 'active', $1) returning id`, user).Scan(&tenant))
	h := sha256.Sum256([]byte("churn"))
	require.NoError(t, super.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'it', 'sa_otlp_xxxx', $2, $3) returning id`,
		tenant, h[:], user).Scan(&token))

	wcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleMetricsWriter)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, wcfg, "churn-writer")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	clock := time.Now().UTC().Truncate(time.Minute).Add(-6 * time.Hour)
	w := &Writer{
		set: Settings{NodeID: "churn", Batch: 64, FirstPointThreshold: DefaultFirstPointThreshold, RouteCap: 300, ServiceCap: 50},
		db:  d, log: slog.New(slog.DiscardHandler), now: func() time.Time { return clock }, maxAge: 7 * 24 * time.Hour,
	}

	// one process generation: a new resource (service.instance.id changes
	// at every start) with one cumulative stream per route, 10 s old
	const routes = 3
	process := func(g int) *item {
		at := clock
		pts := make([]Decoded, 0, routes)
		for r := range routes {
			var counts Buckets
			counts[3] = 1
			pts = append(pts, Decoded{
				Point: Point{Tenant: tenant.String(), ResourceHash: fmt.Sprintf("%064x", g+1), Metric: metricDuration,
					Attrs: []Attr{{attrRoute, fmt.Sprintf("/r%d", r)}}, Temporality: Cumulative,
					Start: at.Add(-10 * time.Second).UnixNano(), Time: at.UnixNano(), Count: 1, Sum: 0.02, Counts: counts},
				TokenID: token, Source: SourceMetric, Service: "api", Method: "GET", Route: fmt.Sprintf("/r%d", r), Status: 200, Class: "app",
			})
		}
		return &item{id: uuid.NewString(), tenant: tenant, meta: &jetstream.MsgMetadata{Timestamp: at.Add(time.Second)},
			decoded: Message{Tenant: tenant, Points: pts, Resources: 1}}
	}
	apply := func(g int) *result {
		var res *result
		require.NoError(t, d.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgx.Tx) error {
			var err error
			res, err = w.applyTx(ctx, tx, []*item{process(g)})
			return err
		}))
		return res
	}
	// the streams of the process that exported at the current clock
	rowsNow := func() int {
		var n int
		require.NoError(t, super.QueryRow(ctx, `select count(*) from api.series_state where tenant_id = $1 and start_unix_nano = $2`,
			tenant, clock.Add(-10*time.Second).UnixNano()).Scan(&n))
		return n
	}

	// earlier restarts left a full cap of streams, last seen two hours ago
	_, err = super.Exec(ctx, `insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano,
		last_count, last_sum, last_counts, last_seen_at)
		select $1, sha256(int8send(g)), 1, 2, 1, 0.5, array_fill(0::bigint, array[15]), $2
		from generate_series(1, $3::bigint) g`, tenant, clock.Add(-2*time.Hour), MaxSeriesPerTenant)
	require.NoError(t, err)

	// restarts keep coming, one every 90 minutes: each new process is stored
	for g := range 4 {
		res := apply(g)
		require.Zero(t, res.rejected, "process %d refused although its predecessors are idle", g)
		require.Equal(t, routes, rowsNow(), "process %d streams", g)
		clock = clock.Add(90 * time.Minute)
	}

	// eviction keeps the active streams and the most recently idle ones
	jcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleJobs)
	require.NoError(t, err)
	jd, err := pdb.Open(ctx, jcfg, "churn-jobs")
	require.NoError(t, err)
	t.Cleanup(jd.Close)
	clock = clock.Add(-90 * time.Minute) // the last process exported "now"
	const keep = 50
	require.NoError(t, jd.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		n, err := jobsq.New(tx).EvictIdleSeriesStates(ctx, jobsq.EvictIdleSeriesStatesParams{
			IdleBefore: clock.Add(-ActiveSeriesWindow), MaxStored: keep})
		require.Equal(t, int64(MaxSeriesPerTenant+4*routes-keep), n)
		return err
	}))
	var total, newest int
	require.NoError(t, super.QueryRow(ctx, `select count(*), count(*) filter (where start_unix_nano > 2) from api.series_state where tenant_id = $1`,
		tenant).Scan(&total, &newest))
	require.Equal(t, keep, total)
	require.Equal(t, 4*routes, newest, "every stream of the four processes survives, the active one included")

	// the cap itself still holds for streams alive at once
	_, err = super.Exec(ctx, `insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano,
		last_count, last_sum, last_counts, last_seen_at)
		select $1, sha256(int8send(-g)), 1, 2, 1, 0.5, array_fill(0::bigint, array[15]), $2
		from generate_series(1, $3::bigint) g`, tenant, clock, MaxSeriesPerTenant)
	require.NoError(t, err)
	clock = clock.Add(time.Minute)
	res := apply(10)
	require.Equal(t, routes, res.rejected, "a tenant with the cap's number of active streams gets no new one")
}
