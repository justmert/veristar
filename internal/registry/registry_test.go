package registry_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

func site(status string) registry.Site {
	return registry.Site{
		ID:                  uuid.NewString(),
		TenantID:            uuid.NewString(),
		Status:              status,
		Origins:             []string{"https://example.org"},
		QuarantineWebsiteID: uuid.NewString(),
	}
}

func TestWatcherSurvivesRestart(t *testing.T) {
	srv := natsctr.StartSingle(t)
	nc, err := nats.Connect(srv.URL, nats.MaxReconnects(-1), nats.ReconnectWait(200*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	spec, err := natsx.Topology(1)
	require.NoError(t, err)
	_, err = natsx.Apply(ctx, js, spec)
	require.NoError(t, err)

	pub, err := registry.NewPublisher(ctx, js)
	require.NoError(t, err)
	before := site(registry.StatusActive)
	require.NoError(t, pub.PutSite(ctx, before))

	log := slog.New(slog.DiscardHandler)
	w := registry.NewSiteWatcher(js, log)
	ow := registry.NewOTLPWatcher(js, log)
	require.False(t, w.Loaded())
	go func() { _ = w.Run(ctx) }()
	go func() { _ = ow.Run(ctx) }()
	require.Eventually(t, w.Loaded, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, ow.Loaded, 10*time.Second, 20*time.Millisecond)
	got, ok := w.Get(before.ID)
	require.True(t, ok)
	require.Equal(t, before, got)
	snap, loadedGen := w.SnapshotAt()
	require.Equal(t, map[string]registry.Site{before.ID: before}, snap)
	require.Positive(t, loadedGen, "the loaded snapshot is a change")
	require.Equal(t, loadedGen, w.Generation())

	// updates and deletes arrive, and each one moves the generation
	suspended := before
	suspended.Status = "suspended"
	require.NoError(t, pub.PutSite(ctx, suspended))
	require.Eventually(t, func() bool { s, _ := w.Get(before.ID); return !s.Active() }, 5*time.Second, 20*time.Millisecond)
	_, gen := w.SnapshotAt()
	require.Greater(t, gen, loadedGen, "an update moves the generation")
	gone := site(registry.StatusActive)
	require.NoError(t, pub.PutSite(ctx, gone))
	require.Eventually(t, func() bool { _, ok := w.Get(gone.ID); return ok }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, pub.DeleteSite(ctx, gone.ID))
	require.Eventually(t, func() bool { _, ok := w.Get(gone.ID); return !ok }, 5*time.Second, 20*time.Millisecond)

	// an unreadable value is dropped, not kept at its old value
	kv, err := js.KeyValue(ctx, natsx.BucketRegistrySites)
	require.NoError(t, err)
	_, err = kv.Put(ctx, before.ID, []byte(`{"status":`))
	require.NoError(t, err)
	require.Eventually(t, func() bool { _, ok := w.Get(before.ID); return !ok }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, pub.PutSite(ctx, before))
	require.Eventually(t, func() bool { _, ok := w.Get(before.ID); return ok }, 5*time.Second, 20*time.Millisecond)

	// otlp tokens
	token := registry.OTLPToken{Hash: registry.HashOTLPToken("sa_otlp_example"), TenantID: uuid.NewString(), TokenID: uuid.NewString(), Status: registry.StatusActive}
	require.NoError(t, pub.PutOTLPToken(ctx, token))
	require.Eventually(t, func() bool { v, ok := ow.Get(token.Hash); return ok && v.Active() }, 5*time.Second, 20*time.Millisecond)

	// nats goes away: the snapshot keeps serving
	srv.Stop(t)
	require.Eventually(t, func() bool { return !nc.IsConnected() }, 10*time.Second, 20*time.Millisecond)
	time.Sleep(3 * time.Second)
	got, ok = w.Get(before.ID)
	require.True(t, ok, "snapshot lost while nats was down")
	require.True(t, got.Active())
	require.True(t, w.Loaded())
	_, ok = ow.Get(token.Hash)
	require.True(t, ok)

	// nats comes back with its file storage: new writes reach the watcher
	srv.Start(t)
	require.Eventually(t, nc.IsConnected, 30*time.Second, 50*time.Millisecond)
	after := site(registry.StatusActive)
	require.Eventually(t, func() bool { return pub.PutSite(ctx, after) == nil }, 30*time.Second, 200*time.Millisecond)
	require.Eventually(t, func() bool { _, ok := w.Get(after.ID); return ok }, 15*time.Second, 50*time.Millisecond)
	_, ok = w.Get(before.ID)
	require.True(t, ok, "entries from before the restart are still present")
	require.Equal(t, 2, w.Len())
}

func TestValidation(t *testing.T) {
	s := site(registry.StatusActive)
	require.NoError(t, s.Validate())
	bad := s
	bad.ID = "not-a-uuid"
	require.Error(t, bad.Validate())
	bad = s
	bad.Origins = nil
	require.Error(t, bad.Validate(), "an active site without origins")
	bad = s
	bad.Status = "Active!"
	require.Error(t, bad.Validate())
	require.Equal(t, 64, len(registry.HashOTLPToken("x")))
	_, err := registry.DecodeOTLPToken("ABC", []byte(`{}`))
	require.Error(t, err)
}
