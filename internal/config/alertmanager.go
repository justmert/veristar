package config

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"
)

// Alertmanager is where the control plane reads the live alerts for the
// operator alerts page (docs/reference/control-plane-api.md section 11).
// every member of the cluster is listed: prometheus sends each alert to all
// of them, so any member that answers has the whole set, and the page can
// say which member did not answer.
type Alertmanager struct {
	// URLs are the base urls of the members, without a path. empty means
	// no alertmanager is configured; the route then answers
	// alert_source_not_configured instead of an empty list.
	URLs []*url.URL
	// TLS is set when ALERTMANAGER_TLS_CA is. the members ask for a client
	// certificate of the internal ca, which
	// ALERTMANAGER_TLS_CERT and ALERTMANAGER_TLS_KEY name.
	TLS *TLSFiles
}

// Configured reports whether at least one member is listed.
func (a Alertmanager) Configured() bool { return len(a.URLs) > 0 }

// ClientTLS returns the tls settings of the member connections, or nil for
// plain http members.
func (a Alertmanager) ClientTLS() (*tls.Config, error) {
	if a.TLS == nil {
		return nil, nil
	}
	return a.TLS.ClientConfig()
}

// LoadAlertmanager reads the optional ALERTMANAGER_URLS with
// ALERTMANAGER_TLS_CA, ALERTMANAGER_TLS_CERT and ALERTMANAGER_TLS_KEY.
func LoadAlertmanager(e *Env) (Alertmanager, error) {
	var c Alertmanager
	c.TLS = loadTLSFiles(e, "ALERTMANAGER_TLS_CA", "ALERTMANAGER_TLS_CERT", "ALERTMANAGER_TLS_KEY")
	v, ok := e.raw("ALERTMANAGER_URLS")
	if !ok {
		// tls files without a member to use them on is a broken deployment
		// file, not an intent to run without alerts
		if c.TLS != nil {
			e.fail("ALERTMANAGER_URLS", "is required when ALERTMANAGER_TLS_CA is set")
		}
		return c, e.Err()
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		u, err := url.Parse(part)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			e.fail("ALERTMANAGER_URLS", fmt.Sprintf("must be a comma separated list of http:// or https:// base urls without a path, got %q", redactURL(part)))
			return c, e.Err()
		}
		u.Path = ""
		if seen[u.String()] {
			e.fail("ALERTMANAGER_URLS", fmt.Sprintf("lists %s twice", u.String()))
			return c, e.Err()
		}
		seen[u.String()] = true
		switch {
		// an https member without the ca would be checked against the
		// system roots, which never sign the private network's servers
		case u.Scheme == "https" && c.TLS == nil:
			e.fail("ALERTMANAGER_TLS_CA", "is required for https:// urls")
			return c, e.Err()
		// configured tls that a plain url would quietly not use
		case u.Scheme == "http" && c.TLS != nil:
			e.fail("ALERTMANAGER_URLS", "must use https:// when ALERTMANAGER_TLS_CA is set, got "+u.String())
			return c, e.Err()
		}
		c.URLs = append(c.URLs, u)
	}
	return c, e.Err()
}
