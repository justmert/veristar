// Package members serves the member and invite routes
// (control-plane-api 4.2.5). people are added by github login or by stellar
// address: an account that has signed in before becomes a member at once,
// anyone else gets an invite that their first sign-in with that identity
// resolves (contract 4.1.2 step 6, 4.1.7).
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

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/identity"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// limits of contract 4.2.5
const (
	MaxMembersAndInvites = 50
	InviteLifetime       = 14 * 24 * time.Hour
)

// githubLoginRe is github's login syntax: letters, digits and single
// hyphens, no hyphen at either end. ValidLogin adds the 39 character limit.
var githubLoginRe = regexp.MustCompile(`^[A-Za-z0-9](?:-?[A-Za-z0-9]){0,38}$`)

// ValidLogin reports whether s is a syntactically valid github login.
func ValidLogin(s string) bool { return len(s) <= 39 && githubLoginRe.MatchString(s) }

// Member is the member object of contract 3.1. each identity is null when
// the account does not hold it: github_id and github_login, stellar_address
// (a wallet's G-address) and passkey_address (a soropass account's
// C-address).
type Member struct {
	UserID         uuid.UUID       `json:"user_id"`
	GitHubID       *int64          `json:"github_id"`
	GitHubLogin    *string         `json:"github_login"`
	StellarAddress *string         `json:"stellar_address"`
	PasskeyAddress *string         `json:"passkey_address"`
	DisplayName    string          `json:"display_name"`
	Role           string          `json:"role"`
	CreatedAt      httpx.Timestamp `json:"created_at"`
}

func newMember(userID uuid.UUID, gh *int64, login, address, passkey *string, role string, at time.Time) Member {
	return Member{
		UserID: userID, GitHubID: gh, GitHubLogin: login, StellarAddress: address, PasskeyAddress: passkey,
		DisplayName: identity.DisplayName(login, address, passkey), Role: role, CreatedAt: httpx.Timestamp(at),
	}
}

// Invite is the invite object of contract 3.1: it names a github account
// (github_id and github_login) or a stellar address (stellar_address): a
// wallet's G-address or a passkey account's C-address.
type Invite struct {
	ID             uuid.UUID       `json:"id"`
	GitHubID       *int64          `json:"github_id"`
	GitHubLogin    *string         `json:"github_login"`
	StellarAddress *string         `json:"stellar_address"`
	DisplayName    string          `json:"display_name"`
	Role           string          `json:"role"`
	CreatedBy      uuid.UUID       `json:"created_by"`
	CreatedAt      httpx.Timestamp `json:"created_at"`
	ExpiresAt      httpx.Timestamp `json:"expires_at"`
}

