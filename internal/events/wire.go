package events

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// error kinds of the wire parser. the gateway maps them to status codes:
// ErrBodyTooLarge to 413, ErrMissingProof to 403, ErrMalformed to 400.
var (
	ErrBodyTooLarge = errors.New("body too large")
	ErrMalformed    = errors.New("malformed event")
	ErrMissingProof = errors.New("missing proof")
)

// ParseError carries a kind and a short reason. Reason is a fixed snake_case
// token, never input data, so it can be a metric label and a stats outcome.
type ParseError struct {
	Kind   error
	Reason string
}

func (e *ParseError) Error() string { return e.Kind.Error() + ": " + e.Reason }
func (e *ParseError) Unwrap() error { return e.Kind }

func malformed(reason string) error { return &ParseError{Kind: ErrMalformed, Reason: reason} }

// Proof is the challenge the tracker echoes plus the nonce it found
//.
type Proof struct {
	IAT   int64
	Salt  [proofSaltBytes]byte
	Bits  int
	Tag   [proofTagBytes]byte
	Nonce uint64
}

// Wire is a parsed wire format v1 body.
type Wire struct {
	// SDKVersion is the tracker release that sent the event (x.y.z), empty
	// for releases before 1.2.0, which do not send it
	SDKVersion string
	SiteID     string
	Type       string
	Hostname   string
	URL        string
	Referrer   string
	Title      string
	Screen     string
	Language   string
	Name       string
	// Data holds string, json.Number and bool values only.
	Data map[string]any
	// ClientTS is informational only and never trusted.
	ClientTS int64
	Proof    Proof
}

var (
	hostnameRe = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	screenRe   = regexp.MustCompile(`^[0-9]{1,5}x[0-9]{1,5}$`)
	languageRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	// a plain non-negative decimal integer: no sign, fraction, exponent or
	// leading zero
	uintRe = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	// a release version as packages/tracker/releases names them
	sdkVersionRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
)

// lowerHex is how the tracker encodes the proof salt and tag. only lowercase
// is accepted, so every value has exactly one spelling.
var lowerHex = regexp.MustCompile(`^[0-9a-f]+$`)

// ParseWire parses and validates one wire format v1 body. it refuses
// unknown, duplicate and null fields, values of the wrong type, trailing
// data and anything over the limits.
func ParseWire(body []byte) (*Wire, error) {
	if len(body) > MaxBody {
		return nil, &ParseError{Kind: ErrBodyTooLarge, Reason: "body_too_large"}
	}
	if !utf8.Valid(body) {
		return nil, malformed("invalid_utf8")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	raw := map[string]json.RawMessage{}
	if err := readObject(dec, raw); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, malformed("trailing_data")
	}

	var w Wire
	known := map[string]bool{"v": true, "sv": true, "w": true, "t": true, "h": true, "u": true, "r": true, "ti": true,
		"s": true, "l": true, "n": true, "d": true, "ts": true, "p": true}
	for k := range raw {
		if !known[k] {
			return nil, malformed("unknown_field")
		}
	}
	v, err := uintField(raw, "v", true, maxSafeInteger)
	if err != nil {
		return nil, err
	}
	if v != wireVersion {
		return nil, malformed("unsupported_version")
	}
	if w.SDKVersion, err = stringField(raw, "sv", false, 20, true); err != nil {
		return nil, err
	}
	if _, ok := raw["sv"]; ok && !sdkVersionRe.MatchString(w.SDKVersion) {
		return nil, malformed("invalid_sdk_version")
	}
	if w.SiteID, err = stringField(raw, "w", true, 36, true); err != nil {
		return nil, err
	}
	if u, err := uuid.Parse(w.SiteID); err != nil || u.String() != w.SiteID {
		return nil, malformed("invalid_site_id")
	}
	if w.Type, err = stringField(raw, "t", true, 2, true); err != nil {
		return nil, err
	}
	if w.Type != TypePageview && w.Type != TypeEvent {
		return nil, malformed("invalid_type")
	}
	if w.Hostname, err = stringField(raw, "h", true, MaxHostname, true); err != nil {
		return nil, err
	}
	if !validHostname(w.Hostname) {
		return nil, malformed("invalid_hostname")
	}
	if w.URL, err = stringField(raw, "u", true, MaxURL, true); err != nil {
		return nil, err
	}
	if w.URL == "" || w.URL[0] != '/' {
		return nil, malformed("invalid_url")
	}
	if w.Referrer, err = stringField(raw, "r", true, MaxReferrer, false); err != nil {
		return nil, err
	}
	if w.Title, err = stringField(raw, "ti", true, MaxTitle, false); err != nil {
		return nil, err
	}
	if w.Screen, err = stringField(raw, "s", true, MaxScreen, true); err != nil {
		return nil, err
	}
	if w.Screen != "" && !screenRe.MatchString(w.Screen) {
		return nil, malformed("invalid_screen")
	}
	if w.Language, err = stringField(raw, "l", true, MaxLanguage, true); err != nil {
		return nil, err
	}
	if w.Language != "" && !languageRe.MatchString(w.Language) {
		return nil, malformed("invalid_language")
	}
	ts, err := uintField(raw, "ts", true, maxSafeInteger)
	if err != nil {
		return nil, err
	}
	w.ClientTS = int64(ts) //nolint:gosec // bounded by maxSafeInteger above

	_, hasName := raw["n"]
	_, hasData := raw["d"]
	switch w.Type {
	case TypeEvent:
		if w.Name, err = stringField(raw, "n", true, MaxEventName, true); err != nil {
			return nil, err
		}
		if w.Name == "" {
			return nil, malformed("missing_event_name")
		}
		if formulaTrigger(w.Name) {
			// umami answers 400 for these names, which would lose the event
			// in the dlq
			return nil, malformed("invalid_event_name")
		}
		if hasData {
			if w.Data, err = parseData(raw["d"]); err != nil {
				return nil, err
			}
			if err := checkStorable(w.Data); err != nil {
				return nil, err
			}
		}
	default:
		if hasName || hasData {
			return nil, malformed("event_fields_on_pageview")
		}
	}

	p, ok := raw["p"]
	if !ok {
		return nil, &ParseError{Kind: ErrMissingProof, Reason: "missing_proof"}
	}
	if w.Proof, err = parseProof(p); err != nil {
		return nil, err
	}
	return &w, nil
}

