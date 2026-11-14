// Package prober is the outside view of the platform. from
// the probe node it sends a real web event through the tracker protocol and
// a real otlp metric with a probe token, reads both back through the machine
// api, checks the console, the umami host, the tracker files and every node's
// tls, and pushes each result to gatus external endpoints and to prometheus.
package prober

import (
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/justmert/veristar/internal/config"
)

// Node is one server checked by address: every host it serves is fetched
// over a tls connection to Address with that host as sni.
type Node struct {
	Name    string
	Address string
	Hosts   []string
}

// Config is everything the prober reads from its environment.
type Config struct {
	OpsAddr  string
	Interval time.Duration
	// Timeout bounds one request.
	Timeout time.Duration
	// FreshnessMaxDelay is how long an accepted event may take to show in
	// the machine api (10 minutes).
	FreshnessMaxDelay time.Duration
	// CertMinValidity fails a node check whose certificate expires sooner
	// (14 days).
	CertMinValidity time.Duration

	IngestURL  *url.URL
	OTLPURL    *url.URL
	ConsoleURL *url.URL
	WebURL     *url.URL
	// MachineURL is the base of the machine api, ending in /api/metrics/v1.
	MachineURL *url.URL
	MachineKey config.Secret

	SiteID    string
	Origin    string
	UserAgent string
	OTLPToken config.Secret

	TrackerManifest string
	Nodes           []Node

	GatusURL   *url.URL
	GatusToken config.Secret

	// Roots verifies every tls connection. nil means the system pool.
	Roots *x509.CertPool
}

// Load reads the configuration. every key is required except PROBER_CA_FILE,
// which is set only where the nodes use a private ca (the local converge).
func Load(e *config.Env) (Config, error) {
	c := Config{
		OpsAddr:           e.ListenAddr("PROBER_OPS_ADDR"),
		Interval:          e.Duration("PROBER_INTERVAL"),
		Timeout:           e.Duration("PROBER_TIMEOUT"),
		FreshnessMaxDelay: e.Duration("PROBER_FRESHNESS_MAX_DELAY"),
		CertMinValidity:   e.Duration("PROBER_CERT_MIN_VALIDITY"),
		IngestURL:         e.URL("PROBER_INGEST_URL"),
		OTLPURL:           e.URL("PROBER_OTLP_URL"),
		ConsoleURL:        e.URL("PROBER_CONSOLE_URL"),
		WebURL:            e.URL("PROBER_WEB_URL"),
		MachineURL:        e.URL("PROBER_MACHINE_API_URL"),
		MachineKey:        e.Secret("PROBER_MACHINE_API_KEY", 46),
		SiteID:            e.String("PROBER_SITE_ID"),
		Origin:            e.String("PROBER_SITE_ORIGIN"),
		// umami drops events whose user agent looks like a bot, so the
		// operator sets a browser user agent here on purpose
		UserAgent:       e.String("PROBER_USER_AGENT"),
		OTLPToken:       e.Secret("PROBER_OTLP_TOKEN", 48),
		TrackerManifest: e.String("PROBER_TRACKER_MANIFEST"),
		GatusURL:        e.URL("PROBER_GATUS_URL"),
		GatusToken:      e.Secret("PROBER_GATUS_TOKEN", 24),
	}
	nodes := e.String("PROBER_NODES")
	if err := e.Err(); err != nil {
		return Config{}, err
	}
	var err error
	if c.Nodes, err = ParseNodes(nodes); err != nil {
		return Config{}, fmt.Errorf("PROBER_NODES: %w", err)
	}
	if c.Interval < 10*time.Second {
		return Config{}, fmt.Errorf("PROBER_INTERVAL: must be at least 10s, gatus refuses faster heartbeats")
	}
	if c.Timeout >= c.Interval {
		return Config{}, fmt.Errorf("PROBER_TIMEOUT: must be shorter than PROBER_INTERVAL")
	}
	if o, err := url.Parse(c.Origin); err != nil || o.Scheme != "https" && o.Scheme != "http" || o.Host == "" || o.Path != "" {
		return Config{}, fmt.Errorf("PROBER_SITE_ORIGIN: must be a scheme and host such as https://probe.example.org")
	}
	if !strings.HasSuffix(c.MachineURL.Path, "/api/metrics/v1") {
		return Config{}, fmt.Errorf("PROBER_MACHINE_API_URL: must end in /api/metrics/v1")
	}
	if e.Has("PROBER_CA_FILE") {
		b, err := os.ReadFile(e.String("PROBER_CA_FILE"))
		if err != nil {
			return Config{}, fmt.Errorf("PROBER_CA_FILE: %w", err)
		}
		c.Roots = x509.NewCertPool()
		if !c.Roots.AppendCertsFromPEM(b) {
			return Config{}, fmt.Errorf("PROBER_CA_FILE: no pem certificate in the file")
		}
	}
	return c, nil
}

// ParseNodes reads "name@address=host,host;name@address=host". the address
// is host:port, usually the node's public ipv4 and 443.
func ParseNodes(s string) ([]Node, error) {
	var out []Node
	seen := map[string]bool{}
	for part := range strings.SplitSeq(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		head, hosts, ok := strings.Cut(part, "=")
		name, addr, ok2 := strings.Cut(head, "@")
		if !ok || !ok2 || name == "" || hosts == "" {
			return nil, fmt.Errorf("%q is not name@address=host,host", part)
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return nil, fmt.Errorf("node %s: address %q is not host:port", name, addr)
		}
		if seen[name] {
			return nil, fmt.Errorf("node %s is listed twice", name)
		}
		seen[name] = true
		n := Node{Name: name, Address: addr}
		for h := range strings.SplitSeq(hosts, ",") {
			if h = strings.TrimSpace(h); h != "" {
				n.Hosts = append(n.Hosts, h)
			}
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("lists no node")
	}
	return out, nil
}
