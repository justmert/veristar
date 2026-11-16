package operator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/members"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
)

// audit actions of the roster (contract 3.3)
const (
	ActionOperatorAdd           = "operator.add"
	ActionOperatorRemove        = "operator.remove"
	ActionOperatorSuspend       = "operator.suspend"
	ActionOperatorResume        = "operator.resume"
	ActionOperatorRoleChange    = "operator.role_change"
	ActionOperatorPasskeysReset = "operator.passkeys_reset"
	ActionOperatorBootstrap     = "operator.bootstrap"
	// ActionOperatorSync is a changed is_operator flag found by the startup
	// recompute
	ActionOperatorSync = "user.operator_sync"
)

// ActorStartup is the actor of the startup bootstrap.
const ActorStartup = "system:startup"

// reasonMax bounds the reason of a roster change, in characters
const reasonMax = 500

// RosterTarget names a roster row in the audit log.
func RosterTarget(id uuid.UUID) string { return "operator:" + id.String() }

// GitHubLookup resolves a github login to its account id; *auth.UserLookup
// implements it.
type GitHubLookup interface {
	Lookup(ctx context.Context, login string) (auth.GitHubUser, error)
}

// Roster serves the operators page (contract 5.16): every operator lists
// the roster, operator admins change it.
type Roster struct {
	db     *db.DB
	guard  *Guard
	lookup GitHubLookup
	boot   identity.Operators
	window time.Duration
	now    func() time.Time
}

// RosterConfig wires the roster routes.
type RosterConfig struct {
	DB     *db.DB
	Guard  *Guard
	Lookup GitHubLookup
	// Bootstrap are OPERATOR_GITHUB_IDS and OPERATOR_STELLAR_ADDRESSES. their
	// rows are ensured at every start, so the console refuses to suspend,
	// remove or demote them: the next start would undo it
	Bootstrap identity.Operators
	// EnrollWindow is the first-passkey window an added or reset row gets
	EnrollWindow time.Duration
	Now          func() time.Time
}

// NewRoster checks cfg.
func NewRoster(cfg RosterConfig) (*Roster, error) {
	if cfg.DB == nil || cfg.Guard == nil || cfg.Lookup == nil || cfg.EnrollWindow <= 0 {
		return nil, errors.New("operator: roster needs the database, the guard, the github lookup and the enroll window")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Roster{db: cfg.DB, guard: cfg.Guard, lookup: cfg.Lookup, boot: cfg.Bootstrap, window: cfg.EnrollWindow, now: now}, nil
}

// Routes registers the roster under the guarded, stepped-up router.
func (ro *Roster) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/roster", httpx.HandlerFunc(ro.list))
	r.Group(func(a chi.Router) {
		a.Use(ro.guard.RequireAdmin)
		a.Method(http.MethodPost, "/roster", httpx.HandlerFunc(ro.add))
		for _, act := range []string{"suspend", "resume", "remove", "role", "reset-passkeys"} {
			a.Method(http.MethodPost, "/roster/{entry_id}/"+act, ro.action(act))
		}
	})
}

// RosterAccount is the live console account holding a row's identity.
type RosterAccount struct {
	UserID      uuid.UUID        `json:"user_id"`
	DisplayName string           `json:"display_name"`
	LastLoginAt *httpx.Timestamp `json:"last_login_at"`
	Passkeys    int64            `json:"passkeys"`
}

// RosterEntry is one row of GET /api/operator/roster.
type RosterEntry struct {
	ID              uuid.UUID        `json:"id"`
	Kind            string           `json:"kind"`
	GitHubID        *int64           `json:"github_id"`
	GitHubLogin     *string          `json:"github_login"`
	StellarAddress  *string          `json:"stellar_address"`
	Role            string           `json:"role"`
	Status          string           `json:"status"`
	StatusReason    *string          `json:"status_reason"`
	Bootstrap       bool             `json:"bootstrap"`
	EnrollmentUntil *httpx.Timestamp `json:"enrollment_until"`
	CreatedAt       httpx.Timestamp  `json:"created_at"`
	UpdatedAt       httpx.Timestamp  `json:"updated_at"`
	Account         *RosterAccount   `json:"account"`
}

func (ro *Roster) bootstrapped(o systemq.CpOperator) bool {
	return ro.boot.Is(o.GithubID, o.StellarAddress)
}

func (ro *Roster) entry(o systemq.CpOperator) RosterEntry {
	e := RosterEntry{
		ID: o.ID, GitHubID: o.GithubID, GitHubLogin: o.GithubLogin, StellarAddress: o.StellarAddress,
		Role: o.Role, Status: o.Status, StatusReason: o.StatusReason, Bootstrap: ro.bootstrapped(o),
		CreatedAt: httpx.Timestamp(o.CreatedAt), UpdatedAt: httpx.Timestamp(o.UpdatedAt), Kind: "github",
	}
	if o.StellarAddress != nil {
		e.Kind = "stellar"
	}
	if o.EnrollUntil != nil && ro.now().Before(*o.EnrollUntil) {
		t := httpx.Timestamp(*o.EnrollUntil)
		e.EnrollmentUntil = &t
	}
	return e
}

