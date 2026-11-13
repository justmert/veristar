// Command tlsaudit reads packet captures of the local converge's private
// network (tests/localconverge/tls-audit.sh) and reports every tcp flow and
// udp datagram that carries readable protocol data instead of tls (finding
// O-26 of the review notes).
//
//	tlsaudit -markers <file> <capture.pcap>...
//
// a tcp direction passes when its first payload bytes are a tls record, or
// postgresql's 8 byte SSLRequest followed by the server's one byte "S" and
// then tls. every other direction with payload is a finding, and so is any
// udp payload. independent of that, every payload is searched for each
// marker (one per line of -markers: the environment's passwords, the
// end-to-end run's page paths, protocol words such as "CONNECT {"), so a
// readable secret is reported even inside a flow that looked like tls.
// it prints a json report and exits 1 when anything is found.
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type key struct {
	src, dst         [4]byte
	srcPort, dstPort uint16
}

type direction struct {
	key      key
	payload  []byte // first bytes of the direction, capped
	packets  int
	bytes    int
	sawSYN   bool
	isServer bool
}

const keepBytes = 1 << 16

func (k key) String() string {
	return fmt.Sprintf("%d.%d.%d.%d:%d->%d.%d.%d.%d:%d", k.src[0], k.src[1], k.src[2], k.src[3], k.srcPort,
		k.dst[0], k.dst[1], k.dst[2], k.dst[3], k.dstPort)
}

func (k key) reverse() key {
	return key{src: k.dst, dst: k.src, srcPort: k.dstPort, dstPort: k.srcPort}
}

