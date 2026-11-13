// Command drillctl drives the this stage drills against the local converge
// (docs/reference/drills/2026-11-13.md): it creates the drill tenants, runs
// the load profiles of tests/load with a ledger of every acknowledged event,
// checks the ledger against umami and the platform database, probes the
// dashboards, writes and checks committed transactions, takes table
// checksums for the point-in-time restore and measures clock agreement.
//
// it is test tooling: it signs in through the test-only oauth fixture of the
// local converge and reads the database as the postgres superuser.
//
//	go run ./tests/drills/drillctl <command> [flags]
//
// commands: setup, load, verify, probe, commits, snapshot, checksum, clock,
// lag. every command prints one json document on stdout.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: drillctl setup|load|verify|probe|commits|snapshot|checksum|clock|lag [flags]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmds := map[string]func(context.Context, []string) (any, error){
		"setup":    runSetup,
		"load":     runLoad,
		"verify":   runVerify,
		"probe":    runProbe,
		"commits":  runCommits,
		"snapshot": runSnapshot,
		"checksum": runChecksum,
		"clock":    runClock,
		"lag":      runLag,
	}
	f, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	out, err := f(ctx, os.Args[2:])
	if out != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "drillctl:", err)
		os.Exit(1)
	}
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) //nolint:gosec // drill files named by the operator running the drill
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
