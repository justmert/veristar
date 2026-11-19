package netguard_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/netguard"
	"github.com/justmert/veristar/tests/fixtures/domainfix"
)

func TestRefused(t *testing.T) {
	t.Parallel()
	refused := []string{
		"127.0.0.1", "127.255.0.9", "::1", "::ffff:127.0.0.1", // loopback, also mapped
		"10.1.2.3", "172.16.0.1", "192.168.0.10", "fd12::1", "::ffff:10.0.0.1", // private
		"100.64.0.1", "100.127.255.254", // cgnat and tailscale
		"169.254.169.254", "fd00:ec2::254", "fe80::1", // metadata endpoints, link-local
		"0.0.0.0", "0.1.2.3", "::", "224.0.0.1", "ff02::1", "255.255.255.255", "240.0.0.1",
		"192.0.0.8", "192.0.2.1", "198.51.100.7", "203.0.113.9", "192.88.99.1", "198.18.0.1",
		"64:ff9b::7f00:1", "64:ff9b:1::1", "100::1", "2001::1", "2001:db8::1", "3fff::1",
		"2002:7f00:1::", "5f00::1",
	}
	for _, s := range refused {
		require.True(t, netguard.Refused(netip.MustParseAddr(s)), s)
	}
	require.True(t, netguard.Refused(netip.Addr{}), "zero address")
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "100.128.0.1", "2606:4700:4700::1111", "::ffff:1.1.1.1"} {
		require.False(t, netguard.Refused(netip.MustParseAddr(s)), s)
	}
}

const host = "files.stellar-verify.test"

var loopback = netip.MustParseAddr("127.0.0.1")

type harness struct {
	dns *domainfix.DNS
	srv *domainfix.HTTPS
}

func start(t *testing.T, h http.Handler) *harness {
	t.Helper()
	d := domainfix.StartDNS(t)
	d.SetAddrs(host, loopback)
	return &harness{dns: d, srv: domainfix.StartHTTPS(t, h, host)}
}

// guard treats loopback as public only when allowLoopback is set, so the
// same server tests both acceptance and refusal.
func (h *harness) guard(t *testing.T, allowLoopback bool) *netguard.Guard {
	t.Helper()
	o := netguard.TestOptions{RootCAs: h.srv.Roots, Port: h.srv.Port}
	if allowLoopback {
		o.Allow = []netip.Prefix{netip.PrefixFrom(loopback, 32)}
	}
	g, err := netguard.NewForTest(h.dns.Resolver(), o)
	require.NoError(t, err)
	return g
}

func files() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/ok.txt", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) })
	m.HandleFunc("/missing", http.NotFound)
	return m
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, code, netguard.Code(err), err.Error())
}

func TestGetPublicHost(t *testing.T) {
	t.Parallel()
	h := start(t, files())
	g := h.guard(t, true)
	r, err := g.Get(t.Context(), host, "/ok.txt", 1024)
	require.NoError(t, err)
	require.Equal(t, 200, r.Status)
	require.Equal(t, "hello", string(r.Body))
	r, err = g.Get(t.Context(), host, "/missing", 1024)
	require.NoError(t, err)
	require.Equal(t, 404, r.Status)
	require.Nil(t, r.Body)
}

// TestRefusedAddresses points the test domain at every kind of internal
// address; nothing is dialed, the fetch fails with refused_address.
func TestRefusedAddresses(t *testing.T) {
	t.Parallel()
	h := start(t, files())
	g := h.guard(t, false)
	cases := map[string][]netip.Addr{
		"loopback":         {loopback},
		"loopback-mapped":  {netip.MustParseAddr("::ffff:127.0.0.1")},
		"private":          {netip.MustParseAddr("10.0.0.5")},
		"private-192":      {netip.MustParseAddr("192.168.0.1")},
		"cgnat":            {netip.MustParseAddr("100.64.1.1")},
		"metadata":         {netip.MustParseAddr("169.254.169.254")},
		"metadata-v6":      {netip.MustParseAddr("fd00:ec2::254")},
		"nat64":            {netip.MustParseAddr("64:ff9b::a9fe:a9fe")},
		"all-bad-together": {netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("::1")},
	}
	for name, addrs := range cases {
		d := name + ".stellar-verify.test"
		h.dns.SetAddrs(d, addrs...)
		_, err := g.Get(t.Context(), d, "/ok.txt", 1024)
		requireCode(t, err, netguard.CodeRefusedAddress)
	}
}

// TestRefusedNames never even looks local names up.
func TestRefusedNames(t *testing.T) {
	t.Parallel()
	h := start(t, files())
	g := h.guard(t, true)
	for _, n := range []string{"localhost", "app.localhost", "metadata.google.internal", "printer.local", "nas.home.arpa"} {
		h.dns.SetAddrs(n, loopback)
		_, err := g.Get(t.Context(), n, "/ok.txt", 1024)
		requireCode(t, err, netguard.CodeRefusedAddress)
		require.Zero(t, h.dns.Queries(n), n)
	}
	for _, n := range []string{"127.0.0.1", "[::1]", "Files.Stellar-Verify.Test", ""} {
		_, err := g.Get(t.Context(), n, "/ok.txt", 1024)
		require.Error(t, err, n)
	}
}

