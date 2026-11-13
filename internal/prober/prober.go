package prober

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// gatus groups and names of the external endpoints. the slo group is what
// cmd/sloreport reads; the keys must stay stable once reports exist.
const (
	groupSLO   = "slo"
	groupProbe = "probe"
	groupNodes = "nodes"
)

var (
	KeyIngestion     = GatusKey(groupSLO, "ingestion")
	KeyDashboard     = GatusKey(groupSLO, "dashboard")
	KeyFreshnessWeb  = GatusKey(groupSLO, "freshness web")
	KeyFreshnessAPI  = GatusKey(groupSLO, "freshness api")
	KeyWebEvent      = GatusKey(groupProbe, "web event")
	KeyOTLPMetric    = GatusKey(groupProbe, "otlp metric")
	KeyConsole       = GatusKey(groupProbe, "console")
	KeyWebHost       = GatusKey(groupProbe, "web host")
	KeyMachineAPI    = GatusKey(groupProbe, "machine api")
	KeyTrackerFiles  = GatusKey(groupProbe, "tracker integrity")
	errNotMeasured   = errors.New("not measured")
	freshnessWindow  = 20 * time.Hour
	gatusPushTimeout = 10 * time.Second
)

// NodeKey is the gatus key of one host on one node.
func NodeKey(node, host string) string { return GatusKey(groupNodes, node+" "+host) }

type metrics struct {
	success  *prometheus.GaugeVec
	runs     *prometheus.CounterVec
	duration *prometheus.GaugeVec
	lastRun  *prometheus.GaugeVec
	expiry   *prometheus.GaugeVec
	backlog  *prometheus.GaugeVec
	pushFail *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		success: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "prober_check_success", Help: "1 when the last run of the check passed, else 0. check is the gatus endpoint key.",
		}, []string{"check"}),
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prober_check_runs_total", Help: "check runs by check and result (ok, fail).",
		}, []string{"check", "result"}),
		duration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "prober_check_duration_seconds", Help: "duration of the last run of the check.",
		}, []string{"check"}),
		lastRun: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "prober_check_last_run_timestamp_seconds", Help: "unix time of the last run of the check.",
		}, []string{"check"}),
		expiry: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "prober_tls_certificate_expiry_timestamp_seconds", Help: "notAfter of the leaf certificate a node served for a host, read over a connection to the node's address.",
		}, []string{"node", "host"}),
		backlog: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "prober_freshness_backlog_events", Help: "probe events accepted longer ago than the freshness limit and not yet visible in the machine api.",
		}, []string{"pipeline"}),
		pushFail: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prober_gatus_push_failures_total", Help: "results gatus did not accept, by endpoint key.",
		}, []string{"check"}),
	}
	for _, c := range []prometheus.Collector{m.success, m.runs, m.duration, m.lastRun, m.expiry, m.backlog, m.pushFail} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("prober: register metrics: %w", err)
		}
	}
	return m, nil
}

// Prober runs the checks.
type Prober struct {
	cfg   Config
	log   *slog.Logger
	m     *metrics
	http  *http.Client
	gatus *http.Client
	clock func() time.Time

	web, api Freshness
}

// New checks the configuration and registers the metrics.
func New(cfg Config, log *slog.Logger, reg prometheus.Registerer) (*Prober, error) {
	m, err := newMetrics(reg)
	if err != nil {
		return nil, err
	}
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("prober: http.DefaultTransport is not an *http.Transport")
	}
	tr := def.Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: cfg.Roots, MinVersion: tls.VersionTLS12}
	p := &Prober{
		cfg: cfg, log: log, m: m, clock: time.Now,
		http:  &http.Client{Transport: tr, Timeout: cfg.Timeout},
		gatus: &http.Client{Timeout: gatusPushTimeout},
		web:   Freshness{MaxDelay: cfg.FreshnessMaxDelay, Window: freshnessWindow},
		api:   Freshness{MaxDelay: cfg.FreshnessMaxDelay, Window: freshnessWindow},
	}
	for _, k := range p.keys() {
		m.success.WithLabelValues(k)
		m.runs.WithLabelValues(k, "ok")
		m.runs.WithLabelValues(k, "fail")
	}
	return p, nil
}

func (p *Prober) keys() []string {
	k := []string{KeyIngestion, KeyDashboard, KeyFreshnessWeb, KeyFreshnessAPI, KeyWebEvent, KeyOTLPMetric,
		KeyConsole, KeyWebHost, KeyMachineAPI, KeyTrackerFiles}
	for _, n := range p.cfg.Nodes {
		for _, h := range n.Hosts {
			k = append(k, NodeKey(n.Name, h))
		}
	}
	return k
}

