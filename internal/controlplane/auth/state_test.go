package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSealedState(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	s, err := newSealer(key)
	require.NoError(t, err)
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	p := statePayload{State: "st", Verifier: "ver", ReturnTo: "/x", Exp: now.Add(stateCookieTTL).Unix()}
	v, err := s.seal(p)
	require.NoError(t, err)
	require.NotContains(t, v, "ver")

	got, err := s.open(v, now.Add(9*time.Minute))
	require.NoError(t, err)
	require.Equal(t, p, got)

	// github's code lives 10 minutes, and so does the cookie
	_, err = s.open(v, now.Add(stateCookieTTL))
	require.ErrorIs(t, err, errBadState)

	// another key cannot open it
	other, err := newSealer([]byte(strings.Repeat("j", 32)))
	require.NoError(t, err)
	_, err = other.open(v, now)
	require.ErrorIs(t, err, errBadState)

	for _, bad := range []string{"", "x", v[:len(v)-2], "!!!"} {
		_, err = s.open(bad, now)
		require.ErrorIs(t, err, errBadState, bad)
	}

	_, err = newSealer([]byte("short"))
	require.Error(t, err)
}

func TestCleanReturnTo(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "/",
		"/":                            "/",
		"/p?x=1#y":                     "/p?x=1#y",
		"//evil":                       "/",
		`/\evil`:                       "/",
		`/a\b`:                         "/",
		"/a\tb":                        "/",
		"http://x":                     "/",
		"/" + strings.Repeat("a", 511): "/" + strings.Repeat("a", 511),
		"/" + strings.Repeat("a", 512): "/",
	} {
		require.Equal(t, want, cleanReturnTo(in), in)
	}
}
