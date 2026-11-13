package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

// TestDLQ runs list, replay and purge against a real nats server with the
// production users and permissions. the dlq messages are made the way the
// replayer and the metrics writer make them: consumed from the work queue
// and moved with natsx.DeadLetter.
func TestDLQ(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	envOf := func(service string) *config.Env {
		return config.FromMap(map[string]string{
			"NATS_URL": srv.URL, "NATS_REPLICAS": "1",
			"NATS_USER": service, "NATS_PASSWORD": creds[service],
		})
	}
	logs := &lockedBuffer{}
	log := obs.NewLogger(logs, slog.LevelInfo)
	js := func(service string) jetstream.JetStream {
		t.Helper()
		cfg, err := config.LoadNATS(envOf(service))
		require.NoError(t, err)
		nc, err := natsx.Connect(cfg, service, log)
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		j, err := jetstream.New(nc)
		require.NoError(t, err)
		return j
	}
	admin := js(natsx.ServicePlatformctl)
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, admin, spec)
	require.NoError(t, err)
	gateway, collector := js(natsx.ServiceGateway), js(natsx.ServiceCollector)
	replayer, writer := js(natsx.ServiceReplayer), js(natsx.ServiceMetricsWriter)

	// deadLetter drains n messages of a work queue into its dlq, one reason
	// per message, as the consuming service does on a permanent failure
	deadLetter := func(j jetstream.JetStream, stream, consumer, dlqStream, dlqPrefix string, reasons ...string) {
		t.Helper()
		c, err := j.Consumer(ctx, stream, consumer)
		require.NoError(t, err)
		b, err := c.Fetch(len(reasons), jetstream.FetchMaxWait(5*time.Second))
		require.NoError(t, err)
		i := 0
		for m := range b.Messages() {
			require.NoError(t, natsx.DeadLetter(ctx, j, dlqStream, natsx.Subject(dlqPrefix, lastToken(m.Subject())), m, reasons[i]))
			require.NoError(t, m.DoubleAck(ctx))
			i++
		}
		require.NoError(t, b.Error())
		require.Equal(t, len(reasons), i)
	}
	for i, site := range []string{"site-a", "site-a", "site-a", "site-b"} {
		_, err := natsx.PublishDedup(ctx, gateway, natsx.StreamWebEvents, natsx.Subject(natsx.SubjectWebEvents, site),
			"e"+string(rune('1'+i)), []byte(`{"id":"e`+string(rune('1'+i))+`","secret":"payload-marker"}`), nil)
		require.NoError(t, err)
	}
	deadLetter(replayer, natsx.StreamWebEvents, natsx.ConsumerReplayer, natsx.StreamWebDLQ, natsx.SubjectWebDLQ,
		"umami 400: bad\nrequest", "umami 400: bad\nrequest", "schema: no site", "schema: no site")
	_, err = natsx.PublishDedup(ctx, collector, natsx.StreamAPIMetrics, natsx.Subject(natsx.SubjectAPIMetrics, "tenant-1"), "m1", []byte("batch"), nil)
	require.NoError(t, err)
	deadLetter(writer, natsx.StreamAPIMetrics, natsx.ConsumerMetricsWriter, natsx.StreamAPIDLQ, natsx.SubjectAPIDLQ, "decode: bad batch")

	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runDLQWith(ctx, args, envOf(natsx.ServicePlatformctl), &out, log)
		return out.String(), err
	}
	count := func(stream string) uint64 {
		t.Helper()
		s, err := admin.Stream(ctx, stream)
		require.NoError(t, err)
		info, err := s.Info(ctx)
		require.NoError(t, err)
		return info.State.Msgs
	}

	t.Run("list shows headers, never the payload", func(t *testing.T) {
		out, err := run("list", "--queue", "web")
		require.NoError(t, err)
		require.Contains(t, out, "4 message(s) in WEB_DLQ")
		require.Contains(t, out, "WEB_EVENTS:1 web.events.site-a")
		require.Contains(t, out, "umami 400: bad request")
		require.NotContains(t, out, "payload-marker")
		out, err = run("list", "--queue", "web", "--key", "site-b")
		require.NoError(t, err)
		require.Contains(t, out, "1 message(s) in WEB_DLQ")
		out, err = run("list", "--queue", "web", "--from", "2", "--to", "3")
		require.NoError(t, err)
		require.Contains(t, out, "2 message(s) in WEB_DLQ")
	})

	t.Run("destructive commands need an explicit selection and purge needs --yes", func(t *testing.T) {
		_, err := run("replay", "--queue", "web")
		require.ErrorContains(t, err, "select messages")
		_, err = run("purge", "--queue", "web", "--all")
		require.ErrorContains(t, err, "--yes")
		_, err = run("list", "--queue", "nope")
		require.Error(t, err)
		_, err = run("list", "--queue", "web", "--from", "3", "--to", "2")
		require.Error(t, err)
		_, err = run("list", "--queue", "web", "--key", "a.>")
		require.Error(t, err)
		require.Equal(t, uint64(4), count(natsx.StreamWebDLQ))
	})

	t.Run("replay a sequence range", func(t *testing.T) {
		out, err := run("replay", "--queue", "web", "--from", "1", "--to", "2")
		require.NoError(t, err)
		require.Contains(t, out, "replayed 2 message(s)")
		require.Equal(t, uint64(2), count(natsx.StreamWebDLQ))
		require.Equal(t, uint64(2), count(natsx.StreamWebEvents))

		s, err := admin.Stream(ctx, natsx.StreamWebEvents)
		require.NoError(t, err)
		m, err := s.GetMsg(ctx, 0, jetstream.WithGetMsgSubject(natsx.SubjectWebEvents+".>"))
		require.NoError(t, err)
		require.Equal(t, "web.events.site-a", m.Subject)
		require.JSONEq(t, `{"id":"e1","secret":"payload-marker"}`, string(m.Data), "the payload and its event id are kept")
		require.Empty(t, m.Header.Get(natsx.HeaderDLQReason))
		require.Equal(t, "WEB_DLQ.1", m.Header.Get(HeaderDLQReplay))
		require.Equal(t, "e1.replay.WEB_DLQ.1", m.Header.Get(jetstream.MsgIDHeader))
	})

	t.Run("replay after a crash between publish and delete stores once", func(t *testing.T) {
		// the earlier run published dlq message 3 and died before deleting it
		_, err := natsx.PublishDedup(ctx, admin, natsx.StreamWebEvents, "web.events.site-a", replayID("e3", natsx.StreamWebDLQ, 3), []byte("x"), nil)
		require.NoError(t, err)
		out, err := run("replay", "--queue", "web", "--reason", "schema")
		require.NoError(t, err)
		require.Contains(t, out, "replayed 2 message(s) from WEB_DLQ to WEB_EVENTS (1 already there")
		require.Zero(t, count(natsx.StreamWebDLQ))
		require.Equal(t, uint64(4), count(natsx.StreamWebEvents))
	})

	t.Run("a message dead-lettered again replays with a stable id", func(t *testing.T) {
		deadLetter(replayer, natsx.StreamWebEvents, natsx.ConsumerReplayer, natsx.StreamWebDLQ, natsx.SubjectWebDLQ, "still failing")
		out, err := run("list", "--queue", "web")
		require.NoError(t, err)
		require.Contains(t, out, "still failing")
		_, err = run("replay", "--queue", "web", "--all")
		require.NoError(t, err)
		s, err := admin.Stream(ctx, natsx.StreamWebEvents)
		require.NoError(t, err)
		info, err := s.Info(ctx)
		require.NoError(t, err)
		m, err := s.GetMsg(ctx, info.State.LastSeq)
		require.NoError(t, err)
		require.Equal(t, "e1.replay.WEB_DLQ.5", m.Header.Get(jetstream.MsgIDHeader))
	})

	t.Run("replay refuses a subject outside the original stream", func(t *testing.T) {
		h := nats.Header{}
		h.Set(natsx.HeaderDLQSubject, "api.metrics.tenant-1")
		// written as the replayer, the only user that may publish to the web dlq
		_, err := natsx.PublishDedup(ctx, replayer, natsx.StreamWebDLQ, "dlq.web.site-a", "forged", []byte("x"), h)
		require.NoError(t, err)
		out, err := run("replay", "--queue", "web", "--all")
		require.Error(t, err)
		require.Contains(t, out, "skipped 1")
		require.Equal(t, uint64(1), count(natsx.StreamWebDLQ), "a skipped message stays")
	})

	t.Run("purge api", func(t *testing.T) {
		out, err := run("list", "--queue", "api")
		require.NoError(t, err)
		require.Contains(t, out, "decode: bad batch")
		out, err = run("purge", "--queue", "api", "--all", "--yes")
		require.NoError(t, err)
		require.Contains(t, out, "deleted 1 message(s) from API_DLQ")
		require.Zero(t, count(natsx.StreamAPIDLQ))
	})

	require.NotContains(t, strings.ToLower(logs.String()), "permissions violation")
}
