package natsx_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/replayer"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// W-12: visit_state shares each node's memory store with edge_secrets. it
// grows with every delivered event (a sent.<event id> key for 24 hours), so
// it is capped below the store, and edge_secrets, which every ingest needs,
// always has room.
func TestVisitStateLeavesRoomForEdgeSecrets(t *testing.T) {
	spec, err := natsx.Topology(3)
	require.NoError(t, err)
	var visit jetstream.KeyValueConfig
	for _, b := range spec.Buckets {
		if b.Bucket == natsx.BucketVisitState {
			visit = b
		}
	}
	require.Positive(t, visit.MaxBytes, "visit_state needs a byte limit")

	// the store size the edge role gives every nats server
	b, err := os.ReadFile(filepath.Join(natsctr.RepoRoot(t), "infra", "ansible", "roles", "edge", "defaults", "main.yml"))
	require.NoError(t, err)
	m := regexp.MustCompile(`(?m)^edge_nats_max_memory_store:\s*(\d+)M\s*$`).FindSubmatch(b)
	require.NotNil(t, m, "edge_nats_max_memory_store in megabytes")
	mib, err := strconv.ParseInt(string(m[1]), 10, 64)
	require.NoError(t, err)
	store := mib << 20
	require.LessOrEqual(t, visit.MaxBytes, store*3/4, "a quarter of the store stays free for edge_secrets and locks")

	// measured size of one duplicate guard key, so the docs can state how
	// many delivered events a day the cap holds
	srv := natsctr.StartSingle(t)
	nc, err := nats.Connect(srv.URL)
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	single, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), js, single)
	require.NoError(t, err)
	kv, err := js.KeyValue(t.Context(), natsx.BucketVisitState)
	require.NoError(t, err)
	const n = 2000
	for range n {
		_, err := kv.Put(t.Context(), replayer.SentKey(uuid.Must(uuid.NewV7()).String()), []byte{'1'})
		require.NoError(t, err)
	}
	st, err := js.Stream(t.Context(), "KV_"+natsx.BucketVisitState)
	require.NoError(t, err)
	info, err := st.Info(t.Context())
	require.NoError(t, err)
	require.Equal(t, visit.MaxBytes, info.Config.MaxBytes)
	perKey := float64(info.State.Bytes) / n
	perDay := float64(visit.MaxBytes) / perKey
	t.Logf("%.0f bytes per delivered event, %.1f million delivered events a day fit in %d MiB", perKey, perDay/1e6, visit.MaxBytes>>20)
	require.Greater(t, perDay, 2e6, "the cap holds at least two million delivered events a day")
}
