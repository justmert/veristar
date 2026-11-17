package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// forwardEnv is one environment of the cutover: a real nats server with the
// production users and permissions and the applied topology.
type forwardEnv struct {
	url   string
	creds map[string]string
}

func startForwardEnv(t *testing.T) forwardEnv {
	t.Helper()
	users := natsx.Users()
	creds := map[string]string{}
	for _, u := range users {
		b := make([]byte, 24)
		_, err := rand.Read(b)
		require.NoError(t, err)
		creds[u.Name] = hex.EncodeToString(b)
	}
	auth, err := natsx.RenderAuthorization(users, creds)
	require.NoError(t, err)
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	e := forwardEnv{url: srv.URL, creds: creds}
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, e.js(t, natsx.ServicePlatformctl), spec)
	require.NoError(t, err)
	return e
}

func (e forwardEnv) env(service string) *config.Env {
	return config.FromMap(map[string]string{
		"NATS_URL": e.url, "NATS_REPLICAS": "1",
		"NATS_USER": service, "NATS_PASSWORD": e.creds[service],
	})
}

func (e forwardEnv) js(t *testing.T, service string) jetstream.JetStream {
	t.Helper()
	cfg, err := config.LoadNATS(e.env(service))
	require.NoError(t, err)
	nc, err := natsx.Connect(cfg, service, obs.NewLogger(&lockedBuffer{}, slog.LevelInfo))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	j, err := jetstream.New(nc)
	require.NoError(t, err)
	return j
}

func publishWithID(t *testing.T, ctx context.Context, j jetstream.JetStream, subject, id, body string) {
	t.Helper()
	m := nats.NewMsg(subject)
	m.Header.Set(jetstream.MsgIDHeader, id)
	m.Data = []byte(body)
	_, err := j.PublishMsg(ctx, m)
	require.NoError(t, err)
}

// streamBodies reads every message of a stream by sequence, as id=body.
func streamBodies(t *testing.T, ctx context.Context, j jetstream.JetStream, stream string) []string {
	t.Helper()
	s, err := j.Stream(ctx, stream)
	require.NoError(t, err)
	info, err := s.Info(ctx)
	require.NoError(t, err)
	var out []string
	for seq := info.State.FirstSeq; info.State.Msgs > 0 && seq <= info.State.LastSeq; seq++ {
		m, err := s.GetMsg(ctx, seq)
		require.NoError(t, err)
		out = append(out, m.Subject+" "+m.Header.Get(jetstream.MsgIDHeader)+"="+string(m.Data))
	}
	return out
}

