package sites_test

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane/provision"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/jobs/jobstest"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
	"github.com/justmert/stellar-analytics/tests/fixtures/consoleapi"
)

// slowUmami answers every request after delay with 503, as an overloaded
// umami behind its proxy does. a reconcile run holds the tenant row lock
// until its umami call fails.
func slowUmami(t *testing.T, delay time.Duration) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return u
}

//: a member floods site edits while umami is slow. every
// edit triggers a reconcile, and each run holds the tenant row lock and a
// cp_system connection across its umami calls. reconciles coalesce per
// tenant, run on bounded workers and use a pool of their own, so the
// session loads of every other user stay fast.
func TestSiteEditFloodWithSlowUmamiKeepsSessionsFast(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	// the pools as cmd/controlplane opens them: request pools of a fixed
	// size, and a separate cp_system pool for the reconciler
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleCPOperator)
	require.NoError(t, err)
	pcfg.MaxConns = map[pdb.Role]int32{pdb.RoleCPSystem: 4, pdb.RoleCPTenant: 4, pdb.RoleCPOperator: 2}
	d, err := pdb.Open(ctx, pcfg, "flood-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	rcfg, err := pcfg.Only(pdb.RoleCPSystem, tenants.ReconcileWorkers)
	require.NoError(t, err)
	recDB, err := pdb.Open(ctx, rcfg, "flood-test-reconcile")
	require.NoError(t, err)
	t.Cleanup(recDB.Close)

	ns := jobstest.NATS(t)
	_, js := jobstest.Connect(t, ns.URL)
	pub, err := registry.NewPublisher(ctx, js)
	require.NoError(t, err)
	uc, err := umami.New(slowUmami(t, 3*time.Second), nil)
	require.NoError(t, err)
	admin, err := umami.NewAdmin(uc, umami.SeededUsername, "flood-test-admin-password")
	require.NoError(t, err)
	raw := make([]byte, umami.PasswordKeyLen)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	key, err := umami.NewPasswordKey(raw)
	require.NoError(t, err)
	rec := &provision.Reconciler{
		Admin: admin, Registry: pub, Key: key, Tx: recDB.WithSystem, Now: time.Now, Log: slog.New(slog.DiscardHandler),
	}
	var runs atomic.Int64
	counted := tenants.ReconcileFunc(func(ctx context.Context, id uuid.UUID) error {
		runs.Add(1)
		return rec.Reconcile(ctx, id)
	})
	e := consoleapi.Start(t, consoleapi.Options{PG: pg, DB: d, Reconciler: counted})

	flooder := e.SignIn(t, e.NewGitHubID(), "flood-member")
	bystander := e.SignIn(t, e.NewGitHubID(), "flood-bystander")
	tenant, _ := flooder.Enroll(t, "Flooded Project")
	site := flooder.AddSite(t, tenant, "flood.example.org")
	sitePath := "/api/tenants/" + tenant.String() + "/sites/" + site.String()
	// the enrollment and the site create started a run; it now waits on umami
	require.Eventually(t, func() bool { return runs.Load() >= 1 }, 5*time.Second, 10*time.Millisecond)
	before := runs.Load()

	// 60 edits from 6 parallel browser tabs, while the bystander loads its
	// session every 50 ms
	floodCtx, stopFlood := context.WithTimeout(ctx, 6*time.Second)
	defer stopFlood()
	var wg sync.WaitGroup
	var edits atomic.Int64
	for tab := range 6 {
		wg.Go(func() {
			for i := range 10 {
				r, err := flooder.Do(floodCtx, http.MethodPatch, sitePath, map[string]any{"name": "edit " + strconv.Itoa(tab) + "-" + strconv.Itoa(i)})
				if err != nil {
					return
				}
				if r.Status == http.StatusOK {
					edits.Add(1)
				}
			}
		})
	}
	var slowest time.Duration
	loads := 0
	for floodCtx.Err() == nil && loads < 60 {
		started := time.Now()
		r, err := bystander.Do(ctx, http.MethodGet, "/api/me", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, r.Status, string(r.Body))
		slowest = max(slowest, time.Since(started))
		loads++
		time.Sleep(50 * time.Millisecond)
	}
	wg.Wait()
	stopFlood()

	require.Less(t, slowest, 500*time.Millisecond, "a session load waited behind the reconciles (%d loads)", loads)
	require.Equal(t, int64(60), edits.Load(), "every edit was answered")
	// the burst coalesced: the run in flight plus at most one pending
	require.LessOrEqual(t, runs.Load()-before, int64(2), "a burst of edits costs at most two more runs")
}
