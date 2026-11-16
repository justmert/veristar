// Command jobs runs the platform's background tasks. `jobs daemon` is the
// long-running process: it runs on both core nodes, and only the one holding
// the jobs leader lock runs the tasks. `jobs run <name>` runs one task once,
// for operators and tests, under the same lock.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	"github.com/justmert/veristar/internal/controlplane/provision"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/aggregateweb"
	"github.com/justmert/veristar/internal/jobs/apicapacity"
	"github.com/justmert/veristar/internal/jobs/apipartition"
	"github.com/justmert/veristar/internal/jobs/apiretention"
	"github.com/justmert/veristar/internal/jobs/apirollup"
	"github.com/justmert/veristar/internal/jobs/chainactivity"
	"github.com/justmert/veristar/internal/jobs/edgestats"
	"github.com/justmert/veristar/internal/jobs/erasurefollowup"
	"github.com/justmert/veristar/internal/jobs/reconcile"
	"github.com/justmert/veristar/internal/jobs/retainweb"
	"github.com/justmert/veristar/internal/jobs/scheduler"
	"github.com/justmert/veristar/internal/jobs/sessionprune"
	"github.com/justmert/veristar/internal/jobs/statsdedup"
	"github.com/justmert/veristar/internal/jobs/umamirotate"
	"github.com/justmert/veristar/internal/jobs/verifydomains"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/netguard"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/internal/umami"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: jobs <command>

commands:
  daemon            run every task while holding the jobs lock; serves /healthz,
                    /readyz and /metrics on JOBS_OPS_ADDR
  run <task>        run one task once under the jobs lock (tasks: ` + edgestats.TaskName + `, ` + statsdedup.TaskName + `,
                    ` + reconcile.TaskName + `, ` + umamirotate.TaskName + `, ` + sessionprune.TaskName + `,
                    ` + verifydomains.TaskName + `, ` + apirollup.TaskName + `, ` + apipartition.TaskName + `,
                    ` + apiretention.TaskName + `, ` + aggregateweb.TaskName + `,
                    ` + erasurefollowup.TaskName + `, ` + chainactivity.TaskName + `)
  run ` + aggregateweb.TaskName + ` --from <yyyy-mm-dd> --to <yyyy-mm-dd>
                    read the web aggregates of every active site again for
                    each utc day of the range, both days included
  run ` + umamirotate.TaskName + ` --force
                    set every umami user's password for today even when
                    today's rotation is recorded; run it after
                    UMAMI_USER_PASSWORD_KEY changed (credential rotation, or
                    a migration into an environment with its own key)
  retain-web        delete raw umami event and session rows older than
                    WEB_RAW_RETENTION_MONTHS (default 25) as the role
                    jobs_retention; refuses to run on an umami schema it was
                    not tested with. needs PLATFORM_DB_HOST, PLATFORM_DB_PORT,
                    PLATFORM_DB_SSLMODE, PLATFORM_DB_SSLROOTCERT (verify-full only) with the
optional PLATFORM_DB_SSLCERT and PLATFORM_DB_SSLKEY, UMAMI_DB_NAME and
                    UMAMI_DB_JOBS_RETENTION_PASSWORD only; run it once a day
  capacity          print the measured on-disk bytes per row of the api metric
                    tables as json (docs/reference/capacity.md); needs only the
                    PLATFORM_DB_* keys of the jobs role
  version           print the build version