// Run checks every Interval until ctx ends. a cycle that is still running
// when the next tick comes makes that tick skip, so results never overlap.
func (p *Prober) Run(ctx context.Context) error {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	for {
		p.Cycle(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// result records one check in prometheus and pushes it to gatus.
func (p *Prober) result(ctx context.Context, key string, err error, d time.Duration) {
	v, res := 1.0, "ok"
	if err != nil {
		v, res = 0, "fail"
		p.log.Warn("check failed", "check", key, "error", err.Error())
	}
	p.m.success.WithLabelValues(key).Set(v)
	p.m.runs.WithLabelValues(key, res).Inc()
	p.m.duration.WithLabelValues(key).Set(d.Seconds())
	p.m.lastRun.WithLabelValues(key).Set(float64(p.now().Unix()))
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatusPushTimeout)
	defer cancel()
	if perr := p.push(pctx, key, err, d); perr != nil {
		p.m.pushFail.WithLabelValues(key).Inc()
		p.log.Error("gatus push failed", "check", key, "error", perr.Error())
	}
}

func (p *Prober) timed(ctx context.Context, fn func(context.Context) error) (time.Duration, error) {
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	err := fn(cctx)
	return time.Since(start), err
}

// Cycle runs every check once. the pipeline checks are sequential, because
// freshness must know which sends were accepted; tracker and node checks run
// beside them.
func (p *Prober) Cycle(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { p.pipeline(ctx) })
	wg.Go(func() {
		d, err := p.timed(ctx, p.checkTracker)
		p.result(ctx, KeyTrackerFiles, err, d)
	})
	for _, n := range p.cfg.Nodes {
		for _, h := range n.Hosts {
			wg.Go(func() {
				start := time.Now()
				cctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
				r := p.checkNodeHost(cctx, n, h)
				cancel()
				if !r.expiry.IsZero() {
					p.m.expiry.WithLabelValues(n.Name, h).Set(float64(r.expiry.Unix()))
				}
				p.result(ctx, NodeKey(n.Name, h), r.err, time.Since(start))
			})
		}
	}
	wg.Wait()
}

func (p *Prober) pipeline(ctx context.Context) {
	// ingestion: an interval is available when the gateway answered 202
	// and the collector answered success (the design)
	sentAt := p.now()
	webD, webErr := p.timed(ctx, p.sendWebEvent)
	if webErr == nil {
		p.web.Sent(sentAt)
	}
	p.result(ctx, KeyWebEvent, webErr, webD)
	sentAt = p.now()
	otlpD, otlpErr := p.timed(ctx, p.sendOTLP)
	if otlpErr == nil {
		p.api.Sent(sentAt)
	}
	p.result(ctx, KeyOTLPMetric, otlpErr, otlpD)
	p.result(ctx, KeyIngestion, errors.Join(webErr, otlpErr), webD+otlpD)

	// freshness counts against the dashboard and pipeline objective, never
	// against ingestion
	webRead, webReadErr := p.freshness(ctx, &p.web, KeyFreshnessWeb, "web", p.webVisible)
	_, _ = p.freshness(ctx, &p.api, KeyFreshnessAPI, "api", p.apiVisible)

	// dashboard availability: console, umami host and an authenticated
	// machine api call, the /web/hourly read above
	conD, conErr := p.timed(ctx, func(c context.Context) error { return p.getOK(c, p.cfg.ConsoleURL.String()) })
	p.result(ctx, KeyConsole, conErr, conD)
	webHostD, webHostErr := p.timed(ctx, func(c context.Context) error { return p.getOK(c, p.cfg.WebURL.String()) })
	p.result(ctx, KeyWebHost, webHostErr, webHostD)
	p.result(ctx, KeyMachineAPI, webReadErr, webRead)
	p.result(ctx, KeyDashboard, errors.Join(conErr, webHostErr, webReadErr), conD+webHostD+webRead)
}

// freshness reads the visible count and judges it. it returns the read's
// duration and error, so the dashboard check can reuse the call.
func (p *Prober) freshness(ctx context.Context, f *Freshness, key, pipeline string,
	read func(context.Context, time.Time, time.Time) (int64, error)) (time.Duration, error) {
	readAt := p.now()
	from := f.From(readAt)
	var n int64
	d, err := p.timed(ctx, func(c context.Context) error {
		var e error
		n, e = read(c, from, readAt)
		return e
	})
	if err != nil {
		// the pipeline cannot be shown fresh when its numbers cannot be read
		p.result(ctx, key, fmt.Errorf("%w: %w", errNotMeasured, err), d)
		return d, err
	}
	v := f.Observe(readAt, from, n)
	p.m.backlog.WithLabelValues(pipeline).Set(float64(v.Backlog()))
	var ferr error
	if !v.Fresh {
		ferr = fmt.Errorf("%d of the probe's %s events accepted over %s ago are not visible (visible %d, expected at least %d)",
			v.Backlog(), pipeline, p.cfg.FreshnessMaxDelay, v.Visible, v.Expected)
	}
	p.result(ctx, key, ferr, d)
	return d, nil
}
