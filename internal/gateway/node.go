package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
	"github.com/justmert/veristar/internal/registry"
)

// bounds of the in-memory tables. each is a few hundred bytes per entry, so
// the three together stay well under 200 MB on an edge node.
const (
	replayEntries = 200_000
	clusterKeys   = 200_000
	volumeKeys    = 200_000
	// sightingKeys holds the networks reported today per site, about 100
	// bytes each
	sightingKeys = 500_000
)

// NodeConfig is everything one gateway process needs. cmd/gateway fills it
// from the environment; integration tests fill it the same way with an
// injected clock.
type NodeConfig struct {
	Gateway config.Gateway
	NC      *nats.Conn
	// Replicas is NATS_REPLICAS, the replica count edge_secrets is
	// recreated with when a restart lost it.
	Replicas int
	Log      *slog.Logger
	Registry *prometheus.Registry
	// Now is the clock of every component. nil means time.Now.
	Now func() time.Time
}

// Node is one assembled gateway: the public handler, the ops handler and
// the background loops behind them.
type Node struct {
	cfg      NodeConfig
	js       jetstream.JetStream
	secrets  jetstream.KeyValue
	buckets  *natsx.BucketKeeper
	sites    *registry.Watcher[registry.Site]
	keys     *KeyStore
	visitor  *Visitor
	cluster  *IPCluster
	stats    *Stats
	pub      *Publisher
	geo      *Geo
	network  *Network
	releases *Releases
	public   *Server
	ops      http.Handler
	checks   []obs.Check
}

// NewNode builds every component and checks the configuration. it reads the
// release directory and the geo file, recreates edge_secrets when a restart
// lost it and binds it, so a wrong path or a missing topology fails here.
func NewNode(ctx context.Context, c NodeConfig) (*Node, error) {
	if c.NC == nil || c.Log == nil || c.Registry == nil {
		return nil, errors.New("gateway: NodeConfig needs a connection, a logger and a registry")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	g := c.Gateway
	js, err := jetstream.New(c.NC)
	if err != nil {
		return nil, fmt.Errorf("gateway: jetstream: %w", err)
	}
	n := &Node{cfg: c, js: js}
	if n.releases, err = LoadReleases(g.ReleaseDir); err != nil {
		return nil, err
	}
	if n.geo, err = OpenGeo(g.DBIPPath); err != nil {
		return nil, err
	}
	// the alert "db-ip file older than 45 days" reads the build time of the
	// file in use; Reload swaps the reader inside the same Geo, so the gauge
	// follows a monthly update without registering again
	if err := c.Registry.Register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "gateway_geo_build_timestamp_seconds",
		Help: "unix build time recorded in the metadata of the db-ip country file the gateway is using.",
	}, func() float64 { return float64(n.geo.BuildTime().Unix()) })); err != nil {
		return nil, fmt.Errorf("gateway: register geo metric: %w", err)
	}
	// the network data sits beside the country file
	// (scripts/fetch-network.sh writes both into one directory)
	if n.network, err = OpenNetwork(filepath.Dir(g.DBIPPath)); err != nil {
		return nil, err
	}
	if err := c.Registry.Register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "gateway_network_build_timestamp_seconds",
		Help: "unix build time recorded in the metadata of the db-ip asn file the gateway is using.",
	}, func() float64 { return float64(n.network.BuildTime().Unix()) })); err != nil {
		return nil, fmt.Errorf("gateway: register network metric: %w", err)
	}
	sightings, err := NewNetworkSightings(sightingKeys)
	if err != nil {
		return nil, err
	}
	// edge_secrets is memory-backed and gone after a restart of a single
	// server; the gateway recreates it itself so it never waits for an
	// operator
	if n.buckets, err = natsx.NewBucketKeeper(js, c.Replicas, c.Log, natsx.BucketEdgeSecrets); err != nil {
		return nil, err
	}
	if err := n.buckets.Ensure(ctx); err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	if n.secrets, err = js.KeyValue(ctx, natsx.BucketEdgeSecrets); err != nil {
		return nil, fmt.Errorf("gateway: bind %s: %w", natsx.BucketEdgeSecrets, err)
	}
	if n.keys, err = NewKeyStore(ctx, js, KeyStoreOptions{Now: c.Now}); err != nil {
		return nil, err
	}
	challenger, err := NewChallenger(n.keys, g.ProofBits)
	if err != nil {
		return nil, err
	}
	proofs, err := NewProofVerifier(n.keys, g.ReplayLimit, replayEntries)
	if err != nil {
		return nil, err
	}
	ipr, err := NewClientIPReader(g.ClientIPHeader)
	if err != nil {
		return nil, err
	}
	if n.visitor, err = NewVisitor(n.secrets, c.Now); err != nil {
		return nil, err
	}
	if n.cluster, err = NewIPCluster(c.NC, g.NodeID, g.IPClusterThreshold, clusterKeys, c.Now); err != nil {
		return nil, err
	}
	volume, err := NewVisitorVolume(g.VisitorEventsPerHour, volumeKeys, c.Now)
	if err != nil {
		return nil, err
	}
	siteRate := Rate{PerSecond: float64(g.SiteRatePerSecond), Burst: float64(g.SiteBurst)}
	prefixRate := Rate{PerSecond: float64(g.PrefixRatePerSecond), Burst: float64(g.PrefixBurst)}
	limiter, err := NewRateLimiter(siteRate, prefixRate, DefaultRateLimitKeys, c.Now)
	if err != nil {
		return nil, err
	}
	// challenges get their own buckets at the same rates: a page load takes
	// one challenge and sends at least one event, so a visitor within the
	// event limits is within the challenge limits too
	challengeLimiter, err := NewRateLimiter(siteRate, prefixRate, DefaultRateLimitKeys, c.Now)
	if err != nil {
		return nil, err
	}
	scriptCounts, err := NewScriptCountLimiter(DefaultScriptCountRate, DefaultRateLimitKeys, c.Now)
	if err != nil {
		return nil, err
	}
	if n.stats, err = NewStats(StatsConfig{Node: g.NodeID, JS: js, Log: c.Log, Registerer: c.Registry, Now: c.Now}); err != nil {
		return nil, err
	}
	if n.pub, err = NewPublisher(js, c.Now); err != nil {
		return nil, err
	}
	n.sites = registry.NewSiteWatcher(js, c.Log)
	n.public, err = NewServer(ServerConfig{
		Sites:            n.sites,
		Challenger:       challenger,
		Proofs:           proofs,
		Signals:          NewSignalsChecker(g.SecureTransport),
		ClientIP:         ipr,
		Geo:              n.geo,
		Network:          n.network,
		Sightings:        sightings,
		Visitor:          n.visitor,
		Cluster:          n.cluster,
		Volume:           volume,
		Limiter:          limiter,
		ChallengeLimiter: challengeLimiter,
		Stats:            n.stats,
		Publisher:        n.pub,
		Releases:         n.releases,
		Scripts:          NewScriptAttribution(n.sites),
		ScriptCounts:     scriptCounts,
		MaxBody:          g.MaxBody,
		Log:              c.Log,
		Registerer:       c.Registry,
		Now:              c.Now,
	})
	if err != nil {
		return nil, err
	}
	n.checks = ReadinessChecks(n.sites, n.keys, n.visitor, n.pub, n.buckets)
	n.ops = obs.NewOpsHandler(c.Registry, c.Log, n.checks...)
	return n, nil
}

