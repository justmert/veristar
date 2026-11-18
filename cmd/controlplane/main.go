// Command controlplane serves the console api under /api
// (docs/reference/control-plane-api.md). /healthz, /readyz and /metrics are on
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
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/alertmanager"
	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/account"
	"github.com/justmert/veristar/internal/controlplane/apimetrics"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	"github.com/justmert/veristar/internal/controlplane/erasure"
	"github.com/justmert/veristar/internal/controlplane/exports"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/ingest"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/mcpserver"
	"github.com/justmert/veristar/internal/controlplane/members"
	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/controlplane/provision"
	"github.com/justmert/veristar/internal/controlplane/reports"
	"github.com/justmert/veristar/internal/controlplane/scfawards"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/sites"
	"github.com/justmert/veristar/internal/controlplane/stepup"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	"github.com/justmert/veristar/internal/controlplane/umamiread"
	"github.com/justmert/veristar/internal/controlplane/webdash"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/netguard"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: controlplane [serve|version]

serve (the default) runs the api until SIGINT or SIGTERM.

environment: the keys of docs/reference/control-plane-api.md section 4.5:
CONTROLPLANE_ADDR, CONTROLPLANE_OPS_ADDR, CONSOLE_ORIGIN, INGEST_ORIGIN,
GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, GITHUB_REDIRECT_URL,
OAUTH_STATE_KEY, OPERATOR_GITHUB_IDS, UMAMI_URL, UMAMI_ADMIN_PASSWORD,
UMAMI_USER_PASSWORD_KEY, TRACKER_RELEASE_DIR, TRACKER_NPM_PACKAGE (optional),
NATS_URL, NATS_REPLICAS, NATS_USER and NATS_PASSWORD (when the server
requires auth), NATS_TLS_CA with the optional NATS_TLS_CERT and
NATS_TLS_KEY (tls only, from the first byte), PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_NAME,
PLATFORM_DB_SSLMODE, PLATFORM_DB_SSLROOTCERT (verify-full only) with the
optional PLATFORM_DB_SSLCERT and PLATFORM_DB_SSLKEY, PLATFORM_DB_CP_SYSTEM_PASSWORD,
PLATFORM_DB_CP_TENANT_PASSWORD, PLATFORM_DB_CP_OPERATOR_PASSWORD,
API_MINUTE_RETENTION_DAYS and API_HOURLY_RETENTION_MONTHS (section 7.11, the
values jobs runs with), ERASURE_QUEUE_WAIT (optional, section 10.6),
ALERTMANAGER_URLS with ALERTMANAGER_TLS_CA, ALERTMANAGER_TLS_CERT and
ALERTMANAGER_TLS_KEY (optional, section 11.3; without them the operator
alerts page answers alert_source_not_configured).
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
	poolCfg.MaxConns = requestPoolSizes
	// the api metrics ranges switch tables at the horizons the jobs
	// retention task drops data by, so both read the same keys
	retention, _ := config.LoadAPIRetention(env)
	erasureCfg, _ := config.LoadErasure(env)
	amCfg, _ := config.LoadAlertmanager(env)
	// ledger apis of the on-chain proofs (contract 15.4)
	stellarCfg, _ := config.LoadStellar(env)
	// the usage report key (contract 20.2)
	reportKey, _ := config.LoadReportSigning(env)
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
	// the reconciler gets a cp_system pool of its own, one connection per
	// reconcile worker: a run holds its connection across umami calls, so
	// on the request pool a few slow runs would make every session load
	// wait
	recCfg, err := poolCfg.Only(db.RoleCPSystem, tenants.ReconcileWorkers)
	if err != nil {
		return err
	}
	recDB, err := db.Open(cctx, recCfg, "controlplane-reconcile")
	if err != nil {
		return err
	}
	defer recDB.Close()
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

	// OPERATOR_GITHUB_IDS and OPERATOR_STELLAR_ADDRESSES bootstrap the
	// roster: at every start each listed identity is an active operator
	// admin, and every account's operator flag is recomputed (5.1)
	operators := identity.Operators{GitHub: cfg.OperatorGitHubIDs, Stellar: cfg.OperatorStellarAddresses}
	boot, err := operator.Bootstrap(ctx, d, operators, cfg.OperatorEnrollWindow, time.Now())
	if err != nil {
		return err
	}
	log.Info("operator roster bootstrapped", "ensured", boot.Ensured, "changed", boot.Changed,
		"flags_changed", boot.FlagsChanged, "enrollment_windows", boot.Windows, "active_admins", boot.ActiveAdmins)

	rec := &provision.Reconciler{
		Admin: admin, Registry: publisher, Key: pwKey, Tx: recDB.WithSystem, Now: time.Now, Log: log,
	}
	scope, err := tenants.NewScope(d, tm.Version, rec, log)
	if err != nil {
		return err
	}
	// reconciles in flight finish before the process exits; queued ones are
	// left to the periodic reconcile task
	defer scope.Stop()

	sessions := session.New(d)
	// the passkey step-up of the operator area (contract 5.15)
	stepUp, err := stepup.New(stepup.Config{
		ConsoleOrigin: cfg.ConsoleOrigin, MaxAge: cfg.OperatorStepUpMaxAge, EnrollWindow: cfg.OperatorEnrollWindow,
	}, d, sessions)
	if err != nil {
		return err
	}
	authH, err := auth.New(auth.Config{
		ClientID:      cfg.GitHubClientID,
		ClientSecret:  cfg.GitHubClientSecret,
		RedirectURL:   cfg.GitHubRedirectURL,
		StateKey:      cfg.OAuthStateKey.Reveal(),
		ConsoleOrigin: cfg.ConsoleOrigin,
		Endpoints:     endpoints,
		Reconcile:     scope.Reconcile,
		// an operator account changes its sign-in methods only after a
		// step-up
		StepUp: stepUp,
	}, d, sessions)
	if err != nil {
		return err
	}
	// invites and direct adds are bound to the github account id (contract
	// 4.2.5), read with the app's credentials
	users, err := auth.NewUserLookup(auth.UserLookupConfig{
		ClientID: cfg.GitHubClientID, ClientSecret: cfg.GitHubClientSecret, Endpoints: endpoints,
	})
	if err != nil {
		return err
	}
	memberRoutes, err := members.NewRoutes(scope, users)
	if err != nil {
		return err
	}
	guard, err := operator.NewGuard(d, stepUp)
	if err != nil {
		return err
	}
	roster, err := operator.NewRoster(operator.RosterConfig{
		DB: d, Guard: guard, Lookup: users, Bootstrap: operators, EnrollWindow: cfg.OperatorEnrollWindow,
	})
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
	// operator global web and api views, aggregate tables only
	global, err := operator.NewGlobal(d)
	if err != nil {
		return err
	}
	review, err := operator.NewTenants(d)
	if err != nil {
		return err
	}
	// machine api: operators manage keys in the console; machines read with
	// them at /api/metrics/v1, which is mounted beside the session api
	apiKeys, err := operator.NewAPIKeys(d)
	if err != nil {
		return err
	}
	machine, err := operator.NewMachine(operator.MachineConfig{DB: d, Umami: admin, Log: log})
	if err != nil {
		return err
	}
	alertRoutes, err := newAlertRoutes(amCfg, log)
	if err != nil {
		return err
	}
	// the on-chain witness (contract 15) and the exports and scf view
	// (contract 16)
	horizon, rpc, err := chain.Sources(stellarCfg)
	if err != nil {
		return err
	}
	// a source on another network would let a proof speak of another
	// network's account under the same strkey: refuse to start. a source
	// that does not answer now only fails the proofs that need it
	nctx, ncancel := context.WithTimeout(ctx, 15*time.Second)
	err = chain.CheckNetwork(nctx, horizon, rpc, chain.PublicNetwork)
	ncancel()
	if chain.Code(err) == chain.CodeWrongNetwork {
		return err
	}
	if err != nil {
		log.Warn("stellar sources: network not checked", "err", err)
	}
	onchainRoutes, err := onchain.NewRoutes(scope, horizon, rpc, cfg.ConsoleOrigin, time.Now)
	if err != nil {
		return err
	}
	onchainOperator, err := operator.NewOnchain(d, time.Now)
	if err != nil {
		return err
	}
	operatorExports, err := operator.NewExports(global)
	if err != nil {
		return err
	}
	overviewExport, err := exports.NewOverview(scope, time.Now)
	if err != nil {
		return err
	}
	scfView, err := operator.NewSCFView(scope, d, time.Now)
	if err != nil {
		return err
	}
	// cohorts and saved threshold rules (contract 19)
	cohortRoutes, err := operator.NewCohorts(d, time.Now)
	if err != nil {
		return err
	}
	tenantCohort, err := operator.NewTenantCohort(scope, time.Now)
	if err != nil {
		return err
	}
	ruleRoutes, err := operator.NewRules(d, time.Now)
	if err != nil {
		return err
	}
	// the scf award search of the enrollment form (contract 4.2.7) and the
	// award matching on enrollment and site creation (19.5); background
	// matches finish before the pools close
	scfSearch, err := scfawards.NewSearch(d, time.Now)
	if err != nil {
		return err
	}
	scfHooks, err := scfawards.NewHooks(d, log)
	if err != nil {
		return err
	}
	defer scfHooks.Wait()
	// signed usage reports (contract 20)
	reportRoutes, err := reports.New(reports.Config{
		Scope: scope, DB: d, Signing: reportKey, ConsoleOrigin: cfg.ConsoleOrigin, Platform: buildVersion(), Now: time.Now,
	})
	if err != nil {
		return err
	}

	// erasure: runs started by requests finish before the pools close
	eraser, err := erasure.New(erasure.Config{
		DB: d, NC: nc, Umami: admin, QueueWait: erasureCfg.QueueWait, Now: time.Now, Log: log,
	})
	if err != nil {
		return err
	}
	defer eraser.Wait()
	erasureRoutes, err := erasure.NewRoutes(scope, eraser)
	if err != nil {
		return err
	}

	otlpRoutes, err := otlptokens.NewRoutes(scope, publisher)
	if err != nil {
		return err
	}
	metricRoutes, err := apimetrics.NewRoutes(scope, apimetrics.Resolver{Now: time.Now, Retention: retention})
	if err != nil {
		return err
	}
	snippets, err := controlplane.NewSnippetRoutes(scope, tracker, cfg.TrackerNPMPackage)
	if err != nil {
		return err
	}
	// web analytics are read from umami as the member's own umami user,
	// for the console's web dashboard and the mcp web tools alike
	umamiSessions := &umamiread.Sessions{Tx: d.WithSystem, Umami: umamiClient, Key: pwKey, Now: time.Now}
	if err := umamiSessions.Check(); err != nil {
		return err
	}
	webReader, err := umamiread.NewReader(umamiSessions, umamiClient, time.Now)
	if err != nil {
		return err
	}
	webRoutes, err := webdash.NewRoutes(scope, webReader, time.Now)
	if err != nil {
		return err
	}

	// the mcp server (contract 12): its tools call the console's read
	// routes in process, built here with the same constructors as below
	reg := obs.NewRegistry()
	mcpSrv, err := mcpserver.New(mcpserver.Config{
		DB:    d,
		Guard: guard,
		Reads: []mcpserver.Routes{
			tenants.NewRoutes(scope), sites.NewRoutes(scope), metricRoutes, onchainRoutes, scfView,
			tenantCohort,
			controlplane.RoutesFunc(guard.Mount(review, global, onchainOperator, cohortRoutes, ruleRoutes)),
		},
		Web:      webReader,
		Issuer:   cfg.ConsoleOrigin,
		StateKey: cfg.OAuthStateKey.Reveal(),
		Log:      log,
		Registry: reg,
		Version:  buildVersion(),
	})
	if err != nil {
		return err
	}

	// a person deletes their own account (contract 4.1.6)
	accountRoutes, err := account.NewRoutes(account.Config{
		DB: d, Sessions: sessions, Umami: admin, Reconcile: scope.Reconcile, StepUp: stepUp,
	})
	if err != nil {
		return err
	}

	api := controlplane.NewHandler(controlplane.Options{
		Log:           log,
		ConsoleOrigin: cfg.ConsoleOrigin,
		Sessions:      sessions,
		Public: []controlplane.Routes{controlplane.RoutesFunc(authH.Public), controlplane.RoutesFunc(terms.Routes),
			controlplane.RoutesFunc(reportRoutes.Public)},
		Private: []controlplane.Routes{
			controlplane.RoutesFunc(authH.Private),
			accountRoutes,
			tenants.NewRoutes(scope).OnEnroll(scfHooks),
			scfSearch,
			memberRoutes,
			sites.NewRoutes(scope).OnCreate(checks, scfHooks),
			checks,
			ingest.NewRoutes(scope),
			otlpRoutes,
			metricRoutes,
			erasureRoutes,
			snippets,
			onchainRoutes,
			overviewExport,
			scfView,
			tenantCohort,
			controlplane.RoutesFunc(reportRoutes.Private),
			webRoutes,
			// /api/operator: 403 operator_required for everyone else (5.1)
			// mcp access keys and the oauth consent page (contract 12.8)
			mcpSrv.TenantRoutes(scope),
			mcpSrv.ConsentRoutes(),
			controlplane.RoutesFunc(guard.Mount(modeRoutes, keepRoutes, auditRoutes, actions, review, global, apiKeys, alertRoutes,
				mcpSrv.OperatorRoutes(), onchainOperator, operatorExports, cohortRoutes, ruleRoutes, roster)),
		},
		// keys only: no session middleware runs there (contract 9.1); the
		// mcp endpoint and its oauth server authenticate by bearer
		// credential or not at all (contract 12.2)
		Mounts: []controlplane.Mount{
			{Path: operator.MachinePath, Handler: machine.Handler()},
			{Path: mcpserver.Path, Handler: mcpSrv.Handler()},
			{Path: "/oauth", Handler: mcpSrv.OAuthHandler()},
			// with the stellar.toml of the report keys (contract 20.2)
			{Path: "/.well-known", Handler: mcpSrv.MetadataHandler(reportRoutes.WellKnown)},
		},
	})

	ops := http.NewServeMux()
	ops.Handle("GET /corez", obs.CompositeHandler(log, nil, coreHealth(d, nc, umamiClient)...))
	ops.Handle("/", obs.NewOpsHandler(reg, log, readiness(d, nc)...))

	log.Info("controlplane started", "version", buildVersion(), "addr", cfg.Addr, "ops_addr", cfg.OpsAddr,
		"console_origin", cfg.ConsoleOrigin, "terms_version", tm.Version, "operators", len(cfg.OperatorGitHubIDs),
		"wallet_operators", len(cfg.OperatorStellarAddresses), "report_key", reportKey.Address)

	opsCtx, stopOps := context.WithCancel(ctx)
	defer stopOps()
	opsDone := make(chan error, 1)
	go func() { opsDone <- obs.Serve(opsCtx, obs.NewServer(cfg.OpsAddr, ops), log) }()
	apiErr := obs.Serve(ctx, apiServer(cfg.Addr, api), log)
	stopOps()
	return errors.Join(apiErr, <-opsDone)
}

