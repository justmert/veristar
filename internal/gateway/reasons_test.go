package gateway

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/events"
)

// every reason is an outcome EDGE_STATS accepts, and appears once.
func TestReasonsAreOutcomes(t *testing.T) {
	seen := map[Reason]bool{}
	site := uuid.NewString()
	for _, r := range Reasons() {
		require.False(t, seen[r], r)
		seen[r] = true
		require.True(t, strings.HasPrefix(string(r), "rejected_"), r)
		_, _, _, err := events.EncodeEdgeStats([]events.EdgeStat{{Node: "n1", WindowSeq: 1, SiteID: site, Outcome: string(r), Count: 1}})
		require.NoError(t, err, r)
	}
}

func TestReasonStatus(t *testing.T) {
	want := map[Reason]int{
		ReasonBodyTooLarge: http.StatusRequestEntityTooLarge, ReasonMalformed: http.StatusBadRequest,
		ReasonMissingProof: http.StatusForbidden, ReasonUnknownSite: http.StatusNotFound,
		ReasonInactiveSite: http.StatusNotFound, ReasonGPC: http.StatusNoContent,
		ReasonOriginMissing: http.StatusForbidden, ReasonOrigin: http.StatusForbidden,
		ReasonChallengeTag: http.StatusForbidden, ReasonChallengeExpired: http.StatusForbidden,
		ReasonChallengeFuture: http.StatusForbidden, ReasonProofWork: http.StatusForbidden,
		ReasonReplay: http.StatusForbidden, ReasonSignals: http.StatusForbidden,
		ReasonRateLimited: http.StatusTooManyRequests, ReasonUnavailable: http.StatusServiceUnavailable,
	}
	require.Len(t, want, len(Reasons()))
	for _, r := range Reasons() {
		require.Equal(t, want[r], r.Status(), r)
	}
}

func TestParseReason(t *testing.T) {
	_, err := events.ParseWire(make([]byte, events.MaxBody+1))
	require.Equal(t, ReasonBodyTooLarge, ParseReason(err))
	_, err = events.ParseWire([]byte(`{"v":1,"w":"` + uuid.NewString() + `","t":"pv","h":"a.example","u":"/","r":"","ti":"","s":"","l":"","ts":1}`))
	require.Equal(t, ReasonMissingProof, ParseReason(err))
	_, err = events.ParseWire([]byte(`{"v":2}`))
	require.Equal(t, ReasonMalformed, ParseReason(err))

	cause := errors.New("bucket down")
	wrapped := errors.Join(errors.New("context"), &RejectError{Reason: ReasonUnavailable, Err: cause})
	r, ok := ReasonOf(wrapped)
	require.True(t, ok)
	require.Equal(t, ReasonUnavailable, r)
	require.ErrorIs(t, wrapped, cause)
	_, ok = ReasonOf(cause)
	require.False(t, ok)
	require.Equal(t, "rejected_replay", reject(ReasonReplay).Error())
}
