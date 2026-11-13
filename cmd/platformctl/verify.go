package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/justmert/stellar-analytics/internal/migration"
	"github.com/justmert/stellar-analytics/internal/rotationcheck"
)

// verifyUsage lists verify-migration for the main usage text.
const verifyUsage = `  verify-migration --source <env file> --target <env file> [--sites N] [--days N]
                    compare two environments after a data migration: row counts of
                    every table, per-tenant daily web and api aggregates, and umami
                    /stats for a sample of sites and days; exit 1 on any difference.
                    each env file holds PLATFORM_DB_HOST, PLATFORM_DB_PORT,
                    PLATFORM_DB_NAME, PLATFORM_DB_SSLMODE (PLATFORM_DB_SSLROOTCERT for
                    verify-full), PLATFORM_DB_ADMIN_USER,
                    PLATFORM_DB_ADMIN_PASSWORD, UMAMI_DB_NAME, UMAMI_URL and
                    UMAMI_ADMIN_PASSWORD
`

// errDiffers makes run exit 1 after the report, without a second error line
var errDiffers = errors.New("source and target differ")

// verifyTimeout bounds the whole comparison; counting a large umami event
// table is the slow part
const verifyTimeout = 30 * time.Minute

func runVerifyMigration(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("verify-migration", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	src := fs.String("source", "", "env file of the source environment")
	dst := fs.String("target", "", "env file of the target environment")
	sites := fs.Int("sites", 10, "sites in the umami /stats sample")
	days := fs.Int("days", 7, "most recent days with events per sampled website")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("verify-migration: %w", err)
	}
	if *src == "" || *dst == "" || fs.NArg() != 0 {
		return errors.New("usage: platformctl verify-migration --source <env file> --target <env file> [--sites N] [--days N]")
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	open := func(path string) (migration.Side, func(), error) {
		values, err := migration.ReadEnvFile(path)
		if err != nil {
			return migration.Side{}, nil, err
		}
		ep, err := migration.LoadEndpoint(values)
		if err != nil {
			return migration.Side{}, nil, fmt.Errorf("%s: %w", path, err)
		}
		return migration.Open(ctx, ep, "platformctl-verify-migration")
	}
	s, closeS, err := open(*src)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	defer closeS()
	d, closeD, err := open(*dst)
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}
	defer closeD()

	// a comparison across releases would report schema drift as data loss
	sv, err := migration.ReadVersions(ctx, s)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	dv, err := migration.ReadVersions(ctx, d)
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}
	if sv.Platform != dv.Platform || !slices.Equal(sv.Umami, dv.Umami) {
		return fmt.Errorf("schema versions differ: source platform %d with %d umami migrations, target platform %d with %d umami migrations",
			sv.Platform, len(sv.Umami), dv.Platform, len(dv.Umami))
	}

	keys, err := migration.PickStats(ctx, s, migration.Sample{Sites: *sites, Days: *days})
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	ss, err := migration.Read(ctx, s, keys)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	ds, err := migration.Read(ctx, d, keys)
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}
	rep := migration.Compare(ss, ds)

	w := &errWriter{w: stdout}
	w.printf("schema: platform goose version %d, %d umami migrations, equal\n", sv.Platform, len(sv.Umami))
	for _, kind := range []string{"rows", "web", "web-dim", "api", "umami-stats"} {
		w.printf("compared %-11s %d keys\n", kind, rep.Compared[kind])
	}
	for _, e := range rep.Excluded {
		w.printf("not compared: %s\n", e)
	}
	for _, diff := range rep.Differences {
		w.printf("DIFF %s\n", diff)
	}
	if w.err != nil {
		return w.err
	}
	if len(rep.Differences) > 0 {
		w.printf("result: %d differences\n", len(rep.Differences))
		return errDiffers
	}
	w.printf("result: source and target are equal\n")
	return w.err
}

// errWriter keeps the first write error, so the report is printed with one
// check at the end instead of one per line
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}

// rotationUsage lists rotation-check for the main usage text.
const rotationUsage = `  rotation-check    read a json spec on stdin (internal/rotationcheck) naming postgresql
                    logins, nats users and the umami admin and user key, each with its
                    previous and new value; exit 1 unless every previous value is refused
                    and every new one accepted (rotate-all.yml)
`

func runRotationCheck(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	var spec rotationcheck.Spec
	dec := json.NewDecoder(io.LimitReader(stdin, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return fmt.Errorf("rotation-check: spec: %w", err)
	}
	res, err := rotationcheck.Run(ctx, spec, time.Now())
	if err != nil {
		return err
	}
	if len(res) == 0 {
		return errors.New("rotation-check: the spec names no credential")
	}
	w := &errWriter{w: stdout}
	bad := 0
	for _, r := range res {
		mark := "ok  "
		if !r.OK() {
			mark = "FAIL"
			bad++
		}
		w.printf("%s %s\n", mark, r)
	}
	if w.err != nil {
		return w.err
	}
	if bad > 0 {
		w.printf("result: %d of %d credentials not rotated as required\n", bad, len(res))
		return errDiffers
	}
	w.printf("result: %d previous credentials refused, every new one accepted\n", len(res))
	return w.err
}
