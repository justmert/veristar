package erasure

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/replayer"
)

// staleKV answers every get as a replica that has not applied the last
// puts: the key is not there yet.
type staleKV struct{ jetstream.KeyValue }

func (staleKV) Get(context.Context, string) (jetstream.KeyValueEntry, error) {
	return nil, jetstream.ErrKeyNotFound
}

// TestVisitStateReadsTheLeader: the run reads a visitor's record and its
// progress record on the stream leader, so a lagging replica that has not
// applied the replayer's last put cannot hide a session from it.
func TestVisitStateReadsTheLeader(t *testing.T) {
	srv := jobstest.NATS(t)
	nc, js := jobstest.Connect(t, srv.URL)
	ctx := t.Context()
	kv, err := js.KeyValue(ctx, natsx.BucketVisitState)
	require.NoError(t, err)
	st := stores{nc: nc, visits: staleKV{kv}}

	const site, vt = "0f3f3f9f-bf01-4dab-b4d4-7546a9c498c2", "ab12"
	rec, err := visitRecord(ctx, st, site, vt)
	require.NoError(t, err)
	require.Nil(t, rec)

	var want replayer.VisitRecord
	b, err := json.Marshal(want)
	require.NoError(t, err)
	_, err = kv.Put(ctx, replayer.VisitKey(site, vt), b)
	require.NoError(t, err)
	rec, err = visitRecord(ctx, st, site, vt)
	require.NoError(t, err)
	require.NotNil(t, rec, "the leader holds the record the replica has not applied")

	require.NoError(t, savePending(ctx, kv, site, vt, &want))
	p, err := pendingErasure(ctx, st, site, vt)
	require.NoError(t, err)
	require.NotNil(t, p)

	// a purged or deleted key reads as absent
	require.NoError(t, purgeVisit(ctx, kv, site, vt))
	rec, err = visitRecord(ctx, st, site, vt)
	require.NoError(t, err)
	require.Nil(t, rec)
	require.NoError(t, kv.Delete(ctx, pendingKey(site, vt)))
	p, err = pendingErasure(ctx, st, site, vt)
	require.NoError(t, err)
	require.Nil(t, p)
}
