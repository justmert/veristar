//go:build webpath

package webpath

import (
	"bufio"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// SniffLine is one line of tests/webpath/sniff.
type SniffLine struct {
	Kind    string `json:"kind"`
	At      string `json:"at"`
	Ifindex int    `json:"ifindex"`
	Proto   string `json:"proto"`
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	DPort   uint16 `json:"dport"`
	Name    string `json:"name"`
	Packets int    `json:"packets"`
}

// StartSniffer runs the packet logger in umami's network namespace. it
// captures until StopSniffer.
func (s *Stack) StartSniffer(t testing.TB, bin, tag string) string {
	t.Helper()
	name := "sa-wp-sniff-" + tag + "-" + s.ID
	mustDocker(t, "run", "-d", "--name", name, "--network", "container:"+s.Umami,
		"--user", "0:0", "--cap-add", "NET_RAW", "-v", bin+":/sniff:ro", "--entrypoint", "/sniff", staticImage)
	s.names = append(s.names, name)
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(containerLogs(t, name), `"kind":"start"`) {
		if time.Now().After(deadline) {
			t.Fatalf("sniffer did not start:\n%s", containerLogs(t, name))
		}
		time.Sleep(100 * time.Millisecond)
	}
	return name
}

// StopSniffer stops the logger (it prints per-destination packet totals on
// SIGTERM) and parses everything it wrote.
func (s *Stack) StopSniffer(t testing.TB, name string) []SniffLine {
	t.Helper()
	mustDocker(t, "stop", "-t", "10", name)
	var out []SniffLine
	sc := bufio.NewScanner(strings.NewReader(containerLogs(t, name)))
	for sc.Scan() {
		var l SniffLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("sniffer line %q: %v", sc.Text(), err)
		}
		out = append(out, l)
	}
	return out
}

// Egress is what a capture shows beyond the private network.
type Egress struct {
	Flows      []SniffLine // connection attempts to an address outside the private network
	Names      []SniffLine // dns questions for a name that is not a container on it
	LocalNames []string
	Packets    int // outgoing packets seen in total, to tell a quiet capture from a dead one
	LocalFlows int
}

// classify splits a capture. local is the private network, loopback,
// link-local and multicast; a local name is a docker alias without a dot.
func classify(lines []SniffLine, private netip.Prefix) Egress {
	var e Egress
	local := func(a string) bool {
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return false
		}
		return private.Contains(ip) || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified()
	}
	for _, l := range lines {
		switch l.Kind {
		case "flow":
			if local(l.Dst) {
				e.LocalFlows++
			} else {
				e.Flows = append(e.Flows, l)
			}
		case "dns":
			if strings.Contains(strings.TrimSuffix(l.Name, "."), ".") {
				e.Names = append(e.Names, l)
			} else {
				e.LocalNames = append(e.LocalNames, l.Name)
			}
		case "total":
			e.Packets += l.Packets
		}
	}
	return e
}
