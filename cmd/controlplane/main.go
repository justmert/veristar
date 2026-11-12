// Command controlplane serves the console api under /api (the design,
// docs/reference/control-plane-api.md). /healthz, /readyz and /metrics are on
// a separate private ops port.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/controlplane"
	"github.com/justmert/stellar-analytics/internal/controlplane/auth"
	"github.com/justmert/stellar-analytics/internal/controlplane/domaincheck"
	"github.com/justmert/stellar-analytics/internal/controlplane/ingest"
	"github.com/justmert/stellar-analytics/internal/controlplane/lifecycle"
	"github.com/justmert/stellar-analytics/internal/controlplane/members"
	"github.com/justmert/stellar-analytics/internal/controlplane/operator"
	"github.com/justmert/stellar-analytics/internal/controlplane/otlptokens"
	"github.com/justmert/stellar-analytics/internal/controlplane/provision"
	"github.com/justmert/stellar-analytics/internal/controlplane/session"
	"github.com/justmert/stellar-analytics/internal/controlplane/sites"
	"github.com/justmert/stellar-analytics/internal/controlplane/sso"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	"github.com/justmert/stellar-analytics/internal/controlplane/terms"
	"github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/netguard"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/internal/registry"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: controlplane [serve|version]

serve (the default) runs the api until SIGINT or SIGTERM.

environment: the keys of docs/reference/control-plane-api.md section 4.5:
CONTROLPLANE_ADDR, CONTROLPLANE_OPS_ADDR, CONSOLE_ORIGIN, INGEST_ORIGIN,
WEB_ORIGIN, GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, GITHUB_REDIRECT_URL,
OAUTH_STATE_KEY, OPERATOR_GITHUB_IDS, UMAMI_URL, UMAMI_ADMIN_PASSWORD,
UMAMI_USER_PASSWORD_KEY, TRACKER_RELEASE_DIR, TRACKER_NPM_PACKAGE (optional),
NATS_URL, NATS_REPLICAS, NATS_USER and NATS_PASSWORD (when the server
requires auth), PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_NAME,
PLATFORM_DB_SSLMODE, PLATFORM_DB_CP_SYSTEM_PASSWORD,
PLATFORM_DB_CP_TENANT_PASSWORD, PLATFORM_DB_CP_OPERATOR_PASSWORD.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], config.FromEnviron(), os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, env *config.Env, stdout, stderr io.Writer) int {
	log := obs.NewLogger(stderr, slog.LevelInfo)
	switch {
	case len(args) == 1 && args[0] == "version":
		_, _ = fmt.Fprintln(stdout, buildVersion())
		return 0
	case len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"):
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case len(args) == 0 || (len(args) == 1 && args[0] == "serve"):
		if err := serve(ctx, env, log); err != nil {
			log.Error("controlplane failed", "error", err.Error())
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
}

func buildVersion() string {
	v := version
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				v += " " + s.Value
			}
		}
	}
	return v
}

