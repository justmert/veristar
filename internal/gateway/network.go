package gateway

import (
	"bufio"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/justmert/veristar/internal/integrity"
)

// file names of the network data, in the directory of DBIP_MMDB.
// scripts/fetch-network.sh writes all three next to the country file.
const (
	NetworkASNFile     = "dbip-asn-lite.mmdb"
	NetworkClassesFile = "asn-classes.csv"
	NetworkTorFile     = "tor-exits.txt"
)

// dbipASNType is the database_type of the db-ip asn lite file.
const dbipASNType = "DBIP-ASN-Lite (compat=GeoLite2-ASN)"

// maxTorExits bounds the exit list. tor has a few thousand exit addresses
// in a month; a file far beyond that is not the expected list.
const maxTorExits = 100_000

// Network classifies the network of a client address from open data: the
// db-ip asn lite file maps the address to its autonomous system, the
// ipverse as-metadata categories map the autonomous system to a class, and
// tor's published exit addresses mark anonymisers. the address itself is
// never kept; only the class and, for the distinct network count, the
// autonomous system number leave this type, and the number leaves the
// gateway only as an hmac under the daily secret.
type Network struct {
	dir string

	// mu guards the three tables. lookups hold it shared for the decode,
	// because results point into the reader's memory map, which Reload
	// unmaps when it closes the old reader.
	mu      sync.RWMutex
	asn     *maxminddb.Reader
	classes map[uint32]integrity.NetClass
	tor     map[netip.Addr]struct{}
}

// OpenNetwork loads the network data from dir.
func OpenNetwork(dir string) (*Network, error) {
	n := &Network{dir: dir}
	r, classes, tor, err := loadNetwork(dir)
	if err != nil {
		return nil, err
	}
	n.asn, n.classes, n.tor = r, classes, tor
	return n, nil
}

func loadNetwork(dir string) (*maxminddb.Reader, map[uint32]integrity.NetClass, map[netip.Addr]struct{}, error) {
	classes, err := readClasses(filepath.Join(dir, NetworkClassesFile))
	if err != nil {
		return nil, nil, nil, err
	}
	tor, err := readTorExits(filepath.Join(dir, NetworkTorFile))
	if err != nil {
		return nil, nil, nil, err
	}
	path := filepath.Join(dir, NetworkASNFile)
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gateway: open asn database %s: %w", path, err)
	}
	if md := r.Metadata; md.DatabaseType != dbipASNType || md.IPVersion != 6 {
		_ = r.Close()
		return nil, nil, nil, fmt.Errorf("gateway: asn database %s is %q with ip version %d, want %q with ip version 6",
			path, md.DatabaseType, md.IPVersion, dbipASNType)
	}
	return r, classes, tor, nil
}

// readClasses reads asn-classes.csv: a header "asn,class" and one line per
// classified autonomous system. an autonomous system that is not listed is
// unknown.
func readClasses(path string) (map[uint32]integrity.NetClass, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the operator's configuration
	if err != nil {
		return nil, fmt.Errorf("gateway: open asn classes: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	out := map[uint32]integrity.NetClass{}
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if line == 1 {
			if text != "asn,class" {
				return nil, fmt.Errorf("gateway: %s line 1 is %q, want the header asn,class", path, text)
			}
			continue
		}
		num, class, ok := strings.Cut(text, ",")
		n, err := strconv.ParseUint(num, 10, 32)
		c := integrity.NetClass(class)
		if !ok || err != nil || !c.Valid() || c == integrity.NetUnknown || c == integrity.NetAnonymiser {
			return nil, fmt.Errorf("gateway: %s line %d is not <asn>,<access|organisation|hosting>", path, line)
		}
		out[uint32(n)] = c
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("gateway: read %s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("gateway: %s classifies no autonomous system", path)
	}
	return out, nil
}

// readTorExits reads tor-exits.txt, one address per line.
func readTorExits(path string) (map[netip.Addr]struct{}, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the operator's configuration
	if err != nil {
		return nil, fmt.Errorf("gateway: open tor exit list: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	out := map[netip.Addr]struct{}{}
	line := 0
	for sc.Scan() {
		line++
		a, err := netip.ParseAddr(sc.Text())
		if err != nil || a.Zone() != "" {
			// the parse error would quote the line, which is an address
			return nil, fmt.Errorf("gateway: %s line %d is not an ip address", path, line)
		}
		out[a.Unmap()] = struct{}{}
		if len(out) > maxTorExits {
			return nil, fmt.Errorf("gateway: %s lists more than %d addresses", path, maxTorExits)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("gateway: read %s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("gateway: %s lists no address", path)
	}
	return out, nil
}

// Classify returns the class of the address's network and its autonomous
// system number (0 when the file has none). the asn lookup uses the /24 or
// /48 like the country lookup; only the tor check compares the whole
// address, in memory, because exits are single addresses.
func (n *Network) Classify(ip ClientIP) (integrity.NetClass, uint32, error) {
	if !ip.IsValid() {
		return "", 0, errors.New("gateway: network lookup without a client address")
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	var asn uint32
	res := n.asn.Lookup(ip.truncated().Addr())
	if err := res.Err(); err != nil {
		return "", 0, fmt.Errorf("gateway: asn lookup: %w", err)
	}
	if res.Found() {
		if err := res.DecodePath(&asn, "autonomous_system_number"); err != nil {
			return "", 0, fmt.Errorf("gateway: asn record: %w", err)
		}
	}
	if _, ok := n.tor[ip.addr]; ok {
		return integrity.NetAnonymiser, asn, nil
	}
	if c, ok := n.classes[asn]; ok && asn != 0 {
		return c, asn, nil
	}
	return integrity.NetUnknown, asn, nil
}

// BuildTime is when db-ip built the loaded asn file.
func (n *Network) BuildTime() time.Time {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.asn.Metadata.BuildTime().UTC()
}

// Reload reads all three files again and swaps them in together. when any
// of them fails to load the previous data stays in use.
func (n *Network) Reload() error {
	r, classes, tor, err := loadNetwork(n.dir)
	if err != nil {
		return err
	}
	n.mu.Lock()
	old := n.asn
	n.asn, n.classes, n.tor = r, classes, tor
	n.mu.Unlock()
	return old.Close()
}

// Close releases the asn file.
func (n *Network) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.asn.Close()
}
