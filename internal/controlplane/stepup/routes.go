package stepup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	r.Method(http.MethodPost, "/step-up/options", httpx.HandlerFunc(s.assertOptions))
	r.Method(http.MethodPost, "/step-up", httpx.HandlerFunc(s.assert))
	r.Method(http.MethodGet, "/passkeys", httpx.HandlerFunc(s.list))
	r.Method(http.MethodPost, "/passkeys/options", httpx.HandlerFunc(s.registerOptions))
	r.Method(http.MethodPost, "/passkeys", httpx.HandlerFunc(s.register))
	r.Method(http.MethodDelete, "/passkeys/{passkey_id}", httpx.HandlerFunc(s.remove))
}

// Status is the answer of GET /api/operator/step-up.
type Status struct {
	SteppedUp     bool             `json:"stepped_up"`
	SteppedUpAt   *httpx.Timestamp `json:"stepped_up_at"`
	ExpiresAt     *httpx.Timestamp `json:"expires_at"`
	MaxAgeSeconds int64            `json:"max_age_seconds"`
	Passkeys      int64            `json:"passkeys"`
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
// step-up: the account has no passkey, a roster row of one of its
// identities is active with an open window, and the session's sign-in
// happened inside that window. the last rule means a session that existed
// before the window opened (a copied cookie, a forgotten browser) cannot
// enroll; only a fresh sign-in can.
func (s *Service) enrollment(ctx context.Context, tx pgx.Tx, u session.User, passkeys int64) (string, *time.Time, error) {
	if passkeys > 0 {
		return EnrollmentClosed, nil, nil
	}
	rows, err := systemq.New(tx).RosterEntriesForUser(ctx, systemq.RosterEntriesForUserParams{
		GithubID: u.GitHubID, StellarAddress: u.StellarAddress,
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
		n, err := systemq.New(tx).CountPasskeys(ctx, u.ID)
		if err != nil {
			return err
		}
		out.Passkeys = n
		state, until, err := s.enrollment(ctx, tx, u, n)
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

func (s *Service) assertOptions(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var acc Account
	if err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		acc, _, err = LoadAccount(ctx, tx, u)
		return err
	}); err != nil {
		return err
	}
	opts, err := s.BeginAssertion(ctx, acc)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, opts)
	return nil
}

// credentialBody is the browser's PublicKeyCredential json, passed on to
// the webauthn library as it came
type credentialBody struct {
	Credential json.RawMessage `json:"credential"`
}

func (s *Service) assert(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b credentialBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	if len(b.Credential) == 0 {
		return httpx.Validation("credential is required", map[string]string{"credential": httpx.FieldRequired})
	}
	var at time.Time
	err = s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		acc, _, err := LoadAccount(ctx, tx, u)
		if err != nil {
			return err
		}
		cred, err := s.FinishAssertion(ctx, acc, b.Credential)
		if err != nil {
			return err
		}
		if err := TouchPasskey(ctx, tx, u.ID, cred); err != nil {
			return err
		}
		if at, err = s.Record(ctx, u.ID); err != nil {
			return err
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionStepUp, Target: audit.UserTarget(u.ID),
			Detail: map[string]any{"max_age_seconds": int64(s.maxAge / time.Second)},
		})
	})
	if err != nil {
		// a failed transaction must not leave a step-up in the session
		s.Clear(ctx)
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, Status{
		SteppedUp: true, SteppedUpAt: ts(at), ExpiresAt: ts(at.Add(s.maxAge)),
		MaxAgeSeconds: int64(s.maxAge / time.Second),
	})
	return nil
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
	var list []Passkey
	if err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		_, list, err = LoadAccount(ctx, tx, u)
		return err
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

// mayRegister answers whether the request may add a passkey, and whether
// it does so through the enrollment window.
func (s *Service) mayRegister(ctx context.Context, tx pgx.Tx, u session.User, passkeys int64) (bool, error) {
	if ok, _ := s.Fresh(ctx, u.ID); ok {
		return false, nil
	}
	state, _, err := s.enrollment(ctx, tx, u, passkeys)
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

func (s *Service) registerOptions(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var acc Account
	if err := s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		acc, _, err = LoadAccount(ctx, tx, u)
		if err != nil {
			return err
		}
		_, err = s.mayRegister(ctx, tx, u, int64(len(acc.Credentials)))
		return err
	}); err != nil {
		return err
	}
	opts, err := s.BeginRegistration(ctx, acc)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, opts)
	return nil
}

type registerBody struct {
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
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
	if len(b.Credential) == 0 {
		fields["credential"] = httpx.FieldRequired
	}
	if len(fields) > 0 {
		return httpx.Validation("the passkey needs a name and a credential", fields)
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
		acc, _, err := LoadAccount(ctx, tx, u)
		if err != nil {
			return err
		}
		viaEnrollment, err := s.mayRegister(ctx, tx, u, int64(len(acc.Credentials)))
		if err != nil {
			return err
		}
		cred, err := s.FinishRegistration(ctx, acc, b.Credential)
		if err != nil {
			return err
		}
		if out, err = AddPasskey(ctx, tx, u.ID, name, cred); err != nil {
			return err
		}
		if viaEnrollment {
			if _, err := q.CloseEnrollment(ctx, systemq.CloseEnrollmentParams{
				GithubID: u.GitHubID, StellarAddress: u.StellarAddress,
			}); err != nil {
				return err
			}
			// the new credential was just proven present; it counts as the
			// first step-up, so the person does not touch it twice
			if _, err := s.Record(ctx, u.ID); err != nil {
				return err
			}
			recorded = true
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionPasskeyAdd, Target: audit.UserTarget(u.ID),
			Detail: map[string]any{"passkey_id": out.ID.String(), "name": name, "enrollment": viaEnrollment,
				"backup_eligible": out.BackupEligible},
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
		ids, err := q.LockUserPasskeys(ctx, u.ID)
		if err != nil {
			return err
		}
		if !containsID(ids, id) {
			return httpx.NotFound()
		}
		if len(ids) == 1 {
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

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
