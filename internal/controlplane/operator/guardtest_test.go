package operator

import (
	"testing"
	"time"

	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/stepup"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/dbtest"
)

// testGuard is the guard of a test server at srvURL with a real step-up.
// the tests' sign-in shortcuts call stepUp.Record right after the sign-in,
// the way a verified passkey assertion does; the ceremonies themselves are
// tested in internal/controlplane/stepup.
func testGuard(t *testing.T, d *db.DB, sessions *session.Manager, srvURL string) (*Guard, *stepup.Service) {
	t.Helper()
	s, err := stepup.New(stepup.Config{
		ConsoleOrigin: srvURL, MaxAge: time.Hour, EnrollWindow: time.Hour,
	}, d, sessions)
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewGuard(d, s)
	if err != nil {
		t.Fatal(err)
	}
	return g, s
}

// rosterAdmin puts a github id on the roster as an active operator admin,
// before its cp.users row is written: the users_operator_flag trigger
// derives is_operator from the roster on insert
func rosterAdmin(t *testing.T, pg *dbtest.Postgres, gh int64) {
	t.Helper()
	superExec(t, pg, `insert into cp.operators (github_id, role) values ($1, 'operator_admin')`, gh)
}
