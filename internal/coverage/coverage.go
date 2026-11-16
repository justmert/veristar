// Package coverage estimates the share of a site's visits that are not
// measured because the visitor's browser sends a privacy signal. the tracker
// sends nothing when global privacy control is on, by design, so the gap
// cannot be counted from events. the edge counts it instead from the
// requests for the tracker file, which every page load that fetches the file
// makes with or without the signal: two anonymous counters per site and utc
// day, nothing else is kept (docs/compliance/privacy-strategy.md, "Measuring
// what is not measured").
package coverage

// EDGE_STATS outcomes of the family. script_requests counts every request
// for a tracker file the gateway attributed to the site, script_gpc the part
// of them that carried Sec-GPC: 1.
const (
	Prefix         = "script_"
	ScriptRequests = "script_requests"
	ScriptGPC      = "script_gpc"
)

// IsCounter reports whether outcome is one of the two counters.
func IsCounter(outcome string) bool { return outcome == ScriptRequests || outcome == ScriptGPC }

// MinRequests is the fewest attributed script requests a range needs before
// a share is shown. below it one visitor more or less moves the share by
// several points, so the estimate would mislead more than it informs.
const MinRequests = 100

// Share is the estimated unmeasured share of a range, nil when the range has
// fewer than MinRequests attributed script requests.
func Share(requests, gpc int64) *float64 {
	if requests < MinRequests || gpc < 0 || gpc > requests {
		return nil
	}
	v := float64(gpc) / float64(requests)
	return &v
}
