package gateway

import (
	"bytes"
	"log/slog"
	"math/big"
	"math/rand/v2"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
	"github.com/stretchr/testify/require"
)

// geoTestFile returns the db-ip file named by DBIP_MMDB. the file is
// downloaded by make geo-fetch, never committed, so without it the geo tests
// skip and say how to get it. a set variable naming a missing file fails.
func geoTestFile(t *testing.T) string {
	t.Helper()
	p := os.Getenv("DBIP_MMDB")
	if p == "" {
		t.Skip("DBIP_MMDB is not set: run make geo-fetch and set DBIP_MMDB=.scratch/scratch/geo/dbip-country-lite.mmdb")
	}
	if !filepath.IsAbs(p) {
		// go test runs in the package directory; the make target writes
		// relative to the repository root
		p = filepath.Join("..", "..", p)
	}
	_, err := os.Stat(p)
	require.NoError(t, err, "DBIP_MMDB names a file that does not exist")
	return p
}

type geoTestRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// geoRandomIn returns a random address inside p.
func geoRandomIn(rng *rand.Rand, p netip.Prefix) netip.Addr {
	var b []byte
	if p.Addr().Is4() {
		a := p.Addr().As4()
		b = a[:]
	} else {
		a := p.Addr().As16()
		b = a[:]
	}
	for bit := p.Bits(); bit < len(b)*8; bit++ {
		if rng.IntN(2) == 1 {
			b[bit/8] |= 1 << (7 - bit%8)
		}
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

func geoAddr(t *testing.T, a netip.Addr) ClientIP {
	t.Helper()
	ip, err := ParseClientIP(a.String())
	require.NoError(t, err)
	return ip
}

func TestGeoCountryFromDBIPRanges(t *testing.T) {
	path := geoTestFile(t)
	g, err := OpenGeo(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, g.Close()) })
	t.Logf("db-ip file built %s", g.BuildTime())

	raw, err := maxminddb.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })

	// ranges are taken from the file itself, so the test holds for every
	// monthly release: two countries in each family from networks at least
	// as wide as the truncation, where truncation cannot change the answer
	want := map[string]netip.Prefix{}
	var narrow4, narrow6 netip.Prefix
	var narrow4CC, narrow6CC string
	for res := range raw.Networks() {
		var r geoTestRecord
		require.NoError(t, res.Decode(&r))
		p, cc := res.Prefix(), r.Country.ISOCode
		is4 := p.Addr().Is4()
		fam := "6"
		limit := 48
		if is4 {
			fam, limit = "4", 24
		}
		if (cc == "DE" || cc == "JP") && p.Bits() <= limit {
			if _, ok := want[fam+cc]; !ok {
				want[fam+cc] = p
			}
		}
		if p.Bits() > limit && cc != "ZZ" {
			base, err := p.Addr().Prefix(limit)
			require.NoError(t, err)
			var br geoTestRecord
			require.NoError(t, raw.Lookup(base.Addr()).Decode(&br))
			if br.Country.ISOCode != cc && br.Country.ISOCode != "" && br.Country.ISOCode != "ZZ" {
				if is4 && !narrow4.IsValid() {
					narrow4, narrow4CC = p, br.Country.ISOCode
				}
				if !is4 && !narrow6.IsValid() {
					narrow6, narrow6CC = p, br.Country.ISOCode
				}
			}
		}
	}
	require.Len(t, want, 4, "the file has no DE or JP network in some family: %v", want)

	rng := rand.New(rand.NewPCG(7, 11))
	for k, p := range want {
		cc := k[1:]
		for range 20 {
			a := geoRandomIn(rng, p)
			got, err := g.Country(geoAddr(t, a))
			require.NoError(t, err)
			require.Equal(t, cc, got, "%s in %s", a, p)
		}
	}

	// a network narrower than the truncation answers with the country of
	// its /24 or /48, which shows the full address is never looked up
	require.True(t, narrow4.IsValid(), "no ipv4 network narrower than /24 with a differing country")
	got, err := g.Country(geoAddr(t, geoRandomIn(rng, narrow4)))
	require.NoError(t, err)
	require.Equal(t, narrow4CC, got, "ipv4 lookup was not truncated (%s)", narrow4)
	if narrow6.IsValid() {
		got, err = g.Country(geoAddr(t, geoRandomIn(rng, narrow6)))
		require.NoError(t, err)
		require.Equal(t, narrow6CC, got, "ipv6 lookup was not truncated (%s)", narrow6)
	} else {
		t.Log("this release has no ipv6 network narrower than /48 with a differing country")
	}

	// private, loopback, documentation and link-local ranges have no record
	for _, s := range []string{"10.1.2.3", "127.0.0.1", "192.168.0.10", "192.0.2.1", "::1", "fe80::1", "2001:db8::1"} {
		ip, err := ParseClientIP(s)
		require.NoError(t, err)
		got, err := g.Country(ip)
		require.NoError(t, err, s)
		require.Empty(t, got, s)
	}

	_, err = g.Country(ClientIP{})
	require.Error(t, err)
}

