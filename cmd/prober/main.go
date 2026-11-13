// Command prober is the outside view of the platform (the design). it runs
// on the probe node, off the main provider, beside gatus: every interval it
// sends a real web event and a real otlp metric for the internal probe
// tenant, reads them back through the machine api, checks the console, the
// umami host, the tracker files and every node's tls, and pushes each result
// to gatus external endpoints. /healthz, /readyz and /metrics are on
// PROBER_OPS_ADDR.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/internal/prober"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: prober [serve|version]

serve (the default) probes until SIGINT or SIGTERM.

environment: PROBER_OPS_ADDR, PROBER_INTERVAL (30s), PROBER_TIMEOUT,
PROBER_FRESHNESS_MAX_DELAY (10m), PROBER_CERT_MIN_VALIDITY (336h),
PROBER_INGEST_URL, PROBER_OTLP_URL, PROBER_CONSOLE_URL, PROBER_WEB_URL,
PROBER_MACHINE_API_URL (ending in /api/metrics/v1), PROBER_MACHINE_API_KEY
(scoped to the probe tenant), PROBER_SITE_ID, PROBER_SITE_ORIGIN,
PROBER_USER_AGENT (a browser user agent: umami drops bots), PROBER_OTLP_TOKEN,
PROBER_TRACKER_MANIFEST (packages/tracker/releases/manifest.json as
deployed), PROBER_NODES (name@address=host,host;...), PROBER_GATUS_URL,
PROBER_GATUS_TOKEN, and PROBER_CA_FILE only where the nodes use a private ca.
platformctl probe-tenant create prints the site id, token and key.
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
			log.Error("prober failed", "error", err.Error())
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
	cfg, err := prober.Load(env)
	if err != nil {
		return err
	}
	reg := obs.NewRegistry()
	p, err := prober.New(cfg, log, reg)
	if err != nil {
		return err
	}
	opsCtx, stopOps := context.WithCancel(ctx)
	defer stopOps()
	opsDone := make(chan error, 1)
	go func() { opsDone <- obs.Serve(opsCtx, obs.NewServer(cfg.OpsAddr, obs.NewOpsHandler(reg, log)), log) }()
	log.Info("prober started", "version", buildVersion(), "interval", cfg.Interval.String(), "nodes", len(cfg.Nodes))
	runDone := make(chan error, 1)
	go func() { runDone <- p.Run(ctx) }()
	select {
	case err := <-runDone:
		stopOps()
		return errors.Join(err, <-opsDone)
	case err := <-opsDone:
		return err
	}
}
