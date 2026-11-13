package redact

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// valid vectors from SEP-23 v1.4.0 (section tests) plus the seed, pre-auth
// and hash-x vectors of go-stellar-sdk v0.8.0 strkey/decode_test.go, since
// the SEP lists none for those three types.
var validVectors = []struct {
	key  string
	kind Kind
}{
	{"GA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVSGZ", KindAccount},
	{"GA3D5KRYM6CB7OWQ6TWYRR3Z4T7GNZLKERYNZGGA5SOAOPIFY6YQHES5", KindAccount},
	{"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAACJUQ", KindMuxedAccount},
	{"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAAAJLK", KindMuxedAccount},
	{"PA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAQACAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DUPB6IBZGM", KindSignedPayload},
	{"PA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAOQCAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DUAAAAFGBU", KindSignedPayload},
	{"CA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUWDA", KindContract},
	{"LA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUPJN", KindLiquidityPool},
	{"BAAD6DBUX6J22DMZOHIEZTEQ64CVCHEDRKWZONFEUL5Q26QD7R76RGR4TU", KindClaimableBalance},
	{"WA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAAWWC", KindMuxedContract},
	{"WA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAACWJY", KindMuxedContract},
	{"WA3D5KRYM6CB7OWQ6TWYRR3Z4T7GNZLKERYNZGGA5SOAOPIFY6YQGAAAAAAAAAPCIA6IG", KindMuxedContract},
	{"SBU2RRGLXH3E5CQHTD3ODLDF2BWDCYUSSBLLZ5GNW7JXHDIYKXZWHOKR", KindSecretSeed},
	{"TBU2RRGLXH3E5CQHTD3ODLDF2BWDCYUSSBLLZ5GNW7JXHDIYKXZWHXL7", KindPreAuthTx},
	{"XBU2RRGLXH3E5CQHTD3ODLDF2BWDCYUSSBLLZ5GNW7JXHDIYKXZWGTOG", KindHashX},
}

// invalid vectors, verbatim from SEP-23 v1.4.0.
var invalidVectors = []string{
	"GAAAAAAAACGC6",
	"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAACJUR",
	"GA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVSGZA",
	"GA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUACUSI",
	"G47QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVP2I",
	"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAAAJLKA",
	"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAAAAV75I",
	"M47QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAACJUQ",
	"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAACJUK===",
	"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAACJUO",
	"PA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAQACAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DUPB6IAAAAAAAAPM",
	"PA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAOQCAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4Z2PQ",
	"PA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAOQCAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DXFH6",
	"BAAD6DBUX6J22DMZOHIEZTEQ64CVCHEDRKWZONFEUL5Q26QD7R76RGR4TV",
	"BAAT6DBUX6J22DMZOHIEZTEQ64CVCHEDRKWZONFEUL5Q26QD7R76RGXACA",
	"WA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAAWWD",
	"WA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAAWWCA",
	"WA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAAAIOUI",
	"W47QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAADXHW",
	"WA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAAWWA",
}

func TestValidVectors(t *testing.T) {
	seen := map[Kind]bool{}
	for _, v := range validVectors {
		k, ok := Strkey(v.key)
		if !ok || k != v.kind {
			t.Errorf("Strkey(%s) = %v, %v; want %v", v.key, k, ok, v.kind)
		}
		for _, form := range []string{v.key, strings.ToLower(v.key)} {
			in := "/x/" + form + "/y"
			out, f := String(in)
			want := "/x/" + v.kind.Placeholder() + "/y"
			if out != want || !f.Has(v.kind) {
				t.Errorf("String(%q) = %q, %b; want %q", in, out, f, want)
			}
		}
		seen[v.kind] = true
	}
	for k := KindAccount; k <= KindSecretSeed; k++ {
		if !seen[k] {
			t.Errorf("no vector for %v", k)
		}
	}
}

func TestInvalidVectors(t *testing.T) {
	for _, s := range invalidVectors {
		if k, ok := Strkey(s); ok {
			t.Errorf("Strkey(%s) accepted as %v", s, k)
		}
		// a window inside an invalid vector may itself be a valid strkey
		// (the vector with one extra trailing character); then the
		// placeholder must replace exactly that window and nothing else.
		out, _ := String(s)
		if out == s {
			continue
		}
		var hit bool
		for _, v := range validVectors {
			if strings.HasPrefix(s, v.key) && out == v.kind.Placeholder()+s[len(v.key):] {
				hit = true
			}
		}
		if !hit {
			t.Errorf("String(%s) = %s, which is not a known valid prefix", s, out)
		}
	}
}

