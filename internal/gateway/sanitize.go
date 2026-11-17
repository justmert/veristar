package gateway

import (
	"net/url"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/redact"
)

// sanitization of a parsed wire event before it becomes a WebEvent. the
// rules:
//
//   - url: every query parameter is dropped, except the CampaignParams on a
//     site whose owner turned on utm collection; kept values and the path
//     are redacted, a #/ hash route is kept (redacted, without a
//     query of its own) and every other fragment is dropped.
//   - referrer: only the scheme and host of an absolute url are kept (port,
//     user info, path, query and fragment are dropped), and the host is
//     redacted. cnil's self-assessment tool for consent-exempt audience
//     measurement (july 2025) names "the referrer, if collected, is limited
//     to the domain (host)" as its measure against importing outside data
//     (docs/compliance/sources.md S2), and the referrers metric needs
//     nothing beyond the host. a
//     same-origin path becomes empty: it names a page of the site itself,
//     which umami never counts as a referrer. anything else (opaque urls,
//     scheme relative urls, junk) becomes empty too.
//   - hostname is lower cased and redacted like the referrer host. it comes
//     from the checked Origin, and a site registered with a wildcard origin
//     (https://*.example.com) accepts any label in its place, so a page
//     address such as <account>.example.com would otherwise reach umami.
//   - title, event name and event data keys and string values are redacted.
//   - language is cut to its primary subtag.
//   - every field is cut to its field limit after redaction, because a
//     placeholder can be longer than what it replaced (a@b.co is shorter
//     than {email}).
//
// percent escapes: the path and hash route are redacted as sent and then
// once more after decoding %XX escapes, so a%40example.com is caught. only
// when the decoded form still has something to redact is the result
// re-escaped from the decoded text; otherwise the browser's own encoding is
// kept. decoding happens once, like routenorm, so a double
// encoded value (%2540) is not decoded twice.

// CampaignParams are the only query parameters that can survive
// sanitization, and only for a site whose registry entry has CollectUTM.
// they are off by default: no metric the service promises needs them, and
// cnil's self-assessment tool for consent-exempt audience measurement
// recommends not collecting them (docs/compliance/privacy-strategy.md
// section 9). every other parameter can carry personal data (search terms,
// tokens, ids) and is always dropped.
var CampaignParams = map[string]bool{
	"utm_source":   true,
	"utm_medium":   true,
	"utm_campaign": true,
	"utm_content":  true,
	"utm_term":     true,
	"ref":          true,
}

// SanitizeReport says what sanitization changed, for stats and tenant
// warnings. it never carries the removed text.
type SanitizeReport struct {
	// Redacted holds every kind of value that was replaced.
	Redacted redact.Flags
	// DroppedQueryParams counts query parameters of the page url that were
	// not kept: outside CampaignParams, campaign parameters of a site that
	// does not collect them, or with an undecodable name or value. the referrer's
	// query is dropped whole and not counted, because it is never kept.
	DroppedQueryParams int
	// DroppedDataKeys counts event data entries dropped because their key,
	// once redacted, equalled the key of another entry.
	DroppedDataKeys int
}

// SanitizeWire rewrites the privacy relevant fields of w in place: Hostname,
// URL, Referrer, Title, Language, Name and Data. screen and the proof are
// left alone; the wire parser already restricts them to fixed shapes.
// collectUTM is the site's registry setting (registry.Site.CollectUTM).
func SanitizeWire(w *events.Wire, collectUTM bool) SanitizeReport {
	var rep SanitizeReport
	var f redact.Flags
	var n int
	w.Hostname, f = SanitizeHostname(w.Hostname)
	rep.Redacted |= f
	w.URL, f, n = SanitizeURL(w.URL, collectUTM)
	rep.Redacted |= f
	rep.DroppedQueryParams += n
	w.Referrer, f = SanitizeReferrer(w.Referrer)
	rep.Redacted |= f
	w.Title, f = SanitizeText(w.Title, events.MaxTitle)
	rep.Redacted |= f
	w.Language = SanitizeLanguage(w.Language)
	if w.Name != "" {
		w.Name, f = SanitizeText(w.Name, events.MaxEventName)
		rep.Redacted |= f
	}
	if w.Data != nil {
		w.Data, f, n = SanitizeData(w.Data)
		rep.Redacted |= f
		rep.DroppedDataKeys += n
	}
	return rep
}

