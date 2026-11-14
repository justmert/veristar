package tenantauthextension

import (
	"context"
	"crypto/rand"
	"log/slog"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/justmert/veristar/collector/internal/natsconn"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// newToken returns a token in the otlp token format.
func newToken(t *testing.T) string {
	t.Helper()
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 40)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			t.Fatal(err)
		}
		b[i] = alphabet[n.Int64()]
	}
	return "sa_otlp_" + string(b)
}

// startNATS runs a nats server with the real per-service users, applies the
// topology as platformctl and returns the url and a control plane publisher.
func startNATS(t *testing.T) (string, *natsctr.Server, *registry.Publisher) {
	t.Helper()
	creds := map[string]string{}
	for _, u := range natsx.Users() {
		creds[u.Name] = "pw-" + u.Name
	}
	auth, err := natsx.RenderAuthorization(natsx.Users(), creds)
	if err != nil {
		t.Fatal(err)
	}
	srv := natsctr.StartSingle(t, natsctr.WithConfig(string(auth)))
	connect := func(user string) jetstream.JetStream {
		nc, err := nats.Connect(srv.URL, nats.UserInfo(user, creds[user]), nats.CustomInboxPrefix(natsx.InboxPrefix(user)),
			nats.MaxReconnects(-1), nats.ReconnectWait(100*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(nc.Close)
		js, err := jetstream.New(nc)
		if err != nil {
			t.Fatal(err)
		}
		return js
	}
	spec, err := natsx.Topology(1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := natsx.Apply(ctx, connect(natsx.ServicePlatformctl), spec); err != nil {
		t.Fatal(err)
	}
	pub, err := registry.NewPublisher(ctx, connect(natsx.ServiceControlPlane))
	if err != nil {
		t.Fatal(err)
	}
	return srv.URL, srv, pub
}

func TestBearerParsing(t *testing.T) {
	tok := newToken(t)
	for _, c := range []struct {
		name    string
		sources map[string][]string
		ok      bool
	}{
		{"http canonical key", map[string][]string{"Authorization": {"Bearer " + tok}}, true},
		{"grpc lowercase key", map[string][]string{"authorization": {"Bearer " + tok}}, true},
		{"scheme case", map[string][]string{"authorization": {"bearer " + tok}}, true},
		{"missing", map[string][]string{"Content-Type": {"application/x-protobuf"}}, false},
		{"basic scheme", map[string][]string{"Authorization": {"Basic " + tok}}, false},
		{"no scheme", map[string][]string{"Authorization": {tok}}, false},
		{"two values", map[string][]string{"Authorization": {"Bearer " + tok, "Bearer " + tok}}, false},
		{"two keys", map[string][]string{"Authorization": {"Bearer " + tok}, "authorization": {"Bearer " + tok}}, false},
		{"wrong prefix", map[string][]string{"Authorization": {"Bearer sa_op_" + tok[8:]}}, false},
		{"too short", map[string][]string{"Authorization": {"Bearer " + tok[:47]}}, false},
		{"bad char", map[string][]string{"Authorization": {"Bearer " + tok[:47] + "-"}}, false},
	} {
		got, err := bearer(c.sources)
		if c.ok != (err == nil) {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if c.ok && got != tok {
			t.Errorf("%s: token = %q", c.name, got)
		}
		if err != nil && status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: code = %v", c.name, status.Code(err))
		}
	}
}

// a watcher that never ran has no snapshot: every request is refused with
// Unavailable, even one with a well formed token.
func TestFailsClosedWithoutSnapshot(t *testing.T) {
	url, _, _ := startNATS(t)
	nc, err := nats.Connect(url, nats.UserInfo("collector", "pw-collector"), nats.CustomInboxPrefix(natsx.InboxPrefix("collector")))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	w := registry.NewOTLPWatcher(js, slog.New(slog.DiscardHandler))
	_, err = authenticate(context.Background(), w, map[string][]string{"authorization": {"Bearer " + newToken(t)}})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("unloaded snapshot: got %v, want Unavailable", err)
	}
	// and the extension before Start
	a := newAuthenticator(&Config{}, extensiontest.NewNopSettings(Type))
	_, err = a.Authenticate(context.Background(), map[string][]string{"authorization": {"Bearer " + newToken(t)}})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("not started: got %v, want Unavailable", err)
	}
}

func TestAuthenticateAgainstRegistry(t *testing.T) {
	url, _, pub := startNATS(t)
	ctx := context.Background()
	tok := newToken(t)
	entry := registry.OTLPToken{Hash: registry.HashOTLPToken(tok), TenantID: uuid.NewString(), TokenID: uuid.NewString(), Status: registry.StatusActive}
	if err := pub.PutOTLPToken(ctx, entry); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{NATS: natsconn.Config{URL: url, User: "collector", Password: configopaque.String("pw-collector")}, SnapshotTimeout: 30 * time.Second}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ext, err := NewFactory().Create(ctx, extensiontest.NewNopSettings(Type), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ext.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ext.Shutdown(context.Background()) })
	a := ext.(*authenticator)
	// Start returns only with a loaded snapshot
	if !a.reg.Loaded() {
		t.Fatal("Start returned before the snapshot was loaded")
	}

	for _, key := range []string{"Authorization", "authorization"} {
		got, err := a.Authenticate(ctx, map[string][]string{key: {"Bearer " + tok}})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		tid, kid, err := FromContext(got)
		if err != nil || tid != entry.TenantID || kid != entry.TokenID {
			t.Fatalf("%s: auth data %q %q %v", key, tid, kid, err)
		}
	}

	if _, err := a.Authenticate(ctx, map[string][]string{"authorization": {"Bearer " + newToken(t)}}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unknown token: %v", err)
	}

	// revocation (status change) and removal both take effect within 5 s
	waitRefused := func(what string) {
		t.Helper()
		start := time.Now()
		for {
			_, err := a.Authenticate(ctx, map[string][]string{"authorization": {"Bearer " + tok}})
			if status.Code(err) == codes.Unauthenticated {
				t.Logf("%s refused after %v", what, time.Since(start))
				return
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("%s: still accepted after 5 s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	entry.Status = "revoked"
	if err := pub.PutOTLPToken(ctx, entry); err != nil {
		t.Fatal(err)
	}
	waitRefused("revoked token")

	entry.Status = registry.StatusActive
	if err := pub.PutOTLPToken(ctx, entry); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := a.Authenticate(ctx, map[string][]string{"authorization": {"Bearer " + tok}}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reactivated token not accepted within 5 s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := pub.DeleteOTLPToken(ctx, entry.Hash); err != nil {
		t.Fatal(err)
	}
	waitRefused("deleted token")
}

// without the registry bucket no snapshot can load: Start fails after the
// timeout instead of letting receivers open with nothing to check against.
func TestStartFailsWithoutSnapshot(t *testing.T) {
	srv := natsctr.StartSingle(t)
	cfg := &Config{NATS: natsconn.Config{URL: srv.URL}, SnapshotTimeout: 2 * time.Second}
	ext, err := NewFactory().Create(context.Background(), extensiontest.NewNopSettings(Type), cfg)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = ext.Start(context.Background(), nil)
	if err == nil {
		t.Fatal("Start succeeded without a snapshot")
	}
	if d := time.Since(start); d < 2*time.Second || d > 10*time.Second {
		t.Fatalf("Start returned after %v", d)
	}
	t.Logf("start refused after %v: %v", time.Since(start), err)
}

func TestConfigValidate(t *testing.T) {
	for _, c := range []Config{
		{},
		{NATS: natsconn.Config{URL: "http://x:4222"}},
		{NATS: natsconn.Config{URL: "nats://u:p@x:4222"}},
		{NATS: natsconn.Config{URL: "nats://x:4222", User: "collector"}, SnapshotTimeout: time.Second},
		{NATS: natsconn.Config{URL: "nats://x:4222"}},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("config %+v accepted", c.NATS)
		}
	}
}
