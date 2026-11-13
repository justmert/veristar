package config

import (
	"fmt"
	"net/url"
	"strings"
)

// NATS is how a service reaches the nats cluster.
type NATS struct {
	// URLs lists every server of the cluster, so the client can fail over
	// without waiting for gossip from a server it can no longer reach.
	URLs []string
	// Replicas is the replica count `platformctl nats apply` gives every
	// stream and bucket: 1 for the single dev server, 3 elsewhere.
	Replicas int
	// User and Password are the per-service credentials issued in this stage.
	// both are empty only on the dev server, which runs without auth.
	User     string
	Password Secret
	// TLS is set when NATS_TLS_CA is. the client then speaks tls from the
	// first byte (natsx.Options) and never falls back to plain text; nil
	// only for the dev server on a single machine.
	TLS *TLSFiles
}

// LoadNATS reads NATS_URL, NATS_REPLICAS, the optional pair NATS_USER and
// NATS_PASSWORD, and the optional NATS_TLS_CA with NATS_TLS_CERT and
// NATS_TLS_KEY.
func LoadNATS(e *Env) (NATS, error) {
	c := NATS{
		URLs:     natsURLs(e, "NATS_URL"),
		Replicas: e.Int("NATS_REPLICAS", 1, 5),
	}
	// an even replica count gives no extra fault tolerance in raft and is
	// almost always a typo, so it is refused rather than applied
	if c.Replicas > 0 && c.Replicas%2 == 0 {
		e.fail("NATS_REPLICAS", "must be odd (1, 3 or 5)")
	}
	user, hasUser := e.raw("NATS_USER")
	pass, hasPass := e.raw("NATS_PASSWORD")
	switch {
	case hasUser && !hasPass:
		e.fail("NATS_PASSWORD", "is required when NATS_USER is set")
	case hasPass && !hasUser:
		e.fail("NATS_USER", "is required when NATS_PASSWORD is set")
	case hasUser:
		c.User = user
		c.Password = Secret{v: pass}
	}
	c.TLS = loadTLSFiles(e, "NATS_TLS_CA", "NATS_TLS_CERT", "NATS_TLS_KEY")
	// a tls:// url without the ca would make the client trust the system
	// roots, which never sign the private network's servers
	if c.TLS == nil {
		for _, u := range c.URLs {
			if strings.HasPrefix(u, "tls://") {
				e.fail("NATS_TLS_CA", "is required for tls:// urls")
				break
			}
		}
	}
	return c, e.Err()
}

// natsURLs parses a comma separated list of nats:// or tls:// urls.
func natsURLs(e *Env, key string) []string {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		u, err := url.Parse(part)
		if err != nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Host == "" {
			e.fail(key, fmt.Sprintf("must be a comma separated list of nats:// or tls:// urls, got %q", redactURL(part)))
			return nil
		}
		// credentials belong in NATS_USER and NATS_PASSWORD, where they are
		// wrapped as a secret, not in a url that ends up in error messages
		if u.User != nil {
			e.fail(key, "must not contain credentials, use NATS_USER and NATS_PASSWORD")
			return nil
		}
		out = append(out, u.String())
	}
	return out
}

func redactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "unparseable value"
	}
	return u.Redacted()
}
