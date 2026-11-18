package metricswriter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// warnRecorder keeps the messages of the warnings logged through it.
type warnRecorder struct {
	mu   sync.Mutex
	msgs []string
}

func (r *warnRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *warnRecorder) Handle(_ context.Context, rec slog.Record) error {
	if rec.Level >= slog.LevelWarn {
		r.mu.Lock()
		r.msgs = append(r.msgs, rec.Message)
		r.mu.Unlock()
	}
	return nil
}
func (r *warnRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *warnRecorder) WithGroup(string) slog.Handler      { return r }

// a tenant whose clock runs an hour ahead, or whose sdk sends counts its
// buckets do not add up to, gets every point of every export refused. the
// writer logged one warning per point, so one such tenant with many streams
// filled the writer's log; it now logs each kind of refusal once per
// message, with the count.
func TestRefusedPointsLoggedOncePerMessage(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	super, err := pg.Superuser(ctx, "platform")
	require.NoError(t, err)
	t.Cleanup(func() { _ = super.Close(context.Background()) })
	var user, tenant, token uuid.UUID
	require.NoError(t, super.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (4646, 'skew') returning id`).Scan(&user))
	require.NoError(t, super.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ('skew', 'skew', 'active', $1) returning id`, user).Scan(&tenant))
	h := sha256.Sum256([]byte("skew"))
	require.NoError(t, super.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, prefix, hash, created_by) values ($1, 'it', 'sa_otlp_xxxx', $2, $3) returning id`,
		tenant, h[:], user).Scan(&token))

	wcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleMetricsWriter)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, wcfg, "skew-writer")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	rec := &warnRecorder{}
	clock := time.Now().UTC().Truncate(time.Minute)
	w := &Writer{
		set: Settings{NodeID: "skew", Batch: 64, FirstPointThreshold: DefaultFirstPointThreshold, RouteCap: 300, ServiceCap: 50},
		db:  d, log: slog.New(rec), now: func() time.Time { return clock }, maxAge: 7 * 24 * time.Hour,
	}

	const n = 40
	message := func(at time.Time, count uint64) *item {
		pts := make([]Decoded, 0, n)
		for r := range n {
			var counts Buckets
			counts[3] = 1
			pts = append(pts, Decoded{
				Point: Point{Tenant: tenant.String(), ResourceHash: fmt.Sprintf("%064x", 1), Metric: metricDuration,
					Attrs: []Attr{{attrRoute, fmt.Sprintf("/r%d", r)}}, Temporality: Delta,
					Start: at.Add(-time.Minute).UnixNano(), Time: at.UnixNano(), Count: count, Sum: 0.02, Counts: counts},
				TokenID: token, Source: SourceMetric, Service: "api", Method: "GET", Route: fmt.Sprintf("/r%d", r), Status: 200, Class: "app",
			})
		}
		return &item{id: uuid.NewString(), tenant: tenant, meta: &jetstream.MsgMetadata{Timestamp: clock},
			decoded: Message{Tenant: tenant, Points: pts, Resources: 1}}
	}
	var res *result
	require.NoError(t, d.WithRole(ctx, pdb.RoleMetricsWriter, func(tx pgx.Tx) error {
		var err error
		// an hour ahead of the receive time, and buckets that add up to 1
		// for a count of 2
		res, err = w.applyTx(ctx, tx, []*item{message(clock.Add(time.Hour), 1), message(clock, 2)})
		return err
	}))
	require.Equal(t, 2*n, res.rejected)
	require.Equal(t, []string{"points outside the accepted time range, not stored", "points refused"}, rec.msgs)
}
