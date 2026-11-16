package events_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/events"
)

const siteID = "0193a3f2-6a4e-7cc1-9b1a-3f2d8e9c1a20"

var (
	salt = "00112233445566778899aabbccddeeff"
	tag  = strings.Repeat("ab", 32)
)

// body builds a valid pageview and applies edits, so each case changes one
// thing about an otherwise valid event.
func body(t *testing.T, edit func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"v": 1, "w": siteID, "t": "pv", "h": "example.org", "u": "/docs?utm_source=x",
		"r": "https://ref.example/", "ti": "Docs", "s": "1920x1080", "l": "en-US", "ts": 1762950000000,
		"p": map[string]any{"iat": 1762950000, "salt": salt, "bits": 16, "tag": tag, "nonce": 12345},
	}
	if edit != nil {
		edit(m)
	}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

func TestParseValid(t *testing.T) {
	w, err := events.ParseWire(body(t, nil))
	require.NoError(t, err)
	require.Equal(t, siteID, w.SiteID)
	require.Equal(t, "/docs?utm_source=x", w.URL)
	require.Equal(t, uint64(12345), w.Proof.Nonce)
	require.Equal(t, 16, w.Proof.Bits)

	ev, err := events.ParseWire(body(t, func(m map[string]any) {
		m["t"] = "ev"
		m["n"] = "wallet_connect"
		m["d"] = map[string]any{"wallet": "freighter", "count": 2, "ok": true}
	}))
	require.NoError(t, err)
	require.Equal(t, "wallet_connect", ev.Name)
	require.Equal(t, json.Number("2"), ev.Data["count"])
	require.Equal(t, true, ev.Data["ok"])
}

// tracker 1.3.0 sends neither title, screen nor language by default, marks
// framed pages with fr and reports a page left unseen as an nv notice
func TestParseTracker13(t *testing.T) {
	w, err := events.ParseWire(body(t, func(m map[string]any) {
		delete(m, "ti")
		delete(m, "s")
		delete(m, "l")
		m["sv"] = "1.3.0"
		m["fr"] = true
	}))
	require.NoError(t, err)
	require.True(t, w.Framed)
	require.Empty(t, w.Title)
	require.Empty(t, w.Screen)
	require.Empty(t, w.Language)

	top, err := events.ParseWire(body(t, func(m map[string]any) { m["fr"] = false }))
	require.NoError(t, err)
	require.False(t, top.Framed)

	nv, err := events.ParseWire(body(t, func(m map[string]any) {
		m["t"] = "nv"
		for _, k := range []string{"u", "r", "ti", "s", "l"} {
			delete(m, k)
		}
	}))
	require.NoError(t, err)
	require.Equal(t, events.TypeNeverVisible, nv.Type)
	require.Empty(t, nv.URL)
	require.Equal(t, uint64(12345), nv.Proof.Nonce)

	// a release before 1.3.0 keeps sending all three fields
	old, err := events.ParseWire(body(t, nil))
	require.NoError(t, err)
	require.Equal(t, "1920x1080", old.Screen)
	require.Equal(t, "en-US", old.Language)
	require.Equal(t, "Docs", old.Title)
}