// TestQueueForward moves queued web events, api metric batches and edge
// stats from one environment to another the way the handover cutover does
// (docs/handover/plan.md section 2.3): the old environment's consuming
// services are stopped, its edges keep accepting, and the forwarder binds
// their consumers and moves every message with its id, acking each only
// after the new environment's stream stored it.
func TestQueueForward(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	oldEnv, newEnv := startForwardEnv(t), startForwardEnv(t)
	gateway, collector := oldEnv.js(t, natsx.ServiceGateway), oldEnv.js(t, natsx.ServiceCollector)
	oldAdmin, newAdmin := oldEnv.js(t, natsx.ServicePlatformctl), newEnv.js(t, natsx.ServicePlatformctl)

	var wantWeb, wantAPI, wantStats []string
	for i := range 25 {
		id, body := fmt.Sprintf("ev-%d", i), fmt.Sprintf(`{"n":%d}`, i)
		publishWithID(t, ctx, gateway, "web.events.site-a.09", id, body)
		wantWeb = append(wantWeb, "web.events.site-a.09 "+id+"="+body)
	}
	for i := range 7 {
		id, body := fmt.Sprintf("batch-%d", i), fmt.Sprintf("metrics %d", i)
		publishWithID(t, ctx, collector, "api.metrics.tenant-b", id, body)
		wantAPI = append(wantAPI, "api.metrics.tenant-b "+id+"="+body)
	}
	for i := range 3 {
		id, body := fmt.Sprintf("edge-1.%d", i), fmt.Sprintf("stats %d", i)
		publishWithID(t, ctx, oldAdmin, "stats.web.edge-1", id, body)
		wantStats = append(wantStats, "stats.web.edge-1 "+id+"="+body)
	}

	run := func(args ...string) (string, error) {
		out := &strings.Builder{}
		err := runForwardWith(ctx, args, oldEnv.env(natsx.ServicePlatformctl), newEnv.env(natsx.ServicePlatformctl),
			out, obs.NewLogger(&lockedBuffer{}, slog.LevelInfo))
		return out.String(), err
	}

	t.Run("refuses the source as target", func(t *testing.T) {
		err := runForwardWith(ctx, []string{"forward"}, oldEnv.env(natsx.ServicePlatformctl), oldEnv.env(natsx.ServicePlatformctl),
			&strings.Builder{}, obs.NewLogger(&lockedBuffer{}, slog.LevelInfo))
		require.ErrorContains(t, err, "also a source server")
	})

	t.Run("moves every queue with its ids", func(t *testing.T) {
		out, err := run("forward", "--until-empty", "--idle", "1s", "--batch", "10")
		require.NoError(t, err)
		require.Contains(t, out, "api=7 stats=3 web=25")
		require.Equal(t, wantWeb, streamBodies(t, ctx, newAdmin, natsx.StreamWebEvents))
		require.Equal(t, wantAPI, streamBodies(t, ctx, newAdmin, natsx.StreamAPIMetrics))
		require.Equal(t, wantStats, streamBodies(t, ctx, newAdmin, natsx.StreamEdgeStats))
		// work-queue streams drop acked messages: the old side is empty
		for _, s := range []string{natsx.StreamWebEvents, natsx.StreamAPIMetrics, natsx.StreamEdgeStats} {
			require.Empty(t, streamBodies(t, ctx, oldAdmin, s), s)
		}
	})

	// a message accepted by an old edge after the move started (dns still
	// points at it) goes the same way; one the target already holds (a
	// crash between the target's ack and the source ack) is stored once
	t.Run("late events and a repeated id", func(t *testing.T) {
		publishWithID(t, ctx, gateway, "web.events.site-a.09", "ev-late", `{"late":true}`)
		// the target stored ev-dup before the source ack was lost
		publishWithID(t, ctx, newAdmin, "web.events.site-a.09", "ev-dup", `{"dup":true}`)
		publishWithID(t, ctx, gateway, "web.events.site-a.09", "ev-dup", `{"dup":true}`)
		out, err := run("forward", "--queues", "web", "--until-empty", "--idle", "1s")
		require.NoError(t, err)
		require.Contains(t, out, "web=2")
		got := streamBodies(t, ctx, newAdmin, natsx.StreamWebEvents)
		require.Equal(t, append(wantWeb, `web.events.site-a.09 ev-dup={"dup":true}`, `web.events.site-a.09 ev-late={"late":true}`), got)
		require.Empty(t, streamBodies(t, ctx, oldAdmin, natsx.StreamWebEvents))
	})

	// with the queue empty, a running replayer's pull request waits on the
	// consumer; the forwarder must not split the queue with it
	t.Run("refuses while the replayer still pulls", func(t *testing.T) {
		replayer := oldEnv.js(t, natsx.ServiceReplayer)
		c, err := replayer.Consumer(ctx, natsx.StreamWebEvents, natsx.ConsumerPartition(9))
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() {
			b, err := c.Fetch(1, jetstream.FetchMaxWait(8*time.Second))
			if err == nil {
				for range b.Messages() {
				}
				err = b.Error()
			}
			done <- err
		}()
		require.Eventually(t, func() bool {
			info, err := c.Info(ctx)
			return err == nil && info.NumWaiting > 0
		}, 5*time.Second, 50*time.Millisecond)
		_, err = run("forward", "--queues", "web", "--until-empty", "--idle", "1s")
		require.ErrorIs(t, err, errConsumerBusy)
		<-done
	})

	// a target without the stream refuses before anything moves
	t.Run("target without the topology", func(t *testing.T) {
		publishWithID(t, ctx, gateway, "web.events.site-a.09", "ev-kept", `{"kept":true}`)
		require.NoError(t, newAdmin.DeleteStream(ctx, natsx.StreamWebEvents))
		_, err := run("forward", "--queues", "web", "--until-empty", "--idle", "1s")
		require.ErrorContains(t, err, "target stream WEB_EVENTS")
		require.Equal(t, []string{`web.events.site-a.09 ev-kept={"kept":true}`}, streamBodies(t, ctx, oldAdmin, natsx.StreamWebEvents))
	})
}
