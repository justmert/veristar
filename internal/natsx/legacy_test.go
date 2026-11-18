package natsx_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// previousWebEvents creates WEB_EVENTS and its consumer as the releases
// before the partitions declared them: one subject per site and one
// unfiltered consumer.
func previousWebEvents(t *testing.T, js jetstream.JetStream) {
	t.Helper()
	ctx := t.Context()
	_, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name: natsx.StreamWebEvents, Description: "accepted web events, subject token is the site id",
		Subjects: []string{natsx.SubjectWebEvents + ".*"}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: 72 * time.Hour, Duplicates: 2 * time.Minute,
		Discard: jetstream.DiscardOld, MaxMsgs: -1, MaxBytes: -1, MaxMsgSize: -1,
	})
	require.NoError(t, err)
	_, err = js.CreateConsumer(ctx, natsx.StreamWebEvents, jetstream.ConsumerConfig{
		Durable: natsx.ConsumerReplayer, Description: "replayer delivers web events to umami",
		DeliverPolicy: jetstream.DeliverAllPolicy, AckPolicy: jetstream.AckExplicitPolicy,
		AckWait: natsx.AckWait, MaxDeliver: -1,
	})
	require.NoError(t, err)
}

// an upgrade applies the partitioned topology over the previous one: the
// legacy subject and its consumer stay, filtered so they no longer overlap
// the partitions, until retire-legacy-web removes them once they are empty.
// a later apply does not bring them back.
func TestUpgradeKeepsLegacyUntilRetired(t *testing.T) {
	srv := natsctr.StartSingle(t)
	_, js := connect(t, srv.URL)
	ctx := t.Context()
	previousWebEvents(t, js)
	for i := range 3 {
		_, err := natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, "web.events.site-a", fmt.Sprintf("old-%d", i), []byte("old"), nil)
		require.NoError(t, err)
	}

	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	changes, err := natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	var legacyUpdated bool
	for _, c := range changes {
		if c.Name == natsx.StreamWebEvents+"/"+natsx.ConsumerReplayer {
			legacyUpdated = c.Action == natsx.ActionUpdate
		}
	}
	require.True(t, legacyUpdated, "the legacy consumer gets its filter: %v", changes)

	s, err := js.Stream(ctx, natsx.StreamWebEvents)
	require.NoError(t, err)
	require.Equal(t, []string{natsx.WebEventsLegacy, natsx.WebEventsPartitioned}, s.CachedInfo().Config.Subjects)
	legacy, err := js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	require.NoError(t, err)
	require.Equal(t, natsx.WebEventsLegacy, legacy.CachedInfo().Config.FilterSubject)
	require.Equal(t, uint64(3), legacy.CachedInfo().NumPending, "the queued events stay with the legacy consumer")

	// both kinds are accepted while the upgrade runs, and each consumer sees
	// only its own
	_, err = natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, "web.events.site-a", "old-late", []byte("old"), nil)
	require.NoError(t, err)
	_, err = natsx.PublishDedup(ctx, js, natsx.StreamWebEvents, "web.events.site-a.07", "new-1", []byte("new"), nil)
	require.NoError(t, err)
	p7, err := js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerPartition(7))
	require.NoError(t, err)
	info, err := p7.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), info.NumPending)
	info, err = legacy.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(4), info.NumPending)

	again, err := natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	for _, c := range again {
		require.Equal(t, natsx.ActionUnchanged, c.Action, c.String())
	}

	// retiring refuses while legacy events wait
	_, err = natsx.RetireLegacyWeb(ctx, js, 0, 10*time.Millisecond)
	require.ErrorIs(t, err, natsx.ErrLegacyNotEmpty)
	st, err := natsx.ReadLegacyWeb(ctx, js)
	require.NoError(t, err)
	require.Equal(t, natsx.LegacyWebState{Subject: true, Consumer: true, Messages: 4}, st)

	// a drain that finishes while retire waits
	go func() {
		time.Sleep(300 * time.Millisecond)
		b, err := legacy.Fetch(10, jetstream.FetchMaxWait(2*time.Second))
		if err != nil {
			return
		}
		for m := range b.Messages() {
			_ = m.DoubleAck(context.Background())
		}
	}()
	st, err = natsx.RetireLegacyWeb(ctx, js, 30*time.Second, 50*time.Millisecond)
	require.NoError(t, err)
	require.True(t, st.Retired(), "%+v", st)

	s, err = js.Stream(ctx, natsx.StreamWebEvents)
	require.NoError(t, err)
	require.Equal(t, []string{natsx.WebEventsPartitioned}, s.CachedInfo().Config.Subjects)
	_, err = js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	require.ErrorIs(t, err, jetstream.ErrConsumerNotFound)
	// a gateway of the earlier release now gets an error instead of
	// queueing an event nobody would deliver
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = natsx.PublishDedup(pctx, js, natsx.StreamWebEvents, "web.events.site-a", "old-after", []byte("old"), nil)
	require.Error(t, err)
	// the partitioned event is untouched
	info, err = p7.Info(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), info.NumPending)

	// apply after the retirement creates nothing of the legacy part
	after, err := natsx.Apply(ctx, js, spec)
	require.NoError(t, err)
	for _, c := range after {
		require.Equal(t, natsx.ActionUnchanged, c.Action, c.String())
	}
	_, err = js.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerReplayer)
	require.ErrorIs(t, err, jetstream.ErrConsumerNotFound)
	// and a retire on a retired stream is a no-op
	st, err = natsx.RetireLegacyWeb(ctx, js, 0, time.Millisecond)
	require.NoError(t, err)
	require.True(t, st.Retired())
}

// a new installation never gets the legacy subject or consumer.
func TestFreshTopologyHasNoLegacy(t *testing.T) {
	srv := natsctr.StartSingle(t)
	_, js := connect(t, srv.URL)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(t.Context(), js, spec)
	require.NoError(t, err)
	st, err := natsx.ReadLegacyWeb(t.Context(), js)
	require.NoError(t, err)
	require.True(t, st.Retired(), "%+v", st)
}