// validHostname accepts what location.hostname gives: a dns name or ipv4
// address, or an ipv6 literal in brackets such as [::1].
func validHostname(h string) bool {
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		a, err := netip.ParseAddr(h[1 : len(h)-1])
		return err == nil && a.Is6() && a.Zone() == ""
	}
	return hostnameRe.MatchString(h)
}

// readObject reads one json object into out, refusing duplicate keys and
// null values. encoding/json would silently keep the last of two duplicate
// keys, which lets two parsers disagree about the same body.
func readObject(dec *json.Decoder, out map[string]json.RawMessage) error {
	tok, err := dec.Token()
	if err != nil {
		return malformed("invalid_json")
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return malformed("not_an_object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return malformed("invalid_json")
		}
		key, ok := tok.(string)
		if !ok {
			return malformed("invalid_json")
		}
		if _, dup := out[key]; dup {
			return malformed("duplicate_field")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return malformed("invalid_json")
		}
		if bytes.Equal(v, []byte("null")) {
			return malformed("null_value")
		}
		out[key] = v
	}
	if _, err := dec.Token(); err != nil {
		return malformed("invalid_json")
	}
	return nil
}

func parseObject(b json.RawMessage) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	out := map[string]json.RawMessage{}
	if err := readObject(dec, out); err != nil {
		return nil, err
	}
	return out, nil
}

func stringField(raw map[string]json.RawMessage, key string, required bool, maxLen int, identifier bool) (string, error) {
	b, ok := raw[key]
	if !ok {
		if required {
			return "", malformed("missing_" + fieldName[key])
		}
		return "", nil
	}
	s, err := asString(b)
	if err != nil {
		return "", malformed("invalid_" + fieldName[key])
	}
	if tooLong(s, maxLen) {
		return "", malformed(fieldName[key] + "_too_long")
	}
	if (identifier && hasControl(s)) || hasNUL(s) {
		return "", malformed("invalid_" + fieldName[key])
	}
	return s, nil
}

var fieldName = map[string]string{
	"v": "version", "sv": "sdk_version", "w": "site_id", "t": "type", "h": "hostname", "u": "url", "r": "referrer",
	"ti": "title", "s": "screen", "l": "language", "n": "event_name", "ts": "client_ts",
}

func asString(b json.RawMessage) (string, error) {
	if len(b) == 0 || b[0] != '"' {
		return "", errors.New("not a string")
	}
	var s string
	err := json.Unmarshal(b, &s)
	return s, err
}

