package reports

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/justmert/veristar/internal/canonjson"
	"github.com/justmert/veristar/internal/chain"
)

// Verified is what a valid signed file says about itself.
type Verified struct {
	Signer      string
	ID          string
	Project     Project
	Period      Period
	GeneratedAt string
	Format      string
	Version     int
}

// MaxFileBytes bounds a signed file a verifier reads.
const MaxFileBytes = 8 << 20

// Verify checks a signed file (docs/reference/usage-reports.md): it holds
// exactly a report and a signature, the scheme is sep-53, the report names
// the signer as its signing key, and the signature verifies over the
// canonical json bytes of the report for the signer. it does not decide
// whether the signer is the platform's key; the caller compares Signer
// with the published keys.
func Verify(raw []byte) (Verified, error) {
	if len(raw) > MaxFileBytes {
		return Verified{}, errors.New("reports: the file is larger than a report can be")
	}
	var f File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Verified{}, fmt.Errorf("reports: not a signed report file: %w", err)
	}
	if len(f.Report) == 0 || f.Signature.Value == "" || f.Signature.Signer == "" {
		return Verified{}, errors.New("reports: the file needs a report and a signature")
	}
	if f.Signature.Scheme != SignatureScheme {
		return Verified{}, fmt.Errorf("reports: unknown signature scheme %q", f.Signature.Scheme)
	}
	body, err := canonjson.Canonicalize(f.Report)
	if err != nil {
		return Verified{}, fmt.Errorf("reports: the report has no canonical form: %w", err)
	}
	if err := chain.VerifySEP53Bytes(f.Signature.Signer, body, f.Signature.Value); err != nil {
		return Verified{}, err
	}
	var head struct {
		Format      string  `json:"format"`
		Version     int     `json:"version"`
		ID          string  `json:"id"`
		Issuer      Issuer  `json:"issuer"`
		Project     Project `json:"project"`
		Period      Period  `json:"period"`
		GeneratedAt string  `json:"generated_at"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return Verified{}, fmt.Errorf("reports: %w", err)
	}
	if head.Format != Format {
		return Verified{}, fmt.Errorf("reports: the signed document is not a usage report (format %q)", head.Format)
	}
	if head.Issuer.SigningKey != f.Signature.Signer {
		return Verified{}, errors.New("reports: the report names another signing key than the one that signed it")
	}
	return Verified{Signer: f.Signature.Signer, ID: head.ID, Project: head.Project, Period: head.Period,
		GeneratedAt: head.GeneratedAt, Format: head.Format, Version: head.Version}, nil
}
