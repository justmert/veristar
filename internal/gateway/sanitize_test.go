package gateway

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/redact"
)

const (
	sanG    = "GA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVSGZ"
	sanM    = "MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAAAJLK"
	sanC    = "CA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUWDA"
	sanSeed = "SBU2RRGLXH3E5CQHTD3ODLDF2BWDCYUSSBLLZ5GNW7JXHDIYKXZWHOKR"
	sanSite = "8f6b1c3e-2a4d-4e5f-9a7b-0c1d2e3f4a5b"
)

// the sanitization case: a url with a
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
	// a site that turned utm collection on; TestSanitizeUTMOffByDefault
	// covers the default
	rep := SanitizeWire(w, true)

	wantURL := "/account/{account}/contract/{contract}/import/{redacted-secret}?utm_source=news&ref=%7Baccount%7D#/tab/{account}"
	if w.URL != wantURL {
		t.Errorf("url = %q\nwant  %q", w.URL, wantURL)
	}
	// the referrer keeps scheme and host only (cnil host-only referrer)
	if w.Referrer != "https://ref.example" {
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
	// only the page url's email parameter counts; the referrer's query is
	// dropped whole
	if rep.DroppedQueryParams != 1 || rep.DroppedDataKeys != 0 {
		t.Errorf("report = %+v", rep)
	}
	everything := w.URL + w.Referrer + w.Title + w.Data["to"].(string)
	for _, secret := range []string{sanG, sanM, sanC, sanSeed, "jane", "example.com", "hidden", "q=", "user", "pw", "frag", "/p/", "utm_medium", "mail"} {
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

// a site that collects utm parameters; with collection off every query
// parameter is dropped (TestSanitizeUTMOffByDefault)
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
		out, _, dropped := SanitizeURL(c.in, true)
		if out != c.out || dropped != c.dropped {
			t.Errorf("SanitizeURL(%q) = %q, %d; want %q, %d", c.in, out, dropped, c.out, c.dropped)
		}
	}
}

// utm collection is a per-site setting that is off unless the site's owner
// turns it on (registry.Site.CollectUTM): then every query parameter,
// campaign ones included, is dropped and counted, while the path and a #/
// route are kept as before.
func TestSanitizeUTMOffByDefault(t *testing.T) {
	cases := []struct {
		in, out string
		dropped int
	}{
		{"/a?b=1&utm_term=x%20y&utm_campaign=a+b", "/a", 3},
		{"/a?utm_source=news&utm_medium=mail&utm_content=c&ref=x", "/a", 4},
		{"/a?ref", "/a", 1},
		{"/app/" + sanG + "?utm_source=x#/users/7?tab=2", "/app/{account}#/users/7", 1},
	}
	for _, c := range cases {
		out, _, dropped := SanitizeURL(c.in, false)
		if out != c.out || dropped != c.dropped {
			t.Errorf("SanitizeURL(%q, off) = %q, %d; want %q, %d", c.in, out, dropped, c.out, c.dropped)
		}
	}

	w := &events.Wire{URL: "/p?utm_source=news&ref=friend", Referrer: "https://news.example/x?utm_campaign=y"}
	rep := SanitizeWire(w, false)
	if w.URL != "/p" || w.Referrer != "https://news.example" || rep.DroppedQueryParams != 2 {
		t.Errorf("off: url %q referrer %q report %+v", w.URL, w.Referrer, rep)
	}
	w = &events.Wire{URL: "/p?utm_source=news&ref=friend&q=secret"}
	rep = SanitizeWire(w, true)
	if w.URL != "/p?utm_source=news&ref=friend" || rep.DroppedQueryParams != 1 {
		t.Errorf("on: url %q report %+v", w.URL, rep)
	}
}

