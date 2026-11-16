package operator

import (
	"testing"

	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/session"
)

// both the flag and the loaded id list are needed: a stale flag alone (an
// id removed before this start) or a listed id whose flag was never set
// (no sign-in since it was added) grants nothing
const (
	opAddr    = "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN"
	otherAddr = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"
)

func id(n int64) *int64     { return &n }
func addr(s string) *string { return &s }

func TestGuardAllowed(t *testing.T) {
	g, err := NewGuard(identity.Operators{
		GitHub:  map[int64]bool{7: true, 8: false},
		Stellar: map[string]bool{opAddr: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		u    session.User
		want bool
	}{
		{session.User{GitHubID: id(7), IsOperator: true}, true},
		{session.User{GitHubID: id(7), IsOperator: false}, false},
		{session.User{GitHubID: id(9), IsOperator: true}, false},
		{session.User{GitHubID: id(8), IsOperator: true}, false},
		// a wallet-only operator, and a listed address on an account whose
		// github id is not listed
		{session.User{StellarAddress: addr(opAddr), IsOperator: true}, true},
		{session.User{GitHubID: id(9), StellarAddress: addr(opAddr), IsOperator: true}, true},
		{session.User{StellarAddress: addr(opAddr), IsOperator: false}, false},
		{session.User{StellarAddress: addr(otherAddr), IsOperator: true}, false},
		{session.User{IsOperator: true}, false},
	}
	for _, c := range cases {
		if got := g.Allowed(c.u); got != c.want {
			t.Errorf("Allowed(%+v) = %v, want %v", c.u, got, c.want)
		}
	}
	if _, err := NewGuard(identity.Operators{}); err == nil {
		t.Error("an empty operator list was accepted")
	}
}
