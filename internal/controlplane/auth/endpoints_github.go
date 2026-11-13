//go:build !oauthfixture

package auth

import (
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/justmert/veristar/internal/config"
)

// FixtureBuild is false in production builds: the identity provider is
// fixed in code and no configuration key can swap it.
const FixtureBuild = false

// LoadEndpoints returns github's own urls. it reads no key; the env
// parameter exists so the fixture build has the same signature.
func LoadEndpoints(_ *config.Env) Endpoints {
	ep := github.Endpoint
	// github documents client_id and client_secret as body parameters, so
	// the library does not need to probe the header style first
	ep.AuthStyle = oauth2.AuthStyleInParams
	return Endpoints{OAuth: ep, APIBase: "https://api.github.com"}
}
