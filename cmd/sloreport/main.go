// Command sloreport copies the prober's daily results out of gatus and
// writes the monthly slo report (the design).
//
//	sloreport collect -gatus <base url> -day YYYY-MM-DD -out docs/slo/data [-page-size 20160] [-until RFC3339]
//	sloreport report -month YYYY-MM -data docs/slo/data -out docs/slo [-through YYYY-MM-DD]
//	sloreport watch -gatus <base url> -out <data dir> [-days 6] [-every 1h] [-page-size 20160]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/justmert/veristar/internal/sloreport"
)

const usage = `usage:
  sloreport collect -gatus <base url> -day YYYY-MM-DD -out docs/slo/data [-page-size 20160] [-until RFC3339]
  sloreport report -month YYYY-MM -data docs/slo/data -out docs/slo [-through YYYY-MM-DD]
  sloreport watch -gatus <base url> -out <data dir> [-days 6] [-every 1h] [-page-size 20160]
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, time.Now)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "collect":
		err = collect(ctx, args[1:], stdout, stderr, now)
	case "report":
		err = report(args[1:], stdout, stderr)
	case "watch":
		err = watch(ctx, args[1:], stdout, stderr, now)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) || errors.Is(err, errUsage) {
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sloreport %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

func collect(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("gatus", "", "gatus base url, for example https://status.<BASE_DOMAIN>")
	day := fs.String("day", "", "utc day to collect, YYYY-MM-DD; it must have ended")
	out := fs.String("out", "", "data directory, normally docs/slo/data")
	size := fs.Int("page-size", sloreport.DefaultPageSize, "page size; must equal the gatus maximum-number-of-results")
	until := fs.String("until", "", "RFC 3339 time: collect the day up to here although it has not ended, marked partial")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || *day == "" || *out == "" || fs.NArg() != 0 {
		fs.Usage()
		return errUsage
	}
	d, err := sloreport.ParseDay(*day)
	if err != nil {
		return err
	}
	c := &sloreport.Collector{Base: *base, PageSize: *size, HTTP: &http.Client{Timeout: 2 * time.Minute}, Now: now}
	if *until != "" {
		if c.Until, err = time.Parse(time.RFC3339, *until); err != nil {
			return fmt.Errorf("-until: %w", err)
		}
	}
	got, err := c.Collect(ctx, d)
	if err != nil {
		return err
	}
	path, err := sloreport.WriteDay(*out, got)
	if err != nil {
		return err
	}
	for _, k := range sloreport.Keys {
		_, _ = fmt.Fprintf(stdout, "%s %d results\n", k, len(got.Endpoints[k]))
	}
	_, _ = fmt.Fprintf(stdout, "wrote %s\n", path)
	return nil
}

// watch runs beside the probe's gatus (docs/deploy/probe.md) and collects
// every ended day of the last -days days that has no file yet, at start and
// then every -every. gatus keeps about a week of raw results, so -days must
// stay below that; a day that fails (gatus down, results trimmed) is tried
// again on the next round instead of stopping the loop.
func watch(ctx context.Context, args []string, stdout, stderr io.Writer, now func() time.Time) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("gatus", "", "gatus base url, for example https://status.<BASE_DOMAIN>")
	out := fs.String("out", "", "data directory")
	days := fs.Int("days", 6, "how many ended days back to look for a missing file; below gatus's one week")
	every := fs.Duration("every", time.Hour, "time between rounds")
	size := fs.Int("page-size", sloreport.DefaultPageSize, "page size; must equal the gatus maximum-number-of-results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || *out == "" || *days < 1 || *days > 6 || *every < time.Minute || fs.NArg() != 0 {
		fs.Usage()
		return errUsage
	}
	c := &sloreport.Collector{Base: *base, PageSize: *size, HTTP: &http.Client{Timeout: 2 * time.Minute}, Now: now}
	t := time.NewTicker(*every)
	defer t.Stop()
	for {
		watchRound(ctx, c, *out, *days, stdout, stderr)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// watchRound collects the oldest missing day first, so a long outage of
// the collector loses the days gatus is about to drop last
func watchRound(ctx context.Context, c *sloreport.Collector, out string, days int, stdout, stderr io.Writer) {
	today := c.Now().UTC().Truncate(24 * time.Hour)
	for i := days; i >= 1; i-- {
		d := today.AddDate(0, 0, -i)
		if _, err := os.Stat(sloreport.DataPath(out, d)); err == nil {
			continue
		}
		got, err := c.Collect(ctx, d)
		if err == nil && empty(got) {
			// a day before the probe started, or one the probe missed
			// completely: no file, so the report lists it as missing
			// rather than as a measured day without results
			continue
		}
		if err == nil {
			var path string
			if path, err = sloreport.WriteDay(out, got); err == nil {
				_, _ = fmt.Fprintf(stdout, "wrote %s\n", path)
				continue
			}
		}
		_, _ = fmt.Fprintf(stderr, "sloreport watch: %s: %v\n", d.Format("2006-01-02"), err)
	}
}

func empty(d sloreport.Day) bool {
	for _, rs := range d.Endpoints {
		if len(rs) > 0 {
			return false
		}
	}
	return true
}

func report(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	month := fs.String("month", "", "utc month, YYYY-MM")
	data := fs.String("data", "", "data directory, normally docs/slo/data")
	out := fs.String("out", "", "report directory, normally docs/slo")
	through := fs.String("through", "", "last day included, YYYY-MM-DD (default: the last day of the month)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *month == "" || *data == "" || *out == "" || fs.NArg() != 0 {
		fs.Usage()
		return errUsage
	}
	m, err := sloreport.ParseMonth(*month)
	if err != nil {
		return err
	}
	var t time.Time
	if *through != "" {
		if t, err = sloreport.ParseDay(*through); err != nil {
			return err
		}
	}
	loaded, err := sloreport.Load(*data, m, t)
	if err != nil {
		return err
	}
	path, err := sloreport.WriteReport(*out, loaded)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "wrote %s\n", path)
	return nil
}