func TestParseRejects(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := map[string]struct {
		body []byte
		kind error
	}{
		"oversize body":       {append(body(t, nil), []byte(strings.Repeat(" ", events.MaxBody))...), events.ErrBodyTooLarge},
		"missing proof":       {body(t, func(m map[string]any) { delete(m, "p") }), events.ErrMissingProof},
		"not json":            {[]byte(`{"v":1,`), events.ErrMalformed},
		"array":               {[]byte(`[1]`), events.ErrMalformed},
		"trailing data":       {append(body(t, nil), []byte(`{}`)...), events.ErrMalformed},
		"invalid utf8":        {[]byte("{\"v\":1,\"h\":\"\xff\"}"), events.ErrMalformed},
		"duplicate key":       {[]byte(strings.Replace(string(body(t, nil)), `"v":1`, `"v":1,"v":1`, 1)), events.ErrMalformed},
		"unknown field":       {body(t, func(m map[string]any) { m["ip"] = "1.2.3.4" }), events.ErrMalformed},
		"null value":          {body(t, func(m map[string]any) { m["r"] = nil }), events.ErrMalformed},
		"version 2":           {body(t, func(m map[string]any) { m["v"] = 2 }), events.ErrMalformed},
		"version as string":   {body(t, func(m map[string]any) { m["v"] = "1" }), events.ErrMalformed},
		"site id not uuid":    {body(t, func(m map[string]any) { m["w"] = "abc" }), events.ErrMalformed},
		"site id uppercase":   {body(t, func(m map[string]any) { m["w"] = strings.ToUpper(siteID) }), events.ErrMalformed},
		"bad type":            {body(t, func(m map[string]any) { m["t"] = "xx" }), events.ErrMalformed},
		"url too long":        {body(t, func(m map[string]any) { m["u"] = "/" + long(events.MaxURL) }), events.ErrMalformed},
		"url not a path":      {body(t, func(m map[string]any) { m["u"] = "https://x/" }), events.ErrMalformed},
		"referrer too long":   {body(t, func(m map[string]any) { m["r"] = long(events.MaxReferrer + 1) }), events.ErrMalformed},
		"title too long":      {body(t, func(m map[string]any) { m["ti"] = long(events.MaxTitle + 1) }), events.ErrMalformed},
		"hostname too long":   {body(t, func(m map[string]any) { m["h"] = long(events.MaxHostname + 1) }), events.ErrMalformed},
		"hostname control":    {body(t, func(m map[string]any) { m["h"] = "a\nb" }), events.ErrMalformed},
		"screen too long":     {body(t, func(m map[string]any) { m["s"] = "123456x12345" }), events.ErrMalformed},
		"screen format":       {body(t, func(m map[string]any) { m["s"] = "big" }), events.ErrMalformed},
		"language too long":   {body(t, func(m map[string]any) { m["l"] = long(events.MaxLanguage + 1) }), events.ErrMalformed},
		"name on pageview":    {body(t, func(m map[string]any) { m["n"] = "x" }), events.ErrMalformed},
		"event without name":  {body(t, func(m map[string]any) { m["t"] = "ev" }), events.ErrMalformed},
		"event name too long": {body(t, func(m map[string]any) { m["t"] = "ev"; m["n"] = long(events.MaxEventName + 1) }), events.ErrMalformed},
		"data not flat":       {body(t, func(m map[string]any) { m["t"] = "ev"; m["n"] = "x"; m["d"] = map[string]any{"a": []int{1}} }), events.ErrMalformed},
		"data nested object":  {body(t, func(m map[string]any) { m["t"] = "ev"; m["n"] = "x"; m["d"] = map[string]any{"a": map[string]any{}} }), events.ErrMalformed},
		"data null value":     {body(t, func(m map[string]any) { m["t"] = "ev"; m["n"] = "x"; m["d"] = map[string]any{"a": nil} }), events.ErrMalformed},
		"data value too long": {body(t, func(m map[string]any) {
			m["t"] = "ev"
			m["n"] = "x"
			m["d"] = map[string]any{"a": long(events.MaxDataValue + 1)}
		}), events.ErrMalformed},
		"data key too long": {body(t, func(m map[string]any) {
			m["t"] = "ev"
			m["n"] = "x"
			m["d"] = map[string]any{long(events.MaxDataKey + 1): 1}
		}), events.ErrMalformed},
		"data too many keys":        {body(t, func(m map[string]any) { m["t"] = "ev"; m["n"] = "x"; m["d"] = manyKeys(events.MaxDataKeys + 1) }), events.ErrMalformed},
		"data not object":           {body(t, func(m map[string]any) { m["t"] = "ev"; m["n"] = "x"; m["d"] = "str" }), events.ErrMalformed},
		"proof salt short":          {body(t, func(m map[string]any) { proof(m)["salt"] = salt[:30] }), events.ErrMalformed},
		"proof salt long":           {body(t, func(m map[string]any) { proof(m)["salt"] = salt + "00" }), events.ErrMalformed},
		"proof salt uppercase":      {body(t, func(m map[string]any) { proof(m)["salt"] = strings.ToUpper(salt) }), events.ErrMalformed},
		"proof salt base64":         {body(t, func(m map[string]any) { proof(m)["salt"] = "ABEiM0RVZneImaq7zN3u_w" }), events.ErrMalformed},
		"proof tag not hex":         {body(t, func(m map[string]any) { proof(m)["tag"] = strings.Repeat("g", 64) }), events.ErrMalformed},
		"proof tag short":           {body(t, func(m map[string]any) { proof(m)["tag"] = tag[:63] }), events.ErrMalformed},
		"hostname ipv6 no brackets": {body(t, func(m map[string]any) { m["h"] = "::1" }), events.ErrMalformed},
		"hostname ipv4 in brackets": {body(t, func(m map[string]any) { m["h"] = "[127.0.0.1]" }), events.ErrMalformed},
		"hostname ipv6 with zone":   {body(t, func(m map[string]any) { m["h"] = "[fe80::1%eth0]" }), events.ErrMalformed},
		"hostname bad brackets":     {body(t, func(m map[string]any) { m["h"] = "[::1" }), events.ErrMalformed},
		"missing referrer":          {body(t, func(m map[string]any) { delete(m, "r") }), events.ErrMalformed},
		"framed not a boolean":      {body(t, func(m map[string]any) { m["fr"] = 1 }), events.ErrMalformed},
		"framed as a string":        {body(t, func(m map[string]any) { m["fr"] = "true" }), events.ErrMalformed},
		"notice with a url":         {body(t, func(m map[string]any) { m["t"] = "nv"; delete(m, "ti"); delete(m, "s"); delete(m, "l"); delete(m, "r") }), events.ErrMalformed},
		"notice with a referrer":    {body(t, func(m map[string]any) { m["t"] = "nv"; delete(m, "u"); delete(m, "ti"); delete(m, "s"); delete(m, "l") }), events.ErrMalformed},
		"notice with a title":       {body(t, func(m map[string]any) { m["t"] = "nv"; delete(m, "u"); delete(m, "r"); delete(m, "s"); delete(m, "l") }), events.ErrMalformed},
		"notice with event data": {body(t, func(m map[string]any) {
			m["t"] = "nv"
			delete(m, "u")
			delete(m, "r")
			delete(m, "ti")
			delete(m, "s")
			delete(m, "l")
			m["n"] = "x"
		}), events.ErrMalformed},
		"notice without proof": {body(t, func(m map[string]any) {
			m["t"] = "nv"
			delete(m, "u")
			delete(m, "r")
			delete(m, "ti")
			delete(m, "s")
			delete(m, "l")
			delete(m, "p")
		}), events.ErrMissingProof},
		"missing client ts":      {body(t, func(m map[string]any) { delete(m, "ts") }), events.ErrMalformed},
		"title over 500 utf-16":  {body(t, func(m map[string]any) { m["ti"] = strings.Repeat("\U0001F600", 251) }), events.ErrMalformed},
		"proof nonce negative":   {body(t, func(m map[string]any) { proof(m)["nonce"] = -1 }), events.ErrMalformed},
		"proof nonce fraction":   {body(t, func(m map[string]any) { proof(m)["nonce"] = 1.5 }), events.ErrMalformed},
		"proof nonce unsafe":     {body(t, func(m map[string]any) { proof(m)["nonce"] = uint64(1) << 60 }), events.ErrMalformed},
		"proof bits too high":    {body(t, func(m map[string]any) { proof(m)["bits"] = 65 }), events.ErrMalformed},
		"proof extra field":      {body(t, func(m map[string]any) { proof(m)["x"] = 1 }), events.ErrMalformed},
		"proof missing field":    {body(t, func(m map[string]any) { delete(proof(m), "tag") }), events.ErrMalformed},
		"proof not object":       {body(t, func(m map[string]any) { m["p"] = "x" }), events.ErrMalformed},
		"client ts not integer":  {body(t, func(m map[string]any) { m["ts"] = 1.25 }), events.ErrMalformed},
		"client ts leading zero": {[]byte(strings.Replace(string(body(t, nil)), `"ts":1762950000000`, `"ts":01`, 1)), events.ErrMalformed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := events.ParseWire(c.body)
			require.ErrorIs(t, err, c.kind)
			var pe *events.ParseError
			require.ErrorAs(t, err, &pe)
			require.Regexp(t, `^[a-z0-9_]+$`, pe.Reason)
		})
	}
}

