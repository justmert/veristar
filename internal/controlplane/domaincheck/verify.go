// Package domaincheck proves control of a site's domain and reads the
// domain's stellar.toml (control-plane-api 5.8 and 5.12, both beyond the
// rfp). every fetch from a tenant domain goes through internal/netguard.
// the results are evidence for operators; none of them gates ingestion.
package domaincheck

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/justmert/veristar/internal/netguard"
)

// verification methods (cp.sites.verify_method)
const (
	MethodDNS  = "dns"
	MethodFile = "file"
)

// where and what the tenant publishes (the reference docs 4.4)
const (
	DNSLabel     = "_veristar."
	TXTPrefix    = "veristar-verify="
	FilePath     = "/.well-known/veristar.txt"
	MaxFileBytes = 1024
	DNSTimeout   = 5 * time.Second
)

// last_verify_error codes of control-plane-api 5.8. the guard's own codes
// (refused_address, redirect_refused, tls_error, timeout, fetch_error)
// are stored as they are.
const (
	CodeDNSNoRecord   = "dns_no_record"
	CodeDNSMismatch   = "dns_mismatch"
	CodeDNSError      = "dns_error"
	CodeFileNotFound  = "file_not_found"
	CodeFileMismatch  = "file_mismatch"
	CodeFileTooLarge  = "file_too_large"
	resultOK          = "ok"
	ActionVerify      = "site.verify"
	ActionStellarToml = "site.stellar_toml"
)

// Checker runs the checks. Resolver answers the txt lookups (the guard
// has its own for addresses); both are parameters so tests point them at
// a test dns server.
type Checker struct {
	Guard    *netguard.Guard
	Resolver *net.Resolver
}

func (c *Checker) check() error {
	if c == nil || c.Guard == nil || c.Resolver == nil {
		return errors.New("domaincheck: checker needs a guard and a resolver")
	}
	return nil
}

// DNSName and DNSValue are the txt record a tenant publishes.
func DNSName(domain string) string { return DNSLabel + domain }

// DNSValue is the expected txt value for a token.
func DNSValue(token string) string { return TXTPrefix + token }

// FileURL is where the file check fetches the token.
func FileURL(domain string) string { return "https://" + domain + FilePath }

// CheckDNS returns "" when a txt record at _veristar.<domain>
// equals the expected value exactly, else a code.
func (c *Checker) CheckDNS(ctx context.Context, domain, token string) string {
	ctx, cancel := context.WithTimeout(ctx, DNSTimeout)
	defer cancel()
	// go joins the strings of one record into one value
	recs, err := c.Resolver.LookupTXT(ctx, DNSName(domain))
	var de *net.DNSError
	switch {
	case errors.As(err, &de) && de.IsNotFound:
		return CodeDNSNoRecord
	case err != nil:
		return CodeDNSError
	case len(recs) == 0:
		return CodeDNSNoRecord
	}
	want := DNSValue(token)
	for _, r := range recs {
		if r == want {
			return ""
		}
	}
	return CodeDNSMismatch
}

// CheckFile returns "" when https://<domain>/.well-known/veristar.txt
// answers 200 with the token (trailing whitespace ignored), else a code.
func (c *Checker) CheckFile(ctx context.Context, domain, token string) string {
	r, err := c.Guard.Get(ctx, domain, FilePath, MaxFileBytes)
	switch {
	case err != nil:
		return netguard.Code(err)
	case r.Status != 200:
		return CodeFileNotFound
	case r.TooLarge:
		return CodeFileTooLarge
	case strings.TrimRight(string(r.Body), " \t\r\n") != token:
		return CodeFileMismatch
	}
	return ""
}

// Outcome is the result of one verification.
type Outcome struct {
	OK bool
	// Method is the method that succeeded.
	Method string
	// Error is the stored code on failure: the file code when both
	// methods were tried.
	Error string
	// Results maps each tried method to "ok" or its code, for the audit
	// row.
	Results map[string]string
}

// Verify runs one method, or dns and then the file when method is empty.
// the first success wins.
func (c *Checker) Verify(ctx context.Context, domain, token, method string) (Outcome, error) {
	if err := c.check(); err != nil {
		return Outcome{}, err
	}
	methods := []string{MethodDNS, MethodFile}
	switch method {
	case "":
	case MethodDNS, MethodFile:
		methods = []string{method}
	default:
		return Outcome{}, errors.New("domaincheck: unknown method " + method)
	}
	o := Outcome{Results: map[string]string{}}
	for _, m := range methods {
		var code string
		if m == MethodDNS {
			code = c.CheckDNS(ctx, domain, token)
		} else {
			code = c.CheckFile(ctx, domain, token)
		}
		if code == "" {
			o.Results[m] = resultOK
			o.OK, o.Method, o.Error = true, m, ""
			return o, nil
		}
		o.Results[m] = code
		o.Error = code
	}
	return o, nil
}

// auditDetail is the detail of a site.verify row.
func (o Outcome) auditDetail(tenantID string) map[string]any {
	d := map[string]any{"tenant_id": tenantID, "results": o.Results}
	if o.OK {
		d["result"] = resultOK
		d["method"] = o.Method
	} else {
		d["result"] = o.Error
	}
	return d
}

// Verification is the state named by control-plane-api 5.8.
func Verification(verifiedAt, lastVerifyAt *time.Time, lastError *string) string {
	switch {
	case verifiedAt != nil && lastError == nil:
		return "verified"
	case lastVerifyAt == nil:
		return "unchecked"
	}
	return "unverified"
}
