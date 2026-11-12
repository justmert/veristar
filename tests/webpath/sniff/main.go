//go:build linux

// Command sniff is a test-only packet logger for the this stage egress check
// (tests/webpath). it runs as a sidecar in the umami container's network
// namespace (docker run --network container:<umami>) with CAP_NET_RAW and
// reads every packet on every interface of that namespace through an
// AF_PACKET socket, so it needs no libpcap and runs on the pinned distroless
// image. it prints one json line per new outgoing connection attempt (tcp
// syn without ack, any udp or icmp) and per dns question, which is enough to
// show whether umami opens any connection or resolves any name outside the
// private network. replies of umami's own listener carry ack, so they are
// counted but not logged as new flows.
//
// docker's embedded dns answers at 127.0.0.11; a name it cannot answer
// locally is forwarded from this same namespace to the host's resolver, so
// both the question on lo and the forwarded query on eth0 are seen here.
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	ethPAll        = 0x0003
	packetOutgoing = 4
)

type flow struct {
	Proto string `json:"proto"`
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	DPort uint16 `json:"dport"`
	// syn is set for a tcp segment that opens a connection
	syn bool
}

type line struct {
	Kind    string `json:"kind"` // flow, dns, total
	At      string `json:"at"`
	Ifindex int    `json:"ifindex"`
	flow
	Name    string `json:"name,omitempty"`
	Packets int    `json:"packets,omitempty"`
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

func main() {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ethPAll)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "sniff: AF_PACKET socket (needs CAP_NET_RAW):", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	seen := map[flow]bool{}
	totals := map[string]int{}
	names := map[string]bool{}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		// the totals let the test tell a quiet capture from a dead one
		for dst, n := range totals {
			_ = enc.Encode(line{Kind: "total", At: now(), flow: flow{Dst: dst}, Packets: n})
		}
		os.Exit(0)
	}()
	_ = enc.Encode(line{Kind: "start", At: now()})
	buf := make([]byte, 65536)
	for {
		n, from, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			fmt.Fprintln(os.Stderr, "sniff: recv:", err)
			os.Exit(1)
		}
		ll, ok := from.(*syscall.SockaddrLinklayer)
		if !ok || n < 14 {
			continue
		}
		f, payload, ok := parse(buf[:n])
		if !ok {
			continue
		}
		// a dns question is logged whichever direction the capture saw it in,
		// because on lo every packet is seen twice
		if f.Proto == "udp" && (f.DPort == 53 || f.Dst == "127.0.0.11") {
			if q := question(payload); q != "" && !names[q] {
				names[q] = true
				_ = enc.Encode(line{Kind: "dns", At: now(), Ifindex: ll.Ifindex, flow: f, Name: q})
			}
		}
		if ll.Pkttype != packetOutgoing {
			continue
		}
		totals[f.Dst]++
		if f.Proto == "tcp" && !f.syn {
			continue
		}
		f.syn = false
		if !seen[f] {
			seen[f] = true
			_ = enc.Encode(line{Kind: "flow", At: now(), Ifindex: ll.Ifindex, flow: f})
		}
	}
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// parse reads the ethernet, ip and tcp or udp headers. payload is the udp
// payload, nil for tcp.
func parse(b []byte) (flow, []byte, bool) {
	et := binary.BigEndian.Uint16(b[12:14])
	b = b[14:]
	var f flow
	var proto byte
	switch et {
	case 0x0800:
		if len(b) < 20 {
			return f, nil, false
		}
		ihl := int(b[0]&0x0f) * 4
		if ihl < 20 || len(b) < ihl {
			return f, nil, false
		}
		proto = b[9]
		f.Src = netip.AddrFrom4([4]byte(b[12:16])).String()
		f.Dst = netip.AddrFrom4([4]byte(b[16:20])).String()
		b = b[ihl:]
	case 0x86dd:
		if len(b) < 40 {
			return f, nil, false
		}
		proto = b[6]
		f.Src = netip.AddrFrom16([16]byte(b[8:24])).String()
		f.Dst = netip.AddrFrom16([16]byte(b[24:40])).String()
		b = b[40:]
	case 0x0806:
		// arp is how a neighbour is found on the bridge, not egress
		return f, nil, false
	default:
		f.Proto = fmt.Sprintf("ethertype-%04x", et)
		return f, nil, true
	}
	switch proto {
	case 6:
		f.Proto = "tcp"
		if len(b) >= 14 {
			f.DPort = binary.BigEndian.Uint16(b[2:4])
			// flags byte: syn 0x02, ack 0x10
			f.syn = b[13]&0x12 == 0x02
		}
		return f, nil, true
	case 17:
		f.Proto = "udp"
		if len(b) >= 8 {
			f.DPort = binary.BigEndian.Uint16(b[2:4])
			return f, b[8:], true
		}
		return f, nil, true
	case 1, 58:
		f.Proto = "icmp"
		return f, nil, true
	default:
		f.Proto = fmt.Sprintf("ip-%d", proto)
		return f, nil, true
	}
}

// question returns the first question name of a dns query, "" for anything
// that is not a query.
func question(b []byte) string {
	if len(b) < 12 || b[2]&0x80 != 0 || binary.BigEndian.Uint16(b[4:6]) == 0 {
		return ""
	}
	var labels []string
	for i := 12; i < len(b); {
		l := int(b[i])
		if l == 0 {
			return strings.Join(labels, ".")
		}
		if l&0xc0 != 0 || i+1+l > len(b) {
			return ""
		}
		labels = append(labels, string(b[i+1:i+1+l]))
		i += 1 + l
	}
	return ""
}