environment: NATS_URL, NATS_REPLICAS, NATS_USER and NATS_PASSWORD (when the
server requires auth), NATS_TLS_CA with the optional NATS_TLS_CERT and
NATS_TLS_KEY (tls only, from the first byte), PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_NAME,
PLATFORM_DB_SSLMODE, PLATFORM_DB_SSLROOTCERT (verify-full only) with the
optional PLATFORM_DB_SSLCERT and PLATFORM_DB_SSLKEY, PLATFORM_DB_JOBS_PASSWORD, PLATFORM_DB_CP_SYSTEM_PASSWORD
(reconcile and umami-rotate run control plane logic as cp_system), UMAMI_URL,
UMAMI_ADMIN_PASSWORD, UMAMI_USER_PASSWORD_KEY, API_MINUTE_RETENTION_DAYS,
API_HOURLY_RETENTION_MONTHS (at least 24), the optional STELLAR_HORIZON_URL,
STELLAR_RPC_URL, STELLAR_HORIZON_RPS and STELLAR_RPC_RPS (ledger apis of
chain-activity), and JOBS_OPS_ADDR for daemon.
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
	case len(args) == 1 && args[0] == "daemon":
		if err := daemon(ctx, env, log); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("jobs daemon failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) == 1 && args[0] == retainweb.TaskName:
		if err := retainWeb(ctx, env, log, time.Now()); err != nil {
			log.Error("jobs retain-web failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) == 1 && args[0] == "capacity":
		if err := capacity(ctx, env, stdout); err != nil {
			log.Error("jobs capacity failed", "error", err.Error())
			return 1
		}
		return 0
	case len(args) >= 2 && args[0] == "run":
		opts, err := parseRunArgs(args[1], args[2:])
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "jobs run %s: %v\n\n%s", args[1], err, usage)
			return 2
		}
		if err := runOnce(ctx, env, log, args[1], opts); err != nil {
			log.Error("jobs run failed", "task", args[1], "error", err.Error())
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

// dayRange is the --from and --to of `jobs run aggregate-web`.
type dayRange struct {
	from, to time.Time
}

// runOpts are the flags of `jobs run <task>`.
type runOpts struct {
	days *dayRange
	// force makes umami-rotate set today's passwords again
	force bool
}

// parseRunArgs reads the flags after `jobs run <task>`. only aggregate-web
// and umami-rotate take any.
func parseRunArgs(task string, args []string) (runOpts, error) {
	if task == umamirotate.TaskName && len(args) > 0 {
		if len(args) != 1 || args[0] != "--force" {
			return runOpts{}, fmt.Errorf("takes only --force, got %q", strings.Join(args, " "))
		}
		return runOpts{force: true}, nil
	}
	days, err := parseRange(task, args)
	return runOpts{days: days}, err
}

// parseRange reads the flags of aggregate-web, which takes both or neither:
// a backfill with one end guessed would read a range nobody asked for.
func parseRange(task string, args []string) (*dayRange, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if task != aggregateweb.TaskName {
		return nil, fmt.Errorf("takes no arguments, got %q", strings.Join(args, " "))
	}
	fs := flag.NewFlagSet("jobs run "+task, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.String("from", "", "first utc day")
	to := fs.String("to", "", "last utc day")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments %q", strings.Join(fs.Args(), " "))
	}
	if *from == "" || *to == "" {
		return nil, errors.New("--from and --to are both required for a backfill")
	}
	f, err := time.Parse(time.DateOnly, *from)
	if err != nil {
		return nil, fmt.Errorf("--from: %w", err)
	}
	t, err := time.Parse(time.DateOnly, *to)
	if err != nil {
		return nil, fmt.Errorf("--to: %w", err)
	}
	if t.Before(f) {
		return nil, errors.New("--to is before --from")
	}
	return &dayRange{from: f, to: t}, nil
}

// deps are the connections both commands need.
type deps struct {
	nc      *nats.Conn
	buckets *natsx.BucketKeeper
	db      *pdb.DB
	sites   *registry.Watcher[registry.Site]
	sched   *scheduler.Scheduler
	aggWeb  *aggregateweb.Aggregator
	rotator *umamirotate.Rotator
	reg     *prometheus.Registry
	stop    func()
}

// retryEvery is how often a follower tries to take the jobs lock. a third of
// a second keeps takeover close to the lock ttl.
const retryEvery = 300 * time.Millisecond

// connectTimeout bounds the startup connections, so a wrong address fails
// the process instead of hanging it.
const connectTimeout = 30 * time.Second

func open(ctx context.Context, env *config.Env, log *slog.Logger, opsAddr bool) (*deps, string, error) {
	natsCfg, natsErr := config.LoadNATS(env)
	// cp_system for the tasks that run control plane logic
	dbCfg, dbErr := pdb.LoadPoolConfig(env, pdb.RoleJobs, pdb.RoleCPSystem)
	umamiCfg, umamiErr := config.LoadUmamiAdmin(env)
	pwKey := env.UmamiUserPasswordKey()
	apiRet, apiRetErr := config.LoadAPIRetention(env)
	stellarCfg, stellarErr := config.LoadStellar(env)
	var addr string
	if opsAddr {
		addr = env.String("JOBS_OPS_ADDR")
	}
	// one error naming every missing key, as config promises
	if err := errors.Join(natsErr, dbErr, umamiErr, apiRetErr, stellarErr, env.Err()); err != nil {
		return nil, "", err
	}
	key, err := umami.NewPasswordKey(pwKey.Reveal())
	if err != nil {
		return nil, "", err
	}
	umamiClient, err := umami.New(umamiCfg.URL, nil)
	if err != nil {
		return nil, "", err
	}
	admin, err := umami.NewAdmin(umamiClient, umami.SeededUsername, umamiCfg.Password.Reveal())
	if err != nil {
		return nil, "", err
	}
	holder, err := holderID()
	if err != nil {
		return nil, "", err
	}

	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	d, err := pdb.Open(cctx, dbCfg, natsx.ServiceJobs)
	if err != nil {
		return nil, "", err
	}
	nc, err := natsx.Connect(natsCfg, natsx.ServiceJobs, log)
	if err != nil {
		d.Close()
		return nil, "", err
	}
	closeAll := func() { nc.Close(); d.Close() }
	js, err := jetstream.New(nc)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	// locks is memory-backed and gone after a restart of a single server;
	// jobs recreates it itself, so it recovers without an operator
	buckets, err := natsx.NewBucketKeeper(js, natsCfg.Replicas, log, natsx.BucketLocks)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	if err := buckets.Ensure(cctx); err != nil {
		closeAll()
		return nil, "", err
	}
	locks, err := js.KeyValue(cctx, natsx.BucketLocks)
	if err != nil {
		closeAll()
		return nil, "", fmt.Errorf("bind %s (run platformctl nats apply): %w", natsx.BucketLocks, err)
	}
	lock, err := natsx.NewLock(locks, natsx.LockJobs, holder)
	if err != nil {
		closeAll()
		return nil, "", err
	}

	reg := obs.NewRegistry()
	sm, err := scheduler.NewMetrics(reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	em, err := edgestats.NewMetrics(reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	sites := registry.NewSiteWatcher(js, log)
	drainer, err := edgestats.New(js, sites, log, em)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	pruner, err := statsdedup.New(time.Now, log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	// the reconcile task publishes otlp token state as well as sites
	publisher, err := registry.NewPublisher(cctx, js)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	reconciler, err := reconcile.New(provision.Reconciler{
		Admin: admin, Registry: publisher, Key: key, Now: time.Now, Log: log,
	}, log)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	sessionPruner, err := sessionprune.New(time.Now, log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	pruneSessions := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := sessionPruner.Prune(ctx, l.SystemTx)
		return err
	}
	// tenant domains resolve through the system resolver; the guard refuses
	// private and loopback answers and has no setting to allow them
	ng, err := netguard.New(net.DefaultResolver)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	verifier, err := verifydomains.New(&domaincheck.Checker{Guard: ng, Resolver: net.DefaultResolver}, time.Now, log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	verify := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := verifier.Run(ctx, l.SystemTx)
		return err
	}
	rotator := &umamirotate.Rotator{Admin: admin, Key: key, Now: time.Now, Log: log}
	// the alert "daily umami password rotation not run" compares this day
	// with the current utc day. Rotate returns today both after rotating and
	// when the database already records today's rotation, and an error
	// leaves the gauge alone, so it only moves when today is really done
	rotatedDay := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "jobs_umami_rotated_epoch_day",
		Help: "utc epoch day (unix days) of the newest completed umami password rotation, as read or written by the last successful umami-rotate run.",
	})
	if err := reg.Register(rotatedDay); err != nil {
		closeAll()
		return nil, "", fmt.Errorf("jobs: register metrics: %w", err)
	}
	rotate := func(ctx context.Context, l *scheduler.Leader) error {
		res, err := rotator.Rotate(ctx, l.SystemTx)
		if err == nil {
			rotatedDay.Set(float64(res.Day))
		}
		return err
	}
	prune := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := pruner.Prune(ctx, l.Tx)
		return err
	}
	rollup, err := apirollup.New(log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	rollupAPI := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := rollup.Run(ctx, l.Tx)
		return err
	}
	partitioner, err := apipartition.New(time.Now, log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	partitionAPI := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := partitioner.Ensure(ctx, apipartition.TxFunc(l.Tx))
		return err
	}
	retention, err := apiretention.New(apiRet, partitioner, time.Now, log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	retainAPI := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := retention.Run(ctx, l.Tx)
		return err
	}
	// web aggregates are read with the umami admin, like the reconciler,
	// so operators see exactly the numbers umami shows tenants
	aggWeb, err := aggregateweb.New(admin, sites, time.Now, log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	aggregate := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := aggWeb.Run(ctx, l.Tx)
		return err
	}
	followup, err := erasurefollowup.New(aggWeb, time.Now, log)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	followErasure := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := followup.Run(ctx, l.Tx, l.SystemTx)
		return err
	}
	// ledger activity of the declared accounts and contracts; a fresh
	// reader per run keeps its ledger lookups to that run
	chainTask, err := chainactivity.New(func() (*chain.Reader, error) {
		h, r, err := chain.Sources(stellarCfg)
		if err != nil {
			return nil, err
		}
		return chain.NewReader(h, r)
	}, time.Now, log, reg)
	if err != nil {
		closeAll()
		return nil, "", err
	}
	readChain := func(ctx context.Context, l *scheduler.Leader) error {
		_, err := chainTask.Run(ctx, l.Tx)
		return err
	}
	tasks := []scheduler.Task{{
		Name: edgestats.TaskName,
		Run: func(ctx context.Context, l *scheduler.Leader) error {
			return drainer.Run(ctx, l.Tx)
		},
		Once: func(ctx context.Context, l *scheduler.Leader) error {
			n, err := drainer.Drain(ctx, l.Tx)
			log.Info("edge stats drained", "messages", n)
			return err
		},
	}, {
		Name:  statsdedup.TaskName,
		Every: statsdedup.Every,
		Run:   prune,
		Once:  prune,
	}, {
		Name:  reconcile.TaskName,
		Every: reconcile.Every,
		Run:   reconciler.Run,
		Once:  reconciler.Run,
	}, {
		Name:  umamirotate.TaskName,
		Every: umamirotate.Every,
		Run:   rotate,
		Once:  rotate,
	}, {
		Name:  sessionprune.TaskName,
		Every: sessionprune.Every,
		Run:   pruneSessions,
		Once:  pruneSessions,
	}, {
		Name:  verifydomains.TaskName,
		Every: verifydomains.Every,
		Run:   verify,
		Once:  verify,
	}, {
		Name:  apirollup.TaskName,
		Every: apirollup.Every,
		Run:   rollupAPI,
		Once:  rollupAPI,
	}, {
		Name:  apipartition.TaskName,
		Every: apipartition.Every,
		Run:   partitionAPI,
		Once:  partitionAPI,
	}, {
		Name:  apiretention.TaskName,
		Every: apiretention.Every,
		Run:   retainAPI,
		Once:  retainAPI,
	}, {
		Name:  aggregateweb.TaskName,
		Every: aggregateweb.Every,
		Run:   aggregate,
		Once:  aggregate,
	}, {
		Name:  erasurefollowup.TaskName,
		Every: erasurefollowup.Every,
		Run:   followErasure,
		Once:  followErasure,
	}, {
		Name:  chainactivity.TaskName,
		Every: chainactivity.Every,
		Run:   readChain,
		Once:  readChain,
	}}
	return &deps{
		nc:      nc,
		buckets: buckets,
		db:      d,
		sites:   sites,
		aggWeb:  aggWeb,
		rotator: rotator,
		reg:     reg,
		sched: &scheduler.Scheduler{
			Lock: lock, DB: d, Tasks: tasks, Log: log, Metrics: sm, RetryEvery: retryEvery,
			Buckets: buckets,
		},
		stop: closeAll,
	}, addr, nil
}

// holderID names this process in the lock. it must be unique per process,
// so a restarted daemon on the same host is a different holder.
func holderID() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("hostname: %w", err)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ReplaceAll(host, ".", "-") + "-" + hex.EncodeToString(b), nil
}

