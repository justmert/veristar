package domaincheck_test

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane/domaincheck"
	"github.com/justmert/stellar-analytics/internal/netguard"
	"github.com/justmert/stellar-analytics/tests/fixtures/domainfix"
)

const zone = "stellar-verify.test"

var loopback = netip.MustParseAddr("127.0.0.1")

// testNet is a real dns server and a real https server for names under
// stellar-verify.test, all resolving to the loopback https server.
type testNet struct {
	dns *domainfix.DNS
	srv *domainfix.HTTPS
}

func newNet(t *testing.T, h http.Handler, labels ...string) *testNet {
	t.Helper()
	d := domainfix.StartDNS(t)
	for _, l := range labels {
		d.SetAddrs(l+"."+zone, loopback)
	}
	return &testNet{dns: d, srv: domainfix.StartHTTPS(t, h, zone, "*."+zone)}
}

// guard treats loopback as public only with allowLoopback, the one thing
// a test needs that production never allows.
func (n *testNet) guard(t *testing.T, allowLoopback bool) *netguard.Guard {
	t.Helper()
	o := netguard.TestOptions{RootCAs: n.srv.Roots, Port: n.srv.Port}
	if allowLoopback {
		o.Allow = []netip.Prefix{netip.PrefixFrom(loopback, 32)}
	}
	g, err := netguard.NewForTest(n.dns.Resolver(), o)
	require.NoError(t, err)
	return g
}

func (n *testNet) checker(t *testing.T) *domaincheck.Checker {
	t.Helper()
	return &domaincheck.Checker{Guard: n.guard(t, true), Resolver: n.dns.Resolver()}
}

// tokenFiles serves files per host and path from a map the test changes;
// anything else answers 404.
type tokenFiles struct {
	mu    sync.Mutex
	files map[string]string
}

func (f *tokenFiles) set(host, body string) { f.setPath(host, domaincheck.FilePath, body) }

func (f *tokenFiles) setPath(host, path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[host+path] = body
}

func (f *tokenFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	f.mu.Lock()
	b, ok := f.files[host+r.URL.Path]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte(b))
}
