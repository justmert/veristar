package tenants

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

func chiParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

// Fields collects 422 field codes (contract 2.5) in the order found, so the
// message names the first problem.
type Fields struct {
	codes map[string]string
	first string
}

// Add records a code for a json path; the first code of a path wins.
func (f *Fields) Add(path, code string) {
	if f.codes == nil {
		f.codes = map[string]string{}
	}
	if _, ok := f.codes[path]; ok {
		return
	}
	if f.first == "" {
		f.first = path
	}
	f.codes[path] = code
}

// Err is nil without problems, else 422 validation_failed.
func (f *Fields) Err() error {
	if len(f.codes) == 0 {
		return nil
	}
	return httpx.Validation(f.first+" is "+strings.ReplaceAll(f.codes[f.first], "_", " "), f.codes)
}

// Text checks a free text field (contract 3.2): length in characters after
// trimming, and no control characters other than newline and tab. it
// returns the trimmed value, which is what gets stored.
func Text(f *Fields, path, v string, minLen, maxLen int) string {
	v = strings.TrimSpace(v)
	if !utf8.ValidString(v) {
		f.Add(path, httpx.FieldInvalidFormat)
		return v
	}
	for _, r := range v {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			f.Add(path, httpx.FieldInvalidFormat)
			return v
		}
	}
	n := utf8.RuneCountInString(v)
	switch {
	case n == 0 && minLen > 0:
		f.Add(path, httpx.FieldRequired)
	case n < minLen:
		f.Add(path, httpx.FieldTooShort)
	case n > maxLen:
		f.Add(path, httpx.FieldTooLong)
	}
	return v
}

// HTTPSURL checks an absolute https url of at most 2,048 characters.
func HTTPSURL(f *Fields, path, v string) {
	if len(v) > 2048 {
		f.Add(path, httpx.FieldTooLong)
		return
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Opaque != "" || u.User != nil || strings.ContainsAny(v, " \t\r\n") {
		f.Add(path, httpx.FieldNotHTTPSURL)
	}
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// DeriveSlug turns a name into a slug that matches the column check: ascii
// letters and digits kept, every other run of characters becomes one
// hyphen. "" means the name has nothing to build a slug from.
func DeriveSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

// stellar id types allowed at enrollment (contract 3.2): account, muxed
// account and contract
var enrollStrkeyTypes = map[strkey.VersionByte]bool{
	strkey.VersionByteAccountID:    true,
	strkey.VersionByteMuxedAccount: true,
	strkey.VersionByteContract:     true,
}

// StellarIDs checks up to 20 unique G, M or C strkeys.
func StellarIDs(f *Fields, path string, ids []string) {
	if len(ids) > 20 {
		f.Add(path, httpx.FieldTooMany)
		return
	}
	seen := map[string]bool{}
	for i, id := range ids {
		p := path + "[" + strconv.Itoa(i) + "]"
		vb, _, err := strkey.DecodeAny(id)
		if err != nil || !enrollStrkeyTypes[vb] {
			f.Add(p, httpx.FieldInvalidStrkey)
			continue
		}
		if seen[id] {
			f.Add(p, httpx.FieldDuplicate)
		}
		seen[id] = true
	}
}
