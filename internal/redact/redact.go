// Package redact replaces stellar identifiers, secret seeds, email addresses
// and federation addresses in free text with fixed placeholders. the gateway
// applies it to every text field of a web event before publishing, and routenorm reuses it for api routes.
//
// strkeys: all ten SEP-23 v1.4.0 types are recognised (G account, M muxed
// account, C contract, W muxed contract, L liquidity pool, B claimable
// balance, T pre-auth tx, X hash-x, P signed payload, S secret seed). a
// candidate is a window of the base32 alphabet A-Z2-7 whose first character
// names a type, whose second character carries algorithm 0 and whose length
// is exact for that type (56, 58 for B, 69 for M and W, 69 to 165 for P).
// every candidate is first checked cheaply here, without allocating: it is
// decoded into a stack buffer and its crc16 compared, and for a signed
// payload the declared payload length picks the one candidate length. only a
// candidate whose checksum matches (about one in 65536 of random text) is
// then checked with strkey.DecodeAny from
// github.com/stellar/go-stellar-sdk: crc16 checksum, version byte, canonical
// base32 tail and payload length or signed payload structure. one SEP-23
// rule that DecodeAny does not apply is added here: a claimable balance must
// have type byte 0.
//
// checksums are verified on purpose. matching by shape alone would replace
// any 56 character upper case word that starts with G, which is common in
// base32 ids and tokens of other systems and would make the data useless,
// while the checksum makes a false match about one in 65536 per shaped
// candidate. the price is that a strkey with a typo is left in place, which
// leaks nothing usable: a mistyped secret seed does not sign anything.
//
// matching ignores case (the window is upper cased before decoding), because
// some routers and frameworks lower case paths and a lower cased seed is
// still a secret. windows are searched anywhere inside a run of base32
// characters, not only at word boundaries, so a key glued to other letters
// or to a percent escape such as %2F is still found.
//
// email and federation addresses (SEP-2, name*domain) are matched with ascii
// patterns after strkeys. federation runs first so that an email used as a
// federation name (maria@example.com*stellar.org) becomes one placeholder.
//
// every placeholder starts with { and ends with }, characters that none of
// the patterns accept, so redacting already redacted text changes nothing.
package redact

import (
	"encoding/binary"
	"regexp"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
)

// Kind is one thing that redaction replaces.
type Kind uint8

// the kinds, one per SEP-23 strkey type plus email and federation.
const (
	KindAccount          Kind = iota // G
	KindMuxedAccount                 // M
	KindContract                     // C
	KindMuxedContract                // W
	KindLiquidityPool                // L
	KindClaimableBalance             // B
	KindPreAuthTx                    // T
	KindHashX                        // X
	KindSignedPayload                // P
	KindSecretSeed                   // S
	KindEmail
	KindFederation
	numKinds
)

// placeholders follow the routenorm rule list, so the gateway and
// the api route normaliser use the same words.
var placeholders = [numKinds]string{
	KindAccount:          "{account}",
	KindMuxedAccount:     "{muxed}",
	KindContract:         "{contract}",
	KindMuxedContract:    "{muxed_contract}",
	KindLiquidityPool:    "{pool}",
	KindClaimableBalance: "{balance}",
	KindPreAuthTx:        "{preauth}",
	KindHashX:            "{hashx}",
	KindSignedPayload:    "{signed_payload}",
	KindSecretSeed:       "{redacted-secret}",
	KindEmail:            "{email}",
	KindFederation:       "{federation}",
}

var kindNames = [numKinds]string{
	KindAccount:          "account",
	KindMuxedAccount:     "muxed_account",
	KindContract:         "contract",
	KindMuxedContract:    "muxed_contract",
	KindLiquidityPool:    "liquidity_pool",
	KindClaimableBalance: "claimable_balance",
	KindPreAuthTx:        "pre_auth_tx",
	KindHashX:            "hash_x",
	KindSignedPayload:    "signed_payload",
	KindSecretSeed:       "secret_seed",
	KindEmail:            "email",
	KindFederation:       "federation",
}

// Kinds lists every kind in a stable order, for metrics and tests.
func Kinds() []Kind {
	out := make([]Kind, numKinds)
	for i := range out {
		out[i] = Kind(i)
	}
	return out
}

// Placeholder is the text that replaces a match of this kind.
func (k Kind) Placeholder() string {
	if k >= numKinds {
		return ""
	}
	return placeholders[k]
}