func daemon(ctx context.Context, env *config.Env, log *slog.Logger) error {
	dp, addr, err := open(ctx, env, log, true)
	if err != nil {
		return err
	}
	defer dp.stop()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	pool, err := dp.db.Pool(pdb.RoleJobs)
	if err != nil {
		return err
	}
	// ready means able to lead: a follower is ready too, so both core nodes
	// stay in rotation and either can take over
	ops := obs.NewOpsHandler(dp.reg, log,
		obs.Check{Name: "postgres", Fn: pool.Ping},
		obs.Check{Name: "nats", Fn: func(context.Context) error {
			if s := dp.nc.Status(); s != nats.CONNECTED {
				return fmt.Errorf("nats connection is %s", s)
			}
			return nil
		}},
		obs.Check{Name: "registry", Fn: func(context.Context) error {
			if !dp.sites.Loaded() {
				return errors.New("site registry snapshot not loaded")
			}
			return nil
		}},
		obs.Check{Name: "buckets", Fn: dp.buckets.Ready},
	)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	wg.Go(func() { dp.buckets.Run(ctx) })
	wg.Go(func() { errs <- dp.sites.Run(ctx) })
	wg.Go(func() { errs <- obs.ServeListener(ctx, obs.NewServer(addr, ops), ln, log) })
	wg.Go(func() { errs <- dp.sched.Run(ctx) })
	// the first to return ends the daemon: a scheduler that cannot run, or an
	// ops server that died, must not leave a half-working process behind
	err = <-errs
	cancel()
	wg.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return ctx.Err()
}

