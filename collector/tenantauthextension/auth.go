package tenantauthextension

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensionauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/justmert/stellar-analytics/collector/internal/natsconn"
	"github.com/justmert/stellar-analytics/internal/registry"
)

// attribute names of the auth data. tenantguard reads them; they are the
// public contract of this authenticator.
const (
	AttrTenantID = "tenant_id"
	AttrTokenID  = "token_id"
)

// tokenRe is the otlp token format. anything else is
// refused before hashing, so arbitrary header values never reach the lookup.
var tokenRe = regexp.MustCompile(`^sa_otlp_[0-9A-Za-z]{40}$`)

// errors returned to clients. they never contain the token. a refused
// credential is Unauthenticated (401 over http). a missing snapshot is
// Unavailable over grpc so clients retry; confighttp answers every
// authenticator error with 401, which is documented in docs/reference/collector.md.
var (
	errNoCredential = status.Error(codes.Unauthenticated, "missing bearer token")
	errBadToken     = status.Error(codes.Unauthenticated, "invalid token")
	errNotLoaded    = status.Error(codes.Unavailable, "token registry not loaded yet")
)

// snapshot is what Authenticate needs from a registry watcher.
type snapshot interface {
	Loaded() bool
	Get(key string) (registry.OTLPToken, bool)
}

type authenticator struct {
	cfg *Config
	log *slog.Logger
	set extension.Settings

	mu     sync.Mutex
	nc     *nats.Conn
	cancel context.CancelFunc
	done   chan struct{}
	reg    snapshot
}

var (
	_ extension.Extension  = (*authenticator)(nil)
	_ extensionauth.Server = (*authenticator)(nil)
)

func newAuthenticator(cfg *Config, set extension.Settings) *authenticator {
	return &authenticator{cfg: cfg, set: set, log: natsconn.Logger(set.ID.String())}
}

// Start connects to nats, starts the registry watch and returns only once
// the first full snapshot is loaded. the collector starts its receivers
// after its extensions, so no otlp port is open and the health_check
// endpoint stays unavailable (503) until tokens can be checked; edge caddy
// health-checks that endpoint, so clients get a retryable 502 or 503 during
// the window instead of a 401 that sdks treat as final. the component status
// is Starting until then and OK after. if no snapshot loads within
// snapshot_timeout, Start fails and the collector exits. the watch lives
// until Shutdown, so it does not inherit Start's context.
func (a *authenticator) Start(ctx context.Context, host component.Host) error { //nolint:contextcheck // see above
	componentstatus.ReportStatus(host, componentstatus.NewEvent(componentstatus.StatusStarting))
	nc, err := a.cfg.NATS.Connect(a.log)
	if err != nil {
		return err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return err
	}
	w := registry.NewOTLPWatcher(js, a.log)
	wctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(wctx)
	}()
	a.mu.Lock()
	a.nc, a.cancel, a.done, a.reg = nc, cancel, done, w
	a.mu.Unlock()

	if err := waitLoaded(ctx, w, a.cfg.SnapshotTimeout); err != nil {
		_ = a.Shutdown(ctx)
		return err
	}
	componentstatus.ReportStatus(host, componentstatus.NewEvent(componentstatus.StatusOK))
	a.log.Info("token registry loaded, accepting requests")
	return nil
}

// loadPoll is how often Start checks for the first snapshot.
const loadPoll = 50 * time.Millisecond

func waitLoaded(ctx context.Context, w snapshot, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(loadPoll)
	defer tick.Stop()
	for !w.Loaded() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("tenantauth: token registry not loaded within %v", timeout)
		case <-tick.C:
		}
	}
	return nil
}

// Shutdown stops the watch and closes the connection.
func (a *authenticator) Shutdown(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		<-a.done
	}
	if a.nc != nil {
		a.nc.Close()
	}
	a.reg = nil
	return nil
}

// bearer finds the authorization header. http passes canonical keys
// (Authorization), grpc passes lowercase metadata keys (authorization), so
// the key is matched without case. exactly one value is accepted: two
// headers are ambiguous and are refused rather than choosing one.
func bearer(sources map[string][]string) (string, error) {
	var vals []string
	for k, v := range sources {
		if strings.EqualFold(k, "authorization") {
			vals = append(vals, v...)
		}
	}
	if len(vals) != 1 {
		return "", errNoCredential
	}
	scheme, tok, ok := strings.Cut(strings.TrimSpace(vals[0]), " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return "", errNoCredential
	}
	tok = strings.TrimSpace(tok)
	if !tokenRe.MatchString(tok) {
		return "", errBadToken
	}
	return tok, nil
}

// Authenticate maps the bearer token to its tenant.
func (a *authenticator) Authenticate(ctx context.Context, sources map[string][]string) (context.Context, error) {
	a.mu.Lock()
	reg := a.reg
	a.mu.Unlock()
	return authenticate(ctx, reg, sources)
}

func authenticate(ctx context.Context, reg snapshot, sources map[string][]string) (context.Context, error) {
	tok, err := bearer(sources)
	if err != nil {
		return ctx, err
	}
	if reg == nil || !reg.Loaded() {
		return ctx, errNotLoaded
	}
	t, ok := reg.Get(registry.HashOTLPToken(tok))
	if !ok || !t.Active() {
		return ctx, errBadToken
	}
	return ContextWith(ctx, t), nil
}

// ContextWith returns ctx carrying the auth data of an accepted token, the
// way Authenticate passes it down the pipeline.
func ContextWith(ctx context.Context, t registry.OTLPToken) context.Context {
	info := client.FromContext(ctx)
	info.Auth = authData{tenantID: t.TenantID, tokenID: t.TokenID}
	return client.NewContext(ctx, info)
}

// ErrNoAuth is what consumers of the auth data report when it is missing.
var ErrNoAuth = errors.New("tenantauth: no auth data in context")

// FromContext returns the tenant and token of an authenticated request.
func FromContext(ctx context.Context) (tenantID, tokenID string, err error) {
	ad := client.FromContext(ctx).Auth
	if ad == nil {
		return "", "", ErrNoAuth
	}
	tid, _ := ad.GetAttribute(AttrTenantID).(string)
	kid, _ := ad.GetAttribute(AttrTokenID).(string)
	if tid == "" || kid == "" {
		return "", "", ErrNoAuth
	}
	return tid, kid, nil
}

type authData struct {
	tenantID string
	tokenID  string
}

func (d authData) GetAttribute(name string) any {
	switch name {
	case AttrTenantID:
		return d.tenantID
	case AttrTokenID:
		return d.tokenID
	}
	return nil
}

func (authData) GetAttributeNames() []string { return []string{AttrTenantID, AttrTokenID} }
