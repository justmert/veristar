package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// user agents of the engines measured in this stage (browser-proof.md) and of
// engines that predate fetch metadata.
const (
	uaChromium   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.8078.4 Safari/537.36"
	uaFirefox    = "Mozilla/5.0 (X11; Linux x86_64; rv:157.0) Gecko/20100101 Firefox/157.0"
	uaWebKit     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.2 Safari/605.1.15"
	uaSafari163  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.3 Safari/605.1.15"
	uaIOS164     = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.4 Mobile/15E148 Safari/604.1"
	uaIOS15      = "Mozilla/5.0 (iPhone; CPU iPhone OS 15_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.6 Mobile/15E148 Safari/604.1"
	uaChrome79   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.130 Safari/537.36"
	uaFirefox89  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:89.0) Gecko/20100101 Firefox/89.0"
	uaWebView    = "Mozilla/5.0 (Linux; Android 14; Pixel 8; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/156.0.0.0 Mobile Safari/537.36"
	uaCriOS      = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/156.0.0.0 Mobile/15E148 Safari/604.1"
	uaEdgeLegacy = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.102 Safari/537.36 Edge/18.19041"
	uaCurl       = "curl/8.11.1"
)

func pfHeaders(ua, site, mode, dest string) http.Header {
	h := http.Header{}
	set := func(k, v string) {
		if v != "" {
			h.Set(k, v)
		}
	}
	set("User-Agent", ua)
	set("Sec-Fetch-Site", site)
	set("Sec-Fetch-Mode", mode)
	set("Sec-Fetch-Dest", dest)
	return h
}

func TestSignals(t *testing.T) {
	secure := NewSignalsChecker(true)
	plain := NewSignalsChecker(false)

	// what all three measured engines send
	for _, ua := range []string{uaChromium, uaFirefox, uaWebKit, uaIOS164, ""} {
		for _, site := range []string{"cross-site", "same-site"} {
			require.NoError(t, secure.Check(pfHeaders(ua, site, "cors", "empty")), ua)
		}
	}

	// engines that send no fetch metadata are never refused for that
	for _, ua := range []string{uaSafari163, uaIOS15, uaChrome79, uaFirefox89, uaWebView, uaCriOS, uaEdgeLegacy, uaCurl, ""} {
		require.NoError(t, secure.Check(pfHeaders(ua, "", "", "")), ua)
	}
	// chrome 76 to 79 sent site and mode without dest
	require.NoError(t, secure.Check(pfHeaders(uaChrome79, "cross-site", "cors", "")))

	// an engine known to send them, without them, over https
	for _, ua := range []string{uaChromium, uaFirefox, uaWebKit, uaIOS164} {
		require.Equal(t, ReasonSignals, pfReason(t, secure.Check(pfHeaders(ua, "", "", ""))), ua)
		require.Equal(t, ReasonSignals, pfReason(t, secure.Check(pfHeaders(ua, "cross-site", "cors", ""))), ua)
		// over plain http browsers send none, so absence proves nothing
		require.NoError(t, plain.Check(pfHeaders(ua, "", "", "")), ua)
	}

	// present values must be what the tracker's fetch produces, whatever the
	// user agent claims and whatever the transport
	for _, c := range []checker{secure, plain} {
		for _, h := range []http.Header{
			pfHeaders(uaChromium, "same-origin", "cors", "empty"),
			pfHeaders(uaChromium, "none", "cors", "empty"),
			pfHeaders(uaChromium, "cross-site", "no-cors", "empty"),
			pfHeaders(uaChromium, "cross-site", "navigate", "document"),
			pfHeaders(uaChromium, "cross-site", "cors", "script"),
			pfHeaders(uaCurl, "cross-site", "cors", "image"),
			pfHeaders(uaSafari163, "Cross-Site", "cors", "empty"),
		} {
			require.Equal(t, ReasonSignals, pfReason(t, c.Check(h)), "%v", h)
		}
		dup := pfHeaders(uaChromium, "cross-site", "cors", "empty")
		dup.Add("Sec-Fetch-Site", "cross-site")
		require.Equal(t, ReasonSignals, pfReason(t, c.Check(dup)))
	}
}

type checker interface{ Check(http.Header) error }

func TestSendsFetchMetadata(t *testing.T) {
	for ua, want := range map[string]bool{
		uaChromium: true, uaFirefox: true, uaWebKit: true, uaIOS164: true,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.0.0 Safari/537.36": true,
		"Mozilla/5.0 (Windows NT 10.0; rv:90.0) Gecko/20100101 Firefox/90.0":                                             true,
		uaSafari163: false, uaIOS15: false, uaChrome79: false, uaFirefox89: false, uaWebView: false,
		uaCriOS: false, uaEdgeLegacy: false, uaCurl: false, "": false,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17 Safari/605.1.15": true,
	} {
		require.Equal(t, want, sendsFetchMetadata(ua), ua)
	}
}
