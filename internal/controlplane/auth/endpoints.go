package auth

import "golang.org/x/oauth2"

// Endpoints are the github urls the sign-in flow talks to.
type Endpoints struct {
	// OAuth holds the authorize and token urls.
	OAuth oauth2.Endpoint
	// APIBase is the rest api base; the user is read from APIBase + "/user".
	APIBase string
}
