package config

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
)

// Gateway is what the collect gateway needs besides nats. every value is
// required: the starting values live in deploy/compose/dev.yml and
// docs/reference/gateway-limits.md, so a deployment that forgets one fails at
// startup instead of running with a limit nobody chose.
type Gateway struct {
	// NodeID names this gateway on EDGE_STATS and edge.ipcluster. it must be
	// unique per instance and stable across restarts of the same instance.
	NodeID string
	// Addr is the listen address for caddy (tracker files, challenge, events).
	Addr string
	// OpsAddr is the private listen address of /healthz, /readyz, /metrics.
	OpsAddr string
	// ProofBits is the difficulty handed out in challenges (PROOF_BITS).
	ProofBits int
	// ReplayLimit is how many events one proof may carry on this node.
	ReplayLimit int
	// IPClusterThreshold is the distinct visitor token count per site and
	// truncated prefix per hour from which events are quarantined.
	IPClusterThreshold int
	// per-site and per-prefix token buckets, per node
	SiteRatePerSecond   int
	SiteBurst           int
	PrefixRatePerSecond int
	PrefixBurst         int
	// MaxBody is the largest accepted event body in bytes.
	MaxBody int
	// ClientIPHeader is the one header the local caddy sets to the client
	// address.
	ClientIPHeader string
	// DBIPPath is the db-ip country lite mmdb file.
	DBIPPath string
	// ReleaseDir holds manifest.json, revoked.json and <version>/sdk.js.
	ReleaseDir string
	// SecureTransport says browsers reach the gateway over https, so they
	// send fetch metadata (gateway.NewSignalsChecker).
	SecureTransport bool
}

// maxProofBits mirrors gateway.MaxProofBits; config cannot import the gateway.
const maxProofBits = 24

// maxBody mirrors events.MaxBody, the parser's own limit.
const maxBody = 8 << 10

var nodeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// LoadGateway reads the gateway settings.
func LoadGateway(e *Env) (Gateway, error) {
	c := Gateway{
		NodeID:              e.String("GATEWAY_NODE_ID"),
		Addr:                e.ListenAddr("GATEWAY_ADDR"),
		OpsAddr:             e.ListenAddr("GATEWAY_OPS_ADDR"),
		ProofBits:           e.Int("PROOF_BITS", 0, maxProofBits),
		ReplayLimit:         e.Int("PROOF_REPLAY_LIMIT", 1, 100_000),
		IPClusterThreshold:  e.Int("IPCLUSTER_THRESHOLD", 1, 100_000),
		SiteRatePerSecond:   e.Int("RATE_SITE_PER_SECOND", 1, 1_000_000),
		SiteBurst:           e.Int("RATE_SITE_BURST", 1, 1_000_000),
		PrefixRatePerSecond: e.Int("RATE_PREFIX_PER_SECOND", 1, 1_000_000),
		PrefixBurst:         e.Int("RATE_PREFIX_BURST", 1, 1_000_000),
		MaxBody:             e.Int("GATEWAY_MAX_BODY", 1, maxBody),
		ClientIPHeader:      e.String("CLIENT_IP_HEADER"),
		DBIPPath:            e.String("DBIP_MMDB"),
		ReleaseDir:          e.String("TRACKER_RELEASE_DIR"),
		SecureTransport:     e.Bool("GATEWAY_SECURE_TRANSPORT"),
	}
	if c.NodeID != "" && !nodeIDRe.MatchString(c.NodeID) {
		e.fail("GATEWAY_NODE_ID", "must be 1 to 64 letters, digits, - or _ (it is a nats subject token)")
	}
	if c.Addr != "" && c.Addr == c.OpsAddr {
		e.fail("GATEWAY_OPS_ADDR", "must differ from GATEWAY_ADDR, the ops endpoints stay on a private port")
	}
	return c, e.Err()
}

// ListenAddr returns a required host:port with a numeric port. the host may
// be empty (all interfaces).
func (e *Env) ListenAddr(key string) string {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return ""
	}
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		e.fail(key, "must be host:port, such as 127.0.0.1:8080")
		return ""
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		e.fail(key, fmt.Sprintf("has port %q, want 1 to 65535", port))
		return ""
	}
	return v
}

// Bool returns a required value that is exactly true or false.
func (e *Env) Bool(key string) bool {
	v, ok := e.raw(key)
	switch {
	case !ok:
		e.fail(key, "is required but not set")
	case v == "true":
		return true
	case v == "false":
	default:
		e.fail(key, "must be true or false")
	}
	return false
}
