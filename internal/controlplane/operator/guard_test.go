package operator

import (
	"testing"

	"github.com/justmert/veristar/internal/controlplane/session"
)

// both the flag and the loaded id list are needed: a stale flag alone (an
// id removed before this start) or a listed id whose flag was never set
// (no sign-in since it was added) grants nothing
func TestGuardAllowed(t *testing.T) {
	g, err := NewGuard(map[int64]bool{7: true, 8: false})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		u    session.User
		want bool
	}{
		{session.User{GitHubID: 7, IsOperator: true}, true},
		{session.User{GitHubID: 7, IsOperator: false}, false},
		{session.User{GitHubID: 9, IsOperator: true}, false},
		{session.User{GitHubID: 8, IsOperator: true}, false},
	}
	for _, c := range cases {
		if got := g.Allowed(c.u); got != c.want {
			t.Errorf("Allowed(%+v) = %v, want %v", c.u, got, c.want)
		}
	}
	if _, err := NewGuard(nil); err == nil {
		t.Error("an empty operator list was accepted")
	}
}
