// Command gateway is the collect gateway behind in.<BASE_DOMAIN> (plan
// section 4.4, this stage). it serves the immutable tracker files, issues
// challenges, checks and enriches web events and queues them in jetstream.
// the public listener is reached only through the local caddy; /healthz,
// /readyz and /metrics are on a separate private port.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/gateway"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: gateway [serve|version]

serve (the default) runs the gateway until SIGINT or SIGTERM. SIGHUP reloads
the db-ip file and the revoked tracker list.

environment: NATS_URL, NATS_REPLICAS, NATS_USER and NATS_PASSWORD (when the
server requires auth), GATEWAY_NODE_ID, GATEWAY_ADDR, GATEWAY_OPS_ADDR,
PROOF_BITS, PROOF_REPLAY_LIMIT, IPCLUSTER_THRESHOLD, RATE_SITE_PER_SECOND,
RATE_SITE_BURST, RATE_PREFIX_PER_SECOND, RATE_PREFIX_BURST, GATEWAY_MAX_BODY,
CLIENT_IP_HEADER, DBIP_MMDB, TRACKER_RELEASE_DIR, GATEWAY_SECURE_TRANSPORT.
docs/reference/gateway-limits.md gives the starting values.

the ops port also serves /edgez, the edge node's composite health (gateway
ready, collector healthy, local nats current with its meta leader). it reads
EDGE_HEALTH_COLLECTOR_URL (the collector's health_check endpoint) and
EDGE_HEALTH_NATS_URL (the local nats server's monitoring /healthz) and answers
503 while either is unset. caddy serves it on port 80 for the dns monitor.
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
			log.Error("gateway failed", "error", err.Error())
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
	// both loaders record their problems on env and return everything recorded
	// so far, so the combined result is read once from env, not joined twice
	natsCfg, _ := config.LoadNATS(env)
	gwCfg, _ := config.LoadGateway(env)
	if err := env.Err(); err != nil {
		return err
	}
	log = log.With("node", gwCfg.NodeID)
	nc, err := natsx.Connect(natsCfg, natsx.ServiceGateway, log)
	if err != nil {
		return err
	}
	defer nc.Close()

	reg := obs.NewRegistry()
	node, err := gateway.NewNode(ctx, gateway.NodeConfig{Gateway: gwCfg, NC: nc, Replicas: natsCfg.Replicas, Log: log, Registry: reg})
	if err != nil {
		return err
	}

	// the background loops outlive the listeners: on shutdown the http
	// servers finish their requests first, then the loops stop, so the last
	// stats flush includes every answered event
	loopCtx, stopLoops := context.WithCancel(context.WithoutCancel(ctx))
	defer stopLoops()
	loopDone := make(chan error, 1)
	go func() { loopDone <- node.Run(loopCtx) }()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hup:
				if err := node.Reload(); err != nil {
					log.Error("reload failed, previous geo file and revoked list stay in use", "error", err.Error())
					continue
				}
				log.Info("geo file and revoked tracker list reloaded")
			}
		}
	}()

	// one listener failing stops the other, so the process exits
	serveCtx, stopServe := context.WithCancel(ctx)
	defer stopServe()
	errc := make(chan error, 2)
	ops := http.NewServeMux()
	ops.Handle("GET /edgez", edgeHealth(env, node, log))
	ops.Handle("/", node.Ops())
	go func() {
		errc <- obs.Serve(serveCtx, obs.NewServer(gwCfg.OpsAddr, ops), log)
	}()
	go func() {
		errc <- obs.Serve(serveCtx, obs.NewServer(gwCfg.Addr, node.Handler()), log)
	}()
	log.Info("gateway started", "version", buildVersion(), "addr", gwCfg.Addr, "ops_addr", gwCfg.OpsAddr)
	var serveErr error
	for range 2 {
		if err := <-errc; err != nil && serveErr == nil {
			serveErr = err
		}
		stopServe()
	}
	stopLoops()
	return errors.Join(serveErr, <-loopDone)
}

// edgeHealth is the composite health of an edge node (the design). both
// urls are optional so the dev stack and the tests run without them, but a
// node without them answers 503: an edge node that cannot vouch for its
// collector and its nats server must not stay in dns.
func edgeHealth(env *config.Env, node *gateway.Node, log *slog.Logger) http.Handler {
	client := &http.Client{Timeout: 2 * time.Second}
	var missing []string
	checks := append([]obs.Check{}, node.Checks()...)
	for _, k := range []struct{ key, name string }{
		{"EDGE_HEALTH_COLLECTOR_URL", "collector"},
		{"EDGE_HEALTH_NATS_URL", "nats"},
	} {
		if !env.Has(k.key) {
			missing = append(missing, k.key)
			continue
		}
		u := env.URL(k.key)
		if u == nil {
			missing = append(missing, k.key+" (not an absolute http url)")
			continue
		}
		checks = append(checks, obs.HTTPCheck(k.name, u, client))
	}
	return obs.CompositeHandler(log, missing, checks...)
}
