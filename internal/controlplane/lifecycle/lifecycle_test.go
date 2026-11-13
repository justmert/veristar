package lifecycle

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// the transition table of the reference docs 4.1, every status and action
func TestTransitions(t *testing.T) {
	want := map[string]map[Action]string{
		StatusPending:   {Approve: StatusActive, Reject: StatusRejected, Remove: StatusRemoved},
		StatusActive:    {Suspend: StatusSuspended, Remove: StatusRemoved},
		StatusRejected:  {Remove: StatusRemoved},
		StatusSuspended: {Resume: StatusActive, Remove: StatusRemoved},
		StatusRemoved:   {},
	}
	for status, allowed := range want {
		var names []string
		for _, a := range actionOrder {
			to, err := Next(status, a)
			if exp, ok := allowed[a]; ok {
				require.NoError(t, err, "%s from %s", a, status)
				require.Equal(t, exp, to)
				names = append(names, string(a))
				continue
			}
			e, ok := httpx.AsError(err)
			require.True(t, ok, "%s from %s", a, status)
			require.Equal(t, http.StatusConflict, e.Status)
			require.Equal(t, CodeInvalidTransition, e.Code)
		}
		if names == nil {
			names = []string{}
		}
		require.Equal(t, names, AllowedActions(status), status)
	}
}

func TestReason(t *testing.T) {
	s := func(v string) *string { return &v }
	_, err := checkReason(ReasonBody{}, true)
	require.Error(t, err)
	r, err := checkReason(ReasonBody{}, false)
	require.NoError(t, err)
	require.Nil(t, r)
	r, err = checkReason(ReasonBody{Reason: s("  spam\n traffic\t ")}, true)
	require.NoError(t, err)
	require.Equal(t, "spam\n traffic", *r)
	for _, bad := range []string{"", "   ", "a\x07b", strings.Repeat("x", 1001)} {
		_, err := checkReason(ReasonBody{Reason: s(bad)}, false)
		e, ok := httpx.AsError(err)
		require.True(t, ok, "%q", bad)
		require.Equal(t, http.StatusUnprocessableEntity, e.Status)
	}
	r, err = checkReason(ReasonBody{Reason: s(strings.Repeat("é", 1000))}, true)
	require.NoError(t, err)
	require.Len(t, []rune(*r), 1000)
}
