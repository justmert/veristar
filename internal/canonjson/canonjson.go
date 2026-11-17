// Package canonjson writes the canonical json form of a value: rfc 8785,
// the json canonicalization scheme (jcs), restricted to the values a
// signed usage report holds (docs/reference/usage-reports.md). objects
// have their members sorted by the utf-16 code units of their names, no
// whitespace is written, strings use the shortest escapes of rfc 8785
// section 3.2.2.2, and numbers are integers between -(2^53-1) and 2^53-1.
// a number counts by its value, as rfc 8785 reads it through an ieee 754
// double: 12345.0 and 1.2345e4 are both the integer 12345 and are written
// so, which is also what a browser's JSON.parse and every jcs library do.
//
// the integer restriction is what keeps the form exact: for an integer in
// that range every rfc 8785 implementation writes the same digits, so
// any jcs library in any language reproduces the signed bytes. fractions
// never appear in a report; ratios are carried as decimal strings.
package canonjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxSafeInteger is the largest integer every json implementation reads
// exactly (2^53-1, i-json, rfc 7493 section 2.2).
const MaxSafeInteger = 1<<53 - 1

// Marshal encodes v with encoding/json and returns its canonical form.
func Marshal(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Canonicalize(raw)
}

// Canonicalize reads one json text and returns its canonical form. it
// refuses what rfc 8785 cannot represent exactly or what i-json forbids:
// a duplicate member name, a number whose value is not a safe integer,
// invalid utf-8, a lone surrogate escape and trailing data.
func Canonicalize(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("canonjson: the input is not valid utf-8")
	}
	if err := checkSurrogates(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("canonjson: data after the json value")
	}
	var b bytes.Buffer
	if err := write(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// member is one name and value of an object, kept in input order until
// written.
type member struct {
	name  string
	value any
}

type object []member

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("canonjson: %w", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			var o object
			seen := map[string]bool{}
			for dec.More() {
				nt, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("canonjson: %w", err)
				}
				name, ok := nt.(string)
				if !ok {
					return nil, errors.New("canonjson: an object member name is not a string")
				}
				if seen[name] {
					return nil, fmt.Errorf("canonjson: duplicate member name %q", name)
				}
				seen[name] = true
				v, err := readValue(dec)
				if err != nil {
					return nil, err
				}
				o = append(o, member{name, v})
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("canonjson: %w", err)
			}
			if o == nil {
				o = object{}
			}
			return o, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := readValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, fmt.Errorf("canonjson: %w", err)
			}
			return a, nil
		}
		return nil, fmt.Errorf("canonjson: unexpected %v", t)
	default:
		return tok, nil
	}
}

func write(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		writeString(b, x)
	case json.Number:
		n, err := integer(x)
		if err != nil {
			return err
		}
		b.WriteString(strconv.FormatInt(n, 10))
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := write(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case object:
		sorted := slices.Clone(x)
		slices.SortFunc(sorted, func(a, c member) int { return compareUTF16(a.name, c.name) })
		b.WriteByte('{')
		for i, m := range sorted {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, m.name)
			b.WriteByte(':')
			if err := write(b, m.value); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("canonjson: unexpected value of type %T", v)
	}
	return nil
}

// integer accepts a json number whose value, read as an ieee 754 double
// the way rfc 8785 reads it, is an integer within the safe range.
func integer(x json.Number) (int64, error) {
	s := string(x)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, fmt.Errorf("canonjson: %s is outside the safe integer range", s)
	}
	if f != math.Trunc(f) {
		return 0, fmt.Errorf("canonjson: %s is not an integer; the canonical form holds integers only", s)
	}
	if f > MaxSafeInteger || f < -MaxSafeInteger {
		return 0, fmt.Errorf("canonjson: %s is outside the safe integer range", s)
	}
	return int64(f), nil
}

// checkSurrogates refuses a \u escape of a utf-16 surrogate that is not
// part of a pair. encoding/json would read it as u+fffd, a character the
// signer never wrote, so the canonical bytes would differ from what any
// other jcs implementation computes.
func checkSurrogates(raw []byte) error {
	hex4 := func(i int) (rune, bool) {
		if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
			return 0, false
		}
		v, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		return rune(v), err == nil
	}
	in := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case !in:
			in = c == '"'
		case c == '"':
			in = false
		case c == '\\' && i+1 < len(raw) && raw[i+1] == 'u':
			r, ok := hex4(i)
			if !ok {
				return errors.New("canonjson: a \\u escape needs four hex digits")
			}
			switch {
			case utf16.IsSurrogate(r) && r < 0xdc00:
				lo, ok := hex4(i + 6)
				if !ok || lo < 0xdc00 || lo > 0xdfff {
					return errors.New("canonjson: a string holds a lone surrogate escape")
				}
				i += 11
			case utf16.IsSurrogate(r):
				return errors.New("canonjson: a string holds a lone surrogate escape")
			default:
				i += 5
			}
		case c == '\\':
			i++
		}
	}
	return nil
}

// writeString writes s with the escapes of rfc 8785 section 3.2.2.2: the
// two-character escapes for \b \t \n \f \r " and \, \u00xx with lowercase
// hex for every other control character, and every other code point as
// its utf-8 bytes.
func writeString(b *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// compareUTF16 orders two names by their utf-16 code units, as rfc 8785
// section 3.2.3 requires. it differs from byte order only for code points
// above u+ffff against u+e000 to u+ffff.
func compareUTF16(a, c string) int {
	ua, uc := utf16.Encode([]rune(a)), utf16.Encode([]rune(c))
	return slices.Compare(ua, uc)
}
