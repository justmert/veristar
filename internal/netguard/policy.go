// Package netguard fetches files from tenant-supplied domains without
// letting a tenant point the fetch at the platform's own network
// (docs/security/threat-model.md section 3.10). the host is resolved
// first, every address outside the public internet is refused, the
// connection goes to the checked address only (and is checked again right
// before connect(2)), and size, time and redirects are capped.
package netguard

import (
	"net/netip"
	"strings"
)

// refusedPrefixes are the iana special-purpose ranges that the netip
// helpers in Refused do not cover.
var refusedPrefixes = func() []netip.Prefix {
	ps := []string{
		"0.0.0.0/8",          // this network
		"100.64.0.0/10",      // shared address space (cgnat, tailscale)
		"192.0.0.0/24",       // ietf protocol assignments
		"192.0.2.0/24",       // documentation, also the replayer's fake client address
		"198.51.100.0/24",    // documentation
		"203.0.113.0/24",     // documentation
		"192.88.99.0/24",     // deprecated 6to4 relay anycast
		"198.18.0.0/15",      // benchmarking
		"240.0.0.0/4",        // reserved
		"255.255.255.255/32", // broadcast
		"64:ff9b::/96",       // nat64 embeds an ipv4 address the check would not see
		"64:ff9b:1::/48",     // local-use nat64
		"100::/64",           // discard-only
		"2001::/23",          // ietf protocol assignments, includes teredo
		"2001:db8::/32",      // documentation
		"3fff::/20",          // documentation
		"2002::/16",          // 6to4 embeds ipv4
		"5f00::/16",          // srv6 sids
	}
	out := make([]netip.Prefix, len(ps))
	for i, p := range ps {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}()

// Refused reports whether a fetch must never connect to addr: loopback,
// private (rfc 1918, ula), link-local (which holds the cloud metadata
// endpoints), multicast, unspecified, and the special ranges above. an
// ipv4-mapped ipv6 address is unmapped first, so ::ffff:127.0.0.1 is
// loopback too.
func Refused(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	a := addr.Unmap()
	if a.Zone() != "" {
		// a zoned address is link scoped by definition
		return true
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range refusedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// localSuffixes name hosts that only exist inside some private network.
// tenant domains are public by contract, so these are refused before any
// lookup.
var localSuffixes = []string{".localhost", ".internal", ".local", ".home.arpa"}

// refusedName reports a host name that is never fetched.
func refusedName(host string) bool {
	if host == "localhost" {
		return true
	}
	for _, s := range localSuffixes {
		if strings.HasSuffix(host, s) || host == s[1:] {
			return true
		}
	}
	return false
}
