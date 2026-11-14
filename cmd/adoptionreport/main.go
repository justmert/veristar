// Command adoptionreport writes the adoption evidence of the rfp's live proof
// (rfp section 5) from the machine metrics api, so no number in
// the evidence is typed by hand. for each tenant id it reads the machine api
// with an operator key and writes .scratch/adoption/evidence-YYYY-MM-DD.md with
// the consecutive utc days that carry accepted, proof-checked page views, the
// seven web metrics of rfp 3.2 and the api volume, status codes, p50 and p95.
//
//	adoptionreport -api https://app.<BASE_DOMAIN>/api/metrics/v1 -tenants <id>,<id>
//	               [-from YYYY-MM-DD] [-to YYYY-MM-DD] [-min-days 14]
//	               [-out .scratch/adoption] [-raw <dir>] [-force] [-now RFC3339]
//
// the key comes from SA_OPERATOR_KEY, never from a flag, so it stays out of
// shell history and process lists. -raw keeps every response body beside an
// index, which is how the test fixtures in testdata/recorded were made.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// the default evidence threshold, a bar set beyond what the rfp asks
const defaultMinDays = 14

// dayLayout is the contract's Day
const dayLayout = "2006-01-02"

var (
	idRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	opKeyRe = regexp.MustCompile(`^sa_op_[0-9A-Za-z]{40}$`)
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	hc := &http.Client{Timeout: 60 * time.Second}
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, hc, time.Now, sleepCtx)
	stop()
	os.Exit(code)
}

// sleepCtx waits d or until ctx ends
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseDay(s string) (time.Time, error) {
	return time.Parse(dayLayout, s)
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer,
	hc *http.Client, clock func() time.Time, sleep func(context.Context, time.Duration) error) int {
	fs := flag.NewFlagSet("adoptionreport", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apiFlag := fs.String("api", "", "base url of the machine api, ending in /api/metrics/v1")
	tenantsFlag := fs.String("tenants", "", "comma-separated tenant ids")
	fromFlag := fs.String("from", "", "first utc day (default: 29 days before -to)")
	toFlag := fs.String("to", "", "last utc day (default: yesterday, the last complete utc day)")
	minDays := fs.Int("min-days", defaultMinDays, "consecutive days with accepted page views a web project needs")
	outDir := fs.String("out", filepath.Join(".scratch", "adoption"), "directory the evidence file is written to")
	rawDir := fs.String("raw", "", "directory to keep every response body and an index of the requests in")
	force := fs.Bool("force", false, "replace an evidence file of the same day")
	nowFlag := fs.String("now", "", "RFC 3339 time to write the report at instead of the current time")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(format string, a ...any) int {
		_, _ = fmt.Fprintf(stderr, "adoptionreport: "+format+"\n", a...)
		return 2
	}
	if fs.NArg() != 0 {
		return fail("unexpected arguments %q", fs.Args())
	}
	base, err := url.Parse(*apiFlag)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" ||
		!strings.HasSuffix(strings.TrimSuffix(base.Path, "/"), "/api/metrics/v1") {
		return fail("-api must be an http(s) url ending in /api/metrics/v1, got %q", *apiFlag)
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	tenants := splitList(*tenantsFlag)
	if len(tenants) == 0 {
		return fail("-tenants is required")
	}
	seen := map[string]bool{}
	for _, id := range tenants {
		if !idRe.MatchString(id) {
			return fail("-tenants: %q is not a tenant id", id)
		}
		if seen[id] {
			return fail("-tenants: %s is listed twice", id)
		}
		seen[id] = true
	}
	key := getenv("SA_OPERATOR_KEY")
	if !opKeyRe.MatchString(key) {
		return fail("SA_OPERATOR_KEY must hold an operator api key (sa_op_ and 40 base62 characters)")
	}
	if *minDays < 1 {
		return fail("-min-days must be at least 1")
	}
	now := clock().UTC()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return fail("-now: %v", err)
		}
		now = t.UTC()
	}
	today := now.Truncate(24 * time.Hour)
	to := today.AddDate(0, 0, -1)
	if *toFlag != "" {
		if to, err = parseDay(*toFlag); err != nil {
			return fail("-to: %v", err)
		}
	}
	from := to.AddDate(0, 0, -29)
	if *fromFlag != "" {
		if from, err = parseDay(*fromFlag); err != nil {
			return fail("-from: %v", err)
		}
	}
	switch {
	case to.After(today):
		return fail("-to %s is after today (%s)", to.Format(dayLayout), today.Format(dayLayout))
	case from.After(to):
		return fail("-from %s is after -to %s", from.Format(dayLayout), to.Format(dayLayout))
	case to.Sub(from) >= 366*24*time.Hour:
		return fail("the range is longer than the api's 366 days")
	}

	var rec *recorder
	if *rawDir != "" {
		if rec, err = newRecorder(*rawDir); err != nil {
			return fail("%v", err)
		}
	}
	c := &client{base: base, key: key, hc: hc, sleep: sleep, rec: rec}
	ev, err := collect(ctx, c, tenants, from, to)
	if rec != nil {
		if ferr := rec.flush(); ferr != nil && err == nil {
			err = ferr
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "adoptionreport: %v\n", err)
		return 1
	}
	ev.GeneratedAt = now
	ev.Today = today
	ev.API = base.String()
	ev.MinDays = *minDays
	ev.Sources = c.sources

	if err := os.MkdirAll(*outDir, 0o750); err != nil {
		return fail("%v", err)
	}
	path := filepath.Join(*outDir, "evidence-"+now.Format(dayLayout)+".md")
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if *force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644) //nolint:gosec // the path is the operator's own -out directory
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fail("%s exists; pass -force to replace it", path)
		}
		return fail("%v", err)
	}
	werr := render(f, ev)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_, _ = fmt.Fprintf(stderr, "adoptionreport: writing %s: %v\n", path, werr)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s\n", path)
	for _, line := range ev.summaryLines() {
		_, _ = fmt.Fprintln(stdout, line)
	}
	return 0
}