func TestSanitizeURLLength(t *testing.T) {
	// many short emails grow when redacted; the result must still fit.
	in := "/" + strings.Repeat("a@b.co/", 80)
	out, f, _ := SanitizeURL(in[:events.MaxURL], false)
	if !f.Has(redact.KindEmail) || len(out) > events.MaxURL || out[0] != '/' {
		t.Errorf("len %d flags %b", len(out), f)
	}
	// a cut must not leave half of a percent escape behind.
	in = "/" + strings.Repeat("x", events.MaxURL-3) + "%40" + "a@b.co"
	out, _, _ = SanitizeURL(in, false)
	if strings.HasSuffix(out, "%") || strings.HasSuffix(out, "%4") || len(out) > events.MaxURL {
		t.Errorf("cut escape: %q", out[len(out)-5:])
	}
}

func TestSanitizeReferrer(t *testing.T) {
	cases := []struct{ in, out string }{
		{"", ""},
		// a same-origin path names a page of the site itself; it is not kept
		{"/same/" + sanG + "?utm_source=a&q=b", ""},
		{"/", ""},
		{"https://www.google.com/", "https://www.google.com"},
		{"https://www.google.com/search?q=jane%40example.com", "https://www.google.com"},
		{"HTTPS://Me:Secret@Example.COM:8443/a#x", "https://example.com"},
		{"https://news.example/story/42?utm_campaign=x#top", "https://news.example"},
		{"https://" + strings.ToLower(sanG) + ".example/x", "https://{account}.example"},
		{"http://[2001:DB8::1]:8080/path", "http://[2001:db8::1]"},
		{"http://192.0.2.7/a?b=c", "http://192.0.2.7"},
		{"android-app://com.google.android.gm/", "android-app://com.google.android.gm"},
		{"mailto:jane@example.com", ""},
		{"file:///home/jane/page.html", ""},
		{"//evil.example/x", ""},
		{"not a url", ""},
	}
	for _, c := range cases {
		out, _ := SanitizeReferrer(c.in)
		if out != c.out {
			t.Errorf("SanitizeReferrer(%q) = %q, want %q", c.in, out, c.out)
		}
		// whatever is kept is a bare origin: no path, query or fragment
		if out != "" {
			rest := out[strings.Index(out, "://")+3:]
			if strings.ContainsAny(rest, "/?#@") {
				t.Errorf("SanitizeReferrer(%q) = %q keeps more than the host", c.in, out)
			}
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
	rc := rand.New(rand.NewPCG(9, 10))
	parts := []string{"/", "?", "&", "=", "#", "#/", "%", "%2F", "%23", "%3F", "%40", "%53", "@", "*", ".", "x", strings.Repeat("y", 60), "utm_source=",
		"ref=", "example.com", sanSeed, strings.ToLower(sanSeed), sanG, "%2" + sanSeed[:10], sanSeed[10:]}
	for range 20000 {
		var b strings.Builder
		b.WriteByte('/')
		for range r.IntN(30) {
			b.WriteString(parts[r.IntN(len(parts))])
		}
		in := b.String()
		// both settings: the campaign parameters are the only query text
		// that can survive, and only with collection on. a second generator
		// picks the setting, so the urls are the same ones as before
		collect := rc.IntN(2) == 0
		out, _, _ := SanitizeURL(in, collect)
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
		if !collect && strings.Contains(out, "?") {
			t.Fatalf("query kept with utm collection off: %q (from %q)", out, in)
		}
		if again, _, _ := SanitizeURL(out, collect); again != out {
			t.Fatalf("not a fixed point:\n in    %q\n once  %q\n twice %q", in, out, again)
		}
	}
	// the url cap cutting a #/ route that holds %23, or a kept campaign
	// parameter, left a tail the next pass read differently
	for n := 470; n < 505; n++ {
		for _, tail := range []string{"#/a%23b", "%23#/x%23", "#/%23%23%23", "#/r#%23", "?utm_source=a%23b"} {
			in := "/" + strings.Repeat("a", n) + tail
			for _, collect := range []bool{false, true} {
				out, _, _ := SanitizeURL(in, collect)
				if again, _, _ := SanitizeURL(out, collect); again != out {
					t.Fatalf("not a fixed point at length %d:\n in    %q\n once  %q\n twice %q", n, in, out, again)
				}
			}
		}
	}
}
