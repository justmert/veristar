package integrity

import (
	"strconv"
	"strings"
)

// page signals of tracker 1.3.0 on. the tracker reports when a page runs
// inside a frame (fr on every event) and when a page was hidden from load
// until it was left (an nv notice). both are claims of the page, so the
// rules built on them can only lower a grade.
//
// EDGE_STATS outcomes of the family, all counted per site and utc day:
// signal_views counts accepted page views of trackers that report signals,
// which is the base the framed share is taken of; signal_framed counts the
// framed part of them; signal_never_visible counts accepted notices.
const (
	SignalPrefix       = "signal_"
	SignalViews        = "signal_views"
	SignalFramed       = "signal_framed"
	SignalNeverVisible = "signal_never_visible"
)

// Signals lists the signal outcomes in display order.
var Signals = []string{SignalViews, SignalFramed, SignalNeverVisible}

// IsSignal reports whether outcome is one of Signals.
func IsSignal(outcome string) bool {
	switch outcome {
	case SignalViews, SignalFramed, SignalNeverVisible:
		return true
	}
	return false
}

// SignalsSince is the first tracker release that reports page signals.
const SignalsSince = "1.3.0"

// ReportsSignals reports whether a tracker release (the wire field sv, x.y.z)
// sends the frame flag and never-visible notices. an empty version is a
// release before 1.2.0, which names no version and reports no signal.
func ReportsSignals(sdkVersion string) bool {
	return versionAtLeast(sdkVersion, SignalsSince)
}

func versionAtLeast(v, floor string) bool {
	pv, ok := parseVersion(v)
	if !ok {
		return false
	}
	pm, _ := parseVersion(floor)
	for i := range 3 {
		if pv[i] != pm[i] {
			return pv[i] > pm[i]
		}
	}
	return true
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
