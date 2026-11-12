package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/natsx"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/internal/registry"
)

// bounds of the in-memory tables. each is a few hundred bytes per entry, so
// the three together stay well under 200 MB on an edge node.
const (
	replayEntries = 200_000
	clusterKeys   = 200_000
)

// NodeConfig is everything one gateway process needs. cmd/gateway fills it
// from the environment; integration tests fill it the same way with an
// injected clock.
type NodeConfig struct {
	Gateway  config.Gateway
	NC       *nats.Conn
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
	sites    *registry.Watcher[registry.Site]
	keys     *KeyStore
	visitor  *Visitor
	cluster  *IPCluster
	stats    *Stats
	pub      *Publisher
	geo      *Geo
	releases *Releases
	public   *Server
	ops      http.Handler
}

// NewNode builds every component and checks the configuration. it reads the
// release directory and the geo file, and binds the edge_secrets bucket, so
// a wrong path or a missing topology fails here.
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
	limiter, err := NewRateLimiter(
		Rate{PerSecond: float64(g.SiteRatePerSecond), Burst: float64(g.SiteBurst)},
		Rate{PerSecond: float64(g.PrefixRatePerSecond), Burst: float64(g.PrefixBurst)},
		DefaultRateLimitKeys, c.Now)
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
		Sites:      n.sites,
		Challenger: challenger,
		Proofs:     proofs,
		Signals:    NewSignalsChecker(g.SecureTransport),
		ClientIP:   ipr,
		Geo:        n.geo,
		Visitor:    n.visitor,
		Cluster:    n.cluster,
		Limiter:    limiter,
		Stats:      n.stats,
		Publisher:  n.pub,
		Releases:   n.releases,
		MaxBody:    g.MaxBody,
		Log:        c.Log,
		Registerer: c.Registry,
		Now:        c.Now,
	})
	if err != nil {
		return nil, err
	}
	n.ops = obs.NewOpsHandler(c.Registry, c.Log, ReadinessChecks(n.sites, n.keys, n.visitor, n.pub)...)
	return n, nil
}

// Handler is the public handler that caddy proxies to.
func (n *Node) Handler() http.Handler { return n.public }

// Ops serves /healthz, /readyz and /metrics.
func (n *Node) Ops() http.Handler { return n.ops }

// Stats exposes the outcome counters, so a test can flush them at once.
func (n *Node) Stats() *Stats { return n.stats }

// Visitor exposes the daily secrets, so a test can rotate on its clock.
func (n *Node) Visitor() *Visitor { return n.visitor }

// Reload reads the geo file and the revoked tracker list again, for the
// monthly db-ip update and a tracker revocation. both keep their previous
// state when the new one does not load.
func (n *Node) Reload() error {
	return errors.Join(n.geo.Reload(), n.releases.ReloadRevoked())
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
	wg.Go(func() { n.pub.RunCanary(ctx, n.secrets, n.cfg.Gateway.NodeID, log) })
	<-ctx.Done()
	_ = tokens.Unsubscribe()
	wg.Wait()
	return n.geo.Close()
}
