package natsctr

import (
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// a cluster that StartCluster or Restart returns takes a bucket with three
// replicas at once: every test that applies the three-replica topology
// right after starting relies on it
func TestClusterPlacesThreeReplicasAtOnce(t *testing.T) {
	c := StartCluster(t)
	create := func(bucket string) {
		t.Helper()
		nc, err := nats.Connect(c.URLs())
		if err != nil {
			t.Fatal(err)
		}
		defer nc.Close()
		js, err := jetstream.New(nc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := js.CreateKeyValue(t.Context(), jetstream.KeyValueConfig{Bucket: bucket, Replicas: 3}); err != nil {
			t.Fatalf("create %s: %v", bucket, err)
		}
	}
	create("after_start")
	c.Restart(t)
	create("after_restart")
}