func asUint(b json.RawMessage, maxVal uint64) (uint64, bool) {
	if !uintRe.Match(b) || len(b) > maxNumberLiteral {
		return 0, false
	}
	n, err := strconv.ParseUint(string(b), 10, 64)
	if err != nil || n > maxVal {
		return 0, false
	}
	return n, true
}

func uintField(raw map[string]json.RawMessage, key string, required bool, maxVal uint64) (uint64, error) {
	b, ok := raw[key]
	if !ok {
		if required {
			return 0, malformed("missing_" + fieldName[key])
		}
		return 0, nil
	}
	n, ok := asUint(b, maxVal)
	if !ok {
		return 0, malformed("invalid_" + fieldName[key])
	}
	return n, nil
}

// parseData checks event data: a flat object of at most MaxDataKeys keys,
// keys up to MaxDataKey characters, values a string up to MaxDataValue
// characters, a number or a boolean.
func parseData(b json.RawMessage) (map[string]any, error) {
	if len(b) == 0 || b[0] != '{' {
		return nil, malformed("invalid_event_data")
	}
	obj, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	if len(obj) > MaxDataKeys {
		return nil, malformed("event_data_too_many_keys")
	}
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		if k == "" || tooLong(k, MaxDataKey) || hasControl(k) {
			return nil, malformed("invalid_event_data_key")
		}
		val, err := scalar(v)
		if err != nil {
			return nil, err
		}
		out[k] = val
	}
	return out, nil
}

func scalar(v json.RawMessage) (any, error) {
	switch {
	case len(v) == 0:
		return nil, malformed("invalid_event_data_value")
	case v[0] == '"':
		s, err := asString(v)
		if err != nil || hasNUL(s) {
			return nil, malformed("invalid_event_data_value")
		}
		if tooLong(s, MaxDataValue) {
			return nil, malformed("event_data_value_too_long")
		}
		return s, nil
	case bytes.Equal(v, []byte("true")):
		return true, nil
	case bytes.Equal(v, []byte("false")):
		return false, nil
	case v[0] == '-' || (v[0] >= '0' && v[0] <= '9'):
		if len(v) > maxNumberLiteral {
			return nil, malformed("event_data_value_too_long")
		}
		var n json.Number
		if err := json.Unmarshal(v, &n); err != nil {
			return nil, malformed("invalid_event_data_value")
		}
		return n, nil
	default:
		// arrays and objects: event data is flat
		return nil, malformed("event_data_not_flat")
	}
}

func parseProof(b json.RawMessage) (Proof, error) {
	var p Proof
	if len(b) == 0 || b[0] != '{' {
		return p, malformed("invalid_proof")
	}
	obj, err := parseObject(b)
	if err != nil {
		return p, err
	}
	if len(obj) != 5 {
		return p, malformed("invalid_proof")
	}
	iat, ok := asUint(obj["iat"], maxSafeInteger)
	if !ok || iat == 0 {
		return p, malformed("invalid_proof")
	}
	p.IAT = int64(iat) //nolint:gosec // bounded by maxSafeInteger
	bits, ok := asUint(obj["bits"], maxProofBits)
	if !ok {
		return p, malformed("invalid_proof")
	}
	p.Bits = int(bits) //nolint:gosec // bounded by maxProofBits
	if p.Nonce, ok = asUint(obj["nonce"], maxSafeInteger); !ok {
		return p, malformed("invalid_proof")
	}
	if err := decodeFixed(obj["salt"], p.Salt[:]); err != nil {
		return p, err
	}
	if err := decodeFixed(obj["tag"], p.Tag[:]); err != nil {
		return p, err
	}
	return p, nil
}

// decodeFixed decodes exactly 2*len(dst) lowercase hex characters.
func decodeFixed(b json.RawMessage, dst []byte) error {
	s, err := asString(b)
	if err != nil || len(s) != hex.EncodedLen(len(dst)) || !lowerHex.MatchString(s) {
		return malformed("invalid_proof")
	}
	if _, err := hex.Decode(dst, []byte(s)); err != nil {
		return malformed("invalid_proof")
	}
	return nil
}

// String keeps proof bytes out of formatted output.
func (p Proof) String() string { return fmt.Sprintf("proof{iat:%d bits:%d}", p.IAT, p.Bits) }