func (ro *Roster) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	out := struct {
		Items []RosterEntry `json:"items"`
	}{Items: []RosterEntry{}}
	err := ro.db.WithSystem(ctx, func(tx pgx.Tx) error {
		rows, err := systemq.New(tx).ListRoster(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			e := ro.entry(systemq.CpOperator{
				ID: row.ID, GithubID: row.GithubID, GithubLogin: row.GithubLogin, StellarAddress: row.StellarAddress,
				Role: row.Role, Status: row.Status, StatusReason: row.StatusReason, EnrollOpenedAt: row.EnrollOpenedAt,
				EnrollUntil: row.EnrollUntil, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			})
			if row.UserID.Valid {
				a := &RosterAccount{UserID: row.UserID.UUID, Passkeys: row.Passkeys,
					DisplayName: identity.DisplayName(row.AccountGithubLogin, row.StellarAddress)}
				if row.LastLoginAt != nil {
					t := httpx.Timestamp(*row.LastLoginAt)
					a.LastLoginAt = &t
				}
				e.Account = a
			}
			out.Items = append(out.Items, e)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("operator: roster: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func checkReason(f *tenants.Fields, reason *string) string {
	if reason == nil {
		f.Add("reason", httpx.FieldRequired)
		return ""
	}
	return tenants.Text(f, "reason", *reason, 1, reasonMax)
}

func checkRole(f *tenants.Fields, role string) {
	if role != RoleOperator && role != RoleOperatorAdmin {
		f.Add("role", httpx.FieldInvalidRole)
	}
}

type addBody struct {
	GitHubLogin    *string `json:"github_login"`
	StellarAddress *string `json:"stellar_address"`
	Role           string  `json:"role"`
	Reason         *string `json:"reason"`
}

func (ro *Roster) add(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	var b addBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	reason := checkReason(&f, b.Reason)
	checkRole(&f, b.Role)
	switch {
	case (b.GitHubLogin == nil) == (b.StellarAddress == nil):
		f.Add("github_login", httpx.FieldRequired)
	case b.GitHubLogin != nil && !members.ValidLogin(strings.TrimSpace(*b.GitHubLogin)):
		f.Add("github_login", httpx.FieldInvalidGitHubLogin)
	case b.StellarAddress != nil && !identity.ValidAddress(strings.TrimSpace(*b.StellarAddress)):
		f.Add("stellar_address", httpx.FieldInvalidStrkey)
	}
	if err := f.Err(); err != nil {
		return err
	}
	params := systemq.AddRosterEntryParams{Role: b.Role, CreatedBy: uuid.NullUUID{UUID: u.ID, Valid: true}}
	if b.GitHubLogin != nil {
		gh, err := ro.lookup.Lookup(ctx, strings.TrimSpace(*b.GitHubLogin))
		if errors.Is(err, auth.ErrGitHubUserNotFound) {
			return httpx.Validation("no github user has that login", map[string]string{"github_login": httpx.FieldUnknownGitHubLogin})
		}
		if err != nil {
			httpx.Logger(ctx).Warn("roster: github lookup", "error", err.Error())
			return httpx.Unavailable("github could not be reached to resolve the login; try again")
		}
		params.GithubID, params.GithubLogin = &gh.ID, &gh.Login
	} else {
		a := strings.TrimSpace(*b.StellarAddress)
		params.StellarAddress = &a
	}
	now := ro.now().UTC()
	params.EnrollOpenedAt, params.EnrollUntil = now, now.Add(ro.window)
	var out RosterEntry
	err = ro.db.WithSystem(ctx, func(tx pgx.Tx) error {
		row, err := systemq.New(tx).AddRosterEntry(ctx, params)
		if isUnique(err) {
			return httpx.Conflict(httpx.CodeOperatorExists, "this identity is on the roster already")
		}
		if err != nil {
			return err
		}
		out = ro.entry(row)
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: audit.User(u.ID), Action: ActionOperatorAdd, Target: RosterTarget(row.ID),
			Detail: map[string]any{"role": row.Role, "github_id": row.GithubID, "github_login": row.GithubLogin,
				"stellar_address": row.StellarAddress, "reason": reason, "enrollment_until": params.EnrollUntil},
		})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
	return nil
}

type actionBody struct {
	Reason *string `json:"reason"`
	Role   *string `json:"role"`
}

func (ro *Roster) action(act string) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		u, err := session.MustUser(ctx)
		if err != nil {
			return err
		}
		id, err := httpx.ParseID(chi.URLParam(r, "entry_id"))
		if err != nil {
			return err
		}
		var b actionBody
		if err := httpx.DecodeJSON(r, &b); err != nil {
			return err
		}
		var f tenants.Fields
		reason := checkReason(&f, b.Reason)
		if act == "role" {
			if b.Role == nil {
				f.Add("role", httpx.FieldRequired)
			} else {
				checkRole(&f, *b.Role)
			}
		} else if b.Role != nil {
			f.Add("role", httpx.FieldInvalidValue)
		}
		if err := f.Err(); err != nil {
			return err
		}
		out, err := ro.apply(ctx, u, id, act, b.Role, reason)
		if err != nil {
			return err
		}
		if out == nil {
			w.WriteHeader(http.StatusNoContent)
			return nil
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return nil
	})
}

// apply runs one roster change in one cp_system transaction: the active
// admin rows and the target row are locked first, so two changes that
// could each remove the last admin run one after the other.
func (ro *Roster) apply(ctx context.Context, u session.User, id uuid.UUID, act string, role *string, reason string) (*RosterEntry, error) {
	var out *RosterEntry
	err := ro.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		admins, err := q.LockActiveAdmins(ctx)
		if err != nil {
			return err
		}
		cur, err := q.LockRosterEntry(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		activeAdmin := cur.Status == "active" && cur.Role == RoleOperatorAdmin
		losesAdmin := activeAdmin && (act == "suspend" || act == "remove" || (act == "role" && *role != RoleOperatorAdmin))
		if losesAdmin && len(admins) <= 1 && slices.Contains(admins, cur.ID) {
			return httpx.Conflict(httpx.CodeLastOperatorAdmin,
				"the roster must keep at least one active operator admin; add or promote another admin first")
		}
		if ro.bootstrapped(cur) && (act == "suspend" || act == "remove" || (act == "role" && *role != RoleOperatorAdmin)) {
			return httpx.Conflict(httpx.CodeBootstrapOperator,
				"this identity is in OPERATOR_GITHUB_IDS or OPERATOR_STELLAR_ADDRESSES; take it out of the deploy settings first")
		}
		detail := map[string]any{"reason": reason, "github_id": cur.GithubID, "stellar_address": cur.StellarAddress}
		var action string
		var row systemq.CpOperator
		// the live account holding the identity, whose sessions and
		// passkeys some changes end
		holder, err := q.RosterIdentityUser(ctx, systemq.RosterIdentityUserParams{
			GithubID: cur.GithubID, StellarAddress: cur.StellarAddress,
		})
		hasHolder := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		endSessions := false
		switch act {
		case "suspend":
			if cur.Status == "suspended" {
				return httpx.Conflict(httpx.CodeInvalidTransition, "this operator is suspended already")
			}
			action, endSessions = ActionOperatorSuspend, true
			row, err = q.SetRosterStatus(ctx, systemq.SetRosterStatusParams{ID: id, Status: "suspended", StatusReason: &reason})
		case "resume":
			if cur.Status == "active" {
				return httpx.Conflict(httpx.CodeInvalidTransition, "this operator is active already")
			}
			action = ActionOperatorResume
			row, err = q.SetRosterStatus(ctx, systemq.SetRosterStatusParams{ID: id, Status: "active"})
		case "role":
			if cur.Role == *role {
				return httpx.Conflict(httpx.CodeInvalidTransition, "this operator holds that role already")
			}
			action = ActionOperatorRoleChange
			detail["role"] = audit.Change(cur.Role, *role)
			row, err = q.SetRosterRole(ctx, systemq.SetRosterRoleParams{ID: id, Role: *role})
		case "remove":
			action, endSessions = ActionOperatorRemove, true
			row, err = q.DeleteRosterEntry(ctx, id)
			if err == nil && hasHolder {
				// a person who still holds another active row keeps the
				// passkeys; otherwise they are no operator and lose them
				n, derr := deletePasskeysUnlessOperator(ctx, q, holder)
				if derr != nil {
					return derr
				}
				detail["passkeys_deleted"] = n
			}
		case "reset-passkeys":
			action, endSessions = ActionOperatorPasskeysReset, true
			now := ro.now().UTC()
			if hasHolder {
				n, derr := q.DeleteUserPasskeys(ctx, holder)
				if derr != nil {
					return derr
				}
				detail["passkeys_deleted"] = n
			}
			row, err = q.OpenEnrollment(ctx, systemq.OpenEnrollmentParams{ID: id, OpenedAt: now, Until: now.Add(ro.window)})
			detail["enrollment_until"] = now.Add(ro.window)
		default:
			return httpx.NotFound()
		}
		if isLastAdmin(err) {
			return httpx.Conflict(httpx.CodeLastOperatorAdmin, "the roster must keep at least one active operator admin")
		}
		if err != nil {
			return err
		}
		if endSessions && hasHolder {
			// the person signs in again; a reactivated or reset operator
			// then needs a new step-up
			n, err := q.AccountDeleteSessions(ctx, holder)
			if err != nil {
				return err
			}
			detail["sessions_ended"] = n
		}
		if act != "remove" {
			e := ro.entry(row)
			out = &e
		}
		return audit.InSystem(ctx, tx, audit.Entry{Actor: audit.User(u.ID), Action: action, Target: RosterTarget(id), Detail: detail})
	})
	return out, err
}

