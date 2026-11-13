package netguard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/idna"
)

// caps of the reference docs 3.3
const (
	RequestTimeout        = 10 * time.Second
	ConnectTimeout        = 5 * time.Second
	TLSHandshakeTimeout   = 5 * time.Second
	ResponseHeaderTimeout = 5 * time.Second
	MaxHeaderBytes        = 16 << 10
	MaxRedirects          = 3
)

// httpsPort is the only port a fetch connects to. urls are built by this
// package from a domain, never taken from a tenant.
const httpsPort = "443"

// error codes, shared with the verification and stellar.toml codes of
// control-plane-api 5.8 and 5.12
const (
	CodeRefusedAddress  = "refused_address"
	CodeRedirectRefused = "redirect_refused"
	CodeTLSError        = "tls_error"
	CodeTimeout         = "timeout"
	CodeFetchError      = "fetch_error"
)

// Error is a refusal by the guard. Detail is for logs and audit rows; it
// names a host or address, never a token.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return "netguard: " + e.Code + ": " + e.Detail }

func refused(format string, args ...any) error {
	return &Error{Code: CodeRefusedAddress, Detail: fmt.Sprintf(format, args...)}
}

// Code maps an error of Get to one of the codes above.
func Code(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeTimeout
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return CodeTimeout
	}
	var (
		cve *tls.CertificateVerificationError
		he  x509.HostnameError
		ua  x509.UnknownAuthorityError
		ci  x509.CertificateInvalidError
		rh  tls.RecordHeaderError
		ae  tls.AlertError
	)
	if errors.As(err, &cve) || errors.As(err, &he) || errors.As(err, &ua) || errors.As(err, &ci) ||
		errors.As(err, &rh) || errors.As(err, &ae) {
		return CodeTLSError
	}
	return CodeFetchError
}

// Guard fetches https files from public hosts only.
type Guard struct {
	resolver *net.Resolver
	client   *http.Client
	// allow and port exist only for tests (NewForTest): test servers listen
	// on loopback and on an unprivileged port
	allow []netip.Prefix
	port  string
}

// New returns a guard that resolves through resolver (net.DefaultResolver
// in production; the resolver is a parameter so tests can point it at
// their own dns server). there is deliberately no option that permits a
// refused address.
func New(resolver *net.Resolver) (*Guard, error) {
	return build(resolver, nil, nil, httpsPort)
}

// TestOptions loosen the guard for tests that serve files from loopback.
type TestOptions struct {
	// Allow are prefixes treated as public, for example 127.0.0.1/32.
	Allow []netip.Prefix
	// RootCAs trusts the test server's certificate authority.
	RootCAs *x509.CertPool
	// Port is dialed instead of 443.
	Port string
}

// NewForTest returns a guard with TestOptions. it fails outside a test
// binary, so no configuration of a running service can reach it.
func NewForTest(resolver *net.Resolver, o TestOptions) (*Guard, error) {
	if !testing.Testing() {
		return nil, errors.New("netguard: NewForTest is only available in tests")
	}
	if o.Port == "" {
		o.Port = httpsPort
	}
	return build(resolver, o.Allow, o.RootCAs, o.Port)
}

func build(resolver *net.Resolver, allow []netip.Prefix, roots *x509.CertPool, port string) (*Guard, error) {
	if resolver == nil {
		return nil, errors.New("netguard: a resolver is required")
	}
	g := &Guard{resolver: resolver, allow: allow, port: port}
	tr := &http.Transport{
		// nil, never ProxyFromEnvironment: a proxy would resolve the name
		// itself and skip the address check
		Proxy:                  nil,
		DialContext:            g.dial,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout:    TLSHandshakeTimeout,
		ResponseHeaderTimeout:  ResponseHeaderTimeout,
		MaxResponseHeaderBytes: MaxHeaderBytes,
		// a gzip bomb is never inflated
		DisableCompression: true,
		// every fetch resolves and checks afresh
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
	}
	g.client = &http.Client{Transport: tr, CheckRedirect: checkRedirect}
	return g, nil
}