func serve(ctx context.Context, env *config.Env, log *slog.Logger) error {
	// every loader records its problems on the same env, so the last error
	// names every missing or invalid key at once
	endpoints := auth.LoadEndpoints(env)
	natsCfg, _ := config.LoadNATS(env)
	poolCfg, _ := db.LoadPoolConfig(env, db.RoleCPSystem, db.RoleCPTenant, db.RoleCPOperator)
	cfg, err := config.LoadControlPlane(env)
	if err != nil {
		return err
	}
	tm, err := terms.Current()
	if err != nil {
		return err
	}
	if auth.FixtureBuild {
		log.Warn("built with the oauthfixture tag: sign-in goes to OAUTH_FIXTURE_URL, not github; never run this build in production")
	}

	pwKey, err := umami.NewPasswordKey(cfg.UmamiUserPasswordKey.Reveal())
	if err != nil {
		return err
	}
	umamiClient, err := umami.New(cfg.UmamiURL, nil)
	if err != nil {
		return err
	}
	admin, err := umami.NewAdmin(umamiClient, umami.SeededUsername, cfg.UmamiAdminPassword.Reveal())
	if err != nil {
		return err
	}
	tracker, err := controlplane.LoadTracker(cfg.TrackerReleaseDir, cfg.IngestOrigin)
	if err != nil {
		return err
	}

	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	d, err := db.Open(cctx, poolCfg, "controlplane")
	if err != nil {
		return err
	}
	defer d.Close()
	nc, err := natsx.Connect(natsCfg, natsx.ServiceControlPlane, log)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	// both registries: the reconciler publishes sites and otlp tokens
	publisher, err := registry.NewPublisher(cctx, js)
	if err != nil {
		return err
	}

	// operator flags follow OPERATOR_GITHUB_IDS from this start on (5.1)
	changed, err := operator.SyncFlags(ctx, d, cfg.OperatorGitHubIDs)
	if err != nil {
		return err
	}
	log.Info("operator flags synced", "changed", changed)

	rec := &provision.Reconciler{
		Admin: admin, Registry: publisher, Key: pwKey, Tx: d.WithSystem, Now: time.Now, Log: log,
	}
	scope, err := tenants.NewScope(d, tm.Version, rec, log)
	if err != nil {
		return err
	}
	// reconciles started by requests finish before the process exits
	defer scope.Wait()

	sessions := session.New(d)
	authH, err := auth.New(auth.Config{
		ClientID:     cfg.GitHubClientID,
		ClientSecret: cfg.GitHubClientSecret,
		RedirectURL:  cfg.GitHubRedirectURL,
		StateKey:     cfg.OAuthStateKey.Reveal(),
		Operators:    cfg.OperatorGitHubIDs,
		Endpoints:    endpoints,
		Reconcile:    scope.Reconcile,
	}, d, sessions)
	if err != nil {
		return err
	}
	guard, err := operator.NewGuard(cfg.OperatorGitHubIDs)
	if err != nil {
		return err
	}
	// domain checks resolve through the system resolver; the guard refuses
	// private and loopback answers, so no setting can open them
	ng, err := netguard.New(net.DefaultResolver)
	if err != nil {
		return err
	}
	checks, err := domaincheck.NewRoutes(scope, &domaincheck.Checker{Guard: ng, Resolver: net.DefaultResolver}, time.Now, log)
	if err != nil {
		return err
	}
	// background stellar.toml fetches finish before the pools close
	defer checks.Wait()
	modeRoutes, err := operator.NewEnrollmentMode(d)
	if err != nil {
		return err
	}
	auditRoutes, err := operator.NewAuditLog(d)
	if err != nil {
		return err
	}
	keepRoutes, err := operator.NewRemovalKeepAggregates(d)
	if err != nil {
		return err
	}
	actions, err := lifecycle.NewActions(d, scope.Reconcile)
	if err != nil {
		return err
	}
	review, err := operator.NewTenants(d)
	if err != nil {
		return err
	}

	otlpRoutes, err := otlptokens.NewRoutes(scope, publisher)
	if err != nil {
		return err
	}
	snippets, err := controlplane.NewSnippetRoutes(scope, tracker, cfg.TrackerNPMPackage)
	if err != nil {
		return err
	}
	ssoH := &sso.Handler{
		Tx:        d.WithSystem,
		Umami:     umamiClient,
		Key:       pwKey,
		WebOrigin: cfg.WebOrigin,
		UserID: func(r *http.Request) (uuid.UUID, bool) {
			u, ok := session.CurrentUser(r.Context())
			return u.ID, ok
		},
		Now: time.Now,
	}
	if err := ssoH.Check(); err != nil {
		return err
	}
	ssoPath, ok := strings.CutPrefix(sso.Path, "/api")
	if !ok {
		return fmt.Errorf("sso path %s is not under /api", sso.Path)
	}

	api := controlplane.NewHandler(controlplane.Options{
		Log:           log,
		ConsoleOrigin: cfg.ConsoleOrigin,
		Sessions:      sessions,
		Public:        []controlplane.Routes{controlplane.RoutesFunc(authH.Public), controlplane.RoutesFunc(terms.Routes)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(authH.Private),
			tenants.NewRoutes(scope),
			members.NewRoutes(scope),
			sites.NewRoutes(scope).OnCreate(checks),
			checks,
			ingest.NewRoutes(scope),
			otlpRoutes,
			snippets,
			// behind RequireUser like every private group; sso answers its own
			// failures with a redirect, but an anonymous request gets 401
			controlplane.RoutesFunc(func(r chi.Router) { r.Method(http.MethodGet, ssoPath, ssoH) }),
			// /api/operator: 403 operator_required for everyone else (5.1)
			controlplane.RoutesFunc(guard.Mount(modeRoutes, keepRoutes, auditRoutes, actions, review)),
		},
	})

	reg := obs.NewRegistry()
	ops := obs.NewOpsHandler(reg, log, readiness(d, nc)...)

	log.Info("controlplane started", "version", buildVersion(), "addr", cfg.Addr, "ops_addr", cfg.OpsAddr,
		"console_origin", cfg.ConsoleOrigin, "terms_version", tm.Version, "operators", len(cfg.OperatorGitHubIDs))

	opsCtx, stopOps := context.WithCancel(ctx)
	defer stopOps()
	opsDone := make(chan error, 1)
	go func() { opsDone <- obs.Serve(opsCtx, obs.NewServer(cfg.OpsAddr, ops), log) }()
	apiErr := obs.Serve(ctx, apiServer(cfg.Addr, api), log)
	stopOps()
	return errors.Join(apiErr, <-opsDone)
}

// connectTimeout bounds the startup connections, so a wrong address fails
// the process instead of hanging it.
const connectTimeout = 30 * time.Second

// apiServer is obs.NewServer with the read limits of a public api.
func apiServer(addr string, h http.Handler) *http.Server {
	srv := obs.NewServer(addr, h)
	srv.MaxHeaderBytes = 32 << 10
	return srv
}

// readiness checks both database pools and the nats connection.
func readiness(d *db.DB, nc *nats.Conn) []obs.Check {
	checks := make([]obs.Check, 0, 4)
	for _, r := range []db.Role{db.RoleCPSystem, db.RoleCPTenant, db.RoleCPOperator} {
		checks = append(checks, obs.Check{Name: "db " + string(r), Fn: func(ctx context.Context) error {
			p, err := d.Pool(r)
			if err != nil {
				return err
			}
			return p.Ping(ctx)
		}})
	}
	checks = append(checks, obs.Check{Name: "nats", Fn: func(context.Context) error {
		if !nc.IsConnected() {
			return fmt.Errorf("nats: %s", nc.Status())
		}
		return nil
	}})
	return checks
}
