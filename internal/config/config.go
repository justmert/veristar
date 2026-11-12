// Package config loads typed settings from environment variables.
//
// every accessor records a problem instead of returning early, so a service
// that starts with several keys missing reports all of them in one error and
// exits before doing any work.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// KeyError describes one environment key that is missing or invalid.
type KeyError struct {
	Key    string
	Reason string
}

func (e *KeyError) Error() string {
	return fmt.Sprintf("config: %s %s", e.Key, e.Reason)
}

// Env reads keys from a lookup function and collects every problem it finds.
type Env struct {
	lookup func(string) (string, bool)
	errs   []error
}

// FromEnviron reads the process environment.
func FromEnviron() *Env {
	return &Env{lookup: os.LookupEnv}
}

// FromMap reads a fixed set of values. tests and tools that load an env file
// use it so they never mutate the process environment.
func FromMap(values map[string]string) *Env {
	return &Env{lookup: func(k string) (string, bool) {
		v, ok := values[k]
		return v, ok
	}}
}

func (e *Env) fail(key, reason string) {
	e.errs = append(e.errs, &KeyError{Key: key, Reason: reason})
}

// raw returns the trimmed value. an empty value counts as missing, because
// compose files and shells turn unset interpolations into empty strings.
func (e *Env) raw(key string) (string, bool) {
	v, ok := e.lookup(key)
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}

// String returns a required, non-empty value.
func (e *Env) String(key string) string {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
	}
	return v
}

// Secret returns a required value of at least minLen bytes wrapped so that it
// cannot end up in logs or formatted errors by accident.
func (e *Env) Secret(key string, minLen int) Secret {
	v, ok := e.raw(key)
	switch {
	case !ok:
		e.fail(key, "is required but not set")
	case len(v) < minLen:
		e.fail(key, fmt.Sprintf("must be at least %d characters", minLen))
	}
	return Secret{v: v}
}

// URL returns a required absolute http or https url.
func (e *Env) URL(key string) *url.URL {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		e.fail(key, "must be an absolute http or https url")
		return nil
	}
	return u
}

// Duration returns a required value in time.ParseDuration syntax.
func (e *Env) Duration(key string) time.Duration {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		e.fail(key, "must be a positive duration such as 30s")
		return 0
	}
	return d
}

// Int returns a required integer within [lo, hi].
func (e *Env) Int(key string, lo, hi int) int {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		e.fail(key, fmt.Sprintf("must be an integer between %d and %d", lo, hi))
		return 0
	}
	return n
}

// Err returns every problem recorded so far, or nil.
func (e *Env) Err() error {
	return errors.Join(e.errs...)
}

// Secret holds a sensitive value. its String, GoString and LogValue methods
// return a fixed marker so fmt and slog never print the value itself.
type Secret struct{ v string }

const redacted = "[redacted]"

// Reveal returns the underlying value. call it only at the point of use.
func (s Secret) Reveal() string { return s.v }

func (s Secret) String() string { return redacted }

func (s Secret) GoString() string { return redacted }

func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText keeps the value out of json and text encodings as well.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// UmamiAdmin is what platformctl needs to talk to umami as the admin account.
type UmamiAdmin struct {
	// URL is the base url of the umami instance on the private network.
	URL *url.URL
	// Password is the admin password that replaces the seeded one.
	Password Secret
}

// umami rejects new passwords shorter than 8 characters (src/app/api/me/password/route.ts).
// we ask for more because this account is the break-glass credential.
const minUmamiAdminPassword = 16

// LoadUmamiAdmin reads UMAMI_URL and UMAMI_ADMIN_PASSWORD.
func LoadUmamiAdmin(e *Env) (UmamiAdmin, error) {
	c := UmamiAdmin{
		URL:      e.URL("UMAMI_URL"),
		Password: e.Secret("UMAMI_ADMIN_PASSWORD", minUmamiAdminPassword),
	}
	return c, e.Err()
}