func toInvite(i tenantq.CpInvite) Invite {
	return Invite{
		ID: i.ID, GitHubID: i.GithubID, GitHubLogin: i.GithubLogin, StellarAddress: i.StellarAddress,
		DisplayName: identity.DisplayName(i.GithubLogin, i.StellarAddress),
		Role:        i.Role, CreatedBy: i.CreatedBy,
		CreatedAt: httpx.Timestamp(i.CreatedAt), ExpiresAt: httpx.Timestamp(i.ExpiresAt),
	}
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
			out.Members = append(out.Members, newMember(m.UserID, m.GithubID, m.GithubLogin, m.StellarAddress, m.PasskeyAddress, m.Role, m.CreatedAt))
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
	GitHubLogin    *string `json:"github_login"`
	StellarAddress *string `json:"stellar_address"`
	Role           *string `json:"role"`
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

// person is the identity an owner names in an add: a github account,
// resolved from the login, or a stellar account. user is its console
// account when one exists.
type person struct {
	field       string
	name        string
	githubID    *int64
	githubLogin *string
	address     *string
	user        *existing
}

type existing struct {
	ID             uuid.UUID
	GithubID       *int64
	GithubLogin    *string
	StellarAddress *string
	PasskeyAddress *string
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
	switch {
	case b.GitHubLogin != nil && b.StellarAddress != nil:
		// one person per request; naming two identities is ambiguous
		f.Add("stellar_address", httpx.FieldInvalidValue)
	case b.GitHubLogin == nil && b.StellarAddress == nil:
		f.Add("github_login", httpx.FieldRequired)
	case b.GitHubLogin != nil && !ValidLogin(*b.GitHubLogin):
		f.Add("github_login", httpx.FieldInvalidGitHubLogin)
	case b.StellarAddress != nil && !identity.ValidPersonAddress(*b.StellarAddress):
		f.Add("stellar_address", httpx.FieldInvalidStrkey)
	}
	checkRole(&f, b.Role)
	if err := f.Err(); err != nil {
		return err
	}
	role := *b.Role

	var p person
	if b.GitHubLogin != nil {
		if p, err = h.githubPerson(ctx, *b.GitHubLogin); err != nil {
			return err
		}
	} else if p, err = h.stellarPerson(ctx, *b.StellarAddress); err != nil {
		return err
	}

	var res addResult
	err = h.s.InTenant(ctx, a, func(tx pgx.Tx) error {
		q := tenantq.New(tx)
		if err := tenants.LockTenant(ctx, tx, &a); err != nil {
			return err
		}
		if p.user != nil {
			if _, err := q.GetTenantMember(ctx, p.user.ID); err == nil {
				return httpx.Conflict(httpx.CodeAlreadyMember, p.name+" is already a member")
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if _, err := q.DeleteExpiredInvitesForAccount(ctx, tenantq.DeleteExpiredInvitesForAccountParams{
			GithubID: p.githubID, StellarAddress: p.address,
		}); err != nil {
			return err
		}
		total, err := q.CountMembersAndOpenInvites(ctx)
		if err != nil {
			return err
		}
		if total >= MaxMembersAndInvites {
			return httpx.Validation("a project has at most 50 members and open invites",
				map[string]string{p.field: httpx.FieldTooMany})
		}
		if u := p.user; u != nil {
			m, err := q.InsertTenantMember(ctx, tenantq.InsertTenantMemberParams{UserID: u.ID, Role: role})
			if err != nil {
				return err
			}
			mem := newMember(m.UserID, u.GithubID, u.GithubLogin, u.StellarAddress, u.PasskeyAddress, m.Role, m.CreatedAt)
			res = addResult{Kind: "member", Member: &mem}
			return audit.InTenant(ctx, tx, audit.Entry{
				Actor: audit.User(a.User.ID), Action: audit.MemberAdd, Target: audit.UserTarget(u.ID),
				Detail: map[string]any{"tenant_id": a.TenantID, p.field: p.name, "role": role},
			})
		}
		var inv tenantq.CpInvite
		index := "invites_tenant_github_idx"
		detail := map[string]any{"tenant_id": a.TenantID, "role": role}
		if p.githubID != nil {
			inv, err = q.CreateInvite(ctx, tenantq.CreateInviteParams{
				GithubID: p.githubID, GithubLogin: p.githubLogin, Role: role, CreatedBy: a.User.ID,
				ExpiresAt: time.Now().Add(InviteLifetime),
			})
			detail["github_id"], detail["github_login"] = *p.githubID, *p.githubLogin
		} else {
			index = "invites_tenant_stellar_idx"
			inv, err = q.CreateStellarInvite(ctx, tenantq.CreateStellarInviteParams{
				StellarAddress: p.address, Role: role, CreatedBy: a.User.ID,
				ExpiresAt: time.Now().Add(InviteLifetime),
			})
			detail["stellar_address"] = *p.address
		}
		if err != nil {
			if tenants.IsUnique(err, index) {
				return httpx.Conflict(httpx.CodeInviteExists, "an open invite for "+p.name+" exists")
			}
			return err
		}
		v := toInvite(inv)
		res = addResult{Kind: "invite", Invite: &v}
		return audit.InTenant(ctx, tx, audit.Entry{
			Actor: audit.User(a.User.ID), Action: audit.InviteCreate, Target: audit.InviteTarget(inv.ID),
			Detail: detail,
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

// githubPerson resolves a login to the github account that holds it now;
// the member or invite is bound to that account's id: a login can be
// renamed and the freed name taken by someone else, the id never changes
// hands.
func (h *Routes) githubPerson(ctx context.Context, login string) (person, error) {
	gh, err := h.users.Lookup(ctx, login)
	if errors.Is(err, auth.ErrGitHubUserNotFound) {
		return person{}, httpx.Validation("no github user has that login",
			map[string]string{"github_login": httpx.FieldUnknownGitHubLogin})
	}
	if err != nil {
		httpx.Logger(ctx).Warn("github user lookup failed", "error", err.Error())
		return person{}, httpx.Unavailable("github could not be reached to look up the login; try again")
	}
	p := person{field: "github_login", name: gh.Login, githubID: &gh.ID, githubLogin: &gh.Login}
	// cp.users is global, not tenant data, and a person who is not yet a
	// member is invisible under cp_tenant, so the lookup runs as cp_system
	err = h.s.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		u, err := q.FindUserByGitHubID(ctx, &gh.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.RefreshUserLogin(ctx, systemq.RefreshUserLoginParams{GithubLogin: &gh.Login, GithubID: &gh.ID}); err != nil {
			return err
		}
		p.user = &existing{ID: u.ID, GithubID: u.GithubID, GithubLogin: &gh.Login, StellarAddress: u.StellarAddress,
			PasskeyAddress: u.PasskeyAddress}
		return nil
	})
	return p, err
}

// stellarPerson finds the console account of an address, if any: the
// wallet account of a G-address, the passkey account of a C-address. the
// address is checked for its strkey checksum already.
func (h *Routes) stellarPerson(ctx context.Context, address string) (person, error) {
	p := person{field: "stellar_address", name: identity.ShortAddress(address), address: &address}
	err := h.s.DB().WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		var e existing
		var err error
		if identity.ValidContractAddress(address) {
			var u systemq.FindUserByPasskeyAddressRow
			u, err = q.FindUserByPasskeyAddress(ctx, &address)
			e = existing{ID: u.ID, GithubID: u.GithubID, GithubLogin: u.GithubLogin,
				StellarAddress: u.StellarAddress, PasskeyAddress: u.PasskeyAddress}
		} else {
			var u systemq.FindUserByStellarAddressRow
			u, err = q.FindUserByStellarAddress(ctx, &address)
			e = existing{ID: u.ID, GithubID: u.GithubID, GithubLogin: u.GithubLogin,
				StellarAddress: u.StellarAddress, PasskeyAddress: u.PasskeyAddress}
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		p.user = &e
		return nil
	})
	return p, err
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
		out = newMember(m.UserID, m.GithubID, m.GithubLogin, m.StellarAddress, m.PasskeyAddress, m.Role, m.CreatedAt)
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
			Detail: map[string]any{"tenant_id": a.TenantID, "github_login": m.GithubLogin,
				"stellar_address": m.StellarAddress, "passkey_address": m.PasskeyAddress, "role": m.Role},
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
			Detail: map[string]any{"tenant_id": a.TenantID, "github_login": inv.GithubLogin,
				"stellar_address": inv.StellarAddress, "role": inv.Role},
		})
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
