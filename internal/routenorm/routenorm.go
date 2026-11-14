// Package routenorm turns a request path or a tenant-supplied route template
// into a low-cardinality route. the rule set is hard-coded,
// ordered and versioned; tenants cannot configure it, so every tenant's
// routes are normalized the same way and an id can never become a dimension.
//
// rules, applied in this order:
//
//  1. drop the query and the fragment, collapse repeated slashes, remove a
//     trailing slash (keep "/")
//  2. percent-decode every segment once
//  3. stellar strkeys (internal/redact): G {account}, M {muxed}, C {contract},
//     W {muxed_contract}, L {pool}, B {balance}, T {preauth}, X {hashx},
//     P {signed_payload}, S {redacted-secret} (sets FlagSecretSeed)
//  4. federation addresses {federation}, email addresses {email}
//  5. CODE:ISSUER and CODE-ISSUER assets {asset}
//  6. whole segments: 64 hex {hash}, uuid {uuid}, ulid {ulid}, 0x hex {hex},
//     integers {id}, other hex of 16 or more characters {hex}, iso dates
//     {date}, jwts and long base64url strings {token}
//  7. any remaining segment longer than 64 bytes {long}
//
// rule set version 2 adds:
//
//   - after rule 2, a segment that is not valid utf-8 or holds a control
//     character becomes {invalid}. postgresql refuses invalid utf-8 and nul
//     in text, and keeping the replacement characters of a scanner probe
//     would only add routes nobody asked for.
//   - after rule 6, every run of digits inside a segment that stands between
//     separators (- _ . , ~ : + ; = or the segment's edges) becomes {id}:
//     order-12345, 123.json, 42,43, -5 and 1.5.
//   - a segment that still holds a space or an @ (an email is replaced
//     before) is free text such as a search term or a user name, and
//     becomes {text}.
//   - a route longer than MaxRouteLen keeps its leading whole segments that
//     fit and ends in /{long}.
//
// a segment that still holds a percent escape after rule 2 is decoded up to
// four more times only to look for a secret seed; if one appears, the whole
// segment becomes {redacted-secret}.
//
// after decoding, the characters % / ? and # inside a segment are escaped
// again (%25 %2F %3F %23). that keeps the output idempotent: a second pass
// decodes exactly what the first pass escaped, so a double-encoded segment
// does not decode one level further on every pass.
package routenorm

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/justmert/veristar/internal/redact"
)

// Version identifies the rule set. it changes whenever a rule changes, so
// stored routes can be traced to the rules that produced them.
const Version = 2

// Flags reports noteworthy findings of one normalization.
type Flags uint8

// FlagSecretSeed is set when a secret seed was replaced. the seed is exposed
// already (it was in a url), so the tenant must be warned.
const FlagSecretSeed Flags = 1

// MaxSegmentLen is the longest segment rule 7 leaves in place.
const MaxSegmentLen = 64

// MaxRouteLen bounds the whole route. the route columns allow 1024 bytes;
// half of that leaves room for every real route and cannot be exceeded by
// adding a few placeholders.
const MaxRouteLen = 512

// longTail ends a route that was cut at MaxRouteLen.
const longTail = "/{long}"

// Normalize applies rules 1 to 7 to path.
func Normalize(path string) (string, Flags) {
	// rule 1: query and fragment end the path
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	var flags Flags
	var b strings.Builder
	b.Grow(len(path) + 1)
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "" {
			continue // collapses repeated slashes and drops a trailing one
		}
		out, f := segment(seg)
		flags |= f
		b.WriteByte('/')
		b.WriteString(out)
	}
	if b.Len() == 0 {
		return "/", flags
	}
	return bound(b.String()), flags
}

// bound cuts a route longer than MaxRouteLen after the last whole segment
// that leaves room for longTail. an output is never longer than
// MaxRouteLen, so a second pass leaves it as it is.
func bound(r string) string {
	if len(r) <= MaxRouteLen {
		return r
	}
	cut := 0
	for i := 1; i < len(r); i++ {
		if r[i] != '/' {
			continue
		}
		if i > MaxRouteLen-len(longTail) {
			break
		}
		cut = i
	}
	return r[:cut] + longTail
}

func segment(raw string) (string, Flags) {
	s := decodeOnce(raw) // rule 2

	// rules 3 and 4: redact runs strkeys first, then federation, then email
	var flags Flags
	if hiddenSeed(s) {
		return redact.KindSecretSeed.Placeholder(), FlagSecretSeed
	}
	s, rf := redact.String(s)
	if rf.SecretSeed() {
		flags |= FlagSecretSeed
	}
	if !storable(s) {
		return "{invalid}", flags
	}

	// rule 5: the issuer is already {account} after rule 3
	if strings.Contains(s, redact.KindAccount.Placeholder()) {
		s = assetRe.ReplaceAllString(s, "${1}{asset}")
	}

	// rule 6, on the whole segment
	if p, ok := wholeSegment(s); ok {
		return p, flags
	}
	if strings.ContainsAny(s, " @") {
		return "{text}", flags
	}
	s = innerIDs(s)

	s = escape(s)
	// rule 7
	if len(s) > MaxSegmentLen {
		return "{long}", flags
	}
	return s, flags
}