func TestHostnamesAndUTF16Limits(t *testing.T) {
	for _, h := range []string{"[::1]", "[2001:db8::1]", "localhost", "127.0.0.1", "sub.example.org"} {
		_, err := events.ParseWire(body(t, func(m map[string]any) { m["h"] = h }))
		require.NoError(t, err, h)
	}
	// 250 emoji are 500 utf-16 code units, exactly the limit
	_, err := events.ParseWire(body(t, func(m map[string]any) { m["ti"] = strings.Repeat("\U0001F600", 250) }))
	require.NoError(t, err)
	// empty referrer, title and language are valid, as the tracker sends them
	_, err = events.ParseWire(body(t, func(m map[string]any) { m["r"], m["ti"], m["l"] = "", "", "" }))
	require.NoError(t, err)
}

func TestBodyLimitIsInclusive(t *testing.T) {
	b := body(t, nil)
	padded := append(b[:len(b)-1:len(b)-1], []byte(strings.Repeat(" ", events.MaxBody-len(b)))...)
	padded = append(padded, '}')
	require.Len(t, padded, events.MaxBody)
	_, err := events.ParseWire(padded)
	require.NoError(t, err)
}

func manyKeys(n int) map[string]any {
	m := map[string]any{}
	for i := range n {
		m[fmt.Sprintf("k%d", i)] = i
	}
	return m
}

