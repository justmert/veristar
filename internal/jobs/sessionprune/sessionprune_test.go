package sessionprune_test

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/jobs/scheduler"
	"github.com/justmert/veristar/internal/jobs/sessionprune"
	"github.com/justmert/veristar/internal/natsx"
)

func hash(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func sessionNames(t *testing.T, d *pdb.DB, names map[string][]byte) []string {
	t.Helper()
	var out []string
	require.NoError(t, d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		for name, h := range names {
			var n int
			if err := tx.QueryRow(t.Context(), `select count(*) from cp.sessions where token_hash = $1`, h).Scan(&n); err != nil {
				return err
			}
			if n == 1 {
				out = append(out, name)
			}
		}
		return nil
	}))
	return out
}

// TestPrune deletes as cp_system through the fenced SystemTx, with the clock
// injected so the cutoff is exact.
func TestPrune(t *testing.T) {
	srv := jobstest.NATS(t)
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleJobs, pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "sessionprune-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	_, js := jobstest.Connect(t, srv.URL)
	kv, err := js.KeyValue(t.Context(), natsx.BucketLocks)
	require.NoError(t, err)
	lock, err := natsx.NewLock(kv, natsx.LockJobs, "sessionprune-test")
	require.NoError(t, err)
	_, err = lock.Acquire(t.Context())
	require.NoError(t, err)
	leader, err := scheduler.NewLeader(lock, d)
	require.NoError(t, err)

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	rows := map[string]time.Time{
		"expired":  now.Add(-time.Hour),
		"boundary": now,
		"live":     now.Add(time.Minute),
		"tomorrow": now.Add(25 * time.Hour),
	}
	hashes := map[string][]byte{}
	require.NoError(t, d.WithSystem(t.Context(), func(tx pgx.Tx) error {
		for name, exp := range rows {
			hashes[name] = hash(t.Name() + name)
			if _, err := tx.Exec(t.Context(), `insert into cp.sessions (token_hash, data, expiry) values ($1, '\x00', $2)`,
				hashes[name], exp); err != nil {
				return err
			}
		}
		return nil
	}))

	p, err := sessionprune.New(func() time.Time { return now }, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)
	n, err := p.Prune(t.Context(), leader.SystemTx)
	require.NoError(t, err)
	require.Equal(t, int64(2), n, "expired and exactly-now rows go")
	require.ElementsMatch(t, []string{"live", "tomorrow"}, sessionNames(t, d, hashes))

	now = now.Add(2 * time.Hour)
	n, err = p.Prune(t.Context(), leader.SystemTx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.ElementsMatch(t, []string{"tomorrow"}, sessionNames(t, d, hashes))

	// a fenced-off holder deletes nothing
	now = now.Add(48 * time.Hour)
	require.NoError(t, d.WithRole(t.Context(), pdb.RoleJobs, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `update cp.leader_fence set epoch = epoch + 1 where name = 'jobs'`)
		return err
	}))
	_, err = p.Prune(t.Context(), leader.SystemTx)
	require.ErrorIs(t, err, natsx.ErrFenced)
	require.ElementsMatch(t, []string{"tomorrow"}, sessionNames(t, d, hashes))
}
