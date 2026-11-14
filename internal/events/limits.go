// Package events defines what travels on the web event streams: the wire
// format v1 the tracker sends, the WebEvent the gateway publishes to
// WEB_EVENTS, and the EdgeStat counters on EDGE_STATS.
package events

import (
	"unicode"
	"unicode/utf16"
)

// field limits of the wire format. lengths are in utf-16 code units, as
// javascript counts string length and the tracker cuts fields
// (docs/reference/wire-v1.md); the body size is in bytes.
const (
	MaxBody          = 8 << 10
	MaxURL           = 500
	MaxReferrer      = 500
	MaxTitle         = 500
	MaxHostname      = 100
	MaxScreen        = 11
	MaxLanguage      = 35
	MaxEventName     = 50
	MaxDataKeys      = 16
	MaxDataKey       = 50
	MaxDataValue     = 500
	MaxUserAgent     = 500
	TypePageview     = "pv"
	TypeEvent        = "ev"
	BandPrimary      = "primary"
	BandQuarantine   = "quarantine"
	wireVersion      = 1
	proofSaltBytes   = 16
	proofTagBytes    = 32
	maxSafeInteger   = 1<<53 - 1 // largest integer a javascript number holds exactly
	maxProofBits     = 64
	maxNumberLiteral = 32
)

func tooLong(s string, n int) bool {
	units := 0
	for _, r := range s {
		units += utf16.RuneLen(r)
		if units > n {
			return true
		}
	}
	return false
}

// hasControl reports control characters. identifiers (hostname, url, screen,
// language, names and keys) allow none.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// hasNUL reports a nul character. free text (title, referrer, data values)
// may contain tabs and newlines but never nul, which postgres text refuses.
func hasNUL(s string) bool {
	for _, r := range s {
		if r == 0 {
			return true
		}
	}
	return false
}
