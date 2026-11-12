package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP is the client address of one request. it exists only in memory
// for the duration of the request. the type has no
// String method and its formatting, log and json forms are fixed markers, so
// passing it to fmt, slog or encoding/json never reveals the address. code in
// this package reads the address through the unexported field only.
type ClientIP struct{ addr netip.Addr }

const clientIPMarker = "[client-ip]"

// Format makes every fmt verb print the marker instead of the address.
func (ClientIP) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(clientIPMarker)) }

// LogValue makes slog print the marker even under a key the log guard allows.
func (ClientIP) LogValue() slog.Value { return slog.StringValue(clientIPMarker) }

// errClientIPMarshal is returned by the encoders. an address must never be
// serialised, so encoding one is a programming error that should surface.
var errClientIPMarshal = errors.New("gateway: a client address cannot be encoded")

func (ClientIP) MarshalJSON() ([]byte, error) { return nil, errClientIPMarshal }

func (ClientIP) MarshalText() ([]byte, error) { return nil, errClientIPMarshal }

// IsValid reports whether the value holds an address.
func (c ClientIP) IsValid() bool { return c.addr.IsValid() }

// ParseClientIP parses one textual address. the control plane's erasure
// request carries the address this way. ipv4-mapped ipv6 addresses become
// plain ipv4, so a visitor gets the same token whichever form arrives.
func ParseClientIP(s string) (ClientIP, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		// the parse error quotes the input, which is an address
		return ClientIP{}, errors.New("gateway: client address is not a valid ip address")
	}
	if a.Zone() != "" {
		return ClientIP{}, errors.New("gateway: client address carries a zone")
	}
	return ClientIP{addr: a.Unmap()}, nil
}

// canonical is the text that goes into the visitor token. it is the shortest
// form netip produces, so the gateway and an erasure request agree on it.
func (c ClientIP) canonical() string { return c.addr.String() }

// truncated returns the /24 (ipv4) or /48 (ipv6) network of the address. the
// country lookup and the ip-cluster key use only this, never the full address.
func (c ClientIP) truncated() netip.Prefix {
	bits := 48
	if c.addr.Is4() {
		bits = 24
	}
	p, err := c.addr.Prefix(bits)
	if err != nil {
		// Prefix fails only for an invalid address, which callers rule out
		return netip.Prefix{}
	}
	return p
}

// ClientIPReader reads the client address from the one header that the local
// caddy sets. caddy overwrites that header on every request (header_up with
// set, not add), so a value a client sends itself never arrives here. the
// gateway listens on the private side only, so caddy is the single hop.
type ClientIPReader struct{ header string }

// NewClientIPReader returns a reader for the named header. the common proxy
// headers are refused because clients set them and proxies append to them:
// the trusted header must be one that only caddy writes.
func NewClientIPReader(header string) (*ClientIPReader, error) {
	h := http.CanonicalHeaderKey(strings.TrimSpace(header))
	if h == "" {
		return nil, errors.New("gateway: client address header name is empty")
	}
	switch h {
	case "X-Forwarded-For", "X-Real-Ip", "Forwarded", "True-Client-Ip", "Cf-Connecting-Ip":
		return nil, fmt.Errorf("gateway: %s can be set by clients and is not accepted as the client address header", h)
	}
	return &ClientIPReader{header: h}, nil
}

// Header is the canonical header name the reader trusts.
func (r *ClientIPReader) Header() string { return r.header }

// FromRequest returns the client address. the header must be present exactly
// once and hold exactly one address without a port. anything else means
// caddy is misconfigured or bypassed, and the request is refused rather than
// guessed at.
func (r *ClientIPReader) FromRequest(req *http.Request) (ClientIP, error) {
	vals := req.Header.Values(r.header)
	switch len(vals) {
	case 0:
		return ClientIP{}, fmt.Errorf("gateway: %s header is missing", r.header)
	case 1:
	default:
		return ClientIP{}, fmt.Errorf("gateway: %s header appears %d times", r.header, len(vals))
	}
	if strings.ContainsAny(vals[0], ", ") {
		return ClientIP{}, fmt.Errorf("gateway: %s header holds more than one value", r.header)
	}
	return ParseClientIP(vals[0])
}
