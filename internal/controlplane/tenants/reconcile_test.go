package tenants_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db"
)

// gate is a reconciler whose runs block until released, and which counts
// runs per tenant and the most runs in flight at once.
type gate struct {
	mu       sync.Mutex
	runs     map[uuid.UUID]int
	inFlight int
	maxSeen  int
	release  chan struct{}
}

func (g *gate) reconcile(ctx context.Context, id uuid.UUID) error {
	g.mu.Lock()
	g.runs[id]++
	g.inFlight++
	g.maxSeen = max(g.maxSeen, g.inFlight)
	g.mu.Unlock()
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return nil
}

func (g *gate) snapshot() (runs map[uuid.UUID]int, inFlight, maxSeen int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	runs = map[uuid.UUID]int{}
	for k, v := range g.runs {
		runs[k] = v
	}
	return runs, g.inFlight, g.maxSeen
}

// reconciles triggered by requests coalesce per
// tenant (at most one running and one pending) and run on a bounded set of
// workers, so a burst of edits costs at most two runs and cannot start an
// unbounded number of goroutines that each hold a database connection.
func TestReconcileCoalescesAndIsBounded(t *testing.T) {
	g := &gate{runs: map[uuid.UUID]int{}, release: make(chan struct{})}
	// Reconcile never touches the database; the scope only needs one
	s, err := tenants.NewScope(&db.DB{}, "v1", tenants.ReconcileFunc(g.reconcile), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	ctx := context.Background()

	flooded := uuid.New()
	s.Reconcile(ctx, flooded)
	require.Eventually(t, func() bool { _, n, _ := g.snapshot(); return n == 1 }, 5*time.Second, time.Millisecond)
	// a burst of edits while that run is in flight
	for range 50 {
		s.Reconcile(ctx, flooded)
	}
	others := make([]uuid.UUID, 20)
	for i := range others {
		others[i] = uuid.New()
		s.Reconcile(ctx, others[i])
	}
	require.Eventually(t, func() bool { _, n, _ := g.snapshot(); return n == tenants.ReconcileWorkers }, 5*time.Second, time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	_, inFlight, maxSeen := g.snapshot()
	require.Equal(t, tenants.ReconcileWorkers, inFlight)
	require.Equal(t, tenants.ReconcileWorkers, maxSeen, "no more runs at once than workers")

	close(g.release)
	s.Wait()
	runs, _, maxSeen := g.snapshot()
	require.Equal(t, 2, runs[flooded], "the running reconcile plus one pending, whatever the burst")
	for _, id := range others {
		require.Equal(t, 1, runs[id], "every triggered tenant is reconciled once")
	}
	require.LessOrEqual(t, maxSeen, tenants.ReconcileWorkers)

	// a change after the last run finished starts a new one
	s.Reconcile(ctx, flooded)
	s.Wait()
	runs, _, _ = g.snapshot()
	require.Equal(t, 3, runs[flooded])
}

// shutdown waits for the runs in flight only. queued and pending
// runs are dropped (the periodic reconcile task covers them), so a stop with
// a long queue behind a slow umami ends when the in-flight runs end.
func TestStopFinishesInFlightAndDropsTheQueue(t *testing.T) {
	g := &gate{runs: map[uuid.UUID]int{}, release: make(chan struct{})}
	s, err := tenants.NewScope(&db.DB{}, "v1", tenants.ReconcileFunc(g.reconcile), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	ctx := context.Background()
	ids := make([]uuid.UUID, 3*tenants.ReconcileWorkers)
	for i := range ids {
		ids[i] = uuid.New()
		s.Reconcile(ctx, ids[i])
	}
	require.Eventually(t, func() bool { _, n, _ := g.snapshot(); return n == tenants.ReconcileWorkers }, 5*time.Second, time.Millisecond)
	// one more change for a running tenant (the first ones run first) makes
	// it pending
	s.Reconcile(ctx, ids[0])

	done := make(chan int, 1)
	go func() { done <- s.Stop() }()
	// the in-flight runs end; nothing else may start
	time.Sleep(50 * time.Millisecond)
	close(g.release)
	select {
	case dropped := <-done:
		require.Equal(t, len(ids)-tenants.ReconcileWorkers+1, dropped, "queued tenants plus the pending run")
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited for the queue")
	}
	runs, _, _ := g.snapshot()
	total := 0
	for _, n := range runs {
		total += n
	}
	require.Equal(t, tenants.ReconcileWorkers, total, "only the runs in flight at the stop ran")
	// a change after the stop starts nothing
	s.Reconcile(ctx, uuid.New())
	s.Wait()
	runs, _, _ = g.snapshot()
	require.Len(t, runs, tenants.ReconcileWorkers)
}
