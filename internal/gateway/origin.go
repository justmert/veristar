package gateway

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/justmert/veristar/internal/registry"
)

// origin is a parsed origin or origin pattern: scheme, host and effective
// port. wildcard means host is the parent of the allowed subdomains.
type origin struct {
	scheme   string
	host     string
	port     string
	wildcard bool
}

var defaultPort = map[string]string{"http": "80", "https": "443"}

// parseOrigin reads scheme://host[:port] as browsers serialise the Origin
// header: no path, query, fragment or credentials. allowWildcard accepts a
// leading "*." label, which only registered patterns may carry.
func parseOrigin(s string, allowWildcard bool) (origin, error) {
	u, err := url.Parse(s)
	if err != nil {
		return origin{}, errors.New("not a url")
	}
	scheme := strings.ToLower(u.Scheme)
	if _, ok := defaultPort[scheme]; !ok {
		return origin{}, errors.New("scheme is not http or https")
	}
	if u.Opaque != "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
		u.ForceQuery || strings.HasSuffix(s, "#") {
		return origin{}, errors.New("has more than scheme, host and port")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return origin{}, errors.New("has no host")
	}
	port := u.Port()
	if port == "" {
		if strings.HasSuffix(u.Host, ":") {
			return origin{}, errors.New("has an empty port")
		}
		port = defaultPort[scheme]
	}
	o := origin{scheme: scheme, host: host, port: port}
	if rest, ok := strings.CutPrefix(host, "*."); ok {
		if !allowWildcard {
			return origin{}, errors.New("has a wildcard")
		}
		// a wildcard directly over a top-level label would admit every
		// site under it
		if !strings.Contains(rest, ".") {
			return origin{}, errors.New("wildcard needs at least two labels after it")
		}
		o.host, o.wildcard = rest, true
	}
	if strings.Contains(o.host, "*") {
		return origin{}, errors.New("wildcard is allowed only as the first label")
	}
	if net.ParseIP(o.host) == nil && !validDNSName(o.host) {
		return origin{}, errors.New("host is not a dns name or ip address")
	}
	if o.wildcard && net.ParseIP(o.host) != nil {
		return origin{}, errors.New("wildcard over an ip address")
	}
	return o, nil
}

// validDNSName accepts ascii labels of letters, digits and hyphens. browsers
// send internationalised names in their punycode form, so registered
// origins must use that form too.
func validDNSName(h string) bool {
	if len(h) > 253 {
		return false
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// ValidOriginPattern checks a registered origin: scheme://host[:port] or
// scheme://*.host[:port]. the control plane calls it before storing a
// site's origins, so a pattern the gateway cannot match never reaches the
// registry.
func ValidOriginPattern(p string) error {
	if _, err := parseOrigin(p, true); err != nil {
		return fmt.Errorf("gateway: origin %q %w", p, err)
	}
	return nil
}

func (p origin) matches(o origin) bool {
	if p.scheme != o.scheme || p.port != o.port {
		return false
	}
	if p.wildcard {
		// a strict subdomain: the pattern's own host is not included, and
		// evil-example.org is not a subdomain of example.org
		return strings.HasSuffix(o.host, "."+p.host)
	}
	return p.host == o.host
}

// CheckOrigin checks the request's Origin header against the site's
// registered origins. scheme and port must match too, so an http page
// cannot send events for a site registered on https. a missing header, the
// opaque origin "null" and anything that does not parse are refused. a
// registered pattern that does not parse matches nothing.
func CheckOrigin(site registry.Site, header string) error {
	if header == "" {
		return reject(ReasonOriginMissing)
	}
	o, err := parseOrigin(header, false)
	if err != nil {
		return reject(ReasonOrigin)
	}
	for _, s := range site.Origins {
		p, err := parseOrigin(s, true)
		if err == nil && p.matches(o) {
			return nil
		}
	}
	return reject(ReasonOrigin)
}

// OriginHost returns the host of an Origin header as location.hostname
// gives it: lower case, without the port, an ipv6 address in brackets. ok is
// false when the header is not an origin.
func OriginHost(header string) (string, bool) {
	o, err := parseOrigin(header, false)
	if err != nil {
		return "", false
	}
	if strings.Contains(o.host, ":") {
		return "[" + o.host + "]", true
	}
	return o.host, true
}
