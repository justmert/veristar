package operator

import (
	"testing"

	"github.com/justmert/veristar/internal/controlplane/session"
)

func id(n int64) *int64     { return &n }
func addr(s string) *string { return &s }

// the guard trusts the flag alone: the database derives it from the roster
// in the transaction that changes the roster (00039), and RequireUser reads
// it on every request. roster_test covers the derivation.
func TestGuardAllowed(t *testing.T) {
	g := &Guard{}
	if !g.Allowed(session.User{GitHubID: id(7), IsOperator: true}) {
		t.Error("an operator was refused")
	}
	if g.Allowed(session.User{GitHubID: id(7), StellarAddress: addr("G"), IsOperator: false}) {
		t.Error("a user without the flag was allowed")
	}
	if _, err := NewGuard(nil, nil); err == nil {
		t.Error("a guard without the database and the step-up was built")
	}
}