func proof(m map[string]any) map[string]any { return m["p"].(map[string]any) }

func validEvent(t *testing.T) *events.WebEvent {
	id, err := events.NewEventID()
	require.NoError(t, err)
	return &events.WebEvent{
		ID: id, SiteID: siteID, WebsiteID: siteID, Band: events.BandPrimary,
		VT: strings.Repeat("ab", 32), UA: "Mozilla/5.0", Country: "DE", Type: events.TypeEvent,
		Hostname: "example.org", URL: "/", Name: "click",
		Data:       map[string]any{"n": json.Number("1.5"), "s": "x", "b": false},
		AcceptedAt: time.Now().UnixMilli(),
	}
}

func TestWebEventRoundTrip(t *testing.T) {
	e := validEvent(t)
	b, err := e.Marshal()
	require.NoError(t, err)
	got, err := events.DecodeWebEvent(b)
	require.NoError(t, err)
	require.Equal(t, e, got)
	require.Equal(t, "web.events."+siteID, got.Subject())
	require.NotContains(t, string(b), "ip")
}

func TestWebEventRejects(t *testing.T) {
	edits := map[string]func(e *events.WebEvent){
		"id not v7":             func(e *events.WebEvent) { e.ID = uuid.NewString() },
		"band unknown":          func(e *events.WebEvent) { e.Band = "other" },
		"primary wrong website": func(e *events.WebEvent) { e.WebsiteID = uuid.NewString() },
		"quarantine same site":  func(e *events.WebEvent) { e.Band = events.BandQuarantine },
		"vt short":              func(e *events.WebEvent) { e.VT = "ab" },
		"ua too long":           func(e *events.WebEvent) { e.UA = strings.Repeat("a", events.MaxUserAgent+1) },
		"country lowercase":     func(e *events.WebEvent) { e.Country = "de" },
		"no accepted_at":        func(e *events.WebEvent) { e.AcceptedAt = 0 },
		"pageview with name":    func(e *events.WebEvent) { e.Type = events.TypePageview },
		"data nested":           func(e *events.WebEvent) { e.Data = map[string]any{"a": []any{1}} },
		"title too long":        func(e *events.WebEvent) { e.Title = strings.Repeat("a", events.MaxTitle+1) },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			e := validEvent(t)
			edit(e)
			_, err := e.Marshal()
			require.Error(t, err)
		})
	}
	_, err := events.DecodeWebEvent([]byte(`{"id":"x","extra":1}`))
	require.Error(t, err)
}

func TestEdgeStats(t *testing.T) {
	seq := events.WindowSeq(time.Unix(1762950005, 0), 10*time.Second)
	require.Equal(t, uint64(176295000), seq)
	stats := []events.EdgeStat{
		{Node: "edge-1", WindowSeq: seq, SiteID: siteID, Outcome: "accepted", Count: 10},
		{Node: "edge-1", WindowSeq: seq, SiteID: siteID, Outcome: "rejected_origin", Count: 2},
	}
	subject, id, data, err := events.EncodeEdgeStats(stats)
	require.NoError(t, err)
	require.Equal(t, "stats.web.edge-1", subject)
	require.Equal(t, "edge-1.176295000", id)
	got, err := events.DecodeEdgeStats(data)
	require.NoError(t, err)
	require.Equal(t, stats, got)

	mixed := append([]events.EdgeStat(nil), stats...)
	mixed[1].WindowSeq++
	_, _, _, err = events.EncodeEdgeStats(mixed)
	require.Error(t, err)
	bad := append([]events.EdgeStat(nil), stats...)
	bad[0].Outcome = "Accepted"
	_, _, _, err = events.EncodeEdgeStats(bad)
	require.Error(t, err)
	bad[0].Outcome, bad[0].Node = "accepted", "edge.1"
	_, _, _, err = events.EncodeEdgeStats(bad)
	require.Error(t, err)
}

// FuzzParseWire checks that no input panics and that accepted input stays
// inside the limits.
func FuzzParseWire(f *testing.F) {
	f.Add([]byte(`{"v":1}`))
	f.Add([]byte(`{"v":1,"w":"` + siteID + `","t":"ev","h":"a","u":"/","n":"x","d":{"a":1},"p":{}}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		w, err := events.ParseWire(b)
		if err != nil {
			return
		}
		if len(b) > events.MaxBody || len(w.Data) > events.MaxDataKeys {
			t.Fatalf("limits not enforced for %q", b)
		}
	})
}