type finding struct {
	Flow   string `json:"flow"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type portStat struct {
	Port      uint16 `json:"server_port"`
	Flows     int    `json:"flows"`
	TLS       int    `json:"tls"`
	PGThenTLS int    `json:"postgres_sslrequest_then_tls"`
	MidStream int    `json:"mid_stream"`
	Bytes     int    `json:"bytes"`
}

type report struct {
	Files        []string   `json:"files"`
	Packets      int        `json:"packets"`
	TCPFlows     int        `json:"tcp_flows_with_payload"`
	UDPDatagrams int        `json:"udp_datagrams_with_payload"`
	Ports        []portStat `json:"by_server_port"`
	Findings     []finding  `json:"findings"`
	MarkersTried int        `json:"markers_tried"`
}

var sslRequest = []byte{0, 0, 0, 8, 0x04, 0xd2, 0x16, 0x2f}

// readable reports text: at least 64 bytes of which nine in ten are
// printable ascii or whitespace. encrypted bytes are uniformly distributed,
// so about 37 percent of them fall in that range.
func readable(b []byte) bool {
	n := min(len(b), 512)
	if n < 64 {
		return false
	}
	text := 0
	for _, c := range b[:n] {
		if (c >= 0x20 && c < 0x7f) || c == '\n' || c == '\r' || c == '\t' {
			text++
		}
	}
	return text*10 >= n*9
}

func isTLSRecord(b []byte) bool {
	// content type handshake, change cipher spec, alert or application
	// data, then major version 3
	return len(b) >= 3 && b[0] >= 0x14 && b[0] <= 0x17 && b[1] == 3
}

func main() {
	markersPath := flag.String("markers", "", "file with one marker per line")
	flag.Parse()
	var markers [][]byte
	if *markersPath != "" {
		f, err := os.Open(*markersPath)
		if err != nil {
			fail(err)
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			if m := strings.TrimRight(s.Text(), "\r"); len(m) >= 4 {
				markers = append(markers, []byte(m))
			}
		}
		_ = f.Close()
	}
	dirs := map[key]*direction{}
	var order []key
	r := report{Files: flag.Args(), MarkersTried: len(markers)}
	seenMarker := map[string]bool{}
	for _, path := range flag.Args() {
		err := readPcap(path, func(pkt []byte) {
			r.Packets++
			proto, k, flags, payload, ok := parse(pkt)
			if !ok {
				return
			}
			for _, m := range markers {
				if bytes.Contains(payload, m) && !seenMarker[k.String()+string(m)] {
					seenMarker[k.String()+string(m)] = true
					r.Findings = append(r.Findings, finding{Flow: k.String(), Kind: "marker", Detail: fmt.Sprintf("%.24q", m)})
				}
			}
			if proto == 17 {
				if len(payload) > 0 {
					r.UDPDatagrams++
					r.Findings = append(r.Findings, finding{Flow: k.String(), Kind: "udp payload", Detail: fmt.Sprintf("%d bytes %.16q", len(payload), payload)})
				}
				return
			}
			d := dirs[k]
			if d == nil {
				d = &direction{key: k}
				dirs[k] = d
				order = append(order, k)
			}
			d.packets++
			d.bytes += len(payload)
			const syn, ack = 0x02, 0x10
			if flags&syn != 0 {
				d.sawSYN = true
				d.isServer = flags&ack != 0
			}
			if len(d.payload) < keepBytes {
				d.payload = append(d.payload, payload[:min(len(payload), keepBytes-len(d.payload))]...)
			}
		})
		if err != nil {
			fail(fmt.Errorf("%s: %w", path, err))
		}
	}
	ports := map[uint16]*portStat{}
	done := map[key]bool{}
	for _, k := range order {
		if done[k] {
			continue
		}
		a, b := dirs[k], dirs[k.reverse()]
		done[k], done[k.reverse()] = true, true
		client, server := a, b
		// the server side is the one that answered the syn, or else the
		// lower port, which on this network is always the service's
		if (a.sawSYN && a.isServer) || (!a.sawSYN && (b == nil || !b.sawSYN) && a.key.srcPort < a.key.dstPort) {
			client, server = b, a
		}
		if client == nil {
			client, server = server, nil
		}
		cp := client.payload
		var sp []byte
		if server != nil {
			sp = server.payload
		}
		if len(cp) == 0 && len(sp) == 0 {
			continue
		}
		r.TCPFlows++
		port := client.key.dstPort
		ps := ports[port]
		if ps == nil {
			ps = &portStat{Port: port}
			ports[port] = ps
		}
		ps.Flows++
		ps.Bytes += len(cp) + len(sp)
		flow := client.key.String()
		// a connection opened before the capture started shows up mid
		// stream, where a segment can begin inside a tls record; there the
		// prefix rules do not apply, and the bytes are judged readable or
		// not instead (the marker search above covers them as well)
		if !client.sawSYN && (server == nil || !server.sawSYN) {
			ps.MidStream++
			for _, p := range [][]byte{cp, sp} {
				if readable(p) {
					r.Findings = append(r.Findings, finding{Flow: flow, Kind: "readable mid stream", Detail: fmt.Sprintf("%.32q", p)})
				}
			}
			continue
		}
		switch {
		case bytes.HasPrefix(cp, sslRequest):
			rest := cp[len(sslRequest):]
			okServer := len(sp) == 0 || (sp[0] == 'S' && (len(sp) == 1 || isTLSRecord(sp[1:])))
			if (len(rest) == 0 || isTLSRecord(rest)) && okServer {
				ps.PGThenTLS++
			} else {
				r.Findings = append(r.Findings, finding{Flow: flow, Kind: "postgres without tls", Detail: fmt.Sprintf("server %.16q", sp)})
			}
		case len(cp) > 0 && !isTLSRecord(cp):
			r.Findings = append(r.Findings, finding{Flow: flow, Kind: "client plain text", Detail: fmt.Sprintf("%.32q", cp)})
		case len(sp) > 0 && !isTLSRecord(sp):
			r.Findings = append(r.Findings, finding{Flow: flow, Kind: "server plain text", Detail: fmt.Sprintf("%.32q", sp)})
		default:
			ps.TLS++
		}
	}
	for _, p := range ports {
		r.Ports = append(r.Ports, *p)
	}
	sort.Slice(r.Ports, func(i, j int) bool { return r.Ports[i].Port < r.Ports[j].Port })
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
	if len(r.Findings) > 0 {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tlsaudit:", err)
	os.Exit(2)
}

// readPcap reads a classic pcap file with ethernet frames (tcpdump -w on a
// bridge interface).
func readPcap(path string, fn func([]byte)) error {
	f, err := os.Open(path) //nolint:gosec // the capture files the audit script names
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	hdr := make([]byte, 24)
	if _, err := io.ReadFull(br, hdr); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var bo binary.ByteOrder
	switch binary.LittleEndian.Uint32(hdr) {
	case 0xa1b2c3d4, 0xa1b23c4d:
		bo = binary.LittleEndian
	case 0xd4c3b2a1, 0x4d3cb2a1:
		bo = binary.BigEndian
	default:
		return errors.New("not a pcap file")
	}
	if lt := bo.Uint32(hdr[20:]); lt != 1 {
		return fmt.Errorf("link type %d, want ethernet (1)", lt)
	}
	rec := make([]byte, 16)
	for {
		if _, err := io.ReadFull(br, rec); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := bo.Uint32(rec[8:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil // a capture stopped mid-record
		}
		fn(buf)
	}
}

// parse returns the ip protocol, the flow key, the tcp flags and the
// payload of an ethernet ipv4 frame.
func parse(b []byte) (proto byte, k key, flags byte, payload []byte, ok bool) {
	if len(b) < 14 {
		return
	}
	et := binary.BigEndian.Uint16(b[12:])
	b = b[14:]
	if et == 0x8100 && len(b) >= 4 {
		et = binary.BigEndian.Uint16(b[2:])
		b = b[4:]
	}
	if et != 0x0800 || len(b) < 20 {
		return
	}
	ihl := int(b[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(b[2:]))
	if ihl < 20 || len(b) < ihl || total < ihl {
		return
	}
	if total < len(b) {
		b = b[:total]
	}
	proto = b[9]
	copy(k.src[:], b[12:16])
	copy(k.dst[:], b[16:20])
	l4 := b[ihl:]
	switch proto {
	case 6:
		if len(l4) < 20 {
			return
		}
		k.srcPort, k.dstPort = binary.BigEndian.Uint16(l4), binary.BigEndian.Uint16(l4[2:])
		off := int(l4[12]>>4) * 4
		if off < 20 || len(l4) < off {
			return
		}
		return proto, k, l4[13], l4[off:], true
	case 17:
		if len(l4) < 8 {
			return
		}
		k.srcPort, k.dstPort = binary.BigEndian.Uint16(l4), binary.BigEndian.Uint16(l4[2:])
		return proto, k, 0, l4[8:], true
	}
	return
}