func runOnce(ctx context.Context, env *config.Env, log *slog.Logger, name string, opts runOpts) error {
	dp, _, err := open(ctx, env, log, false)
	if err != nil {
		return err
	}
	defer dp.stop()
	if opts.force {
		// same lock and fence as a regular run; only the recorded day is ignored
		for i := range dp.sched.Tasks {
			if dp.sched.Tasks[i].Name == umamirotate.TaskName {
				dp.sched.Tasks[i].Once = func(ctx context.Context, l *scheduler.Leader) error {
					r := *dp.rotator
					r.Force = true
					_, err := r.Rotate(ctx, l.SystemTx)
					return err
				}
			}
		}
	}
	days := opts.days
	if days != nil {
		// a backfill is the same task over another range, under the same
		// lock and fence as a regular run
		for i := range dp.sched.Tasks {
			if dp.sched.Tasks[i].Name == aggregateweb.TaskName {
				dp.sched.Tasks[i].Once = func(ctx context.Context, l *scheduler.Leader) error {
					res, err := dp.aggWeb.Backfill(ctx, l.Tx, days.from, days.to)
					log.Info("aggregate web backfill", "sites", res.Sites, "days", res.Days, "skipped", res.Skipped)
					return err
				}
			}
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { _ = dp.sites.Run(ctx) })
	// a one-off run must not wait forever on a registry that never loads
	if err := waitLoaded(ctx, dp.sites, connectTimeout); err != nil {
		cancel()
		wg.Wait()
		return err
	}
	err = dp.sched.RunOnce(ctx, name)
	cancel()
	wg.Wait()
	return err
}

func waitLoaded(ctx context.Context, sites *registry.Watcher[registry.Site], within time.Duration) error {
	deadline := time.Now().Add(within)
	for !sites.Loaded() {
		if time.Now().After(deadline) {
			return fmt.Errorf("site registry snapshot not loaded within %s (run platformctl nats apply)", within)
		}
		t := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// capacity measures the api metric tables of a running database, for the
// api volume run and later capacity reviews. it reads sizes and row
// counts only, so it needs neither the lock nor nats.
func capacity(ctx context.Context, env *config.Env, stdout io.Writer) error {
	cfg, err := pdb.LoadPoolConfig(env, pdb.RoleJobs)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	d, err := pdb.Open(cctx, cfg, natsx.ServiceJobs)
	if err != nil {
		return err
	}
	defer d.Close()
	var tables []apicapacity.Table
	err = d.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		var err error
		tables, err = apicapacity.Measure(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	type row struct {
		Table           string   `json:"table"`
		Rows            int64    `json:"rows"`
		Bytes           int64    `json:"bytes"`
		Partitions      int      `json:"partitions"`
		EmptyPartitions int      `json:"empty_partitions"`
		EmptyBytes      int64    `json:"empty_bytes"`
		BytesPerRow     *float64 `json:"bytes_per_row"`
	}
	out := make([]row, 0, len(tables))
	for _, t := range tables {
		r := row{Table: t.Name, Rows: t.Rows, Bytes: t.Bytes, Partitions: t.Partitions,
			EmptyPartitions: t.EmptyPartitions, EmptyBytes: t.EmptyBytes}
		// an empty table has no per-row figure; json null says so
		if t.Rows > 0 {
			v := t.BytesPerRow()
			r.BytesPerRow = &v
		}
		out = append(out, r)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// retainWeb runs `jobs retain-web`. it needs no nats and no leader lock:
// each batch deletes only rows past the cutoff, so two runs at once delete
// the same rows once between them.
func retainWeb(ctx context.Context, env *config.Env, log *slog.Logger, now time.Time) error {
	cfg, err := retainweb.Load(env)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	conn, err := pgx.Connect(cctx, cfg.ConnString())
	if err != nil {
		return fmt.Errorf("connect to %s as %s: %w", cfg.Database, retainweb.Role, err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	_, err = retainweb.Run(ctx, conn, retainweb.Cutoff(now, cfg.Months), now, log)
	return err
}
