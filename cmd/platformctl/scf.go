package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/scfawards"
	pdb "github.com/justmert/veristar/internal/db"
)

const scfUsage = `  scf import [-dry-run] FILE
                    import an scf build award export (FILE, or - for standard
                    input; tab or comma separated, with or without a header
                    row) into the award directory, upserting by project and
                    round, then match every award to the enrolled projects
                    and write cohort proposals. keeps organisation data only;
                    prints the counts and every rejected row with its reason.
                    -dry-run parses and reports without touching the database
  scf match         run the matching and the cohort proposals again without
                    importing, after projects proved new evidence
`

func runSCF(ctx context.Context, args []string, env *config.Env, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: platformctl scf import [-dry-run] FILE | scf match")
	}
	switch args[0] {
	case "import":
		fs := flag.NewFlagSet("scf import", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dry := fs.Bool("dry-run", false, "parse and report without writing")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: platformctl scf import [-dry-run] FILE")
		}
		data, err := readExport(fs.Arg(0), stdin)
		if err != nil {
			return err
		}
		if *dry {
			p, err := scfawards.Parse(data, time.Now().UTC())
			if err != nil {
				return err
			}
			return printParsed(stdout, p)
		}
		d, err := openSystem(ctx, env)
		if err != nil {
			return err
		}
		defer d.Close()
		rep, err := scfawards.Import(ctx, d, data, scfawards.ActorPlatformctl, time.Now())
		if err != nil {
			return err
		}
		return printReport(stdout, rep)
	case "match":
		if len(args) != 1 {
			return errors.New("usage: platformctl scf match")
		}
		d, err := openSystem(ctx, env)
		if err != nil {
			return err
		}
		defer d.Close()
		res, err := scfawards.Rematch(ctx, d, scfawards.ActorPlatformctl)
		if err != nil {
			return err
		}
		return printAutofill(stdout, res)
	}
	return errors.New("usage: platformctl scf import [-dry-run] FILE | scf match")
}

// readExport reads the export from path, or from standard input for "-",
// so the file can stream over ssh and never rest on a server.
func readExport(path string, stdin io.Reader) ([]byte, error) {
	r := stdin
	if path != "-" {
		f, err := os.Open(path) //nolint:gosec // the operator names the export to import
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, scfawards.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > scfawards.MaxFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, scfawards.MaxFileBytes)
	}
	return data, nil
}

func openSystem(ctx context.Context, env *config.Env) (*pdb.DB, error) {
	poolCfg, err := pdb.LoadPoolConfig(env, pdb.RoleCPSystem)
	if err != nil {
		return nil, err
	}
	return pdb.Open(ctx, poolCfg, "platformctl-scf")
}

func printRejections(w io.Writer, rej []scfawards.Rejection) error {
	for _, r := range rej {
		if _, err := fmt.Fprintf(w, "rejected record=%d line=%d field=%s reason=%s\n", r.Record, r.Line, r.Field, r.Reason); err != nil {
			return err
		}
	}
	return nil
}

func printParsed(w io.Writer, p scfawards.Parsed) error {
	if _, err := fmt.Fprintf(w, "dry_run=true\nheader=%t\ndelimiter=%s\nrecords=%d\naccepted=%d\nrejected=%d\n",
		p.HasHeader, p.Delimiter, p.Records, len(p.Awards), len(p.Rejected)); err != nil {
		return err
	}
	return printRejections(w, p.Rejected)
}

func printReport(w io.Writer, r scfawards.Report) error {
	if _, err := fmt.Fprintf(w, "import_id=%s\nfile_sha256=%s\nheader=%t\ndelimiter=%s\nrecords=%d\ninserted=%d\nupdated=%d\nunchanged=%d\nrejected=%d\n",
		r.ImportID, r.FileSHA256, r.HasHeader, r.Delimiter, r.Records, r.Inserted, r.Updated, r.Unchanged, len(r.Rejected)); err != nil {
		return err
	}
	if err := printAutofill(w, r.Autofill); err != nil {
		return err
	}
	return printRejections(w, r.Rejected)
}

func printAutofill(w io.Writer, a scfawards.AutofillResult) error {
	if _, err := fmt.Fprintf(w, "matched=%d\nambiguous=%d\nnot_enrolled=%d\nproposals_written=%d\n",
		a.Matched, a.Ambiguous, a.NotEnrolled, a.Proposed); err != nil {
		return err
	}
	for _, s := range a.SortedStates() {
		if _, err := fmt.Fprintf(w, "cohort_%s=%d\n", s, a.States[s]); err != nil {
			return err
		}
	}
	for _, x := range a.AmbiguousAwards {
		slugs := make([]string, 0, len(x.Candidates))
		for _, t := range x.Candidates {
			slugs = append(slugs, t.Slug)
		}
		if _, err := fmt.Fprintf(w, "ambiguous award=%s round=%q project=%q tenants=%s shared_with=%d\n",
			x.Award.ID, x.Award.Round, x.Award.Project, strings.Join(slugs, ","), len(x.SharedWith)); err != nil {
			return err
		}
	}
	return nil
}