// Handler is the public handler that caddy proxies to.
func (n *Node) Handler() http.Handler { return n.public }

// Ops serves /healthz, /readyz and /metrics.
func (n *Node) Ops() http.Handler { return n.ops }

// Checks are the /readyz checks, for the node's composite health.
func (n *Node) Checks() []obs.Check { return n.checks }

// Stats exposes the outcome counters, so a test can flush them at once.
func (n *Node) Stats() *Stats { return n.stats }

// Visitor exposes the daily secrets, so a test can rotate on its clock.
func (n *Node) Visitor() *Visitor { return n.visitor }

// Reload reads the geo file, the network data and the revoked tracker list
// again, for the monthly db-ip update and a tracker revocation. each keeps
// its previous state when the new one does not load.
func (n *Node) Reload() error {
	return errors.Join(n.geo.Reload(), n.network.Reload(), n.releases.ReloadRevoked())
}

// Run runs the background loops until ctx ends: the registry watch, daily
// secret rotation, ip-cluster gossip, visitor token requests, the stats
// flush and the readiness canary. on return the last stats window has been
// flushed and the geo file closed.
func (n *Node) Run(ctx context.Context) error {
	log := n.cfg.Log
	if err := n.cluster.Start(ctx, log); err != nil {
		return err
	}
	// each request is answered under its own bounded context inside the
	// handler, not under the node's run context
	tokens, err := n.visitor.ServeTokenRequests(n.cfg.NC, log) //nolint:contextcheck // see above
	if err != nil {
		return fmt.Errorf("gateway: serve %s: %w", natsx.SubjectVisitorToken, err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { _ = n.sites.Run(ctx) })
	wg.Go(func() { n.visitor.Run(ctx, log) })
	wg.Go(func() { _ = n.stats.Run(ctx) })
	wg.Go(func() { n.buckets.Run(ctx) })
	wg.Go(func() { n.pub.RunCanary(ctx, n.secrets, n.cfg.Gateway.NodeID, log, n.buckets.Kick) })
	<-ctx.Done()
	_ = tokens.Unsubscribe()
	wg.Wait()
	return errors.Join(n.geo.Close(), n.network.Close())
}