// deletePasskeysUnlessOperator removes the passkeys of an account the
// roster no longer names as an active operator.
func deletePasskeysUnlessOperator(ctx context.Context, q *systemq.Queries, user uuid.UUID) (int64, error) {
	u, err := q.GetUser(ctx, user)
	if err != nil {
		return 0, err
	}
	if u.IsOperator {
		return 0, nil
	}
	return q.DeleteUserPasskeys(ctx, user)
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func isLastAdmin(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.ConstraintName == "operators_last_admin"
}

// BootstrapResult says what the startup bootstrap changed.
type BootstrapResult struct {
	Ensured      int
	Changed      int
	FlagsChanged int
	Windows      int
	ActiveAdmins int64
}

// Bootstrap ensures at every start that each identity in
// OPERATOR_GITHUB_IDS and OPERATOR_STELLAR_ADDRESSES is an active operator
// admin, opens a first-passkey window for those whose account has no
// passkey yet, recomputes every account's operator flag, and audits each
// change. it fails when the roster ends without an active operator admin,
// so a control plane never starts with an operator area nobody can manage.
func Bootstrap(ctx context.Context, d *db.DB, ops identity.Operators, window time.Duration, now time.Time) (BootstrapResult, error) {
	var res BootstrapResult
	err := d.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		var rows []systemq.CpOperator
		ids := ops.GitHubIDs()
		slices.Sort(ids)
		ensured := func(id uuid.UUID, gh *int64, addr *string, until *time.Time, inserted, touched bool) error {
			rows = append(rows, systemq.CpOperator{ID: id, GithubID: gh, StellarAddress: addr, EnrollUntil: until})
			if !touched {
				return nil
			}
			res.Changed++
			return audit.InSystem(ctx, tx, audit.Entry{Actor: ActorStartup, Action: ActionOperatorBootstrap,
				Target: RosterTarget(id), Detail: map[string]any{
					"github_id": gh, "stellar_address": addr, "inserted": inserted, "role": RoleOperatorAdmin,
				}})
		}
		for _, gh := range ids {
			r, err := q.BootstrapRosterGitHub(ctx, &gh)
			if err != nil {
				return err
			}
			if err := ensured(r.ID, r.GithubID, r.StellarAddress, r.EnrollUntil, r.Inserted, r.Touched); err != nil {
				return err
			}
		}
		addrs := ops.StellarAddresses()
		slices.Sort(addrs)
		for _, a := range addrs {
			r, err := q.BootstrapRosterStellar(ctx, &a)
			if err != nil {
				return err
			}
			if err := ensured(r.ID, r.GithubID, r.StellarAddress, r.EnrollUntil, r.Inserted, r.Touched); err != nil {
				return err
			}
		}
		res.Ensured = len(rows)
		for _, o := range rows {
			n, err := q.RosterIdentityPasskeys(ctx, systemq.RosterIdentityPasskeysParams{
				GithubID: o.GithubID, StellarAddress: o.StellarAddress,
			})
			if err != nil {
				return err
			}
			if n > 0 || (o.EnrollUntil != nil && now.Before(*o.EnrollUntil)) {
				continue
			}
			if _, err := q.OpenEnrollment(ctx, systemq.OpenEnrollmentParams{ID: o.ID, OpenedAt: now, Until: now.Add(window)}); err != nil {
				return err
			}
			res.Windows++
		}
		flags, err := q.RecomputeOperatorFlags(ctx)
		if err != nil {
			return err
		}
		res.FlagsChanged = len(flags)
		for _, f := range flags {
			if err := audit.InSystem(ctx, tx, audit.Entry{
				Actor: ActorStartup, Action: ActionOperatorSync, Target: audit.UserTarget(f.ID),
				Detail: map[string]any{"is_operator": audit.Change(!f.IsOperator, f.IsOperator)},
			}); err != nil {
				return err
			}
		}
		if res.ActiveAdmins, err = q.CountActiveAdmins(ctx); err != nil {
			return err
		}
		if res.ActiveAdmins == 0 {
			return errors.New("the roster has no active operator admin; set OPERATOR_GITHUB_IDS or OPERATOR_STELLAR_ADDRESSES")
		}
		return nil
	})
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("operator: bootstrap roster: %w", err)
	}
	return res, nil
}
