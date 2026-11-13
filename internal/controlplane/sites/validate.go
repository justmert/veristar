package sites

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/gateway"
)

const maxOrigins = 20

// ValidDomain checks a site domain (contract 3.2): a lowercase dns name in
// its ascii (a-label) form with at least two labels, no port and no trailing
// dot. browsers send internationalised hosts in punycode, so the registered
// value must be punycode too or no Origin would ever match it.
func ValidDomain(d string) bool {
	if len(d) == 0 || len(d) > 253 || !strings.Contains(d, ".") || net.ParseIP(d) != nil {
		return false
	}
	labels := strings.Split(d, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range []byte(l) {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	// an all-digit top label is never a registrable name and would let
	// four numeric labels pose as a domain
	return strings.Trim(labels[len(labels)-1], "0123456789") != ""
}

// localOrigin reports the loopback origins allowed for local testing:
// http://localhost[:port] and http://127.0.0.1[:port].
func localOrigin(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1"
}

// CheckOrigins validates the origins of a site with the given domain
// (contract 3.2): 1 to 20 unique bare origins, https on the domain or a
// subdomain, or a loopback origin over http. each accepted value also
// passes the gateway's own parser, so the registry never holds an origin
// the gateway cannot match.
func CheckOrigins(f *tenants.Fields, origins []string, domain string) {
	switch {
	case len(origins) == 0:
		f.Add("origins", httpx.FieldRequired)
		return
	case len(origins) > maxOrigins:
		f.Add("origins", httpx.FieldTooMany)
		return
	}
	seen := map[string]bool{}
	for i, o := range origins {
		p := "origins[" + strconv.Itoa(i) + "]"
		if code := originCode(o, domain); code != "" {
			f.Add(p, code)
			continue
		}
		if seen[o] {
			f.Add(p, httpx.FieldDuplicate)
		}
		seen[o] = true
	}
}

func originCode(o, domain string) string {
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" {
		return httpx.FieldNotBareOrigin
	}
	// bare and canonical: exactly scheme://host[:port], lowercase, and no
	// default port, which browsers never put into an Origin header
	if o != strings.ToLower(o) || u.Scheme+"://"+u.Host != o {
		return httpx.FieldNotBareOrigin
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") || (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		return httpx.FieldNotBareOrigin
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return httpx.FieldNotBareOrigin
		}
	}
	if gateway.ValidOriginPattern(o) != nil || strings.Contains(u.Host, "*") {
		return httpx.FieldNotBareOrigin
	}
	if localOrigin(u) {
		return ""
	}
	if u.Scheme != "https" {
		return httpx.FieldNotBareOrigin
	}
	h := u.Hostname()
	if domain == "" || (h != domain && !strings.HasSuffix(h, "."+domain)) {
		return httpx.FieldOriginOutsideDomain
	}
	return ""
}
