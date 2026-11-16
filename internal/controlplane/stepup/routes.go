package stepup

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db/systemq"
)

// audit actions of the step-up (contract 3.3)
const (
	ActionStepUp        = "operator.step_up"
	ActionPasskeyAdd    = "operator.passkey_add"
	ActionPasskeyDelete = "operator.passkey_delete"
)

// maxNameLen bounds a passkey's name, in characters
const maxNameLen = 64

// Routes registers the step-up and passkey routes (contract 5.15) under the
// guarded /api/operator router. they sit outside Require: they are how a
// session gets its step-up, and each route that changes passkeys checks the
// step-up, or the enrollment window, itself.
func (s *Service) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/step-up", httpx.HandlerFunc(s.status))
	r.Method(http.MethodPost, "/step-up/challenge", httpx.HandlerFunc(s.stepUpChallenge))
	r.Method(http.MethodPost, "/step-up", httpx.HandlerFunc(s.stepUp))
	r.Method(http.MethodGet, "/passkeys", httpx.HandlerFunc(s.list))
	r.Method(http.MethodPost, "/passkeys/challenge", httpx.HandlerFunc(s.registerChallenge))
	r.Method(http.MethodPost, "/passkeys", httpx.HandlerFunc(s.register))
	r.Method(http.MethodDelete, "/passkeys/{passkey_id}", httpx.HandlerFunc(s.remove))
}

// Status is the answer of GET /api/operator/step-up.
type Status struct {
	SteppedUp     bool             `json:"stepped_up"`
	SteppedUpAt   *httpx.Timestamp `json:"stepped_up_at"`
	ExpiresAt     *httpx.Timestamp `json:"expires_at"`
	MaxAgeSeconds int64            `json:"max_age_seconds"`
	// Passkeys counts the credentials that can step up: the added passkeys
	// and the sign-in passkey
	Passkeys      int64 `json:"passkeys"`
	SignInPasskey bool  `json:"sign_in_passkey"`
	// Enrollment says whether a first passkey may be registered without a
	// step-up: open (until EnrollmentUntil), sign_in_required (the window
	// is open but this session's sign-in is older than it), or closed
	Enrollment      string           `json:"enrollment"`
	EnrollmentUntil *httpx.Timestamp `json:"enrollment_until"`
}

// enrollment states
const (
	EnrollmentOpen           = "open"
	EnrollmentSignInRequired = "sign_in_required"
	EnrollmentClosed         = "closed"
)

// enrollment decides whether u may register a first passkey without a
// step-up: the account has no credential that can step up, a roster row of
// one of its identities is active with an open window, and the session's
// sign-in happened inside that window. the last rule means a session that
// existed before the window opened (a copied cookie, a forgotten browser)
// cannot enroll; only a fresh sign-in can.
func (s *Service) enrollment(ctx context.Context, tx pgx.Tx, u session.User, credentials int64) (string, *time.Time, error) {
	if credentials > 0 {
		return EnrollmentClosed, nil, nil
	}
	rows, err := systemq.New(tx).RosterEntriesForUser(ctx, systemq.RosterEntriesForUserParams{
		GithubID: u.GitHubID, StellarAddress: u.StellarAddress, PasskeyAddress: u.PasskeyAddress,
	})
	if err != nil {
		return "", nil, err
	}
	now := s.now()
	signedIn, hasSignIn := s.sess.SignedInAt(ctx)
	state, until := EnrollmentClosed, (*time.Time)(nil)
	for _, o := range rows {
		if o.Status != "active" || o.EnrollUntil == nil || !now.Before(*o.EnrollUntil) {
			continue
		}
		t := o.EnrollUntil.UTC()
		// sign-in times are kept in whole seconds
		if hasSignIn && !signedIn.Before(o.EnrollOpenedAt.Truncate(time.Second)) {
			return EnrollmentOpen, &t, nil
		}
		state, until = EnrollmentSignInRequired, &t
	}
	return state, until, nil
}

func ts(t time.Time) *httpx.Timestamp { v := httpx.Timestamp(t); return &v }

