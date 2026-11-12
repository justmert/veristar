// Command metricswriter stores the metric batches on API_METRICS as exact
// minute rows in the api schema (the design, this stage). it
// runs on both core nodes; only the holder of the metricswriter lock
// consumes, and every transaction is fenced. /healthz, /readyz and /metrics
// are on a private ops port.
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
	"time"

	"github.com/justmert/stellar-analytics/internal/config"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/metricswriter"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: metricswriter [serve|version]

serve (the default) runs the writer until SIGINT or SIGTERM.

environment: NATS_URL, NATS_REPLICAS, NATS_USER and NATS_PASSWORD (when the
server requires auth), PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_NAME,
PLATFORM_DB_SSLMODE, PLATFORM_DB_METRICS_WRITER_PASSWORD,
METRICSWRITER_NODE_ID, METRICSWRITER_OPS_ADDR, METRICSWRITER_BATCH,
METRICSWRITER_FIRST_POINT_THRESHOLD, METRICSWRITER_ROUTE_CAP,
METRICSWRITER_SERVICE_CAP.
`

// connectTimeout bounds the startup connections, so a wrong address fails
// the process instead of hanging it.
const connectTimeout = 30 * time.Second

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
			log.Error("metricswriter failed", "error", err.Error())
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
	// already names every missing key; the earlier ones are not repeated
	natsCfg, _ := config.LoadNATS(env)
	dbCfg, _ := pdb.LoadPoolConfig(env, pdb.RoleMetricsWriter)
	set, err := metricswriter.LoadSettings(env)
	if err != nil {
		return err
	}
	log = log.With("node", set.NodeID)

	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	d, err := pdb.Open(cctx, dbCfg, natsx.ServiceMetricsWriter)
	if err != nil {
		return err
	}
	defer d.Close()
	nc, err := natsx.Connect(natsCfg, natsx.ServiceMetricsWriter, log)
	if err != nil {
		return err
	}
	defer nc.Close()

	reg := obs.NewRegistry()
	w, err := metricswriter.New(metricswriter.Config{Settings: set, NC: nc, Replicas: natsCfg.Replicas, DB: d, Log: log, Registry: reg})
	if err != nil {
		return err
	}

	// the ops server outlives the consumer, so /metrics still answers while
	// held messages are handed back on shutdown
	opsCtx, stopOps := context.WithCancel(context.WithoutCancel(ctx))
	defer stopOps()
	opsDone := make(chan error, 1)
	go func() { opsDone <- obs.Serve(opsCtx, obs.NewServer(set.OpsAddr, w.Ops()), log) }()

	log.Info("metricswriter started", "version", buildVersion(), "ops_addr", set.OpsAddr,
		"batch", set.Batch, "first_point_threshold", set.FirstPointThreshold.String(),
		"route_cap", set.RouteCap, "service_cap", set.ServiceCap)
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(runCtx) }()
	select {
	case runErr := <-runDone:
		stopOps()
		return errors.Join(runErr, <-opsDone)
	case opsErr := <-opsDone:
		stopRun()
		return errors.Join(opsErr, errors.New("metricswriter: ops server stopped"), <-runDone)
	}
}
