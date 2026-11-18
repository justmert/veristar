// Package scfawards imports an scf awards file into the award directory
// (cp.scf_awards, migration 00050), matches the funded projects to
// enrolled projects on exact evidence, writes cohort proposals from the
// matches (control-plane-api section 19.5) and serves the award search of
// the enrollment form (4.2.7).
//
// an awards file can hold personal data next to the organisation data:
// names of people, linkedin and x handles, document links, descriptions
// and traction text. the parser reads only the organisation fields it maps
// and the stellar ids found in the row, so nothing else leaves this
// package.
package scfawards

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"golang.org/x/net/idna"

	"github.com/justmert/veristar/internal/controlplane/sites"
)

// ProgramBuildAward is the cohort program of an scf build award.
const ProgramBuildAward = "Build Award"

// programs maps the file's award type, folded to lower case, to the
// cohort program. a type outside the table rejects the row, so a new kind
// of award gets a program on purpose, not by accident.
var programs = map[string]string{
	"build":       ProgramBuildAward,
	"build award": ProgramBuildAward,
}

// Categories maps the file's category, folded to lower case, to the
// cohort category (docs/operators/scf-awards.md lists the table). scf names
// its categories itself and operators compare projects by them, so each
// keeps its scf name; the table fixes the spelling and refuses a category
// it does not know.
var Categories = map[string]string{
	"applications":              "Applications",
	"end-user application":      "End-User Application",
	"financial protocols":       "Financial Protocols",
	"developer tooling":         "Developer Tooling",
	"infrastructure & services": "Infrastructure & Services",
}

// collapse trims s and turns every run of white space, line breaks
// included, into one space.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// fold is the lookup form of a mapped value: collapsed and lower case.
func fold(s string) string { return strings.ToLower(collapse(s)) }

var roundRe = regexp.MustCompile(`(?i)^scf\s*#\s*0*([0-9]{1,4})$`)

// NormalizeRound returns the round as "SCF #<n>", the form operators
// filter by. some files write rounds with a trailing space.
func NormalizeRound(s string) (string, bool) {
	m := roundRe.FindStringSubmatch(collapse(s))
	if m == nil {
		return "", false
	}
	n := m[1]
	if n == "" {
		n = "0"
	}
	return "SCF #" + n, true
}

// NormalizeHost returns the host a website names, in the form every match
// compares: lower case, the idna a-label form, no trailing dot and no
// leading "www.". raw is a url or a bare host. ok is false for anything
// that is not a dns name of at least two labels (an ip address, localhost,
// a url with credentials, an unknown scheme).
func NormalizeHost(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return "", false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Opaque != "" {
		return "", false
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "" {
		return "", false
	}
	h, err = idna.Lookup.ToASCII(h)
	if err != nil {
		return "", false
	}
	h = strings.TrimPrefix(h, "www.")
	if !sites.ValidDomain(h) {
		return "", false
	}
	return h, true
}

// sharedHosts are hosts many unrelated organisations publish under (code
// hosting, document sharing, social profiles, scf's own pages). an award
// keeps such a host as its website, but it is never evidence that the
// award belongs to a project.
var sharedHosts = map[string]bool{
	"github.com": true, "gitlab.com": true, "bitbucket.org": true,
	"google.com": true, "docs.google.com": true, "drive.google.com": true, "sites.google.com": true,
	"communityfund.stellar.org": true, "stellar.org": true,
	"medium.com": true, "notion.so": true, "notion.site": true, "linktr.ee": true,
	"x.com": true, "twitter.com": true, "linkedin.com": true, "youtube.com": true, "youtu.be": true,
	"t.me": true, "discord.gg": true, "discord.com": true, "figma.com": true, "docsend.com": true, "loom.com": true,
}

// SharedHost reports whether a normalised host is one many organisations
// share, so it is never match evidence.
func SharedHost(h string) bool { return sharedHosts[h] }

// strkeyRe finds candidates for account (g) and contract (c) strkeys: 56
// base32 characters not inside a longer run.
var strkeyRe = regexp.MustCompile(`[GC][A-Z2-7]{55}`)

// maxStrkeys is the most ids an award keeps (the column check of 00050)
const maxStrkeys = 50

// ExtractStrkeys returns the distinct account and contract strkeys with a
// valid checksum in the texts, sorted. a candidate that touches another
// base32 character on either side is part of something longer and is
// skipped.
func ExtractStrkeys(texts ...string) []string {
	seen := map[string]bool{}
	for _, t := range texts {
		for _, loc := range strkeyRe.FindAllStringIndex(t, -1) {
			if loc[0] > 0 && isBase32(t[loc[0]-1]) || loc[1] < len(t) && isBase32(t[loc[1]]) {
				continue
			}
			k := t[loc[0]:loc[1]]
			if validStrkey(k) {
				seen[k] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isBase32(c byte) bool { return c >= 'A' && c <= 'Z' || c >= '2' && c <= '7' }

func validStrkey(k string) bool {
	vb, _, err := strkey.DecodeAny(k)
	if err != nil {
		return false
	}
	return vb == strkey.VersionByteAccountID || vb == strkey.VersionByteContract
}
