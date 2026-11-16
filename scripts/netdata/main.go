// Command netdata turns ipverse's as-metadata json into the asn-classes.csv
// the gateway loads, then opens the finished directory with the gateway's
// own loader, so fetch-network.sh keeps exactly the files the gateway will
// accept.
//
//	netdata classes <as.json> <out dir>   writes <out dir>/asn-classes.csv
//	netdata check <dir>                   opens dbip-asn-lite.mmdb,
//	                                      asn-classes.csv and tor-exits.txt
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/justmert/veristar/internal/gateway"
	"github.com/justmert/veristar/internal/integrity"
)

func main() {
	var err error
	switch {
	case len(os.Args) == 4 && os.Args[1] == "classes":
		err = classes(os.Args[2], os.Args[3])
	case len(os.Args) == 3 && os.Args[1] == "check":
		err = check(os.Args[2])
	default:
		fmt.Fprintln(os.Stderr, "usage: netdata classes <as.json> <out dir> | netdata check <dir>")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "netdata:", err)
		os.Exit(1)
	}
}

type asEntry struct {
	ASN      uint32 `json:"asn"`
	Metadata *struct {
		Category    *string `json:"category"`
		NetworkRole *string `json:"networkRole"`
	} `json:"metadata"`
}

// classOf maps ipverse's category and network role to a class. isp is an
// access network; hosting covers hosting, cloud and content providers;
// business, education_research and government_admin are organisations. an
// unclassified system whose role is access_provider is an access network;
// every other unclassified system stays unknown and is not written.
func classOf(category, role *string) (integrity.NetClass, bool) {
	if category != nil {
		switch *category {
		case "isp":
			return integrity.NetAccess, true
		case "hosting":
			return integrity.NetHosting, true
		case "business", "education_research", "government_admin":
			return integrity.NetOrganisation, true
		}
	}
	if role != nil && *role == "access_provider" {
		return integrity.NetAccess, true
	}
	return "", false
}

func classes(src, outDir string) error {
	f, err := os.Open(src) //nolint:gosec // a path the caller names
	if err != nil {
		return err
	}
	defer f.Close()
	var entries []asEntry
	if err := json.NewDecoder(bufio.NewReader(f)).Decode(&entries); err != nil {
		return fmt.Errorf("decode %s: %w", src, err)
	}
	type row struct {
		asn   uint32
		class integrity.NetClass
	}
	var rows []row
	counts := map[integrity.NetClass]int{}
	for _, e := range entries {
		if e.Metadata == nil || e.ASN == 0 {
			continue
		}
		if c, ok := classOf(e.Metadata.Category, e.Metadata.NetworkRole); ok {
			rows = append(rows, row{e.ASN, c})
			counts[c]++
		}
	}
	// a file this small cannot be the full registry; refuse it rather than
	// classify most of the internet as unknown
	if counts[integrity.NetAccess] < 10_000 || counts[integrity.NetHosting] < 5_000 {
		return fmt.Errorf("%s classifies only %d access and %d hosting systems", src, counts[integrity.NetAccess], counts[integrity.NetHosting])
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].asn < rows[j].asn })
	out := filepath.Join(outDir, gateway.NetworkClassesFile)
	w, err := os.Create(out) //nolint:gosec // a path the caller names
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	_, _ = bw.WriteString("asn,class\n")
	for _, r := range rows {
		fmt.Fprintf(bw, "%d,%s\n", r.asn, r.class)
	}
	if err := errors.Join(bw.Flush(), w.Close()); err != nil {
		return err
	}
	fmt.Printf("netdata: wrote %s: %d access, %d organisation, %d hosting\n", out,
		counts[integrity.NetAccess], counts[integrity.NetOrganisation], counts[integrity.NetHosting])
	return nil
}

func check(dir string) error {
	n, err := gateway.OpenNetwork(dir)
	if err != nil {
		return err
	}
	defer n.Close()
	// 1.1.1.0/24 belongs to cloudflare (as13335), a hosting network, and
	// has for years; if it does not classify so the files do not fit together
	ip, err := gateway.ParseClientIP("1.1.1.1")
	if err != nil {
		return err
	}
	c, asn, err := n.Classify(ip)
	if err != nil {
		return err
	}
	if asn != 13335 || c != integrity.NetHosting {
		return fmt.Errorf("1.1.1.1 classified as as%d %s, want as13335 hosting", asn, c)
	}
	fmt.Printf("netdata: ok, asn file built %s\n", n.BuildTime().Format(time.RFC3339))
	return nil
}