func (g *Guard) permitted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range g.allow {
		if p.Contains(a) {
			return true
		}
	}
	return !Refused(a)
}

// dial resolves the host once, drops refused addresses and connects to a
// checked address literally, so no second lookup can swap in another one
// (dns rebinding).
func (g *Guard) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, refused("unparsable dial address %q", addr)
	}
	if port != httpsPort {
		return nil, refused("port %s is not 443", port)
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, refused("address literal %s", host)
	}
	ips, err := g.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	var ok []netip.Addr
	for _, ip := range ips {
		if g.permitted(ip) {
			ok = append(ok, ip.Unmap())
		}
	}
	if len(ok) == 0 {
		return nil, refused("%s resolves only to refused addresses %v", host, ips)
	}
	d := net.Dialer{Timeout: ConnectTimeout, ControlContext: g.control}
	var errs []error
	for _, ip := range ok {
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), g.port))
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// control runs after the socket exists and right before connect(2), with
// the address that is actually dialed. it repeats the check so that the
// guard holds even if a later change dials a name instead of an address.
func (g *Guard) control(_ context.Context, _, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return refused("unparsable connect address %q", address)
	}
	if !g.permitted(ap.Addr()) {
		return refused("connect to %s", ap.Addr())
	}
	return nil
}

// checkRedirect allows at most MaxRedirects hops, each to https on the
// original host and port 443. another host, even www. of the same domain,
// is refused (the reference docs 3.4).
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return &Error{Code: CodeRedirectRefused, Detail: fmt.Sprintf("more than %d redirects", MaxRedirects)}
	}
	orig := via[0].URL.Hostname()
	if req.URL.Scheme != "https" {
		return &Error{Code: CodeRedirectRefused, Detail: "redirect to scheme " + req.URL.Scheme}
	}
	if p := req.URL.Port(); p != "" && p != httpsPort {
		return &Error{Code: CodeRedirectRefused, Detail: "redirect to port " + p}
	}
	h, err := normalizeHost(req.URL.Hostname())
	if err != nil || h != orig {
		return &Error{Code: CodeRedirectRefused, Detail: "redirect to host " + req.URL.Hostname()}
	}
	return nil
}

// normalizeHost returns the lowercase idna a-label form of a host name.
func normalizeHost(host string) (string, error) {
	a, err := idna.Lookup.ToASCII(strings.ToLower(host))
	if err != nil {
		return "", err
	}
	return a, nil
}

// Response is the outcome of one fetch.
type Response struct {
	Status int
	// Body is set for status 200 within the size cap.
	Body []byte
	// TooLarge is true when a 200 body is longer than the cap; Body is
	// then empty.
	TooLarge bool
	// Bytes is the length of Body.
	Bytes int
}

// Get fetches https://<host><path> and reads at most maxBody bytes of a 200
// response. host must be a domain name (lowercase a-label); names of local
// networks and address literals are refused before any lookup.
func (g *Guard) Get(ctx context.Context, host, path string, maxBody int64) (Response, error) {
	h, err := normalizeHost(host)
	if err != nil || h != host || host == "" {
		return Response{}, &Error{Code: CodeFetchError, Detail: fmt.Sprintf("host %q is not a lowercase a-label name", host)}
	}
	if _, err := netip.ParseAddr(host); err == nil || refusedName(host) {
		return Response{}, refused("host %s is not a public name", host)
	}
	if !strings.HasPrefix(path, "/") {
		return Response{}, &Error{Code: CodeFetchError, Detail: "path must start with /"}
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	u := url.URL{Scheme: "https", Host: host, Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("User-Agent", "veristar-domain-check")
	resp, err := g.client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	out := Response{Status: resp.StatusCode}
	if resp.StatusCode != http.StatusOK {
		return out, nil
	}
	if resp.ContentLength > maxBody {
		out.TooLarge = true
		return out, nil
	}
	// one byte over the cap is enough to know the file is too large
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Response{}, err
	}
	if int64(len(b)) > maxBody {
		out.TooLarge = true
		return out, nil
	}
	out.Body, out.Bytes = b, len(b)
	return out, nil
}