// TestGeoTruncationMismatch measures how often the truncated lookup differs
// from the full one. the numbers go into docs/reference/geo.md.
func TestGeoTruncationMismatch(t *testing.T) {
	path := geoTestFile(t)
	g, err := OpenGeo(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, g.Close()) })
	raw, err := maxminddb.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })

	full := func(a netip.Addr) string {
		var r geoTestRecord
		res := raw.Lookup(a)
		require.NoError(t, res.Err())
		require.NoError(t, res.Decode(&r))
		if r.Country.ISOCode == "ZZ" {
			return ""
		}
		return r.Country.ISOCode
	}

	type agg struct{ total, mismatch *big.Float }
	exact := map[bool]agg{true: {new(big.Float), new(big.Float)}, false: {new(big.Float), new(big.Float)}}
	var nets []netip.Prefix
	for res := range raw.Networks() {
		p := res.Prefix()
		nets = append(nets, p)
		is4 := p.Addr().Is4()
		host, limit := 128-p.Bits(), 48
		if is4 {
			host, limit = 32-p.Bits(), 24
		}
		size := new(big.Float).SetMantExp(big.NewFloat(1), host)
		a := exact[is4]
		a.total.Add(a.total, size)
		// only a network narrower than the truncation can answer
		// differently from its truncated address
		if p.Bits() > limit {
			got, err := g.Country(geoAddr(t, p.Addr()))
			require.NoError(t, err)
			if got != full(p.Addr()) {
				a.mismatch.Add(a.mismatch, size)
			}
		}
	}

	// a random sample: a network drawn uniformly, then an address inside it
	rng := rand.New(rand.NewPCG(1, 2))
	n := map[bool]int{}
	miss := map[bool]int{}
	const samples = 20000
	for range samples {
		a := geoRandomIn(rng, nets[rng.IntN(len(nets))])
		got, err := g.Country(geoAddr(t, a))
		require.NoError(t, err)
		n[a.Is4()]++
		if got != full(a) {
			miss[a.Is4()]++
		}
	}
	for _, is4 := range []bool{true, false} {
		fam := "ipv6 /48"
		if is4 {
			fam = "ipv4 /24"
		}
		r, _ := new(big.Float).Quo(exact[is4].mismatch, exact[is4].total).Float64()
		t.Logf("%s: per-network sample %d of %d differ (%.2f%%); address-weighted over the whole file %.6f%%",
			fam, miss[is4], n[is4], 100*float64(miss[is4])/float64(n[is4]), 100*r)
		// weighted by address space the loss must stay small, or the
		// truncation decision in docs/reference/geo.md has to be revisited
		require.Less(t, r, 0.005, fam)
	}
	require.GreaterOrEqual(t, n[true]+n[false], 10000)
}

func TestGeoReload(t *testing.T) {
	src := geoTestFile(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "dbip.mmdb")
	b, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))

	g, err := OpenGeo(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, g.Close()) })
	ip, err := ParseClientIP("1.1.1.1")
	require.NoError(t, err)
	before, err := g.Country(ip)
	require.NoError(t, err)
	require.NotEmpty(t, before)

	// the monthly update replaces the file by rename and sends SIGHUP
	tmp := filepath.Join(dir, "next.mmdb")
	require.NoError(t, os.WriteFile(tmp, b, 0o600))
	require.NoError(t, os.Rename(tmp, path))
	require.NoError(t, g.Reload())
	after, err := g.Country(ip)
	require.NoError(t, err)
	require.Equal(t, before, after)

	// a broken file is refused and the loaded one stays in use
	require.NoError(t, os.WriteFile(tmp, []byte("not a database"), 0o600))
	require.NoError(t, os.Rename(tmp, path))
	require.Error(t, g.Reload())
	still, err := g.Country(ip)
	require.NoError(t, err)
	require.Equal(t, before, still)

	_, err = OpenGeo(path)
	require.Error(t, err)

	// SIGHUP reloads: a good file is reported as reloaded, a broken one as
	// failed with the old file kept
	logs := &geoLogBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	g.ReloadOnSIGHUP(t.Context(), log)
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), "geo database reload failed") },
		5*time.Second, 20*time.Millisecond)
	require.NoError(t, os.WriteFile(tmp, b, 0o600))
	require.NoError(t, os.Rename(tmp, path))
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGHUP))
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), "geo database reloaded") },
		5*time.Second, 20*time.Millisecond)
	still, err = g.Country(ip)
	require.NoError(t, err)
	require.Equal(t, before, still)
}

type geoLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *geoLogBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *geoLogBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