// String is a snake_case name usable as a metric label.
func (k Kind) String() string {
	if k >= numKinds {
		return "unknown"
	}
	return kindNames[k]
}

// Flags records which kinds were replaced.
type Flags uint16

// Has reports whether a match of kind k was replaced.
func (f Flags) Has(k Kind) bool { return k < numKinds && f&(1<<k) != 0 }

// SecretSeed reports whether a secret seed was replaced. callers use it to
// warn the tenant, since a seed in a url means the seed is already exposed.
func (f Flags) SecretSeed() bool { return f.Has(KindSecretSeed) }

func flagOf(k Kind) Flags { return 1 << k }

var versionKinds = map[strkey.VersionByte]Kind{
	strkey.VersionByteAccountID:        KindAccount,
	strkey.VersionByteMuxedAccount:     KindMuxedAccount,
	strkey.VersionByteContract:         KindContract,
	strkey.VersionByteMuxedContract:    KindMuxedContract,
	strkey.VersionByteLiquidityPool:    KindLiquidityPool,
	strkey.VersionByteClaimableBalance: KindClaimableBalance,
	strkey.VersionByteHashTx:           KindPreAuthTx,
	strkey.VersionByteHashX:            KindHashX,
	strkey.VersionByteSignedPayload:    KindSignedPayload,
	strkey.VersionByteSeed:             KindSecretSeed,
}

// encodedLen is the unpadded base32 length of n raw bytes.
func encodedLen(n int) int { return (n*8 + 4) / 5 }

// candidate lengths per first character, longest first so that the longest
// valid signed payload wins over a shorter prefix of it.
var lengthsByFirst = func() [256][]int {
	var t [256][]int
	fixed := encodedLen(1 + 32 + 2) // 56
	for _, c := range "GSTXCL" {
		t[c] = []int{fixed}
	}
	t['B'] = []int{encodedLen(1 + 33 + 2)}       // 58
	t['M'] = []int{encodedLen(1 + 40 + 2)}       // 69
	t['W'] = []int{encodedLen(1 + 40 + 2)}       // 69
	for padded := 64; padded >= 4; padded -= 4 { // signed payload: key, length, padded payload
		t['P'] = append(t['P'], encodedLen(1+32+4+padded+2))
	}
	return t
}()

// MaxStrkeyLen is the length of the longest strkey (a signed payload with a
// 64 byte payload).
var MaxStrkeyLen = lengthsByFirst['P'][0]

func isBase32Fold(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')
}

func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

// Strkey reports the kind of s when s is exactly one valid canonical strkey
// (upper case, checksum and length verified).
func Strkey(s string) (Kind, bool) {
	if len(s) > MaxStrkeyLen {
		return 0, false
	}
	v, payload, err := strkey.DecodeAny(s)
	if err != nil {
		return 0, false
	}
	k, ok := versionKinds[v]
	// SEP-23 v1.4.0 rejects a claimable balance whose type byte is not 0
	// (v0); strkey v0.8.0 checks only the length, so the rule is here.
	if k == KindClaimableBalance && payload[0] != 0 {
		return 0, false
	}
	return k, ok
}

// matchAt returns the length and kind of a valid strkey that starts at
// run[0], where run consists of base32 characters of either case. its work
// is bounded per position: one candidate length per type, each checked with
// an allocation-free decode and checksum before the full decode.
func matchAt(run string) (int, Kind, bool) {
	if len(run) < 2 {
		return 0, 0, false
	}
	// the second character holds the low 3 bits of the version byte (the
	// algorithm, always 0 today) in its top 3 bits, so it is A, B, C or D.
	if c := upper(run[1]); c < 'A' || c > 'D' {
		return 0, 0, false
	}
	first := upper(run[0])
	lengths := lengthsByFirst[first]
	var one [1]int
	if first == 'P' {
		n, ok := signedPayloadLen(run)
		if !ok {
			return 0, 0, false
		}
		one[0] = n
		lengths = one[:]
	}
	var buf [256]byte
	for _, n := range lengths {
		if n > len(run) {
			continue
		}
		for i := 0; i < n; i++ {
			buf[i] = upper(run[i])
		}
		if !checksumOK(buf[:n]) {
			continue
		}
		if k, ok := Strkey(string(buf[:n])); ok {
			return n, k, true
		}
	}
	return 0, 0, false
}

