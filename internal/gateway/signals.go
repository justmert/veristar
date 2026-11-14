package gateway

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// fetch metadata the tracker's requests carry in every engine measured in
// docs/reference/browser-proof.md (, request behaviour): a fetch
// from a tenant page to in.<BASE_DOMAIN> is cross-site, or same-site when
// the tenant shares the base domain, in cors mode with an empty destination.
var (
	allowedSite = map[string]bool{"cross-site": true, "same-site": true}
	allowedMode = map[string]bool{"cors": true}
	allowedDest = map[string]bool{"empty": true}
)

// first versions that send Sec-Fetch-Site, -Mode and -Dest together:
// chromium 80, firefox 90, safari 16.4.
const (
	chromiumSince = 80
	firefoxSince  = 90
	safariMajor   = 16
	safariMinor   = 4
)

var (
	chromeRe  = regexp.MustCompile(`\bChrome/(\d+)\.`)
	firefoxRe = regexp.MustCompile(`\bFirefox/(\d+)\.`)
	versionRe = regexp.MustCompile(`\bVersion/(\d+)(?:\.(\d+))?`)
	// tokens of engines or shells whose fetch metadata was not measured:
	// android webviews, ios browsers that are not safari, and edgehtml
	unmeasuredRe = regexp.MustCompile(`; wv\)|CriOS/|FxiOS/|EdgiOS/|OPiOS/|Edge/`)
)

// SignalsChecker checks Sec-Fetch-* consistency.
// a header that is present must carry the value the tracker's fetch
// produces. missing headers are refused only when the user agent claims an
// engine version known to send them and the gateway is reached over a
// secure transport, because browsers send fetch metadata only to
// potentially trustworthy urls. older engines, such as safari before 16.4,
// are never refused for not sending them.
type SignalsChecker struct {
	secure bool
}

// NewSignalsChecker takes whether browsers reach the gateway over https (or
// on a loopback host). behind plain http on another host no browser sends
// fetch metadata, so its absence then says nothing.
func NewSignalsChecker(secureTransport bool) *SignalsChecker {
	return &SignalsChecker{secure: secureTransport}
}

// Check reads Sec-Fetch-Site, Sec-Fetch-Mode, Sec-Fetch-Dest and
// User-Agent. it applies to both GET /v1/c and POST /v1/e, which every
// measured engine sends with the same metadata.
func (c *SignalsChecker) Check(h http.Header) error {
	present := 0
	for _, f := range []struct {
		name    string
		allowed map[string]bool
	}{
		{"Sec-Fetch-Site", allowedSite},
		{"Sec-Fetch-Mode", allowedMode},
		{"Sec-Fetch-Dest", allowedDest},
	} {
		vs := h.Values(f.name)
		switch {
		case len(vs) == 0:
			continue
		case len(vs) > 1 || !f.allowed[vs[0]]:
			return reject(ReasonSignals)
		}
		present++
	}
	if present < 3 && c.secure && sendsFetchMetadata(h.Get("User-Agent")) {
		return reject(ReasonSignals)
	}
	return nil
}

// sendsFetchMetadata reports whether a user agent claims an engine version
// that sends all three headers. anything it cannot place counts as no, so
// an unusual browser is never refused for missing headers.
func sendsFetchMetadata(ua string) bool {
	if ua == "" || unmeasuredRe.MatchString(ua) {
		return false
	}
	if m := chromeRe.FindStringSubmatch(ua); m != nil {
		return atLeast(m[1], "0", chromiumSince, 0)
	}
	if m := firefoxRe.FindStringSubmatch(ua); m != nil {
		return atLeast(m[1], "0", firefoxSince, 0)
	}
	// safari: Version/ carries the safari version, which on ios and ipados
	// equals the os version that ships the engine
	if strings.Contains(ua, "Safari/") && strings.Contains(ua, "AppleWebKit/") {
		if m := versionRe.FindStringSubmatch(ua); m != nil {
			return atLeast(m[1], m[2], safariMajor, safariMinor)
		}
	}
	return false
}

func atLeast(major, minor string, wantMajor, wantMinor int) bool {
	ma, err := strconv.Atoi(major)
	if err != nil {
		return false
	}
	mi := 0
	if minor != "" {
		if mi, err = strconv.Atoi(minor); err != nil {
			return false
		}
	}
	return ma > wantMajor || (ma == wantMajor && mi >= wantMinor)
}
