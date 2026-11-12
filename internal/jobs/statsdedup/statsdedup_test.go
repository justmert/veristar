package statsdedup_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/jobs/scheduler"
	"github.com/justmert/stellar-analytics/internal/jobs/statsdedup"
	"github.com/justmert/stellar-analytics/internal/natsx"
)

// TestPrune runs the delete as the jobs role through a fenced transaction,
// with the clock injected so the cutoff is exact.
func TestPrune(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	d := jobstest.JobsDB(t, pg)
	_, js := jobstest.Connect(t, srv.URL)
	kv, err := js.KeyValue(t.Context(), natsx.BucketLocks)
	require.NoError(t, err)
	lock, err := natsx.NewLock(kv, natsx.LockJobs, "prune-test")
	require.NoError(t, err)
	_, err = lock.Acquire(t.Context())
	require.NoError(t, err)
	leader, err := scheduler.NewLeader(lock, d)
	require.NoError(t, err)

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ids := map[string]time.Time{
		"edge-1.old":      now.Add(-statsdedup.MaxAge - time.Hour),
		"edge-1.boundary": now.Add(-statsdedup.MaxAge),
		"edge-1.week":     now.Add(-7 * 24 * time.Hour),
		"edge-1.recent":   now.Add(-time.Minute),
	}
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		for id, at := range ids {
			if _, err := tx.Exec(t.Context(), `insert into cp.stats_dedup (message_id, applied_at) values ($1, $2)`, id, at); err != nil {
				return err
			}
		}
		return nil
	}))

	p, err := statsdedup.New(func() time.Time { return now }, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	n, err := p.Prune(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "only ids older than 8 days go")
	require.ElementsMatch(t, []string{"edge-1.boundary", "edge-1.recent", "edge-1.week"}, jobstest.DedupIDs(t, d))

	// a day later the boundary id is past the cutoff too
	now = now.Add(24 * time.Hour)
	n, err = p.Prune(t.Context(), leader.Tx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.ElementsMatch(t, []string{"edge-1.recent", "edge-1.week"}, jobstest.DedupIDs(t, d))

	// a fenced-off holder deletes nothing
	now = now.Add(30 * 24 * time.Hour)
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `update cp.leader_fence set epoch = epoch + 1 where name = 'jobs'`)
		return err
	}))
	_, err = p.Prune(t.Context(), leader.Tx)
	require.ErrorIs(t, err, natsx.ErrFenced)
	require.Len(t, jobstest.DedupIDs(t, d), 2)
}
