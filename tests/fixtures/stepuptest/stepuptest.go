// Package stepuptest wires the operator step-up into test harnesses: a
// stepup.Service bound to the test server's origin, the startup roster
// bootstrap, and a helper that gives a signed-in operator's session its
// step-up through the real routes with a TEST-ONLY software authenticator
// (tests/fixtures/webauthnsoft).
package stepuptest

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/stepup"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/tests/fixtures/webauthnsoft"
)

// Origin is the console origin of a test server for the relying party.
// webauthn refuses an ip address as relying party id, so httptest's
// 127.0.0.1 is named localhost; the step-up compares origins only, not the
// address the request came in on.
func Origin(srvURL string) string {
	return strings.Replace(srvURL, "127.0.0.1", "localhost", 1)
}

// Guard bootstraps the roster with ops, as cmd/controlplane does at start,
// and returns the step-up and the guard of a harness whose console runs at
// srvURL.
func Guard(t testing.TB, d *db.DB, sessions stepup.Sessions, srvURL string, ops identity.Operators) (*stepup.Service, *operator.Guard) {
	t.Helper()
	if _, err := operator.Bootstrap(t.Context(), d, ops, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	s, err := stepup.New(stepup.Config{ConsoleOrigin: Origin(srvURL), MaxAge: time.Hour, EnrollWindow: time.Hour}, d, sessions)
	if err != nil {
		t.Fatal(err)
	}
	g, err := operator.NewGuard(d, s)
	if err != nil {
		t.Fatal(err)
	}
	return s, g
}

// Call sends one console request (json body, csrf header and origin on
// state changes) and returns the status and body.
type Call func(method, path string, body any) (int, []byte)

// Keys holds one software authenticator per account, so every session of
// an operator steps up with the key its first session registered.
type Keys struct {
	mu   sync.Mutex
	keys map[uuid.UUID]*webauthnsoft.Authenticator
}

// StepUp gives the session behind call a step-up for user: the account's
// first passkey through the enrollment window the bootstrap or an admin
// opened, or an assertion with the key registered before.
func (k *Keys) StepUp(t testing.TB, user uuid.UUID, origin string, call Call) {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.keys == nil {
		k.keys = map[uuid.UUID]*webauthnsoft.Authenticator{}
	}
	key, ok := k.keys[user]
	if !ok {
		var err error
		if key, err = webauthnsoft.New(); err != nil {
			t.Fatal(err)
		}
		st, opts := call(http.MethodPost, "/api/operator/passkeys/options", map[string]any{})
		if st != http.StatusOK {
			t.Fatalf("passkey options: %d %s", st, opts)
		}
		cred, err := key.Register(opts, origin)
		if err != nil {
			t.Fatal(err)
		}
		st, b := call(http.MethodPost, "/api/operator/passkeys", map[string]any{"name": "test key", "credential": cred})
		if st != http.StatusCreated {
			t.Fatalf("register passkey: %d %s", st, b)
		}
		k.keys[user] = key
		return
	}
	st, opts := call(http.MethodPost, "/api/operator/step-up/options", map[string]any{})
	if st != http.StatusOK {
		t.Fatalf("step-up options: %d %s", st, opts)
	}
	cred, err := key.Assert(opts, origin)
	if err != nil {
		t.Fatal(err)
	}
	st, b := call(http.MethodPost, "/api/operator/step-up", map[string]any{"credential": json.RawMessage(cred)})
	if st != http.StatusOK {
		t.Fatalf("step-up: %d %s", st, b)
	}
}

// StepUpMe is StepUp for the account /api/me names.
func (k *Keys) StepUpMe(t testing.TB, origin string, call Call) {
	t.Helper()
	st, b := call(http.MethodGet, "/api/me", nil)
	if st != http.StatusOK {
		t.Fatalf("/api/me: %d %s", st, b)
	}
	var me struct {
		User struct {
			ID uuid.UUID `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(b, &me); err != nil {
		t.Fatal(err)
	}
	k.StepUp(t, me.User.ID, origin, call)
}
