package prober

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blackholeProxy forwards tcp to a backend until silence is called. after
// that every connection open at the time swallows bytes in both directions
// without closing, the way a peer that rebooted looks from the client side.
// connections accepted later forward normally again.
type blackholeProxy struct {
	ln      net.Listener
	backend string
	mu      sync.Mutex
	conns   []*atomic.Bool
}

func newBlackholeProxy(t *testing.T, backend string) *blackholeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &blackholeProxy{ln: ln, backend: backend}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

func (p *blackholeProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		b, err := net.Dial("tcp", p.backend)
		if err != nil {
			_ = c.Close()
			continue
		}
		dead := new(atomic.Bool)
		p.mu.Lock()
		p.conns = append(p.conns, dead)
		p.mu.Unlock()
		go pipe(c, b, dead)
		go pipe(b, c, dead)
	}
}

func pipe(dst, src net.Conn, dead *atomic.Bool) {
	// closing both ends when either side goes lets the backend see the
	// client drop the connection, so srv.Close does not wait on it.
	defer dst.Close()
	defer src.Close()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			return
		}
		if dead.Load() {
			continue
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return
		}
	}
}

func (p *blackholeProxy) silence() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.conns {
		d.Store(true)
	}
}

// get runs one request and reports whether it reused a pooled connection.
func get(c *http.Client, url string) (reused bool, err error) {
	trace := &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) { reused = i.Reused }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return reused, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.ProtoMajor != 2 {
		return reused, &protoError{resp.Proto}
	}
	return reused, nil
}

type protoError struct{ proto string }

func (e *protoError) Error() string { return "negotiated " + e.proto + ", want HTTP/2" }

func TestTransportDropsSilentHTTP2Connection(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())

	const timeout = 2 * time.Second
	run := func(t *testing.T, sendPing time.Duration) error {
		proxy := newBlackholeProxy(t, srv.Listener.Addr().String())
		url := "https://" + proxy.ln.Addr().String() + "/"
		tr, err := newTransport(roots, timeout, sendPing, 300*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(tr.CloseIdleConnections)
		c := &http.Client{Transport: tr, Timeout: timeout}
		if _, err := get(c, url); err != nil {
			t.Fatalf("first request: %v", err)
		}
		proxy.silence()
		// long enough for one idle ping and its timeout to run out
		time.Sleep(time.Second)
		reused, err := get(c, url)
		if err == nil && reused {
			t.Fatal("request succeeded on the silenced connection")
		}
		return err
	}

	t.Run("health check dials a fresh connection", func(t *testing.T) {
		if err := run(t, 200*time.Millisecond); err != nil {
			t.Fatalf("request after the peer went silent: %v", err)
		}
	})
	// the control shows the setup reproduces the hang: without pings the
	// pool hands out the dead connection and the request runs into the
	// client timeout.
	t.Run("without health check the request hangs", func(t *testing.T) {
		if err := run(t, 0); err == nil {
			t.Fatal("request succeeded without a health check, the proxy does not reproduce a dead peer")
		}
	})
}

func TestNewTransportBoundsEveryPhase(t *testing.T) {
	tr, err := newTransport(nil, 7*time.Second, h2SendPingTimeout, h2PingTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if tr.TLSHandshakeTimeout != 7*time.Second || tr.ResponseHeaderTimeout != 7*time.Second {
		t.Fatalf("handshake %s, response header %s", tr.TLSHandshakeTimeout, tr.ResponseHeaderTimeout)
	}
	if tr.DialContext == nil || !tr.ForceAttemptHTTP2 {
		t.Fatal("dialer missing or http/2 not attempted")
	}
	if tr.HTTP2 == nil || tr.HTTP2.SendPingTimeout != h2SendPingTimeout || tr.HTTP2.PingTimeout != h2PingTimeout {
		t.Fatalf("http/2 health check %+v", tr.HTTP2)
	}
}
