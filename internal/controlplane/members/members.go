// Package members serves the member and invite routes
// (control-plane-api 4.2.5). people are added by github login: a login that
// has signed in before becomes a member at once, any other login gets an
// invite that sign-in resolves (contract 4.1.2 step 6).
package members

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/audit"
	"github.com/justmert/stellar-analytics/internal/controlplane/auth"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/controlplane/tenants"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
	"github.com/justmert/stellar-analytics/internal/db/tenantq"
)

// limits of contract 4.2.5
const (
	maxMembersAndInvites = 50
	inviteLifetime       = 14 * 24 * time.Hour
)

// githubLoginRe is github's login syntax: letters, digits and single
// hyphens, no hyphen at either end. ValidLogin adds the 39 character limit.
var githubLoginRe = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9]){0,38}$`)

// ValidLogin reports whether s is a syntactically valid github login.
func ValidLogin(s string) bool { return len(s) <= 39 && githubLoginRe.MatchString(s) }

// Member is the member object of contract 3.1.
type Member struct {
	UserID      uuid.UUID       `json:"user_id"`
	GitHubID    int64           `json:"github_id"`
	GitHubLogin string          `json:"github_login"`
	Role        string          `json:"role"`
	CreatedAt   httpx.Timestamp `json:"created_at"`
}

// Invite is the invite object of contract 3.1.
type Invite struct {
	ID          uuid.UUID       `json:"id"`
	GitHubID    int64           `json:"github_id"`
	GitHubLogin string          `json:"github_login"`
	Role        string          `json:"role"`
	CreatedBy   uuid.UUID       `json:"created_by"`
	CreatedAt   httpx.Timestamp `json:"created_at"`
	ExpiresAt   httpx.Timestamp `json:"expires_at"`
}

func toInvite(i tenantq.CpInvite) Invite {
	return Invite{ID: i.ID, GitHubID: i.GithubID, GitHubLogin: i.GithubLogin, Role: i.Role, CreatedBy: i.CreatedBy,
		CreatedAt: httpx.Timestamp(i.CreatedAt), ExpiresAt: httpx.Timestamp(i.ExpiresAt)}
}

// UserLookup resolves a github login to its account (auth.UserLookup).
type UserLookup interface {
	Lookup(ctx context.Context, login string) (auth.GitHubUser, error)
}

// Routes are the member and invite routes.
type Routes struct {
	s     *tenants.Scope
	users UserLookup
}

// NewRoutes returns the routes over a scope. users resolves every added
// login to its github account id (contract 4.2.5).
func NewRoutes(s *tenants.Scope, users UserLookup) (*Routes, error) {
	if s == nil || users == nil {
		return nil, errors.New("members: scope and user lookup are required")
	}
	return &Routes{s: s, users: users}, nil
}

// Routes registers the paths under a router mounted at /api.
func (h *Routes) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/tenants/{tenant_id}/members", httpx.HandlerFunc(h.list))
	r.Method(http.MethodPost, "/tenants/{tenant_id}/members", httpx.HandlerFunc(h.add))
	r.Method(http.MethodPatch, "/tenants/{tenant_id}/members/{user_id}", httpx.HandlerFunc(h.setRole))
	r.Method(http.MethodDelete, "/tenants/{tenant_id}/members/{user_id}", httpx.HandlerFunc(h.remove))
	r.Method(http.MethodDelete, "/tenants/{tenant_id}/invites/{invite_id}", httpx.HandlerFunc(h.revoke))
}

type listBody struct {
	Members []Member `json:"members"`
	Invites []Invite `json:"invites"`
}

func (h *Routes) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	out := listBody{Members: []Member{}, Invites: []Invite{}}
	if err := h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		ms, err := q.ListTenantMembers(ctx)
		if err != nil {
			return err
		}
		for _, m := range ms {
			out.Members = append(out.Members, Member{UserID: m.UserID, GitHubID: m.GithubID,
				GitHubLogin: m.GithubLogin, Role: m.Role, CreatedAt: httpx.Timestamp(m.CreatedAt)})
		}
		is, err := q.ListOpenInvites(ctx)
		if err != nil {
			return err
		}
		for _, i := range is {
			out.Invites = append(out.Invites, toInvite(i))
		}
		return nil
	}); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

type addBody struct {
	GitHubLogin *string `json:"github_login"`
	Role        *string `json:"role"`
}

type addResult struct {
	Kind   string  `json:"kind"`
	Member *Member `json:"member,omitempty"`
	Invite *Invite `json:"invite,omitempty"`
}

func checkRole(f *tenants.Fields, role *string) {
	if role == nil {
		f.Add("role", httpx.FieldRequired)
	} else if *role != tenants.RoleOwner && *role != tenants.RoleMember {
		f.Add("role", httpx.FieldInvalidRole)
	}
}

func (h *Routes) add(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	var b addBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	if b.GitHubLogin == nil {
		f.Add("github_login", httpx.FieldRequired)
	} else if !ValidLogin(*b.GitHubLogin) {
		f.Add("github_login", httpx.FieldInvalidGitHubLogin)
	}
	checkRole(&f, b.Role)
	if err := f.Err(); err != nil {
		return err
	}
	role := *b.Role

	// the login is resolved to the github account that holds it now, and
	// the member or invite is bound to that account's id (review,
	// C-10): a login can be renamed and the freed name taken by someone
	// else, the id never changes hands
	gh, err := h.users.Lookup(ctx, *b.GitHubLogin)
	if errors.Is(err, auth.ErrGitHubUserNotFound) {
		return httpx.Validation("no github user has that login",
			map[string]string{"github_login": httpx.FieldUnknownGitHubLogin})
	}
	if err != nil {
		httpx.Logger(ctx).Warn("github user lookup failed", "error", err.Error())
		return httpx.Unavailable("github could not be reached to look up the login; try again")
	}
	login := gh.Login

	// cp.users is global, not tenant data, and a person who is not yet a
	// member is invisible under cp_tenant, so the lookup runs as cp_system
	var user *systemq.FindUserByGitHubIDRow
	if err := h.s.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		u, err := q.FindUserByGitHubID(ctx, gh.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.RefreshUserLogin(ctx, systemq.RefreshUserLoginParams{GithubLogin: gh.Login, GithubID: gh.ID}); err != nil {
			return err
		}
		u.GithubLogin = gh.Login
		user = &u
		return nil
	}); err != nil {
		return err
	}

	var res addResult
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		if err := tenants.LockTenant(ctx, tx, &a); err != nil {
			return err
		}
		if user != nil {
			if _, err := q.GetTenantMember(ctx, user.ID); err == nil {
				return httpx.Conflict(httpx.CodeAlreadyMember, user.GithubLogin+" is already a member")
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if _, err := q.DeleteExpiredInvitesForAccount(ctx, gh.ID); err != nil {
			return err
		}
		total, err := q.CountMembersAndOpenInvites(ctx)
		if err != nil {
			return err
		}
		if total >= maxMembersAndInvites {
			return httpx.Validation("a project has at most 50 members and open invites",
				map[string]string{"github_login": httpx.FieldTooMany})
		}
		if user != nil {
			m, err := q.InsertTenantMember(ctx, tenantq.InsertTenantMemberParams{UserID: user.ID, Role: role})
			if err != nil {
				return err
			}
			res = addResult{Kind: "member", Member: &Member{UserID: m.UserID, GitHubID: user.GithubID,
				GitHubLogin: user.GithubLogin, Role: m.Role, CreatedAt: httpx.Timestamp(m.CreatedAt)}}
			return audit.InTenant(ctx, tx, audit.Entry{
				Actor: audit.User(a.User.ID), Action: audit.MemberAdd, Target: audit.UserTarget(user.ID),
				Detail: map[string]any{"tenant_id": a.TenantID, "github_login": user.GithubLogin, "role": role},
			})
		}
		inv, err := q.CreateInvite(ctx, tenantq.CreateInviteParams{
			GithubID: gh.ID, GithubLogin: login, Role: role, CreatedBy: a.User.ID, ExpiresAt: time.Now().Add(inviteLifetime),
		})
		if err != nil {
			if tenants.IsUnique(err, "invites_tenant_github_idx") {
				return httpx.Conflict(httpx.CodeInviteExists, "an open invite for "+login+" exists")
			}
			return err
		}
		v := toInvite(inv)
		res = addResult{Kind: "invite", Invite: &v}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.InviteCreate, Target: audit.InviteTarget(inv.ID),
			Detail: map[string]any{"tenant_id": a.TenantID, "github_id": gh.ID, "github_login": login, "role": role},
		})
	})
	if err != nil {
		return err
	}
	if res.Kind == "member" {
		h.s.Reconcile(ctx, a.TenantID)
		httpx.Created(w, "/api/tenants/"+a.TenantID.String()+"/members/"+res.Member.UserID.String(), res)
		return nil
	}
	httpx.Created(w, "/api/tenants/"+a.TenantID.String()+"/invites/"+res.Invite.ID.String(), res)
	return nil
}

type roleBody struct {
	Role *string `json:"role"`
}

func (h *Routes) setRole(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	userID, err := httpx.ParseID(chi.URLParam(r, "user_id"))
	if err != nil {
		return err
	}
	var b roleBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	var f tenants.Fields
	checkRole(&f, b.Role)
	if err := f.Err(); err != nil {
		return err
	}
	var out Member
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		owners, err := q.LockTenantOwners(ctx)
		if err != nil {
			return err
		}
		m, err := q.GetTenantMember(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		out = Member{UserID: m.UserID, GitHubID: m.GithubID, GitHubLogin: m.GithubLogin, Role: m.Role, CreatedAt: httpx.Timestamp(m.CreatedAt)}
		if m.Role == *b.Role {
			return nil
		}
		if m.Role == tenants.RoleOwner && len(owners) <= 1 {
			return httpx.Conflict(httpx.CodeLastOwner, "the project needs at least one owner")
		}
		if _, err := q.SetTenantMemberRole(ctx, tenantq.SetTenantMemberRoleParams{Role: *b.Role, UserID: userID}); err != nil {
			return err
		}
		out.Role = *b.Role
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.MemberRoleChange, Target: audit.UserTarget(userID),
			Detail: map[string]any{"tenant_id": a.TenantID, "role": audit.Change(m.Role, *b.Role)},
		})
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// remove is allowed to owners and to any member for themselves (leaving).
// it is not blocked by unaccepted terms (contract 4.2.6), so people can
// always leave.
func (h *Routes) remove(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	userID, err := httpx.ParseID(chi.URLParam(r, "user_id"))
	if err != nil {
		return err
	}
	if userID != a.User.ID {
		if err := a.RequireOwner(); err != nil {
			return err
		}
	}
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		owners, err := q.LockTenantOwners(ctx)
		if err != nil {
			return err
		}
		m, err := q.GetTenantMember(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		if m.Role == tenants.RoleOwner && len(owners) <= 1 {
			return httpx.Conflict(httpx.CodeLastOwner, "the project needs at least one owner")
		}
		if _, err := q.DeleteTenantMember(ctx, userID); err != nil {
			return err
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.MemberRemove, Target: audit.UserTarget(userID),
			Detail: map[string]any{"tenant_id": a.TenantID, "github_login": m.GithubLogin, "role": m.Role},
		})
	})
	if err != nil {
		return err
	}
	h.s.Reconcile(ctx, a.TenantID)
	httpx.NoContent(w)
	return nil
}

func (h *Routes) revoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, err := h.s.Tenant(r, "tenant_id")
	if err != nil {
		return err
	}
	if err := a.RequireOwner(); err != nil {
		return err
	}
	if err := a.RequireTerms(); err != nil {
		return err
	}
	inviteID, err := httpx.ParseID(chi.URLParam(r, "invite_id"))
	if err != nil {
		return err
	}
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		inv, err := tenantq.New(tx).DeleteInvite(ctx, inviteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound()
		}
		if err != nil {
			return err
		}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.InviteRevoke, Target: audit.InviteTarget(inviteID),
			Detail: map[string]any{"tenant_id": a.TenantID, "github_login": inv.GithubLogin, "role": inv.Role},
		})
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
