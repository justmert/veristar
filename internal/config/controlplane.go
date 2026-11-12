package config

import (
	"encoding/base64"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ControlPlane is what the control plane needs besides the database pools
// (internal/db LoadPoolConfig) and nats (LoadNATS). the keys are those of
// docs/reference/control-plane-api.md section 4.5, plus OPERATOR_GITHUB_IDS
// (plan an operator input.
type ControlPlane struct {
	// Addr is the api listener; OpsAddr serves /healthz, /readyz, /metrics.
	Addr    string
	OpsAddr string
	// ConsoleOrigin is the exact Origin accepted on state changes.
	ConsoleOrigin string
	// IngestOrigin is https://in.<BASE_DOMAIN>, used in snippets.
	IngestOrigin string
	// WebOrigin is https://web.<BASE_DOMAIN>, the target of sso redirects.
	WebOrigin string

	GitHubClientID     string
	GitHubClientSecret Secret
	// GitHubRedirectURL is <ConsoleOrigin>/api/auth/github/callback.
	GitHubRedirectURL string
	// OAuthStateKey seals the sa_oauth cookie (aes-256-gcm).
	OAuthStateKey Key32
	// OperatorGitHubIDs are the numeric github ids of the operators.
	OperatorGitHubIDs map[int64]bool

	UmamiURL             *url.URL
	UmamiAdminPassword   Secret
	UmamiUserPasswordKey Key32

	TrackerReleaseDir string
	// TrackerNPMPackage is empty until the tracker is published (this stage).
	TrackerNPMPackage string
}

// CallbackPath is the path of the github callback on the console origin.
const CallbackPath = "/api/auth/github/callback"

// LoadControlPlane reads the control plane settings.
func LoadControlPlane(e *Env) (ControlPlane, error) {
	c := ControlPlane{
		Addr:                 e.ListenAddr("CONTROLPLANE_ADDR"),
		OpsAddr:              e.ListenAddr("CONTROLPLANE_OPS_ADDR"),
		ConsoleOrigin:        e.origin("CONSOLE_ORIGIN"),
		IngestOrigin:         e.origin("INGEST_ORIGIN"),
		WebOrigin:            e.origin("WEB_ORIGIN"),
		GitHubClientID:       e.String("GITHUB_CLIENT_ID"),
		GitHubClientSecret:   e.Secret("GITHUB_CLIENT_SECRET", 1),
		GitHubRedirectURL:    e.String("GITHUB_REDIRECT_URL"),
		OAuthStateKey:        e.key32("OAUTH_STATE_KEY"),
		OperatorGitHubIDs:    e.githubIDs("OPERATOR_GITHUB_IDS"),
		UmamiURL:             e.URL("UMAMI_URL"),
		UmamiAdminPassword:   e.Secret("UMAMI_ADMIN_PASSWORD", minUmamiAdminPassword),
		UmamiUserPasswordKey: e.key32("UMAMI_USER_PASSWORD_KEY"),
		TrackerReleaseDir:    e.String("TRACKER_RELEASE_DIR"),
	}
	if v, ok := e.raw("TRACKER_NPM_PACKAGE"); ok {
		c.TrackerNPMPackage = v
	}
	if c.Addr != "" && c.Addr == c.OpsAddr {
		e.fail("CONTROLPLANE_OPS_ADDR", "must differ from CONTROLPLANE_ADDR, the ops endpoints stay on a private port")
	}
	// the callback must land on the console origin: the session cookie is
	// host-only and the csrf origin check compares against that origin
	if c.ConsoleOrigin != "" && c.GitHubRedirectURL != "" && c.GitHubRedirectURL != c.ConsoleOrigin+CallbackPath {
		e.fail("GITHUB_REDIRECT_URL", "must be CONSOLE_ORIGIN followed by "+CallbackPath)
	}
	return c, e.Err()
}

// origin returns a required bare origin: scheme http or https, a host, an
// optional port, nothing else, lowercase.
func (e *Env) origin(key string) string {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || v != strings.ToLower(v) ||
		strings.HasSuffix(v, "/") {
		e.fail(key, "must be a bare lowercase origin such as https://app.example.org")
		return ""
	}
	return v
}

// Key32 is a 32-byte key read as base64. it never prints.
type Key32 struct{ b []byte }

// Reveal returns the key bytes. call it only at the point of use.
func (k Key32) Reveal() []byte { return k.b }

func (k Key32) String() string { return redacted }

// GoString keeps the key out of %#v.
func (k Key32) GoString() string { return redacted }

// LogValue keeps the key out of slog.
func (k Key32) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText keeps the key out of json and text encodings.
func (k Key32) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// key32 reads a required standard base64 value that decodes to 32 bytes.
func (e *Env) key32(key string) Key32 {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return Key32{}
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != 32 {
		e.fail(key, "must be 32 bytes in standard base64 (openssl rand -base64 32)")
		return Key32{}
	}
	return Key32{b: b}
}

// githubIDs reads a required comma separated list of numeric github user
// ids. at least one is required: a deployment without an operator could
// not govern enrollment.
func (e *Env) githubIDs(key string) map[int64]bool {
	v, ok := e.raw(key)
	if !ok {
		e.fail(key, "is required but not set")
		return nil
	}
	out := map[int64]bool{}
	for _, part := range strings.Split(v, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || n <= 0 {
			e.fail(key, "must be a comma separated list of numeric github user ids")
			return nil
		}
		out[n] = true
	}
	return out
}

// SortedOperatorIDs returns the operator ids in ascending order, for logs.
func (c ControlPlane) SortedOperatorIDs() []int64 {
	ids := make([]int64, 0, len(c.OperatorGitHubIDs))
	for id := range c.OperatorGitHubIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