// newAlertRoutes builds the operator alerts route (contract 11). without
// ALERTMANAGER_URLS the route exists and answers alert_source_not_configured,
// so the console says why it shows no alerts instead of showing none.
func newAlertRoutes(c config.Alertmanager, log *slog.Logger) (*operator.Alerts, error) {
	if !c.Configured() {
		log.Warn("no alertmanager configured (ALERTMANAGER_URLS): the operator alerts page answers alert_source_not_configured")
		return operator.NewAlerts(nil, time.Now), nil
	}
	tlsCfg, err := c.ClientTLS()
	if err != nil {
		return nil, err
	}
	am, err := alertmanager.New(c.URLs, tlsCfg, alertmanager.DefaultTimeout)
	if err != nil {
		return nil, err
	}
	return operator.NewAlerts(am, time.Now), nil
}

// requestPoolSizes are the pools the api requests use, sized explicitly
// rather than by the cpu count of the node. every request loads its
// session from cp_system; tenant routes then work under cp_tenant and
// operator views under cp_operator. the reconciler has its own pool.
var requestPoolSizes = map[db.Role]int32{
	db.RoleCPSystem:   16,
	db.RoleCPTenant:   16,
	db.RoleCPOperator: 8,
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

// coreHealth is the composite health of a core node, served
// on the ops port as /corez and by caddy on port 80 for the dns monitor: the
// readiness checks, the database behind haproxy is the writable leader, and
// umami answers its heartbeat. a ping alone would also pass on a replica that
// haproxy should never have routed to.
func coreHealth(d *db.DB, nc *nats.Conn, um *umami.Client) []obs.Check {
	checks := readiness(d, nc)
	checks = append(checks,
		obs.Check{Name: "db leader", Fn: func(ctx context.Context) error {
			p, err := d.Pool(db.RoleCPSystem)
			if err != nil {
				return err
			}
			var recovery bool
			if err := p.QueryRow(ctx, "select pg_is_in_recovery()").Scan(&recovery); err != nil {
				return err
			}
			if recovery {
				return errors.New("database is a replica, not the leader")
			}
			return nil
		}},
		obs.Check{Name: "umami", Fn: um.Heartbeat},
	)
	return checks
}