// SanitizeURL applies the url rules to a page url (path, optional query,
// optional fragment). it returns the result, the kinds redacted and the
// number of query parameters dropped. a url that does not start with / is
// returned as / so the result always satisfies the WebEvent contract; the
// wire parser rejects such urls before this point.
func SanitizeURL(u string, collectUTM bool) (string, redact.Flags, int) {
	out, flags, dropped := sanitizePathQueryFragment(u, collectUTM)
	out = sanCapURL(sanRooted(out), events.MaxURL)
	// the cap can cut a url inside its query or its #/ route, leaving
	// "#", "?utm_sou" or a route end that the rules read differently, so a
	// second pass would change it again. the capped result goes through the
	// rules until it stops changing; each pass only drops a cut tail, so
	// this ends after a pass or two, and the result is a fixed point that
	// the replayer and a re-sanitization see the same way.
	for range 8 {
		again, _, _ := sanitizePathQueryFragment(out, collectUTM)
		again = sanCapURL(sanRooted(again), events.MaxURL)
		if again == out {
			break
		}
		out = again
	}
	return out, flags, dropped
}

// sanRooted returns / for a url that does not start with one.
func sanRooted(s string) string {
	if s == "" || s[0] != '/' {
		return "/"
	}
	return s
}

func sanitizePathQueryFragment(u string, collectUTM bool) (string, redact.Flags, int) {
	rest, frag, hasFrag := strings.Cut(u, "#")
	path, query, hasQuery := strings.Cut(rest, "?")

	var b strings.Builder
	p, flags := sanRedactPath(path)
	b.WriteString(p)
	dropped := 0
	if hasQuery {
		q, f, n := sanitizeQuery(query, collectUTM)
		flags |= f
		dropped = n
		if q != "" {
			b.WriteByte('?')
			b.WriteString(q)
		}
	}
	// only a #/ route is kept; #section and #access_token=... are dropped
	// because a fragment can carry an oauth token (wire-v1.md, fragments).
	if hasFrag && strings.HasPrefix(frag, "/") {
		route, _, _ := strings.Cut(frag, "?")
		r, f := sanRedactPath(route)
		flags |= f
		b.WriteByte('#')
		b.WriteString(r)
	}
	return b.String(), flags, dropped
}

// sanRedactPath redacts a percent-encoded path as sent and, when decoding
// reveals more, the decoded form re-escaped.
func sanRedactPath(p string) (string, redact.Flags) {
	out, flags := redact.String(p)
	dec, changed := sanPercentDecode(out)
	if !changed {
		return out, flags
	}
	red, f := redact.String(dec)
	if f == 0 {
		return out, flags
	}
	return sanEscapePath(red), flags | f
}

func sanUnhex(c byte) (byte, bool) {
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

// sanPercentDecode decodes every well formed %XX escape and leaves stray %
// signs as they are. url.PathUnescape refuses the whole string on one stray
// %, which would let a malformed escape hide an address elsewhere.
func sanPercentDecode(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := sanUnhex(s[i+1])
			lo, ok2 := sanUnhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				changed = true
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String(), changed
}

// sanPathByte reports bytes that may stay literal in a path: rfc 3986
// unreserved, sub-delims, : @ / and the braces of redact placeholders.
func sanPathByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("-._~!$&'()*+,;=:@/{}", c) >= 0
}

func sanEscapePath(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if sanPathByte(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&15])
	}
	return b.String()
}