func (s *Service) status(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	out := Status{MaxAgeSeconds: int64(s.maxAge / time.Second)}
	if ok, at := s.Fresh(ctx, u.ID); ok {
		out.SteppedUp, out.SteppedUpAt, out.ExpiresAt = true, ts(at), ts(at.Add(s.maxAge))
	}
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		k, err := LoadKeys(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		out.Passkeys, out.SignInPasskey = k.Count(), k.SignIn
		state, until, err := s.enrollment(ctx, tx, u, k.Count())
		if err != nil {
			return err
		}
		out.Enrollment = state
		if until != nil {
			out.EnrollmentUntil = ts(*until)
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) stepUpChallenge(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var k Keys
	if err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		k, err = LoadKeys(ctx, tx, u.ID)
		return err
	}); err != nil {
		return err
	}
	if k.Count() == 0 {
		return httpx.Conflict(httpx.CodePasskeyRequired, "register a passkey first")
	}
	c, err := s.IssueChallenge(ctx, u.ID, PurposeStepUp)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

// use marks a verified challenge used in the caller's transaction, as
// passkey sign-in does; a second use is a replay
func (s *Service) use(ctx context.Context, q *systemq.Queries, challenge string) error {
	n, err := q.UseWalletChallenge(ctx, systemq.UseWalletChallengeParams{
		NonceHash: usedHash(challenge), ExpiresAt: s.now().Add(challengeTTL),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return errNoCeremony
	}
	return nil
}

func (s *Service) stepUp(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var a Answer
	if err := httpx.DecodeJSON(r, &a); err != nil {
		return err
	}
	challenge, err := s.takeChallenge(ctx, u.ID, PurposeStepUp)
	if err != nil {
		return err
	}
	fields := map[string]string{}
	as, credID, _ := a.decoded(fields, false)
	if len(fields) > 0 {
		return httpx.Validation("the passkey answer is incomplete or malformed", fields)
	}
	var at time.Time
	recorded := false
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		k, err := LoadKeys(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		pub, ok := k.byID[credID]
		if !ok {
			return failed(ctx, errors.New("the credential is not a passkey of this account"))
		}
		if err := s.rp.Verify(as, challenge, pub); err != nil {
			return failed(ctx, err)
		}
		if err := s.use(ctx, q, challenge); err != nil {
			return err
		}
		if _, err := q.TouchPasskey(ctx, systemq.TouchPasskeyParams{UserID: u.ID, CredentialID: credID}); err != nil {
			return err
		}
		if at, err = s.Record(ctx, u.ID); err != nil {
			return err
		}
		recorded = true
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionStepUp, Target: audit.UserTarget(u.ID),
			Detail: map[string]any{"max_age_seconds": int64(s.maxAge / time.Second), "sign_in_passkey": credID == k.signInID},
		})
	})
	if err != nil {
		// a transaction that failed after the step-up was recorded must not
		// leave it in the session; a refused answer leaves an earlier step-up
		// as it was
		if recorded {
			s.Clear(ctx)
		}
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, Status{
		SteppedUp: true, SteppedUpAt: ts(at), ExpiresAt: ts(at.Add(s.maxAge)),
		MaxAgeSeconds: int64(s.maxAge / time.Second),
	})
	return nil
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	if ok, _ := s.Fresh(ctx, u.ID); !ok {
		return StepUpRequired()
	}
	var k Keys
	if err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		k, err = LoadKeys(ctx, tx, u.ID)
		return err
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": k.Added, "sign_in_passkey": k.SignIn})
	return nil
}

// mayRegister answers whether the request may add a passkey, and whether
// it does so through the enrollment window.
func (s *Service) mayRegister(ctx context.Context, tx pgx.Tx, u session.User, credentials int64) (bool, error) {
	if ok, _ := s.Fresh(ctx, u.ID); ok {
		return false, nil
	}
	state, _, err := s.enrollment(ctx, tx, u, credentials)
	if err != nil {
		return false, err
	}
	switch state {
	case EnrollmentOpen:
		return true, nil
	case EnrollmentSignInRequired:
		return false, httpx.E(http.StatusUnauthorized, httpx.CodeEnrollmentClosed,
			"sign in again to register your first passkey")
	}
	return false, StepUpRequired()
}

