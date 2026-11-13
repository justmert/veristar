//go:build oauthfixture

package auth

import (
	"strings"

	"golang.org/x/oauth2"

	"github.com/justmert/veristar/internal/config"
)

// FixtureBuild is true only in binaries built with the oauthfixture tag,
// which integration tests use to sign in against tests/fixtures/githuboauth.
// production binaries are built without the tag.
const FixtureBuild = true

// LoadEndpoints reads OAUTH_FIXTURE_URL, the base of a github-compatible
// fixture that serves github's paths: /login/oauth/authorize,
// /login/oauth/access_token and /user.
func LoadEndpoints(e *config.Env) Endpoints {
	u := e.URL("OAUTH_FIXTURE_URL")
	if u == nil {
		return Endpoints{}
	}
	base := strings.TrimSuffix(u.String(), "/")
	return Endpoints{
		OAuth: oauth2.Endpoint{
			AuthURL:   base + "/login/oauth/authorize",
			TokenURL:  base + "/login/oauth/access_token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		APIBase: base,
	}
}
