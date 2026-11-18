package natsx_test

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// the earlier release's nats apply runs over the partitioned topology in a
// rollback before retire-legacy-web (docs/deploy/upgrade.md, "Rollback"):
// it puts WEB_EVENTS back to the single subject and drops the filter of the
// legacy consumer. both must succeed, and the partitioned release applies
// again afterwards.
func TestRollbackApplyOfEarlierRelease(t *testing.T) {
	srv := natsctr.StartSingle(t)
	_, js := connect(t, srv.URL)
	ctx := t.Context()
	previousWebEvents(t, js)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	// an event a gateway of this release queued before the rollback
	_, err = natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, "web.events.site-a.p07", "new-0", []byte("new"), nil)
	require.NoError(t, err)

	_, err = js.UpdateStream(ctx, jetstream.StreamConfig{
		Name: natsx.StreamWebEvents, Description: "accepted web events, subject token is the site id",
		Subjects: []string{natsx.SubjectWebEvents + ".*"}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: 72 * time.Hour, Duplicates: 2 * time.Minute,
		Discard: jetstream.DiscardOld, MaxMsgs: -1, MaxBytes: -1, MaxMsgSize: -1,
	})
	require.NoError(t, err, "stream update")
	_, err = js.UpdateConsumer(ctx, natsx.StreamWebEvents, jetstream.ConsumerConfig{
		Durable: natsx.ConsumerReplayer, Description: "replayer delivers web events to umami",
		DeliverPolicy: jetstream.DeliverAllPolicy, AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: natsx.AckWait, MaxDeliver: -1,
	})
	require.NoError(t, err, "consumer update")

	// the earlier replayer gets the partitioned event through its consumer
	cons, err := js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	require.NoError(t, err)
	batch, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	require.NoError(t, err)
	var got []string
	for m := range batch.Messages() {
		got = append(got, m.Subject())
		require.NoError(t, m.Nak())
	}
	require.Equal(t, []string{"web.events.site-a.p07"}, got)

	// deploying the partitioned release again restores its topology
	_, err = natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	changes, err := natsx.Diff(ctx, js, spec)
	require.NoError(t, err)
	for _, c := range changes {
		require.Equal(t, natsx.ActionUnchanged, c.Action, c.String())
	}
}
