// Command replayer delivers accepted web events from WEB_EVENTS to umami
//. it runs on both core nodes; only the holder of
// the replayer lock consumes. /healthz, /readyz and /metrics are on a private
// ops port.
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
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/internal/replayer"
	"github.com/justmert/stellar-analytics/internal/umami"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: replayer [serve|version]

serve (the default) runs the replayer until SIGINT or SIGTERM.

environment: NATS_URL, NATS_REPLICAS, NATS_USER and NATS_PASSWORD (when the
server requires auth), REPLAYER_NODE_ID, REPLAYER_OPS_ADDR, UMAMI_URL,
REPLAYER_WORKERS, REPLAYER_MAX_IN_FLIGHT.
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
			log.Error("replayer failed", "error", err.Error())
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
	// both loaders record problems on the same env, and the second one's
	// error already holds every problem of the first, so only it is returned
	natsCfg, _ := config.LoadNATS(env)
	set, err := replayer.LoadSettings(env)
	if err != nil {
		return err
	}
	log = log.With("node", set.NodeID)
	uc, err := umami.New(set.UmamiURL, nil)
	if err != nil {
		return err
	}
	nc, err := natsx.Connect(natsCfg, natsx.ServiceReplayer, log)
	if err != nil {
		return err
	}
	defer nc.Close()

	reg := obs.NewRegistry()
	rp, err := replayer.New(ctx, replayer.Config{Settings: set, NC: nc, Replicas: natsCfg.Replicas, Umami: uc, Log: log, Registry: reg})
	if err != nil {
		return err
	}

	// the ops server outlives the consumer, so /metrics still answers while
	// held messages are handed back on shutdown
	opsCtx, stopOps := context.WithCancel(context.WithoutCancel(ctx))
	defer stopOps()
	opsDone := make(chan error, 1)
	go func() { opsDone <- obs.Serve(opsCtx, obs.NewServer(set.OpsAddr, rp.Ops()), log) }()

	log.Info("replayer started", "version", buildVersion(), "ops_addr", set.OpsAddr,
		"workers", set.Workers, "max_in_flight", set.MaxInFlight)
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	runDone := make(chan error, 1)
	go func() { runDone <- rp.Run(runCtx) }()
	select {
	case runErr := <-runDone:
		stopOps()
		return errors.Join(runErr, <-opsDone)
	case opsErr := <-opsDone:
		// the ops listener failed (port in use); stop consuming as well, so
		// held messages are handed back before the process exits
		stopRun()
		return errors.Join(opsErr, errors.New("replayer: ops server stopped"), <-runDone)
	}
}
