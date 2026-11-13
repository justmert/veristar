package routenorm

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/justmert/stellar-analytics/internal/redact"
)

type corpusCase struct {
	line            int
	in, want, flags string
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	f, err := os.Open("testdata/corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []corpusCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			t.Fatalf("corpus line %d has %d columns", n, len(cols))
		}
		out = append(out, corpusCase{n, cols[0], cols[1], cols[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCorpus(t *testing.T) {
	cases := loadCorpus(t)
	if len(cases) < 300 {
		t.Fatalf("corpus has %d cases, at least 300 are required", len(cases))
	}
	for _, c := range cases {
		got, f := Normalize(c.in)
		if got != c.want {
			t.Errorf("line %d: Normalize(%q) = %q, want %q", c.line, c.in, got, c.want)
		}
		wantSecret := c.flags == "secret"
		if (f&FlagSecretSeed != 0) != wantSecret {
			t.Errorf("line %d: Normalize(%q) secret flag = %v, want %v", c.line, c.in, f&FlagSecretSeed != 0, wantSecret)
		}
		// every corpus output is already normal
		again, _ := Normalize(got)
		if again != got {
			t.Errorf("line %d: not idempotent: %q then %q", c.line, got, again)
		}
	}
}

// every SEP-23 strkey type must appear in the corpus with its placeholder.
func TestCorpusCoversEveryStrkeyType(t *testing.T) {
	cases := loadCorpus(t)
	for _, k := range redact.Kinds() {
		p := k.Placeholder()
		found := false
		for _, c := range cases {
			if strings.Contains(c.want, p) && !strings.Contains(c.in, p) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no corpus case produces %s", p)
		}
	}
	for _, p := range []string{"{asset}", "{hash}", "{hex}", "{uuid}", "{ulid}", "{id}", "{token}", "{date}", "{long}", "{invalid}", "{text}"} {
		found := false
		for _, c := range cases {
			if strings.Contains(c.want, p) && !strings.Contains(c.in, p) {
				found = true
			}
		}
		if !found {
			t.Errorf("no corpus case produces %s", p)
		}
	}
}

// random generators for the property tests. keys come from the real
// encoder, so every one is a valid strkey with a correct checksum.
func randBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.UintN(256))
	}
	return b
}

func randKey(r *rand.Rand, v strkey.VersionByte) string {
	n := 32
	switch v {
	case strkey.VersionByteMuxedAccount, strkey.VersionByteMuxedContract:
		n = 40
	case strkey.VersionByteClaimableBalance:
		b := append([]byte{0}, randBytes(r, 32)...)
		s, err := strkey.Encode(v, b)
		if err != nil {
			panic(err)
		}
		return s
	case strkey.VersionByteSignedPayload:
		sp, err := strkey.NewSignedPayload(mustEncode(strkey.VersionByteAccountID, randBytes(r, 32)), randBytes(r, 1+r.IntN(64)))
		if err != nil {
			panic(err)
		}
		s, err := sp.Encode()
		if err != nil {
			panic(err)
		}
		return s
	}
	return mustEncode(v, randBytes(r, n))
}

func mustEncode(v strkey.VersionByte, b []byte) string {
	s, err := strkey.Encode(v, b)
	if err != nil {
		panic(err)
	}
	return s
}

var versions = []strkey.VersionByte{
	strkey.VersionByteAccountID, strkey.VersionByteMuxedAccount, strkey.VersionByteContract,
	strkey.VersionByteMuxedContract, strkey.VersionByteLiquidityPool, strkey.VersionByteClaimableBalance,
	strkey.VersionByteHashTx, strkey.VersionByteHashX, strkey.VersionByteSignedPayload, strkey.VersionByteSeed,
}

const hexDigits = "0123456789abcdef"

func randFrom(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

// randID returns an id of the given kind; two calls give different ids that
// must normalize to the same placeholder.
func randID(r *rand.Rand, kind int) string {
	switch kind {
	case 0:
		return fmt.Sprint(1 + r.IntN(1_000_000_000))
	case 1:
		return randFrom(r, hexDigits, 64)
	case 2:
		return fmt.Sprintf("%s-%s-%s-%s-%s", randFrom(r, hexDigits, 8), randFrom(r, hexDigits, 4),
			randFrom(r, hexDigits, 4), randFrom(r, hexDigits, 4), randFrom(r, hexDigits, 12))
	case 3:
		return randFrom(r, "01234567", 1) + randFrom(r, "0123456789ABCDEFGHJKMNPQRSTVWXYZ", 25)
	case 4:
		return "0x" + randFrom(r, hexDigits, 40)
	case 5:
		return fmt.Sprintf("%04d-%02d-%02d", 1970+r.IntN(100), 1+r.IntN(12), 1+r.IntN(28))
	case 6:
		// upper, lower and digit guaranteed, then random base64url
		return "Aa0" + randFrom(r, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", 29)
	case 7:
		return randFrom(r, "abcdefghijklmnopqrstuvwxyz", 1+r.IntN(8)) + "@example.com"
	case 8:
		return "USDC:" + randKey(r, strkey.VersionByteAccountID)
	default:
		return randKey(r, versions[r.IntN(len(versions))])
	}
}

func TestPropertyIdempotent(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	alphabet := "abcXYZ019/%?#.-_*@:{}+ ~"
	for i := 0; i < 20000; i++ {
		var in string
		if i%2 == 0 {
			in = randFrom(r, alphabet, r.IntN(80))
		} else {
			in = "/" + randID(r, r.IntN(10)) + "/" + randFrom(r, alphabet, r.IntN(20))
			if r.IntN(3) == 0 {
				in = url.PathEscape(in)
			}
		}
		once, f1 := Normalize(in)
		twice, f2 := Normalize(once)
		if once != twice {
			t.Fatalf("Normalize not idempotent for %q: %q then %q", in, once, twice)
		}
		if f2&FlagSecretSeed != 0 {
			t.Fatalf("second pass found a seed in %q (from %q, first flags %b)", once, in, f1)
		}
	}
}

// containsSeed scans s for a valid secret seed in any case, also after up to
// four percent decodings, so an escaped seed counts as present.
func containsSeed(s string) bool {
	for range 5 {
		if _, f := redact.String(s); f.SecretSeed() {
			return true
		}
		d, err := url.PathUnescape(s)
		if err != nil || d == s {
			// a partial decode still matters, so try the lenient decoder
			d = decodeOnce(s)
			if d == s {
				return false
			}
		}
		s = d
	}
	return false
}

func TestPropertyNoSeedSurvives(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 5000; i++ {
		seed := randKey(r, strkey.VersionByteSeed)
		form := seed
		switch r.IntN(5) {
		case 0:
			form = strings.ToLower(seed)
		case 1:
			form = strings.ReplaceAll(seed, "S", "%53")
		case 2:
			form = strings.ReplaceAll(seed, "S", "%2553")
		case 3:
			form = "x" + seed + "y"
		}
		in := "/" + randFrom(r, "abc/", r.IntN(10)) + "/" + form + "/" + randFrom(r, "abc/?#", r.IntN(10))
		out, f := Normalize(in)
		if containsSeed(out) {
			t.Fatalf("seed survives: %q -> %q", in, out)
		}
		// the prefix has no ? or #, so the seed is always inside the path
		if f&FlagSecretSeed == 0 {
			t.Fatalf("seed not flagged: %q -> %q", in, out)
		}
	}
}

func TestPropertyIDsCollapse(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 5000; i++ {
		kinds := []int{r.IntN(10), r.IntN(10)}
		mk := func() string {
			return "/v1/things/" + randID(r, kinds[0]) + "/sub/" + randID(r, kinds[1]) + "/end"
		}
		a, b := mk(), mk()
		na, _ := Normalize(a)
		nb, _ := Normalize(b)
		// strkeys of different types differ in placeholder on purpose, so
		// compare only when both ids per position are of the same kind,
		// which mk guarantees except for the random strkey type of kind 9
		if kinds[0] == 9 || kinds[1] == 9 {
			continue
		}
		if na != nb {
			t.Fatalf("ids of the same kind differ:\n%q -> %q\n%q -> %q", a, na, b, nb)
		}
	}
}

func TestEveryStrkeyTypeRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	for _, v := range versions {
		for range 200 {
			k := randKey(r, v)
			kind, ok := redact.Strkey(k)
			if !ok {
				t.Fatalf("generated key %s not recognised", k)
			}
			got, _ := Normalize("/a/" + k)
			if got != "/a/"+kind.Placeholder() {
				t.Fatalf("Normalize(/a/%s) = %s, want %s", k, got, kind.Placeholder())
			}
		}
	}
}

// every output must be storable: valid utf-8 without control characters
// (postgresql refuses invalid utf-8 and nul in text) and at most MaxRouteLen
// bytes (the route columns allow 1024). the inputs are raw bytes and
// arbitrary escapes, as a scanner sends them.
func TestPropertyStorable(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for range 1 + r.IntN(120) {
			switch r.IntN(4) {
			case 0:
				b.WriteByte(byte(r.UintN(256)))
			case 1:
				fmt.Fprintf(&b, "%%%02X", r.UintN(256))
			case 2:
				b.WriteByte('/')
			default:
				b.WriteString(randFrom(r, "abc019-_. @", 1+r.IntN(30)))
			}
		}
		in := b.String()
		out, _ := Normalize(in)
		if !utf8.ValidString(out) {
			t.Fatalf("Normalize(%q) = %q is not valid utf-8", in, out)
		}
		for _, c := range out {
			if c < 0x20 || c == 0x7f {
				t.Fatalf("Normalize(%q) = %q holds control character %U", in, out, c)
			}
		}
		if len(out) > MaxRouteLen {
			t.Fatalf("Normalize(%q) is %d bytes, more than %d", in, len(out), MaxRouteLen)
		}
		if again, _ := Normalize(out); again != out {
			t.Fatalf("not idempotent: %q then %q", out, again)
		}
	}
}
