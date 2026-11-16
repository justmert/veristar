package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db/systemq"
)

// linked sign-in methods (contract 4.1.8). an account links another
// identity only with both proofs in one browser session: the session proves
// the account, and the github flow, a signed wallet challenge or a passkey
// assertion proves the identity being added. an account always keeps at least one identity.

// accountPath is the console page that starts linking; the github link
// flow returns there.
const accountPath = "/account"

// audit actions of linking
const (
	actionLink   = "user.identity_link"
	actionUnlink = "user.identity_unlink"
)

// unique indexes whose violation means the identity names another account
const (
	githubLiveIdx        = "users_github_id_live_idx"
	stellarLiveIdx       = "users_stellar_address_live_idx"
	passkeyAddressIdx    = "users_passkey_address_live_idx"    //nolint:gosec // an index name, not a credential
	passkeyCredentialIdx = "users_passkey_credential_live_idx" //nolint:gosec // an index name, not a credential
)

func isUnique(err error, index string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == index
}

// failLink sends the browser back to the account page with a code, as fail
// does for sign-in.
func failLink(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, accountPath+"?link_error="+url.QueryEscape(code), http.StatusSeeOther)
}

// linkGitHubCallback finishes a github link flow: the state was checked and
// the github account read by callback.
func (h *Handler) linkGitHubCallback(w http.ResponseWriter, r *http.Request, p statePayload, gh githubUser) {
	ctx := r.Context()
	log := httpx.Logger(ctx)
	uid, ok := h.sessions.UserID(ctx)
	if !ok || uid.String() != p.LinkUser {
		// the session ended or changed hands during the round trip, so the
		// two proofs no longer belong to one session
		log.Info("link refused", "reason", failStateMismatch, "detail", "session changed")
		failLink(w, r, failStateMismatch)
		return
	}
	joined, err := h.link(ctx, uid, "github", func(q *systemq.Queries) (systemq.CpUser, error) {
		return q.LinkGitHub(ctx, systemq.LinkGitHubParams{GithubID: &gh.ID, GithubLogin: &gh.Login, ID: uid})
	}, map[string]any{"github_id": gh.ID, "github_login": gh.Login})
	if err != nil {
		if he, ok := httpx.AsError(err); ok {
			log.Info("link refused", "reason", he.Code)
			failLink(w, r, he.Code)
			return
		}
		log.Error("link failed", "reason", failInternal, "error", err.Error())
		failLink(w, r, failInternal)
		return
	}
	for _, t := range joined {
		h.reconcile(ctx, t)
	}
	log.Info("identity linked", "user_id", uid.String(), "kind", "github")
	http.Redirect(w, r, accountPath+"?linked=github", http.StatusSeeOther)
}

// link adds an identity to account uid in one cp_system transaction: set
// runs the link statement, then the operator flag follows both identities,
// open invites for the new identity become memberships, and the audit row
// is written. it returns the tenants the account joined.
func (h *Handler) link(ctx context.Context, uid uuid.UUID, kind string,
	set func(q *systemq.Queries) (systemq.CpUser, error), detail map[string]any,
	extra ...func(q *systemq.Queries) error,
) ([]uuid.UUID, error) {
	var joined []uuid.UUID
	err := h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		for _, f := range extra {
			if err := f(q); err != nil {
				return err
			}
		}
		u, err := set(q)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return httpx.Conflict(httpx.CodeIdentityLinked,
				"this account already has a "+kind+" sign-in; unlink it first")
		case isUnique(err, githubLiveIdx), isUnique(err, stellarLiveIdx),
			isUnique(err, passkeyAddressIdx), isUnique(err, passkeyCredentialIdx):
			return httpx.Conflict(httpx.CodeIdentityInUse,
				"this "+kind+" identity signs in to another account; delete that account or unlink it there first")
		case err != nil:
			return fmt.Errorf("link %s: %w", kind, err)
		}
		u, err = q.SetOperatorFlag(ctx, systemq.SetOperatorFlagParams{
			IsOperator: h.operators.Is(u.GithubID, u.StellarAddress, u.PasskeyAddress), ID: u.ID,
		})
		if err != nil {
			return fmt.Errorf("operator flag: %w", err)
		}
		actor := "user:" + u.ID.String()
		resolved, err := q.ResolveInvites(ctx, systemq.ResolveInvitesParams{
			GithubID: u.GithubID, StellarAddress: u.StellarAddress, PasskeyAddress: u.PasskeyAddress, UserID: u.ID,
		})
		if err != nil {
			return fmt.Errorf("resolve invites: %w", err)
		}
		for _, inv := range resolved {
			if inv.Added {
				joined = append(joined, inv.TenantID)
			}
			if err := audit(ctx, q, actor, "invite.resolve", "invite:"+inv.ID.String(), map[string]any{
				"tenant_id": inv.TenantID.String(), "user_id": u.ID.String(), "role": inv.Role, "added": inv.Added,
			}); err != nil {
				return err
			}
		}
		detail["kind"] = kind
		detail["is_operator"] = u.IsOperator
		return audit(ctx, q, actor, actionLink, "user:"+u.ID.String(), detail)
	})
	return joined, err
}

