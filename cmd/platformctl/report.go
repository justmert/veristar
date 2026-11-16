package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/justmert/veristar/internal/controlplane/reports"
)

// reportUsage lists the report commands for the main usage text.
const reportUsage = `  report verify <file> [--signer G...]
                    verify a signed usage report: the canonical json form of its
                    report member and the sep-53 signature by the key it names.
                    without --signer the key must be listed in ACCOUNTS of the
                    stellar.toml the report names (fetched over https); with
                    --signer it must be that account and nothing is fetched.
                    exit 0 valid, 1 not valid, 2 the check could not run
  report keygen <dir>
                    make a new report signing key pair: write
                    report_signing_seed.json and report_signing_key.json (json
                    strings, mode 0600, for sops set --value-file) into <dir>,
                    which must not exist, and print the public account id
  report keygen --env
                    print a new pair as REPORT_SIGNING_SEED= and
                    REPORT_SIGNING_KEY= lines, for a local env file
`

// errNotValid makes run exit 1 after the verdict, without a second error
var errNotValid = errors.New("the report is not valid")

func runReport(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("report needs verify or keygen")
	}
	switch args[0] {
	case "verify":
		return reportVerify(ctx, args[1:], stdout, http.DefaultClient)
	case "keygen":
		return reportKeygen(args[1:], stdout)
	}
	return fmt.Errorf("unknown report command %q", args[0])
}

func reportVerify(ctx context.Context, args []string, stdout io.Writer, hc *http.Client) error {
	fs := flag.NewFlagSet("report verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	signer := fs.String("signer", "", "the account id the report must be signed by")
	// the file comes first or last; flag stops at the first non-flag
	var file string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if file == "" && fs.NArg() == 1 {
		file = fs.Arg(0)
	} else if fs.NArg() != 0 || file == "" {
		return errors.New("report verify takes one file")
	}
	if *signer != "" && !strkey.IsValidEd25519PublicKey(*signer) {
		return errors.New("--signer must be a stellar account id (G...)")
	}
	raw, err := readLimited(file)
	if err != nil {
		return err
	}
	v, err := reports.Verify(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "NOT VALID: %v\n", err)
		return errNotValid
	}
	_, _ = fmt.Fprintf(stdout, "signature: valid sep-53 signature by %s over the canonical report\n", v.Signer)
	_, _ = fmt.Fprintf(stdout, "report:    %s, %s (%s), %s to %s, generated %s\n", v.ID, v.Project.Name, v.Project.Slug,
		v.Period.From, v.Period.To, v.GeneratedAt)
	if *signer != "" {
		if v.Signer != *signer {
			_, _ = fmt.Fprintf(stdout, "NOT VALID: signed by %s, not by %s\n", v.Signer, *signer)
			return errNotValid
		}
		_, _ = fmt.Fprintln(stdout, "signer:    the account given with --signer")
		_, _ = fmt.Fprintln(stdout, "VALID")
		return nil
	}
	var head struct {
		Report struct {
			Issuer reports.Issuer `json:"issuer"`
		} `json:"report"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return err
	}
	tomlURL := head.Report.Issuer.StellarTOML
	accounts, err := fetchAccounts(ctx, hc, tomlURL)
	if err != nil {
		return fmt.Errorf("read the published keys at %s: %w", tomlURL, err)
	}
	if !slices.Contains(accounts, v.Signer) {
		_, _ = fmt.Fprintf(stdout, "NOT VALID: %s is not listed in ACCOUNTS of %s\n", v.Signer, tomlURL)
		return errNotValid
	}
	_, _ = fmt.Fprintf(stdout, "signer:    listed in ACCOUNTS of %s\n", tomlURL)
	_, _ = fmt.Fprintln(stdout, "VALID")
	return nil
}

func readLimited(file string) ([]byte, error) {
	f, err := os.Open(file) //nolint:gosec // the operator names the file to verify
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, reports.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > reports.MaxFileBytes {
		return nil, errors.New("the file is larger than a report can be")
	}
	return b, nil
}

// maxTOML is sep-1's size limit of a stellar.toml.
const maxTOML = 100 << 10

// fetchAccounts reads ACCOUNTS of a stellar.toml at an https url whose path
// is /.well-known/stellar.toml, as sep-1 places it.
func fetchAccounts(ctx context.Context, hc *http.Client, raw string) ([]string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != reports.StellarTOMLPath || u.RawQuery != "" {
		return nil, errors.New("the report's stellar_toml is not an https url of /.well-known/stellar.toml")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxTOML+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxTOML {
		return nil, errors.New("larger than the 100 KB sep-1 allows")
	}
	var doc struct {
		Accounts []string `toml:"ACCOUNTS"`
	}
	if err := toml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	return doc.Accounts, nil
}

func reportKeygen(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("report keygen takes a directory or --env")
	}
	kp, err := keypair.Random()
	if err != nil {
		return err
	}
	if args[0] == "--env" {
		_, err := fmt.Fprintf(stdout, "REPORT_SIGNING_SEED=%s\nREPORT_SIGNING_KEY=%s\n", kp.Seed(), kp.Address())
		return err
	}
	dir := args[0]
	if err := os.Mkdir(dir, 0o700); err != nil { //nolint:gosec // the operator names the directory

		return fmt.Errorf("the directory must not exist yet: %w", err)
	}
	for name, v := range map[string]string{"report_signing_seed.json": kp.Seed(), "report_signing_key.json": kp.Address()} {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil { //nolint:gosec // inside the directory the operator named

			return err
		}
	}
	_, err = fmt.Fprintln(stdout, kp.Address())
	return err
}
