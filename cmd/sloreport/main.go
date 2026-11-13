// Command sloreport copies the prober's daily results out of gatus and
// writes the monthly slo report (the design).
//
//	sloreport collect -gatus <base url> -day YYYY-MM-DD -out docs/slo/data [-page-size 20160] [-until RFC3339]
//	sloreport report -month YYYY-MM -data docs/slo/data -out docs/slo [-through YYYY-MM-DD]
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

	"github.com/justmert/stellar-analytics/internal/sloreport"
)

const usage = `usage:
  sloreport collect -gatus <base url> -day YYYY-MM-DD -out docs/slo/data [-page-size 20160] [-until RFC3339]
  sloreport report -month YYYY-MM -data docs/slo/data -out docs/slo [-through YYYY-MM-DD]
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
