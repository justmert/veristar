package gateway

import (
	"errors"
	"net/http"

	"github.com/justmert/veristar/internal/events"
)

// Reason is why the gateway refused a request. its value is the outcome the
// stats publisher counts on EDGE_STATS and the jobs task stores in
// cp.web_ingest_hourly, so the strings never change once released.
type Reason string

// every reason the gateway counts. each is rejected_<reason>, the shape
// events.EncodeEdgeStats accepts.
const (
	ReasonBodyTooLarge     Reason = "rejected_body_too_large"
	ReasonMalformed        Reason = "rejected_malformed"
	ReasonMissingProof     Reason = "rejected_missing_proof"
	ReasonUnknownSite      Reason = "rejected_unknown_site"
	ReasonInactiveSite     Reason = "rejected_inactive_site"
	ReasonGPC              Reason = "rejected_gpc"
	ReasonOriginMissing    Reason = "rejected_origin_missing"
	ReasonOrigin           Reason = "rejected_origin"
	ReasonChallengeTag     Reason = "rejected_challenge_tag"
	ReasonChallengeExpired Reason = "rejected_challenge_expired"
	ReasonChallengeFuture  Reason = "rejected_challenge_future"
	ReasonProofWork        Reason = "rejected_proof_work"
	ReasonReplay           Reason = "rejected_replay"
	ReasonSignals          Reason = "rejected_signals"
	ReasonRateLimited      Reason = "rejected_rate_limited"
	ReasonUnavailable      Reason = "rejected_unavailable"
	// ReasonRevokedVersion: the event names a tracker release listed in
	// revoked.json, which the gateway also refuses to serve
	ReasonRevokedVersion Reason = "rejected_revoked_version"
)

// Reasons lists every reason, for metric label pre-registration and tests.
func Reasons() []Reason {
	return []Reason{
		ReasonBodyTooLarge, ReasonMalformed, ReasonMissingProof, ReasonUnknownSite, ReasonInactiveSite,
		ReasonGPC, ReasonOriginMissing, ReasonOrigin, ReasonChallengeTag, ReasonChallengeExpired,
		ReasonChallengeFuture, ReasonProofWork, ReasonReplay, ReasonSignals, ReasonRateLimited,
		ReasonUnavailable, ReasonRevokedVersion,
	}
}

// Status is the http status of the response for this reason. gpc is a drop, not an error, so the browser sees 204.
func (r Reason) Status() int {
	switch r {
	case ReasonBodyTooLarge:
		return http.StatusRequestEntityTooLarge
	case ReasonMalformed:
		return http.StatusBadRequest
	case ReasonUnknownSite, ReasonInactiveSite:
		return http.StatusNotFound
	case ReasonGPC:
		return http.StatusNoContent
	case ReasonRateLimited:
		return http.StatusTooManyRequests
	case ReasonUnavailable:
		return http.StatusServiceUnavailable
	case ReasonRevokedVersion:
		return http.StatusGone
	case ReasonMissingProof, ReasonOriginMissing, ReasonOrigin, ReasonChallengeTag, ReasonChallengeExpired,
		ReasonChallengeFuture, ReasonProofWork, ReasonReplay, ReasonSignals:
		return http.StatusForbidden
	}
	// an unlisted reason is a programming error; refusing is the safe answer
	return http.StatusForbidden
}

// RejectError carries the reason of a refusal and, for unavailability, the
// cause. it never holds request data, so it is safe to log.
type RejectError struct {
	Reason Reason
	Err    error
}

func (e *RejectError) Error() string {
	if e.Err != nil {
		return string(e.Reason) + ": " + e.Err.Error()
	}
	return string(e.Reason)
}

func (e *RejectError) Unwrap() error { return e.Err }

func reject(r Reason) error { return &RejectError{Reason: r} }

// ReasonOf returns the reason of a *RejectError anywhere in err's chain.
func ReasonOf(err error) (Reason, bool) {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason, true
	}
	return "", false
}

// ParseReason maps an error of events.ParseWire to its reason. any other
// error counts as malformed, because the parser is the only caller.
func ParseReason(err error) Reason {
	switch {
	case errors.Is(err, events.ErrBodyTooLarge):
		return ReasonBodyTooLarge
	case errors.Is(err, events.ErrMissingProof):
		return ReasonMissingProof
	default:
		return ReasonMalformed
	}
}
