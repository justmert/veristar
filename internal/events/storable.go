package events

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// what umami 3.5.0 can store, checked against the pinned image
// (packages/tracker/test/vectors/storable-v1.json). umami refuses some of it
// with 400, which the replayer moves to the dlq, and fails on the rest with
// 500 after it already inserted the event row. both lose the event, so the
// gateway refuses such events up front, as the tracker does.
const (
	// maxDataNumber: event_data.number_value is numeric(19,4), 15 integer
	// digits; umami answers 400 from 1e15 (src/app/api/send/request-schema.ts
	// MAX_DATA_NUMBER)
	maxDataNumber = 1e15
	// maxRevenue: umami answers 400 for revenue from 1e14 when a currency is
	// set (request-schema.ts MAX_REVENUE, isRevenueOutOfRange)
	maxRevenue = 1e14
)

// umamiDateRe is umami's DATETIME_REGEX (src/lib/constants.ts). a string
// value that matches it and parses as a date is written to the timestamptz
// column event_data.date_value, which refuses years before 1.
var umamiDateRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{3})?(Z|\+[0-9]{2}:[0-9]{2})?$`)

// formulaTrigger is umami's FORMULA_TRIGGER_RE for event names: a name that
// starts with one of these is answered 400 (spreadsheet formula injection).
func formulaTrigger(name string) bool {
	return name != "" && strings.ContainsRune("=+-@\t\r", rune(name[0]))
}

// checkStorable refuses event data that umami 3.5.0 cannot store. data
// values are string, json.Number or bool (parseData).
func checkStorable(d map[string]any) error {
	for _, v := range d {
		switch x := v.(type) {
		case json.Number:
			f, ok := parseNumber(string(x))
			if !ok || math.Abs(f) >= maxDataNumber {
				return malformed("event_data_number_out_of_range")
			}
		case string:
			if dateBeforeYearTwo(x) {
				return malformed("event_data_date_out_of_range")
			}
		}
	}
	if revenueOutOfRange(d) {
		return malformed("event_data_revenue_out_of_range")
	}
	return nil
}

// parseNumber reads a json number literal as javascript's JSON.parse does:
// to the nearest double, with an overflow giving infinity.
func parseNumber(lit string) (float64, bool) {
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		// ErrRange still returns the rounded value (infinity or zero)
		if !errors.Is(err, strconv.ErrRange) {
			return 0, false
		}
	}
	return f, true
}

// dateBeforeYearTwo reports a value in umami's date format dated 0000 or
// 0001-01-01. year 0 is refused by postgres. on 0001-01-01 a positive
// offset, or umami's own time zone for a value without one, can move the
// instant into year 0; the whole day is refused rather than modelling time
// zones, a value nobody needs.
func dateBeforeYearTwo(s string) bool {
	if !umamiDateRe.MatchString(s) {
		return false
	}
	return strings.HasPrefix(s, "0000-") || strings.HasPrefix(s, "0001-01-01T")
}

// revenueOutOfRange mirrors umami's isRevenueOutOfRange: revenue a number
// or a string, currency a string that is not blank after trimming, and
// Number(revenue) finite and at least maxRevenue.
func revenueOutOfRange(d map[string]any) bool {
	cur, ok := d["currency"].(string)
	if !ok || strings.TrimFunc(cur, jsWhitespace) == "" {
		return false
	}
	var rev float64
	switch x := d["revenue"].(type) {
	case json.Number:
		f, ok := parseNumber(string(x))
		if !ok {
			return false
		}
		rev = f
	case string:
		f, ok := jsStringToNumber(x)
		if !ok {
			return false
		}
		rev = f
	default:
		return false
	}
	return !math.IsInf(rev, 0) && !math.IsNaN(rev) && rev >= maxRevenue
}

// jsWhitespace is javascript's WhiteSpace and LineTerminator, what
// String.prototype.trim and Number(string) strip.
func jsWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u2028', '\u2029', '\ufeff':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

var (
	jsDecimalRe = regexp.MustCompile(`^[+-]?(Infinity|[0-9]+\.?[0-9]*([eE][+-]?[0-9]+)?|\.[0-9]+([eE][+-]?[0-9]+)?)$`)
	jsRadixRe   = regexp.MustCompile(`^0([xX][0-9a-fA-F]+|[oO][0-7]+|[bB][01]+)$`)
)

// jsStringToNumber is javascript's Number(string) (ecma-262
// StringToNumber): false for NaN. an empty or blank string is 0, numeric
// separators are not allowed, and 0x, 0o and 0b take no sign.
func jsStringToNumber(s string) (float64, bool) {
	s = strings.TrimFunc(s, jsWhitespace)
	switch {
	case s == "":
		return 0, true
	case jsRadixRe.MatchString(s):
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		n, ok := new(big.Int).SetString(s[2:], base)
		if !ok {
			return 0, false
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return f, true
	case !jsDecimalRe.MatchString(s):
		return 0, false
	case strings.HasSuffix(s, "Infinity"):
		if s[0] == '-' {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	}
	return parseNumber(s)
}