// sanitizeQuery keeps the campaign parameters of a site that collects them
// in their original order, redacts their decoded values and re-encodes
// them. every other parameter, and one whose name or value does not decode,
// is dropped and counted.
func sanitizeQuery(q string, collectUTM bool) (string, redact.Flags, int) {
	var b strings.Builder
	var flags redact.Flags
	dropped := 0
	for part := range strings.SplitSeq(q, "&") {
		if part == "" {
			continue
		}
		rawKey, rawVal, hasVal := strings.Cut(part, "=")
		key, err := url.QueryUnescape(rawKey)
		if err != nil || !collectUTM || !CampaignParams[key] {
			dropped++
			continue
		}
		val, err := url.QueryUnescape(rawVal)
		if err != nil {
			dropped++
			continue
		}
		red, f := redact.String(val)
		flags |= f
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(key)
		if hasVal {
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(red))
		}
	}
	return b.String(), flags, dropped
}

// SanitizeReferrer reduces a referrer to scheme://host, lower cased and
// redacted, or to "" when it is empty, a same-origin path or not an absolute
// url with a host. it returns the result and the kinds redacted.
func SanitizeReferrer(r string) (string, redact.Flags) {
	if r == "" || r[0] == '/' {
		return "", 0
	}
	pu, err := url.Parse(r)
	if err != nil || pu.Scheme == "" || pu.Opaque != "" || pu.Hostname() == "" {
		return "", 0
	}
	// the port goes with the path: a host is what the referrers metric lists
	host, flags := redact.String(strings.ToLower(pu.Hostname()))
	if strings.Contains(host, ":") {
		// an ipv6 literal keeps its brackets so the result still parses
		host = "[" + host + "]"
	}
	return sanCapURL(strings.ToLower(pu.Scheme)+"://"+host, events.MaxReferrer), flags
}

// SanitizeHostname lower cases and redacts a page hostname, then cuts it to
// events.MaxHostname. an ipv6 literal keeps its brackets.
func SanitizeHostname(h string) (string, redact.Flags) {
	out, flags := redact.String(strings.ToLower(h))
	return sanCapUnits(out, events.MaxHostname), flags
}

// SanitizeText redacts free text and cuts it to max utf-16 code units.
func SanitizeText(s string, max int) (string, redact.Flags) {
	out, flags := redact.String(s)
	return sanCapUnits(out, max), flags
}

// SanitizeLanguage reduces a language tag to its lower cased primary
// subtag: en-US becomes en. a primary subtag that is not 2 to 8 ascii
// letters (bcp 47 language subtag) gives an empty language, since it cannot
// name a language.
func SanitizeLanguage(l string) string {
	end := strings.IndexAny(l, "-_")
	if end < 0 {
		end = len(l)
	}
	p := strings.ToLower(l[:end])
	if len(p) < 2 || len(p) > 8 {
		return ""
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 'a' || p[i] > 'z' {
			return ""
		}
	}
	return p
}

// SanitizeData redacts event data keys and string values and cuts them to
// their limits. numbers and booleans pass unchanged. when two keys redact to
// the same text (two addresses as keys), the entry whose original key sorts
// first is kept and the others are dropped and counted, so the outcome does
// not depend on map order.
func SanitizeData(d map[string]any) (map[string]any, redact.Flags, int) {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(d))
	var flags redact.Flags
	dropped := 0
	for _, k := range keys {
		rk, f := SanitizeText(k, events.MaxDataKey)
		flags |= f
		if _, dup := out[rk]; dup {
			dropped++
			continue
		}
		v := d[k]
		if s, ok := v.(string); ok {
			v, f = SanitizeText(s, events.MaxDataValue)
			flags |= f
		}
		out[rk] = v
	}
	return out, flags, dropped
}

// sanCapUnits cuts s to at most max utf-16 code units without splitting a
// character, matching how the tracker and events.WebEvent count length.
func sanCapUnits(s string, max int) string {
	units := 0
	for i, r := range s {
		units += utf16.RuneLen(r)
		if units > max {
			return s[:i]
		}
	}
	return s
}

// sanCapURL is sanCapUnits that also never leaves a cut %XX escape behind.
func sanCapURL(s string, max int) string {
	c := sanCapUnits(s, max)
	if len(c) == len(s) {
		return c
	}
	if i := strings.LastIndexByte(c, '%'); i >= 0 && len(c)-i < 3 {
		c = c[:i]
	}
	return c
}
