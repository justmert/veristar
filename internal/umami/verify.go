package umami

import (
	"context"
	"errors"
	"net/http"
)

// Verify calls POST /api/auth/verify (src/app/api/auth/verify/route.ts) with
// token and returns the user it belongs to. a token refused by umami, for
// example one issued before a password change, returns an error matching
// ErrUnauthorized. the umami ui makes the same call on every page load.
func (c *Client) Verify(ctx context.Context, token Token) (User, error) {
	if token.Empty() {
		return User{}, errors.New("umami verify: no token")
	}
	var out User
	if err := c.do(ctx, "verify", http.MethodPost, "/api/auth/verify", token, nil, &out); err != nil {
		return User{}, err
	}
	if out.ID == "" {
		return User{}, errors.New("umami verify: response has no user id")
	}
	return out, nil
}
