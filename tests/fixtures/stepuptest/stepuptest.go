// Package stepuptest wires the operator step-up into test harnesses: a
// stepup.Service bound to the test server's origin, the startup roster
// bootstrap, and a helper that gives a signed-in operator's session its
// step-up through the real routes with a TEST-ONLY software authenticator
// (tests/fixtures/webauthnsoft).
package stepuptest

import (
	"encoding/json"
	"net/http"
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

// Origin is the console origin of a test server, which the relying party
// checks answers against.
func Origin(srvURL string) string {
	return srvURL
}

// Guard bootstraps the roster with ops, as cmd/controlplane does at start,
// and returns the step-up and the guard of a harness whose console runs at
// srvURL.
func Guard(t testing.TB, d *db.DB, sessions stepup.Sessions, srvURL string, ops identity.Operators) (*stepup.Service, *operator.Guard) {
	t.Helper()
	if _, err := operator.Bootstrap(t.Context(), d, ops, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	s, err := stepup.New(stepup.Config{ConsoleOrigin: srvURL, MaxAge: time.Hour, EnrollWindow: time.Hour}, d, sessions)
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
		st, c := call(http.MethodPost, "/api/operator/passkeys/challenge", map[string]any{})
		if st != http.StatusOK {
			t.Fatalf("passkey challenge: %d %s", st, c)
		}
		answer, err := key.Answer(c, origin)
		if err != nil {
			t.Fatal(err)
		}
		answer["name"] = "test key"
		answer["public_key"] = key.PublicKey()
		st, b := call(http.MethodPost, "/api/operator/passkeys", answer)
		if st != http.StatusCreated {
			t.Fatalf("register passkey: %d %s", st, b)
		}
		k.keys[user] = key
		return
	}
	st, c := call(http.MethodPost, "/api/operator/step-up/challenge", map[string]any{})
	if st != http.StatusOK {
		t.Fatalf("step-up challenge: %d %s", st, c)
	}
	answer, err := key.Answer(c, origin)
	if err != nil {
		t.Fatal(err)
	}
	st, b := call(http.MethodPost, "/api/operator/step-up", answer)
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