func TestOversize(t *testing.T) {
	t.Parallel()
	m := http.NewServeMux()
	m.HandleFunc("/exact", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 1024)) })
	m.HandleFunc("/declared", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 1025)) })
	m.HandleFunc("/streamed", func(w http.ResponseWriter, _ *http.Request) {
		// no content-length: chunks until the client stops reading
		for range 1000 {
			if _, err := w.Write(make([]byte, 1024)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	h := start(t, m)
	g := h.guard(t, true)
	r, err := g.Get(t.Context(), host, "/exact", 1024)
	require.NoError(t, err)
	require.False(t, r.TooLarge)
	require.Equal(t, 1024, r.Bytes)
	for _, p := range []string{"/declared", "/streamed"} {
		r, err = g.Get(t.Context(), host, p, 1024)
		require.NoError(t, err)
		require.True(t, r.TooLarge, p)
		require.Nil(t, r.Body)
	}
}

// TestSlow runs against the real caps: headers later than 5 s, and a body
// that trickles past the 10 s request deadline.
func TestSlow(t *testing.T) {
	t.Parallel()
	m := http.NewServeMux()
	m.HandleFunc("/slow-headers", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(netguard.ResponseHeaderTimeout + 3*time.Second):
		}
	})
	m.HandleFunc("/trickle", func(w http.ResponseWriter, r *http.Request) {
		for range 60 {
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	})
	h := start(t, m)
	g := h.guard(t, true)
	for p, within := range map[string]time.Duration{
		"/slow-headers": netguard.ResponseHeaderTimeout + 2*time.Second,
		"/trickle":      netguard.RequestTimeout + 2*time.Second,
	} {
		began := time.Now()
		r, err := g.Get(t.Context(), host, p, 1024)
		require.Error(t, err, "%s answered status %d with %d bytes after %s", p, r.Status, r.Bytes, time.Since(began))
		requireCode(t, err, netguard.CodeTimeout)
		require.Less(t, time.Since(began), within, p)
	}
}

func TestRedirects(t *testing.T) {
	t.Parallel()
	m := files()
	redirect := func(path, to string) {
		m.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, to, http.StatusFound) })
	}
	redirect("/same", "/ok.txt")
	redirect("/other-host", "https://other.stellar-verify.test/ok.txt")
	redirect("/www", "https://www."+host+"/ok.txt")
	redirect("/plain-http", "http://"+host+"/ok.txt")
	redirect("/port", "https://"+host+":8443/ok.txt")
	for i := range 5 {
		redirect(fmt.Sprintf("/loop%d", i), fmt.Sprintf("/loop%d", i+1))
	}
	h := start(t, m)
	h.dns.SetAddrs("other.stellar-verify.test", loopback)
	h.dns.SetAddrs("www."+host, loopback)
	g := h.guard(t, true)

	r, err := g.Get(t.Context(), host, "/same", 1024)
	require.NoError(t, err)
	require.Equal(t, "hello", string(r.Body))
	for _, p := range []string{"/other-host", "/www", "/plain-http", "/port", "/loop0"} {
		_, err := g.Get(t.Context(), host, p, 1024)
		requireCode(t, err, netguard.CodeRedirectRefused)
	}
	require.Zero(t, h.dns.Queries("other.stellar-verify.test"), "a refused redirect is never resolved")
}

// TestDNSRebinding answers the first lookup with an allowed address and the
// second with a private one. the redirect hop resolves again and is
// refused: the address checked is always the address dialed.
func TestDNSRebinding(t *testing.T) {
	t.Parallel()
	m := files()
	m.HandleFunc("/hop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok.txt", http.StatusFound) })
	h := start(t, m)
	h.dns.SetAddrSequence(host, []netip.Addr{loopback}, []netip.Addr{netip.MustParseAddr("10.0.0.7")})
	g := h.guard(t, true)
	_, err := g.Get(t.Context(), host, "/hop", 1024)
	requireCode(t, err, netguard.CodeRefusedAddress)
	require.Equal(t, 2, h.dns.Queries(host))
	require.Contains(t, err.Error(), "10.0.0.7")
}

// TestIgnoresProxyEnvironment: a proxy would resolve the name itself, so
// the guard never uses one.
func TestIgnoresProxyEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")
	h := start(t, files())
	r, err := h.guard(t, true).Get(t.Context(), host, "/ok.txt", 1024)
	require.NoError(t, err)
	require.Equal(t, "hello", string(r.Body))
}

func TestTLSFailure(t *testing.T) {
	t.Parallel()
	h := start(t, files())
	// a guard that does not trust the test authority
	g, err := netguard.NewForTest(h.dns.Resolver(), netguard.TestOptions{
		Allow: []netip.Prefix{netip.PrefixFrom(loopback, 32)}, Port: h.srv.Port,
	})
	require.NoError(t, err)
	_, err = g.Get(t.Context(), host, "/ok.txt", 1024)
	requireCode(t, err, netguard.CodeTLSError)
	// a name the certificate does not cover
	h.dns.SetAddrs("wrong.stellar-verify.test", loopback)
	_, err = h.guard(t, true).Get(t.Context(), "wrong.stellar-verify.test", "/ok.txt", 1024)
	requireCode(t, err, netguard.CodeTLSError)
	require.True(t, strings.Contains(err.Error(), "certificate"), err.Error())
}

func TestUnknownHost(t *testing.T) {
	t.Parallel()
	h := start(t, files())
	_, err := h.guard(t, true).Get(t.Context(), "nowhere.stellar-verify.test", "/ok.txt", 1024)
	requireCode(t, err, netguard.CodeFetchError)
}
