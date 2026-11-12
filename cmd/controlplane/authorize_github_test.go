//go:build !oauthfixture

package main

// a production build sends sign-in to github whatever OAUTH_FIXTURE_URL says
const authorizePrefix = "https://github.com/login/oauth/authorize?"
