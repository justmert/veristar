//go:build webpath

package webpath

import (
	"crypto/rand"
	"math/big"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/oschwald/maxminddb-golang/v2"
)

// countrySubnets picks, for each country, a random /24 inside a network the
// db-ip file baked into the gateway image assigns to that country. the suite
// gives a docker network that subnet, so caddy's tcp peer is an address the
// gateway resolves to the country. picking at random among all candidates
// keeps the borrowed /24 (unreachable from this host while the test runs)
// away from any one fixed range.
func countrySubnets(t testing.TB, ccs ...string) map[string][]netip.Prefix {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dbip.mmdb")
	id := mustDocker(t, "create", gatewayImage)
	defer func() { _, _ = docker("rm", id) }()
	mustDocker(t, "cp", id+":/geo/dbip-country-lite.mmdb", path)
	r, err := maxminddb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := map[string]bool{}
	for _, cc := range ccs {
		want[cc] = true
	}
	cands := map[string][]netip.Prefix{}
	for res := range r.Networks() {
		var rec struct {
			Country struct {
				ISOCode string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
		}
		if err := res.Decode(&rec); err != nil {
			t.Fatal(err)
		}
		p := res.Prefix()
		// a /24 or wider ipv4 network, outside the ranges docker or the lan use
		if !p.Addr().Is4() || p.Bits() > 24 || p.Bits() < 12 || !want[rec.Country.ISOCode] || p.Addr().IsPrivate() {
			continue
		}
		cands[rec.Country.ISOCode] = append(cands[rec.Country.ISOCode], p)
	}
	out := map[string][]netip.Prefix{}
	for _, cc := range ccs {
		if len(cands[cc]) == 0 {
			t.Fatalf("db-ip file has no /24 or wider ipv4 network for %s", cc)
		}
		// several choices, so a subnet another docker network holds can be skipped
		for range 5 {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(cands[cc]))))
			p := cands[cc][n.Int64()]
			blocks := int64(1) << (24 - p.Bits())
			k, _ := rand.Int(rand.Reader, big.NewInt(blocks))
			a := p.Addr().As4()
			v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8
			v += uint32(k.Int64()) << 8
			sub := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), 0}), 24)
			// confirm the chosen client address itself resolves to the country
			var rec struct {
				Country struct {
					ISOCode string `maxminddb:"iso_code"`
				} `maxminddb:"country"`
			}
			client := netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), 10})
			if err := r.Lookup(client).Decode(&rec); err != nil || rec.Country.ISOCode != cc {
				continue
			}
			out[cc] = append(out[cc], sub)
		}
		if len(out[cc]) == 0 {
			t.Fatalf("no usable /24 for %s", cc)
		}
	}
	return out
}
