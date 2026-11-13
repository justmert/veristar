//go:build !oauthfixture

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/config"
)

// a production build ignores OAUTH_FIXTURE_URL: no key can swap the
// identity provider
func TestProductionEndpointsAreGitHub(t *testing.T) {
	require.False(t, FixtureBuild)
	e := config.FromMap(map[string]string{"OAUTH_FIXTURE_URL": "http://127.0.0.1:9999"})
	ep := LoadEndpoints(e)
	require.NoError(t, e.Err())
	require.Equal(t, "https://github.com/login/oauth/authorize", ep.OAuth.AuthURL)
	require.Equal(t, "https://github.com/login/oauth/access_token", ep.OAuth.TokenURL)
	require.Equal(t, "https://api.github.com", ep.APIBase)
}