func TestLengths(t *testing.T) {
	want := map[byte][]int{'G': {56}, 'S': {56}, 'T': {56}, 'X': {56}, 'C': {56}, 'L': {56}, 'B': {58}, 'M': {69}, 'W': {69}}
	for c, l := range want {
		if got := lengthsByFirst[c]; len(got) != 1 || got[0] != l[0] {
			t.Errorf("lengths for %c = %v, want %v", c, got, l)
		}
	}
	p := lengthsByFirst['P']
	if len(p) != 16 || p[0] != 165 || p[len(p)-1] != 69 || MaxStrkeyLen != 165 {
		t.Errorf("signed payload lengths = %v, max %d", p, MaxStrkeyLen)
	}
}

func TestEmailFederation(t *testing.T) {
	cases := []struct{ in, out string }{
		{"mail me at jane.doe+x@example.co.uk now", "mail me at {email} now"},
		{"/u/maria@gmail.com*stellar.org/p", "/u/{federation}/p"},
		{"pay jed*stellar.org", "pay {federation}"},
		{"pay jed*xn--p1ai.xn--p1ai", "pay {federation}"},
		{"no@tld", "no@tld"},
		{"a*b", "a*b"},
		{"glob *.js", "glob *.js"},
	}
	for _, c := range cases {
		if got, _ := String(c.in); got != c.out {
			t.Errorf("String(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}

func TestNoFalsePositiveOnShape(t *testing.T) {
	// right shape, wrong checksum: the reason for verifying checksums.
	s := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if out, f := String(s); out != s || f != 0 {
		t.Errorf("String(%s) = %s, %b", s, out, f)
	}
}

func TestGluedAndEscaped(t *testing.T) {
	seed := "SBU2RRGLXH3E5CQHTD3ODLDF2BWDCYUSSBLLZ5GNW7JXHDIYKXZWHOKR"
	for _, in := range []string{"%2F" + seed, "QQQ" + seed + "ZZZ", "x=" + strings.ToLower(seed) + "&y"} {
		out, f := String(in)
		if !f.SecretSeed() || !strings.Contains(out, "{redacted-secret}") || hasSeed(out) {
			t.Errorf("String(%q) = %q, %b", in, out, f)
		}
	}
}

// hasSeed is an independent brute-force check: any window of any position,
// case folded, that decodes as a secret seed.
func hasSeed(s string) bool {
	u := strings.ToUpper(s)
	for i := 0; i+56 <= len(u); i++ {
		if _, err := strkey.Decode(strkey.VersionByteSeed, u[i:i+56]); err == nil {
			return true
		}
	}
	return false
}

var allVersions = []strkey.VersionByte{
	strkey.VersionByteAccountID, strkey.VersionByteMuxedAccount, strkey.VersionByteContract,
	strkey.VersionByteMuxedContract, strkey.VersionByteLiquidityPool, strkey.VersionByteClaimableBalance,
	strkey.VersionByteHashTx, strkey.VersionByteHashX, strkey.VersionByteSignedPayload, strkey.VersionByteSeed,
}

func randomStrkey(r *rand.Rand) string {
	v := allVersions[r.IntN(len(allVersions))]
	var n int
	switch v {
	case strkey.VersionByteClaimableBalance:
		b := make([]byte, 33) // type 0 then a hash
		for i := 1; i < 33; i++ {
			b[i] = byte(r.Uint32())
		}
		return strkey.MustEncode(v, b)
	case strkey.VersionByteMuxedAccount, strkey.VersionByteMuxedContract:
		n = 40
	case strkey.VersionByteSignedPayload:
		key := make([]byte, 32)
		for i := range key {
			key[i] = byte(r.Uint32())
		}
		payload := make([]byte, 1+r.IntN(64))
		for i := range payload {
			payload[i] = byte(r.Uint32())
		}
		sp, err := strkey.NewSignedPayload(strkey.MustEncode(strkey.VersionByteAccountID, key), payload)
		if err != nil {
			panic(err)
		}
		s, err := sp.Encode()
		if err != nil {
			panic(err)
		}
		return s
	default:
		n = 32
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return strkey.MustEncode(v, b)
}

var fragments = []string{"/", "?", "&", "=", " ", "%2F", "%40", "@", "*", ".", "-", "_", "{", "}", "a", "Z", "7",
	"example.com", "stellar.org", "user", "{account}", "{redacted-secret}", "é", "\t", "SA", "GA", "WA"}

func randomText(r *rand.Rand) string {
	var b strings.Builder
	for range 1 + r.IntN(12) {
		switch x := r.IntN(10); {
		case x < 3:
			k := randomStrkey(r)
			if r.IntN(3) == 0 {
				k = strings.ToLower(k)
			}
			b.WriteString(k)
		case x < 4:
			for range r.IntN(70) {
				b.WriteByte("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"[r.IntN(32)])
			}
		default:
			b.WriteString(fragments[r.IntN(len(fragments))])
		}
	}
	return b.String()
}

func checkProperties(t *testing.T, in string) {
	t.Helper()
	once, f := String(in)
	twice, f2 := String(once)
	if twice != once || f2 != 0 {
		t.Fatalf("not idempotent:\n in    %q\n once  %q\n twice %q (%b)", in, once, twice, f2)
	}
	if hasSeed(once) {
		t.Fatalf("seed left in %q", once)
	}
	if hasSeed(in) && !f.SecretSeed() {
		t.Fatalf("seed in %q not flagged", in)
	}
	if hasAnyStrkey(once) {
		t.Fatalf("strkey left in %q", once)
	}
}

// hasAnyStrkey brute forces every window of every strkey length.
func hasAnyStrkey(s string) bool {
	u := strings.ToUpper(s)
	for n := 56; n <= MaxStrkeyLen; n++ {
		for i := 0; i+n <= len(u); i++ {
			v, p, err := strkey.DecodeAny(u[i : i+n])
			if err == nil && (v != strkey.VersionByteClaimableBalance || p[0] == 0) {
				return true
			}
		}
	}
	return false
}

func TestProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 4000 {
		checkProperties(t, randomText(r))
	}
}

func FuzzString(f *testing.F) {
	for _, v := range validVectors {
		f.Add(v.key)
		f.Add("a@" + v.key + ".com")
	}
	r := rand.New(rand.NewPCG(3, 4))
	for range 50 {
		f.Add(randomText(r))
	}
	f.Fuzz(func(t *testing.T, s string) { checkProperties(t, s) })
}

func TestKindNames(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Kinds() {
		p := k.Placeholder()
		if p == "" || p[0] != '{' || p[len(p)-1] != '}' || seen[p] || seen[k.String()] {
			t.Errorf("kind %d: placeholder %q name %q", k, p, k.String())
		}
		seen[p], seen[k.String()] = true, true
	}
}

func BenchmarkString(b *testing.B) {
	r := rand.New(rand.NewPCG(5, 6))
	var sb strings.Builder
	for sb.Len() < 500 {
		sb.WriteString(randomText(r))
	}
	s := sb.String()[:500]
	b.ReportAllocs()
	for b.Loop() {
		String(s)
	}
}

// worstCases are inputs that make many positions look like the start of a
// strkey: "PA" is a signed payload candidate at every second position, "GA"
// an account candidate. 8000 characters is about the redactable text one
// event body carries.
func worstCases() map[string]string {
	return map[string]string{
		"pa_500":  strings.Repeat("PA", 250),
		"pa_8000": strings.Repeat("PA", 4000),
		"ga_8000": strings.Repeat("GA", 4000),
		"mixed":   strings.Repeat("PAGAMABAWA", 800),
	}
}

func BenchmarkStringWorstCase(b *testing.B) {
	for name, s := range worstCases() {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				String(s)
			}
		})
	}
}

// W-5: the work per input stays linear and small. every strkey decode
// attempt allocates, so the allocation count bounds the decode attempts
// without timing the host: before the fix "PA" x 4000 tried 16 signed
// payload lengths at each of 4000 positions.
func TestWorstCaseWorkIsBounded(t *testing.T) {
	for name, s := range worstCases() {
		allocs := testing.AllocsPerRun(5, func() { String(s) })
		// at most one decode, with its few allocations, per two characters
		if limit := float64(len(s)) * 2; allocs > limit {
			t.Errorf("%s: %.0f allocations for %d characters, want at most %.0f", name, allocs, len(s), limit)
		}
	}
}