func (s *Service) registerChallenge(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	if err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		k, err := LoadKeys(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		_, err = s.mayRegister(ctx, tx, u, k.Count())
		return err
	}); err != nil {
		return err
	}
	c, err := s.IssueChallenge(ctx, u.ID, PurposeRegister)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

type registerBody struct {
	Name string `json:"name"`
	Answer
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b registerBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	challenge, err := s.takeChallenge(ctx, u.ID, PurposeRegister)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(b.Name)
	fields := map[string]string{}
	switch {
	case name == "":
		fields["name"] = httpx.FieldRequired
	case utf8.RuneCountInString(name) > maxNameLen || len(name) > 4*maxNameLen:
		fields["name"] = httpx.FieldTooLong
	case strings.ContainsFunc(name, func(c rune) bool { return c < 0x20 || c == 0x7f }):
		fields["name"] = httpx.FieldInvalidFormat
	}
	as, credID, pub := b.decoded(fields, true)
	if len(fields) > 0 {
		return httpx.Validation("the passkey needs a name and a complete answer", fields)
	}
	// the new key must have signed this console's challenge: a
	// registration answer carries no signature, so the key is trusted only
	// once it signs
	if err := s.rp.Verify(as, challenge, pub); err != nil {
		return failed(ctx, err)
	}
	var out Passkey
	recorded := false
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		// the account's passkeys are locked, so two first registrations in
		// one window cannot both use it
		if _, err := q.LockUserPasskeys(ctx, u.ID); err != nil {
			return err
		}
		k, err := LoadKeys(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		viaEnrollment, err := s.mayRegister(ctx, tx, u, k.Count())
		if err != nil {
			return err
		}
		if _, dup := k.byID[credID]; dup {
			return httpx.Validation("this passkey is registered already", map[string]string{"credential_id": httpx.FieldDuplicate})
		}
		if err := s.use(ctx, q, challenge); err != nil {
			return err
		}
		if out, err = AddPasskey(ctx, tx, u.ID, name, credID, pub); err != nil {
			if isUnique(err) {
				return httpx.Validation("this passkey is registered to another account",
					map[string]string{"credential_id": httpx.FieldDuplicate})
			}
			return err
		}
		if viaEnrollment {
			if _, err := q.CloseEnrollment(ctx, systemq.CloseEnrollmentParams{
				GithubID: u.GitHubID, StellarAddress: u.StellarAddress, PasskeyAddress: u.PasskeyAddress,
			}); err != nil {
				return err
			}
		}
		// the new passkey has just signed a challenge with user
		// verification; it counts as the step-up, so the person does not
		// touch it twice
		if _, err := s.Record(ctx, u.ID); err != nil {
			return err
		}
		recorded = true
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionPasskeyAdd, Target: audit.UserTarget(u.ID),
			Detail: map[string]any{"passkey_id": out.ID.String(), "name": name, "enrollment": viaEnrollment},
		})
	})
	if err != nil {
		if recorded {
			s.Clear(ctx)
		}
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
	return nil
}

func (s *Service) remove(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "passkey_id"))
	if err != nil {
		return err
	}
	if ok, _ := s.Fresh(ctx, u.ID); !ok {
		return StepUpRequired()
	}
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		if _, err := q.LockUserPasskeys(ctx, u.ID); err != nil {
			return err
		}
		k, err := LoadKeys(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		found := false
		for _, p := range k.Added {
			found = found || p.ID == id
		}
		if !found {
			return httpx.NotFound()
		}
		if k.Count() == 1 {
			return httpx.Conflict(httpx.CodeLastPasskey,
				"register another passkey before you delete your last one")
		}
		row, err := q.DeletePasskey(ctx, systemq.DeletePasskeyParams{ID: id, UserID: u.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionPasskeyDelete, Target: audit.UserTarget(u.ID),
			Detail: map[string]any{"passkey_id": row.ID.String(), "name": row.Name},
		})
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
