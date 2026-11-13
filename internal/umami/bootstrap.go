package umami

import (
	"context"
	"errors"
	"fmt"
)

// the seed migration (prisma/migrations/01_init/migration.sql) creates this
// account with this password. both are public upstream defaults, not secrets.
const (
	SeededUsername = "admin"
	SeededPassword = "umami"
)

// Outcome says what Bootstrap did.
type Outcome string

const (
	// Rotated means the seeded password was replaced in this run.
	Rotated Outcome = "rotated"
	// AlreadySet means the configured password was already in place.
	AlreadySet Outcome = "already-set"
)

// ErrPasswordUnknown means neither the seeded nor the configured password is
// accepted. someone changed the admin password outside this tool.
var ErrPasswordUnknown = errors.New("umami bootstrap: admin accepts neither the seeded password nor UMAMI_ADMIN_PASSWORD; it was changed outside platformctl")

// Bootstrap replaces the seeded admin password with password and proves the
// seeded one no longer works. it is safe to run repeatedly.
//
// umami v3.5.0 flags the seeded admin with passwordChangeRequired, but the
// flag only drives a modal in the web ui (src/components/modals/PasswordChangeRequiredModal.tsx).
// parseRequest does not block the api (src/lib/request.ts and its test
// "does not block an account that still uses the default password"), so
// the seeded session can call /api/me/password directly.
func Bootstrap(ctx context.Context, c *Client, password string) (Outcome, error) {
	if password == SeededPassword {
		return "", errors.New("umami bootstrap: UMAMI_ADMIN_PASSWORD must differ from the seeded password")
	}
	if err := c.Heartbeat(ctx); err != nil {
		return "", err
	}

	outcome := AlreadySet
	seeded, err := c.Login(ctx, SeededUsername, SeededPassword)
	switch {
	case err == nil:
		if !seeded.User.IsAdmin {
			return "", fmt.Errorf("umami bootstrap: user %q is not an admin", SeededUsername)
		}
		if _, err := c.ChangeOwnPassword(ctx, seeded.Token, SeededPassword, password); err != nil {
			return "", err
		}
		outcome = Rotated
	case errors.Is(err, ErrUnauthorized):
		// the seeded password is already gone, which is the state we want
	default:
		return "", err
	}

	if err := verify(ctx, c, password); err != nil {
		return "", err
	}
	return outcome, nil
}

func verify(ctx context.Context, c *Client, password string) error {
	s, err := c.Login(ctx, SeededUsername, password)
	if errors.Is(err, ErrUnauthorized) {
		return ErrPasswordUnknown
	}
	if err != nil {
		return err
	}
	if !s.User.IsAdmin {
		return fmt.Errorf("umami bootstrap: user %q is not an admin", SeededUsername)
	}
	if s.User.PasswordChangeRequired {
		return errors.New("umami bootstrap: umami still reports the seeded password as in use")
	}
	_, err = c.Login(ctx, SeededUsername, SeededPassword)
	switch {
	case errors.Is(err, ErrUnauthorized):
		return nil
	case err == nil:
		return errors.New("umami bootstrap: the seeded password is still accepted after the change")
	default:
		return fmt.Errorf("umami bootstrap: checking that the seeded password is rejected: %w", err)
	}
}

// RotateAdmin replaces the admin password previous with next and proves
// previous is refused afterwards. it is safe to run repeatedly: when next
// is already in place and previous refused, it changes nothing. it is the
// credential rotation of the design and the re-keying of an environment
// that imported another environment's umami database.
func RotateAdmin(ctx context.Context, c *Client, previous, next string) (Outcome, error) {
	if previous == next {
		return "", errors.New("umami rotate admin: the new password equals the previous one")
	}
	if next == SeededPassword {
		return "", errors.New("umami rotate admin: the new password must differ from the seeded password")
	}
	if err := c.Heartbeat(ctx); err != nil {
		return "", err
	}
	outcome := AlreadySet
	old, err := c.Login(ctx, SeededUsername, previous)
	switch {
	case err == nil:
		if !old.User.IsAdmin {
			return "", fmt.Errorf("umami rotate admin: user %q is not an admin", SeededUsername)
		}
		if _, err := c.ChangeOwnPassword(ctx, old.Token, previous, next); err != nil {
			return "", err
		}
		outcome = Rotated
	case errors.Is(err, ErrUnauthorized):
		// already rotated by an earlier run, which next must prove below
	default:
		return "", err
	}
	s, err := c.Login(ctx, SeededUsername, next)
	if errors.Is(err, ErrUnauthorized) {
		return "", errors.New("umami rotate admin: the admin accepts neither the previous nor the new password")
	}
	if err != nil {
		return "", err
	}
	if !s.User.IsAdmin {
		return "", fmt.Errorf("umami rotate admin: user %q is not an admin", SeededUsername)
	}
	_, err = c.Login(ctx, SeededUsername, previous)
	switch {
	case errors.Is(err, ErrUnauthorized):
		return outcome, nil
	case err == nil:
		return "", errors.New("umami rotate admin: the previous password is still accepted after the change")
	default:
		return "", fmt.Errorf("umami rotate admin: checking that the previous password is refused: %w", err)
	}
}
