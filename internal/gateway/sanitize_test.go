package gateway

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/redact"
)

const (
	sanG    = "GA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVSGZ"
	sanM    = "MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAAAJLK"
	sanC    = "CA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUWDA"
	sanSeed = "SBU2RRGLXH3E5CQHTD3ODLDF2BWDCYUSSBLLZ5GNW7JXHDIYKXZWHOKR"
	sanSite = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b"
)

// the sanitization case of the this stage verification list: a url with a
// G-address, a contract id, a secret seed and an email query parameter, a
// title with an M-address and event data with a G-address, parsed from a
// real wire body and checked against the WebEvent contract afterwards.
func TestSanitizeVerificationCase(t *testing.T) {
	body := map[string]any{
		"v": 1, "w": sanSite, "t": "ev", "h": "site.example",
		"u":  "/account/" + sanG + "/contract/" + sanC + "/import/" + sanSeed + "?email=jane%40example.com&utm_source=news&ref=" + sanG + "#/tab/" + sanG + "?x=1",
		"r":  "https://user:pw@Ref.Example/p/" + sanG + "?q=hidden&utm_medium=mail#frag",
		"ti": "Payment to " + sanM, "s": "1920x1080", "l": "en-US", "n": "send",
		"d":  map[string]any{"to": sanG, "amount": 5, "ok": true},
		"ts": 1794528001234,
		"p": map[string]any{"iat": 1794528000, "salt": "00112233445566778899aabbccddeeff", "bits": 8,
			"tag": strings.Repeat("ab", 32), "nonce": 87},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w, err := events.ParseWire(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rep := SanitizeWire(w)

	wantURL := "/account/{account}/contract/{contract}/import/{redacted-secret}?utm_source=news&ref=%7Baccount%7D#/tab/{account}"
	if w.URL != wantURL {
		t.Errorf("url = %q\nwant  %q", w.URL, wantURL)
	}
	if w.Referrer != "https://ref.example/p/{account}?utm_medium=mail" {
		t.Errorf("referrer = %q", w.Referrer)
	}
	if w.Title != "Payment to {muxed}" {
		t.Errorf("title = %q", w.Title)
	}
	if w.Language != "en" {
		t.Errorf("language = %q", w.Language)
	}
	if w.Data["to"] != "{account}" || w.Data["ok"] != true || w.Data["amount"] != json.Number("5") {
		t.Errorf("data = %#v", w.Data)
	}
	for _, k := range []redact.Kind{redact.KindAccount, redact.KindContract, redact.KindSecretSeed, redact.KindMuxedAccount} {
		if !rep.Redacted.Has(k) {
			t.Errorf("report misses %v: %b", k, rep.Redacted)
		}
	}
	if rep.DroppedQueryParams != 2 || rep.DroppedDataKeys != 0 {
		t.Errorf("report = %+v", rep)
	}
	everything := w.URL + w.Referrer + w.Title + w.Data["to"].(string)
	for _, secret := range []string{sanG, sanM, sanC, sanSeed, "jane", "example.com", "hidden", "q=", "user", "pw", "frag"} {
		if strings.Contains(everything, secret) {
			t.Errorf("%q survived: %s", secret, everything)
		}
	}

	id, err := events.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	ev := events.WebEvent{ID: id, SiteID: sanSite, WebsiteID: sanSite, Band: events.BandPrimary,
		VT: strings.Repeat("0a", 32), UA: "Mozilla/5.0", Type: w.Type, Hostname: w.Hostname, URL: w.URL,
		Referrer: w.Referrer, Title: w.Title, Screen: w.Screen, Language: w.Language, Name: w.Name,
		Data: w.Data, AcceptedAt: time.Now().UnixMilli()}
	if err := ev.Validate(); err != nil {
		t.Errorf("sanitized event breaks the WebEvent contract: %v", err)
	}
}

func TestSanitizeURL(t *testing.T) {
	cases := []struct {
		in, out string
		dropped int
	}{
		{"/", "/", 0},
		{"/a?b=1&utm_term=x%20y&utm_campaign=a+b", "/a?utm_term=x+y&utm_campaign=a+b", 1},
		{"/a?only=drop", "/a", 1},
		{"/a?ref", "/a?ref", 0},
		{"/a?ref=%zz", "/a", 1},
		{"/a?UTM_SOURCE=x", "/a", 1},
		{"/cb#access_token=abc", "/cb", 0},
		{"/cb#section", "/cb", 0},
		{"/cb#", "/cb", 0},
		{"/app#/users/7?tab=2", "/app#/users/7", 0},
		{"/u/jane%40example.com/x", "/u/{email}/x", 0},
		{"/u/jane%40example.com/%E2%82%AC%3F", "/u/{email}/%E2%82%AC%3F", 0},
		{"/caf%C3%A9/%zz", "/caf%C3%A9/%zz", 0},
		{"/k/%2F" + sanSeed, "/k/%2F{redacted-secret}", 0},
		{"/k/" + strings.ToLower(sanSeed), "/k/{redacted-secret}", 0},
		{"/pay/jed*stellar.org", "/pay/{federation}", 0},
	}
	for _, c := range cases {
		out, _, dropped := SanitizeURL(c.in)
		if out != c.out || dropped != c.dropped {
			t.Errorf("SanitizeURL(%q) = %q, %d; want %q, %d", c.in, out, dropped, c.out, c.dropped)
		}
	}
}

func TestSanitizeURLLength(t *testing.T) {
	// many short emails grow when redacted; the result must still fit.
	in := "/" + strings.Repeat("a@b.co/", 80)
	out, f, _ := SanitizeURL(in[:events.MaxURL])
	if !f.Has(redact.KindEmail) || len(out) > events.MaxURL || out[0] != '/' {
		t.Errorf("len %d flags %b", len(out), f)
	}
	// a cut must not leave half of a percent escape behind.
	in = "/" + strings.Repeat("x", events.MaxURL-3) + "%40" + "a@b.co"
	out, _, _ = SanitizeURL(in)
	if strings.HasSuffix(out, "%") || strings.HasSuffix(out, "%4") || len(out) > events.MaxURL {
		t.Errorf("cut escape: %q", out[len(out)-5:])
	}
}

func TestSanitizeReferrer(t *testing.T) {
	cases := []struct{ in, out string }{
		{"", ""},
		{"/same/" + sanG + "?utm_source=a&q=b", "/same/{account}?utm_source=a"},
		{"https://www.google.com/", "https://www.google.com/"},
		{"https://www.google.com/search?q=jane%40example.com", "https://www.google.com/search"},
		{"HTTPS://Me:Secret@Example.COM:8443/a#x", "https://example.com:8443/a"},
		{"android-app://com.google.android.gm/", "android-app://com.google.android.gm/"},
		{"mailto:jane@example.com", ""},
		{"//evil.example/x", ""},
		{"not a url", ""},
	}
	for _, c := range cases {
		if out, _, _ := SanitizeReferrer(c.in); out != c.out {
			t.Errorf("SanitizeReferrer(%q) = %q, want %q", c.in, out, c.out)
		}
	}
}

func TestSanitizeLanguage(t *testing.T) {
	cases := map[string]string{"en-US": "en", "pt_BR": "pt", "TR": "tr", "zh-Hant-TW": "zh", "": "", "x-klingon": "", "e1": "", "a": ""}
	for in, want := range cases {
		if got := SanitizeLanguage(in); got != want {
			t.Errorf("SanitizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeData(t *testing.T) {
	other := "GA3D5KRYM6CB7OWQ6TWYRR3Z4T7GNZLKERYNZGGA5SOAOPIFY6YQHES5"
	in := map[string]any{
		sanG:    "first",
		other:   "second",
		"email": "jane@example.com",
		"long":  strings.Repeat("a@b.co ", 80)[:events.MaxDataValue],
		"n":     json.Number("1.5"),
	}
	out, f, dropped := SanitizeData(in)
	if dropped != 1 || len(out) != 4 {
		t.Fatalf("dropped %d, out %#v", dropped, out)
	}
	// the original keys sort as GA3D... before GA7Q..., so "second" stays
	if out["{account}"] != "second" || out["email"] != "{email}" || out["n"] != json.Number("1.5") {
		t.Errorf("out = %#v", out)
	}
	if s := out["long"].(string); len(s) > events.MaxDataValue || !strings.HasPrefix(s, "{email} ") {
		t.Errorf("long = %d %q", len(s), s[:20])
	}
	if !f.Has(redact.KindAccount) || !f.Has(redact.KindEmail) {
		t.Errorf("flags %b", f)
	}
}

func TestSanitizeText(t *testing.T) {
	// cut on a character boundary counted in utf-16 units: the emoji takes
	// two units, so it does not fit in the last single unit.
	s, _ := SanitizeText(strings.Repeat("x", 9)+"😀", 10)
	if s != strings.Repeat("x", 9) {
		t.Errorf("got %q", s)
	}
	s, f := SanitizeText("Send to "+sanM+"\nnow", events.MaxTitle)
	if s != "Send to {muxed}\nnow" || !f.Has(redact.KindMuxedAccount) {
		t.Errorf("got %q %b", s, f)
	}
}

// property: whatever the url, the result starts with /, fits the limit,
// holds no secret seed even after one round of percent decoding, and is a
// fixed point of SanitizeURL.
func TestSanitizeURLProperties(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	parts := []string{"/", "?", "&", "=", "#", "#/", "%", "%2F", "%40", "%53", "@", "*", ".", "x", "utm_source=",
		"ref=", "example.com", sanSeed, strings.ToLower(sanSeed), sanG, "%2" + sanSeed[:10], sanSeed[10:]}
	for range 20000 {
		var b strings.Builder
		b.WriteByte('/')
		for range r.IntN(30) {
			b.WriteString(parts[r.IntN(len(parts))])
		}
		in := b.String()
		out, _, _ := SanitizeURL(in)
		dec, _ := sanPercentDecode(out)
		if out == "" || out[0] != '/' || len(out) > events.MaxURL {
			t.Fatalf("SanitizeURL(%q) = %q", in, out)
		}
		if _, f := redact.String(out); f.SecretSeed() {
			t.Fatalf("seed left in %q (from %q)", out, in)
		}
		if _, f := redact.String(dec); f.SecretSeed() {
			t.Fatalf("encoded seed left in %q (from %q)", out, in)
		}
		if again, _, _ := SanitizeURL(out); again != out {
			t.Fatalf("not a fixed point:\n in    %q\n once  %q\n twice %q", in, out, again)
		}
	}
}
