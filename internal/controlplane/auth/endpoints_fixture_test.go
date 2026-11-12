//go:build oauthfixture

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/config"
)

func TestFixtureEndpoints(t *testing.T) {
	require.True(t, FixtureBuild)
	e := config.FromMap(map[string]string{"OAUTH_FIXTURE_URL": "http://127.0.0.1:9999/"})
	ep := LoadEndpoints(e)
	require.NoError(t, e.Err())
	require.Equal(t, "http://127.0.0.1:9999/login/oauth/authorize", ep.OAuth.AuthURL)
	require.Equal(t, "http://127.0.0.1:9999/login/oauth/access_token", ep.OAuth.TokenURL)
	require.Equal(t, "http://127.0.0.1:9999", ep.APIBase)

	// the fixture build requires the key instead of falling back to github
	missing := config.FromMap(map[string]string{})
	LoadEndpoints(missing)
	require.ErrorContains(t, missing.Err(), "OAUTH_FIXTURE_URL")
}
