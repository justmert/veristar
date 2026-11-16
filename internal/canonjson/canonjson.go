// Package canonjson writes the canonical json form of a value: rfc 8785,
// the json canonicalization scheme (jcs), restricted to the values a
// signed usage report holds (docs/reference/usage-reports.md). objects
// have their members sorted by the utf-16 code units of their names, no
// whitespace is written, strings use the shortest escapes of rfc 8785
// section 3.2.2.2, and numbers are integers between -(2^53-1) and 2^53-1.
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
	"slices"
	"strconv"
	"strings"
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
// a duplicate member name, a number that is not a safe integer, invalid
// utf-8 and trailing data.
func Canonicalize(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("canonjson: the input is not valid utf-8")
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

// integer accepts a json number only when it is an integer written without
// a fraction or exponent and within the safe range.
func integer(x json.Number) (int64, error) {
	s := string(x)
	if strings.ContainsAny(s, ".eE") {
		return 0, fmt.Errorf("canonjson: %s is not an integer; the canonical form holds integers only", s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > MaxSafeInteger || n < -MaxSafeInteger {
		return 0, fmt.Errorf("canonjson: %s is outside the safe integer range", s)
	}
	return n, nil
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
