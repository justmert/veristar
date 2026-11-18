package erasure

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// erasure waits for every queued event of the site accepted before the
// request: on each of its partitions and, while an upgrade drains it, on the
// legacy subject. events of other sites and later ones do not hold it up.
// it runs as the control plane's own nats user, so a read its permissions
// lack fails here.
func TestWaitQueueCoversEveryPartition(t *testing.T) {
	users := natsx.Users()
	creds := map[string]string{}
	for _, u := range users {
		creds[u.Name] = "pw-" + u.Name
	}
	auth, err := natsx.RenderAuthorization(users, creds)
	require.NoError(t, err)
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	conn := func(service string) jetstream.JetStream {
		cfg, err := config.LoadNATS(config.FromMap(map[string]string{
			"NATS_URL": srv.URL, "NATS_REPLICAS": "1", "NATS_USER": service, "NATS_PASSWORD": creds[service],
		}))
		require.NoError(t, err)
		nc, err := natsx.Connect(cfg, service, obs.NewLogger(&discard{}, slog.LevelError))
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		js, err := jetstream.New(nc)
		require.NoError(t, err)
		return js
	}
	admin := conn(natsx.ServicePlatformctl)
	// the stream as an upgrade leaves it: the legacy subject kept next to
	// the partitions
	_, err = admin.CreateStream(ctx, jetstream.StreamConfig{
		Name: natsx.StreamWebEvents, Subjects: []string{natsx.WebEventsLegacy}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: 72 * time.Hour, Duplicates: 2 * time.Minute,
	})
	require.NoError(t, err)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, admin, spec)
	require.NoError(t, err)

	gw := conn(natsx.ServiceGateway)
	pub := func(subject, id string) uint64 {
		ack, err := natsx.PublishDedup(ctx, gw, natsx.StreamWebEvents, subject, id, []byte("e"), nil)
		require.NoError(t, err)
		return ack.Sequence
	}
	legacy := pub("web.events.site-a", "l1")
	p3 := pub("web.events.site-a.03", "p3")
	p17 := pub("web.events.site-a.17", "p17")
	pub("web.events.site-b.17", "other-site")
	cp := conn(natsx.ServiceControlPlane)
	events, err := cp.Stream(ctx, natsx.StreamWebEvents)
	require.NoError(t, err)
	snap, err := lastSeq(ctx, events)
	require.NoError(t, err)
	pub("web.events.site-a.05", "after-the-request")

	adminEvents, err := admin.Stream(ctx, natsx.StreamWebEvents)
	require.NoError(t, err)
	for _, seq := range []uint64{legacy, p3, p17} {
		require.ErrorIs(t, waitQueue(ctx, events, "site-a", snap, 200*time.Millisecond), errQueueBehind, "before deleting %d", seq)
		// stands in for the replayer's ack, which removes the message
		require.NoError(t, adminEvents.DeleteMsg(ctx, seq))
	}
	require.NoError(t, waitQueue(ctx, events, "site-a", snap, 200*time.Millisecond))

	// after retire-legacy-web the stream has no legacy subject; the read of
	// it finds nothing instead of failing
	st, err := natsx.RetireLegacyWeb(ctx, admin, 0, time.Millisecond)
	require.NoError(t, err)
	require.True(t, st.Retired(), "%+v", st)
	pub("web.events.site-a.11", "p11")
	snap, err = lastSeq(ctx, events)
	require.NoError(t, err)
	require.ErrorIs(t, waitQueue(ctx, events, "site-a", snap, 200*time.Millisecond), errQueueBehind)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
