// Package domainfix runs what the domain check tests fetch from: a real dns
// server on loopback (udp and tcp) that answers A, AAAA and TXT queries for
// test names, and a real https file server with a certificate for a test
// domain. the code under test gets a *net.Resolver pointed at the dns
// server, so it resolves through the standard library exactly as in
// production. only *_test.go files import it.
package domainfix

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

// DNS is an authoritative test dns server for any name it is told about.
// unknown names answer nxdomain.
type DNS struct {
	mu      sync.Mutex
	names   map[string]*record
	queries map[string]int
	udp     net.PacketConn
	tcp     net.Listener
}

type record struct {
	// addrs is a sequence: the nth A query gets addrs[min(n, len-1)], so a
	// test can answer differently on a later lookup (dns rebinding)
	addrs [][]netip.Addr
	txt   [][]string
}

// StartDNS starts the server and stops it when the test ends.
func StartDNS(t testing.TB) *DNS {
	t.Helper()
	var lc net.ListenConfig
	udp, err := lc.ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		_ = udp.Close()
		t.Fatal(err)
	}
	d := &DNS{names: map[string]*record{}, queries: map[string]int{}, udp: udp, tcp: tcp}
	var wg sync.WaitGroup
	wg.Go(d.serveUDP)
	wg.Go(d.serveTCP)
	t.Cleanup(func() {
		_ = udp.Close()
		_ = tcp.Close()
		wg.Wait()
	})
	return d
}

func key(name string) string { return strings.TrimSuffix(strings.ToLower(name), ".") }

func (d *DNS) rec(name string) *record {
	r, ok := d.names[key(name)]
	if !ok {
		r = &record{}
		d.names[key(name)] = r
	}
	return r
}

// SetAddrs answers A and AAAA queries for name with addrs.
func (d *DNS) SetAddrs(name string, addrs ...netip.Addr) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rec(name).addrs = [][]netip.Addr{addrs}
}

// SetAddrSequence answers the nth A query for name with seq[n], and every
// later one with the last entry. AAAA queries follow the same sequence
// position as the A query count at the time.
func (d *DNS) SetAddrSequence(name string, seq ...[]netip.Addr) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rec(name).addrs = seq
}

// SetTXT answers TXT queries for name with one record per entry; each
// entry is the record's character strings.
func (d *DNS) SetTXT(name string, records ...[]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rec(name).txt = records
}

// Queries returns how many A queries name received.
func (d *DNS) Queries(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.queries[key(name)]
}

// Resolver returns a pure-go resolver that sends every query to this
// server, whatever /etc/resolv.conf names.
func (d *DNS) Resolver() *net.Resolver {
	udp, tcp := d.udp.LocalAddr().String(), d.tcp.Addr().String()
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var nd net.Dialer
			if strings.HasPrefix(network, "udp") {
				return nd.DialContext(ctx, "udp", udp)
			}
			return nd.DialContext(ctx, "tcp", tcp)
		},
	}
}

func (d *DNS) serveUDP() {
	buf := make([]byte, 4096)
	for {
		n, from, err := d.udp.ReadFrom(buf)
		if err != nil {
			return
		}
		out, err := d.answer(buf[:n])
		if err != nil {
			continue
		}
		_, _ = d.udp.WriteTo(out, from)
	}
}

func (d *DNS) serveTCP() {
	for {
		c, err := d.tcp.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = c.Close() }()
			for {
				var l [2]byte
				if _, err := io.ReadFull(c, l[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := io.ReadFull(c, msg); err != nil {
					return
				}
				out, err := d.answer(msg)
				if err != nil {
					return
				}
				binary.BigEndian.PutUint16(l[:], uint16(len(out))) //nolint:gosec // dns messages are below 64 KiB
				if _, err := c.Write(append(l[:], out...)); err != nil {
					return
				}
			}
		}()
	}
}

func (d *DNS) answer(in []byte) ([]byte, error) {
	var p dnsmessage.Parser
	h, err := p.Start(in)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	if q.Class != dnsmessage.ClassINET {
		return nil, errors.New("only class IN")
	}
	name := key(q.Name.String())

	d.mu.Lock()
	r, known := d.names[name]
	var addrs []netip.Addr
	var txt [][]string
	if known {
		n := d.queries[name]
		if q.Type == dnsmessage.TypeA {
			d.queries[name]++
		}
		if len(r.addrs) > 0 {
			addrs = r.addrs[min(n, len(r.addrs)-1)]
		}
		txt = r.txt
	}
	d.mu.Unlock()

	rh := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired}
	if !known {
		rh.RCode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, rh)
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	res := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 0}
	switch q.Type {
	case dnsmessage.TypeA:
		for _, a := range addrs {
			if a.Is4() {
				if err := b.AResource(res, dnsmessage.AResource{A: a.As4()}); err != nil {
					return nil, err
				}
			}
		}
	case dnsmessage.TypeAAAA:
		for _, a := range addrs {
			if !a.Is4() {
				if err := b.AAAAResource(res, dnsmessage.AAAAResource{AAAA: a.As16()}); err != nil {
					return nil, err
				}
			}
		}
	case dnsmessage.TypeTXT:
		for _, t := range txt {
			if err := b.TXTResource(res, dnsmessage.TXTResource{TXT: t}); err != nil {
				return nil, err
			}
		}
	}
	return b.Finish()
}