// signedPayloadLen reads the payload length a signed payload candidate
// declares (bytes 33 to 36, after the version byte and the 32 byte key) and
// returns the one strkey length it allows. a strkey with another length
// fails strkey.DecodeAny anyway, so trying every length is wasted work.
func signedPayloadLen(run string) (int, bool) {
	const prefix = 60 // characters that hold the first 37 bytes
	if len(run) < prefix {
		return 0, false
	}
	var src [prefix]byte
	for i := range src {
		src[i] = upper(run[i])
	}
	var raw [38]byte
	n, ok := decodeBase32(raw[:], src[:])
	if !ok || n < 37 {
		return 0, false
	}
	declared := binary.BigEndian.Uint32(raw[33:37])
	if declared < 1 || declared > 64 {
		return 0, false
	}
	padded := int(declared+3) &^ 3
	return encodedLen(1 + 32 + 4 + padded + 2), true
}

// decodeBase32 decodes upper case unpadded base32 into dst and reports the
// byte count. leftover bits are ignored; strkey.DecodeAny checks them on the
// candidates that get that far.
func decodeBase32(dst, src []byte) (int, bool) {
	var acc uint32
	bits, n := 0, 0
	for _, c := range src {
		var v byte
		switch {
		case c >= 'A' && c <= 'Z':
			v = c - 'A'
		case c >= '2' && c <= '7':
			v = c - '2' + 26
		default:
			return 0, false
		}
		acc = acc<<5 | uint32(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			if n == len(dst) {
				return 0, false
			}
			dst[n] = byte(acc >> bits) //nolint:gosec // keeping the low 8 bits is the decode
			n++
		}
	}
	return n, true
}

// crcTable is crc16-xmodem (polynomial 0x1021, initial 0), the checksum
// SEP-23 appends little endian.
var crcTable = func() [256]uint16 {
	var t [256]uint16
	for i := range t {
		crc := uint16(i) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
		t[i] = crc
	}
	return t
}()

// checksumOK decodes an upper case candidate on the stack and compares its
// crc16 with the two bytes it ends in.
func checksumOK(cand []byte) bool {
	var raw [128]byte
	n, ok := decodeBase32(raw[:], cand)
	if !ok || n < 3 {
		return false
	}
	var crc uint16
	for _, b := range raw[:n-2] {
		crc = crc<<8 ^ crcTable[byte(crc>>8)^b]
	}
	return crc == binary.LittleEndian.Uint16(raw[n-2:n])
}

func redactStrkeys(s string) (string, Flags) {
	var b strings.Builder
	var flags Flags
	last := 0
	for i := 0; i < len(s); {
		if !isBase32Fold(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && isBase32Fold(s[j]) {
			j++
		}
		for k := i; k < j; {
			n, kind, ok := matchAt(s[k:j])
			if !ok {
				k++
				continue
			}
			if b.Len() == 0 {
				b.Grow(len(s))
			}
			b.WriteString(s[last:k])
			b.WriteString(kind.Placeholder())
			flags |= flagOf(kind)
			k += n
			last = k
		}
		i = j
	}
	if flags == 0 {
		return s, 0
	}
	b.WriteString(s[last:])
	return b.String(), flags
}

// domain is one or more dns labels and a top level domain of letters or an
// idna xn-- label. ascii only: idna hosts reach the gateway as punycode.
const domain = `(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+(?:xn--[A-Za-z0-9-]{1,59}|[A-Za-z]{2,63})`

var (
	federationRe = regexp.MustCompile(`[A-Za-z0-9._%+@-]+\*` + domain)
	emailRe      = regexp.MustCompile(`[A-Za-z0-9._%+-]+@` + domain)
)

func redactPattern(s string, re *regexp.Regexp, k Kind) (string, Flags) {
	if !re.MatchString(s) {
		return s, 0
	}
	return re.ReplaceAllLiteralString(s, k.Placeholder()), flagOf(k)
}

// String replaces every strkey, email address and federation address in s
// with its placeholder and reports which kinds it replaced. it never fails:
// input that is not valid utf-8 is processed byte by byte like any other.
func String(s string) (string, Flags) {
	out, f1 := redactStrkeys(s)
	out, f2 := redactPattern(out, federationRe, KindFederation)
	out, f3 := redactPattern(out, emailRe, KindEmail)
	return out, f1 | f2 | f3
}