func (h *Handler) unlinkGitHub(w http.ResponseWriter, r *http.Request) error {
	return h.unlink(w, r, "github", func(u session.User) bool { return u.GitHubID != nil },
		func(ctx context.Context, q *systemq.Queries, id uuid.UUID) (systemq.CpUser, error) {
			return q.UnlinkGitHub(ctx, id)
		})
}

func (h *Handler) unlinkStellar(w http.ResponseWriter, r *http.Request) error {
	return h.unlink(w, r, "stellar", func(u session.User) bool { return u.StellarAddress != nil },
		func(ctx context.Context, q *systemq.Queries, id uuid.UUID) (systemq.CpUser, error) {
			return q.UnlinkStellar(ctx, id)
		})
}

func (h *Handler) unlinkPasskey(w http.ResponseWriter, r *http.Request) error {
	return h.unlink(w, r, "passkey", func(u session.User) bool { return u.PasskeyAddress != nil },
		func(ctx context.Context, q *systemq.Queries, id uuid.UUID) (systemq.CpUser, error) {
			return q.UnlinkPasskey(ctx, id)
		})
}

// unlink removes one identity of the signed-in account. 404 when the
// account has no identity of that kind, 409 last_sign_in_method when it is
// the only one. the other sessions of the account stay: they were opened by
// the person, whichever method they used.
func (h *Handler) unlink(w http.ResponseWriter, r *http.Request, kind string, has func(session.User) bool,
	del func(context.Context, *systemq.Queries, uuid.UUID) (systemq.CpUser, error),
) error {
	ctx := r.Context()
	u, err := session.MustUser(ctx)
	if err != nil {
		return err
	}
	if !has(u) {
		return httpx.NotFound()
	}
	err = h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		row, err := del(ctx, q, u.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Conflict(httpx.CodeLastSignInMethod,
				"this is the only way to sign in to this account; link another one first")
		}
		if err != nil {
			return fmt.Errorf("unlink %s: %w", kind, err)
		}
		row, err = q.SetOperatorFlag(ctx, systemq.SetOperatorFlagParams{
			IsOperator: h.operators.Is(row.GithubID, row.StellarAddress, row.PasskeyAddress), ID: row.ID,
		})
		if err != nil {
			return fmt.Errorf("operator flag: %w", err)
		}
		// the detail names the kind only: the removed identity is personal
		// data the account no longer holds
		return audit(ctx, q, "user:"+u.ID.String(), actionUnlink, "user:"+u.ID.String(),
			map[string]any{"kind": kind, "is_operator": row.IsOperator})
	})
	if err != nil {
		return err
	}
	httpx.Logger(ctx).Info("identity unlinked", "user_id", u.ID.String(), "kind", kind)
	httpx.NoContent(w)
	return nil
}