// storable reports whether s is valid utf-8 without control characters.
func storable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0) {
			return false
		}
	}
	return true
}

// innerSep lists the characters that separate an id from the rest of a
// segment.
const innerSep = "-_.,~:+;="

// innerIDs replaces every all-digit token between separators with {id}.
// letters glued to digits (v1, oauth2, a hex hash) are left alone: they
// are names or already handled by rule 6.
func innerIDs(s string) string {
	if !strings.ContainsAny(s, "0123456789") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	start := 0
	flush := func(end int) {
		tok := s[start:end]
		if tok != "" && intRe.MatchString(tok) {
			b.WriteString("{id}")
		} else {
			b.WriteString(tok)
		}
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(innerSep, s[i]) >= 0 {
			flush(i)
			b.WriteByte(s[i])
			start = i + 1
		}
	}
	flush(len(s))
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// decodeOnce decodes every valid %XX escape and keeps a % that does not start
// one literally, so a malformed escape never discards the segment.
func decodeOnce(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// maxDecodeDepth bounds the extra decoding that looks for an encoded seed.
const maxDecodeDepth = 4

// hiddenSeed reports whether decoding s further (it was decoded once
// already) reveals a secret seed. rule 2 decodes only once, so a seed encoded
// twice would otherwise survive as %2553BU2..., which anyone can decode. the
// whole segment is then replaced, since its other content cannot be trusted
// to be decoded consistently.
func hiddenSeed(s string) bool {
	for range maxDecodeDepth {
		if !strings.Contains(s, "%") {
			return false
		}
		next := decodeOnce(s)
		if next == s {
			return false
		}
		s = next
		if _, f := redact.String(s); f.SecretSeed() {
			return true
		}
	}
	return false
}

// escape re-encodes the characters that would change the meaning of the
// output on a second pass: % (another decode), / (a new segment), ? and #
// (rule 1 would cut there).
func escape(s string) string {
	if !strings.ContainsAny(s, "%/?#") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%':
			b.WriteString("%25")
		case '/':
			b.WriteString("%2F")
		case '?':
			b.WriteString("%3F")
		case '#':
			b.WriteString("%23")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

var (
	// an asset code is 1 to 12 alphanumerics (SEP-11), glued to an issuer
	// with : or -. the left edge must not be another alphanumeric, so a
	// longer word is not cut to its last 12 characters.
	assetRe = regexp.MustCompile(`(^|[^A-Za-z0-9])[A-Za-z0-9]{1,12}[:-]\{account\}`)

	hash64Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	uuidRe   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// crockford base32, 26 characters, first one 0-7 because a ulid is 128 bits
	ulidRe  = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{25}$`)
	hex0xRe = regexp.MustCompile(`^0[xX][0-9a-fA-F]+$`)
	intRe   = regexp.MustCompile(`^[0-9]+$`)
	hex16Re = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
	dateRe  = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])` +
		`([Tt ][0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]+)?)?([Zz]|[+-][0-9]{2}:?[0-9]{2})?)?$`)
	jwtRe = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)
	b64Re = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}={0,2}$`)
)

// longBase64 accepts a base64url string of 20 or more characters that mixes
// upper case, lower case and digits. route words (get-account-history,
// ledger_entries) almost never mix all three, random tokens almost always do.
func longBase64(s string) bool {
	if !b64Re.MatchString(s) {
		return false
	}
	var up, low, dig bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			up = true
		case c >= 'a' && c <= 'z':
			low = true
		case c >= '0' && c <= '9':
			dig = true
		}
	}
	return up && low && dig
}

// wholeSegment applies rule 6. the order matters where shapes overlap: a
// 64 character hex string is a {hash} before it is {hex}, a ulid made only of
// hex digits stays {ulid}, and a run of digits is {id} before it is {hex},
// so two ids of the same kind always map to the same placeholder.
func wholeSegment(s string) (string, bool) {
	switch {
	case hash64Re.MatchString(s):
		return "{hash}", true
	case uuidRe.MatchString(s):
		return "{uuid}", true
	case ulidRe.MatchString(s):
		return "{ulid}", true
	case hex0xRe.MatchString(s):
		return "{hex}", true
	case intRe.MatchString(s):
		return "{id}", true
	case hex16Re.MatchString(s):
		return "{hex}", true
	case dateRe.MatchString(s):
		return "{date}", true
	case jwtRe.MatchString(s), longBase64(s):
		return "{token}", true
	}
	return "", false
}
