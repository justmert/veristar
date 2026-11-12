package umami

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Admin calls umami as the admin service account. it logs in on first use
// and once more whenever umami answers 401, because without redis an umami
// token is refused after any password change of the account and there is no
// refresh route.
//
// umami answers 401 both for a bad token and for a permission denial
// (src/lib/response.ts unauthorized). the admin holds every permission, so a
// 401 after a fresh login is returned to the caller as is.
type Admin struct {
	c        *Client
	username string
	password string

	mu  sync.Mutex
	tok Token
}

// NewAdmin returns an admin session for username and password. it does not
// log in until the first call.
func NewAdmin(c *Client, username, password string) (*Admin, error) {
	if c == nil || username == "" || password == "" {
		return nil, errors.New("umami admin: client, username and password are required")
	}
	return &Admin{c: c, username: username, password: password}, nil
}

// Client returns the underlying client.
func (a *Admin) Client() *Client { return a.c }

func (a *Admin) token(ctx context.Context, stale Token) (Token, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// another goroutine may have logged in again while this one waited
	if !a.tok.Empty() && a.tok != stale {
		return a.tok, nil
	}
	s, err := a.c.Login(ctx, a.username, a.password)
	if err != nil {
		return Token{}, err
	}
	if !s.User.IsAdmin {
		return Token{}, fmt.Errorf("umami admin: user %q is not an admin", a.username)
	}
	a.tok = s.Token
	return a.tok, nil
}

// call runs one request with the current token and repeats it once with a new
// token after a 401. repeating is safe for writes too: umami checks auth and
// permission before it changes anything.
func (a *Admin) call(ctx context.Context, op, method, path string, in, out any) error {
	a.mu.Lock()
	tok := a.tok
	a.mu.Unlock()
	if tok.Empty() {
		var err error
		if tok, err = a.token(ctx, Token{}); err != nil {
			return err
		}
	}
	err := a.c.do(ctx, op, method, path, tok, in, out)
	if !errors.Is(err, ErrUnauthorized) {
		return err
	}
	if tok, err = a.token(ctx, tok); err != nil {
		return err
	}
	return a.c.do(ctx, op, method, path, tok, in, out)
}

func (a *Admin) callOK(ctx context.Context, op, method, path string, in any) error {
	var out struct {
		OK bool `json:"ok"`
	}
	if err := a.call(ctx, op, method, path, in, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf(`umami %s: response did not contain "ok":true`, op)
	}
	return nil
}

// global user roles (src/lib/constants.ts ROLES, src/lib/schema.ts userRoleParam)
const (
	RoleAdmin    = "admin"
	RoleUser     = "user"
	RoleViewOnly = "view-only"
)

// team roles accepted when adding a member (src/lib/schema.ts teamRoleParam).
// team-owner is only ever assigned by team creation.
const (
	TeamRoleOwner    = "team-owner"
	TeamRoleManager  = "team-manager"
	TeamRoleMember   = "team-member"
	TeamRoleViewOnly = "team-view-only"
)

// NewUser is the body of POST /api/users. umami lowercases the username and
// requires a password of 8 to 255 characters.
type NewUser struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// CreateUser calls POST /api/users (src/app/api/users/route.ts). an existing
// username, including a deleted one, is a 400.
func (a *Admin) CreateUser(ctx context.Context, u NewUser) (User, error) {
	if u.Username == "" || u.Role == "" {
		return User{}, errors.New("umami create user: username and role are required")
	}
	if len(u.Password) < 8 {
		return User{}, errors.New("umami create user: password must be at least 8 characters")
	}
	var out User
	if err := a.call(ctx, "create user", http.MethodPost, "/api/users", u, &out); err != nil {
		return User{}, err
	}
	if out.ID == "" {
		return User{}, errors.New("umami create user: response has no id")
	}
	return out, nil
}

// UserUpdate is the body of POST /api/users/<id>. empty fields are left
// unchanged. as admin, a password set here needs no current password.
type UserUpdate struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Role     string `json:"role,omitempty"`
}

// UpdateUser calls POST /api/users/<id> (src/app/api/users/[userId]/route.ts).
// a password change invalidates every token the user holds.
func (a *Admin) UpdateUser(ctx context.Context, id string, u UserUpdate) (User, error) {
	if id == "" {
		return User{}, errors.New("umami update user: no user id")
	}
	if u == (UserUpdate{}) {
		return User{}, errors.New("umami update user: nothing to change")
	}
	if u.Password != "" && len(u.Password) < 8 {
		return User{}, errors.New("umami update user: password must be at least 8 characters")
	}
	var out User
	if err := a.call(ctx, "update user", http.MethodPost, "/api/users/"+url.PathEscape(id), u, &out); err != nil {
		return User{}, err
	}
	return out, nil
}

// DeleteUser calls DELETE /api/users/<id>. umami answers 500 for an id that
// does not exist.
func (a *Admin) DeleteUser(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("umami delete user: no user id")
	}
	return a.callOK(ctx, "delete user", http.MethodDelete, "/api/users/"+url.PathEscape(id), nil)
}

// Team is the subset of umami's team object the platform reads.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// TeamUser is a team membership.
type TeamUser struct {
	ID     string `json:"id"`
	TeamID string `json:"teamId"`
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

// CreateTeam calls POST /api/teams (src/app/api/teams/route.ts). umami picks
// the team id. ownerID is honoured only for an admin caller; empty makes the
// admin itself the owner. umami answers [team, ownerMembership]. the name is
// at most 50 characters.
func (a *Admin) CreateTeam(ctx context.Context, name, ownerID string) (Team, TeamUser, error) {
	if name == "" || len(name) > 50 {
		return Team{}, TeamUser{}, errors.New("umami create team: name must be 1 to 50 characters")
	}
	in := map[string]string{"name": name}
	if ownerID != "" {
		in["ownerId"] = ownerID
	}
	var out []json.RawMessage
	if err := a.call(ctx, "create team", http.MethodPost, "/api/teams", in, &out); err != nil {
		return Team{}, TeamUser{}, err
	}
	if len(out) != 2 {
		return Team{}, TeamUser{}, fmt.Errorf("umami create team: want [team, teamUser], got %d elements", len(out))
	}
	var t Team
	var tu TeamUser
	if err := json.Unmarshal(out[0], &t); err != nil {
		return Team{}, TeamUser{}, fmt.Errorf("umami create team: decode team: %w", err)
	}
	if err := json.Unmarshal(out[1], &tu); err != nil {
		return Team{}, TeamUser{}, fmt.Errorf("umami create team: decode owner: %w", err)
	}
	if t.ID == "" || tu.TeamID != t.ID || tu.Role != TeamRoleOwner {
		return Team{}, TeamUser{}, errors.New("umami create team: response does not hold the team and its owner")
	}
	return t, tu, nil
}

// AddTeamUser calls POST /api/teams/<id>/users. adding an existing member is a 400.
func (a *Admin) AddTeamUser(ctx context.Context, teamID, userID, role string) (TeamUser, error) {
	if teamID == "" || userID == "" {
		return TeamUser{}, errors.New("umami add team user: team id and user id are required")
	}
	switch role {
	case TeamRoleManager, TeamRoleMember, TeamRoleViewOnly:
	default:
		return TeamUser{}, fmt.Errorf("umami add team user: role %q is not one umami accepts", role)
	}
	in := map[string]string{"userId": userID, "role": role}
	var out TeamUser
	if err := a.call(ctx, "add team user", http.MethodPost, "/api/teams/"+url.PathEscape(teamID)+"/users", in, &out); err != nil {
		return TeamUser{}, err
	}
	if out.ID == "" {
		return TeamUser{}, errors.New("umami add team user: response has no id")
	}
	return out, nil
}

// RemoveTeamUser calls DELETE /api/teams/<id>/users/<userId>. a user who is
// not a member is a 400.
func (a *Admin) RemoveTeamUser(ctx context.Context, teamID, userID string) error {
	if teamID == "" || userID == "" {
		return errors.New("umami remove team user: team id and user id are required")
	}
	return a.callOK(ctx, "remove team user", http.MethodDelete,
		"/api/teams/"+url.PathEscape(teamID)+"/users/"+url.PathEscape(userID), nil)
}

// DeleteTeam calls DELETE /api/teams/<id>. umami deletes the team's websites
// and their data with it (src/queries/prisma/team.ts deleteTeam). an unknown
// id is a 500.
func (a *Admin) DeleteTeam(ctx context.Context, teamID string) error {
	if teamID == "" {
		return errors.New("umami delete team: no team id")
	}
	return a.callOK(ctx, "delete team", http.MethodDelete, "/api/teams/"+url.PathEscape(teamID), nil)
}

// Website is the subset of umami's website object the platform reads.
type Website struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Domain  string     `json:"domain"`
	TeamID  string     `json:"teamId"`
	UserID  string     `json:"userId"`
	ResetAt *time.Time `json:"resetAt"`
}

// NewWebsite is the body of POST /api/websites. the platform always supplies
// the id (the site id) and the team.
type NewWebsite struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	TeamID string `json:"teamId"`
}

// CreateWebsite calls POST /api/websites (src/app/api/websites/route.ts). ids
// share one namespace with every umami entity, so a used id is a 400.
func (a *Admin) CreateWebsite(ctx context.Context, w NewWebsite) (Website, error) {
	if w.ID == "" || w.TeamID == "" || w.Name == "" || w.Domain == "" {
		return Website{}, errors.New("umami create website: id, team id, name and domain are required")
	}
	var out Website
	if err := a.call(ctx, "create website", http.MethodPost, "/api/websites", w, &out); err != nil {
		return Website{}, err
	}
	if out.ID != w.ID || out.TeamID != w.TeamID {
		return Website{}, errors.New("umami create website: response does not echo the requested id and team")
	}
	return out, nil
}

// DeleteWebsite calls DELETE /api/websites/<id>. without CLOUD_MODE umami
// deletes the row and all its data. an unknown id is a 500.
func (a *Admin) DeleteWebsite(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("umami delete website: no website id")
	}
	return a.callOK(ctx, "delete website", http.MethodDelete, "/api/websites/"+url.PathEscape(id), nil)
}

// ResetWebsite calls POST /api/websites/<id>/reset, which deletes every
// event and session of the website and keeps the website. an unknown id is a 500.
func (a *Admin) ResetWebsite(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("umami reset website: no website id")
	}
	return a.callOK(ctx, "reset website", http.MethodPost, "/api/websites/"+url.PathEscape(id)+"/reset", nil)
}

// DeleteSession calls DELETE /api/websites/<id>/sessions/<sessionId>, which
// deletes the session with its events. an unknown session is a 404.
func (a *Admin) DeleteSession(ctx context.Context, websiteID, sessionID string) error {
	if websiteID == "" || sessionID == "" {
		return errors.New("umami delete session: website id and session id are required")
	}
	return a.callOK(ctx, "delete session", http.MethodDelete,
		"/api/websites/"+url.PathEscape(websiteID)+"/sessions/"+url.PathEscape(sessionID), nil)
}

// Stats calls GET /api/websites/<id>/stats as the admin.
func (a *Admin) Stats(ctx context.Context, websiteID string, r Range) (Stats, error) {
	var out Stats
	path, err := statsPath(websiteID, r)
	if err != nil {
		return out, err
	}
	err = a.call(ctx, "stats", http.MethodGet, path, nil, &out)
	return out, err
}

// Metrics calls GET /api/websites/<id>/metrics as the admin.
func (a *Admin) Metrics(ctx context.Context, websiteID string, q MetricsQuery) ([]MetricRow, error) {
	var out []MetricRow
	path, err := metricsPath(websiteID, q)
	if err != nil {
		return nil, err
	}
	err = a.call(ctx, "metrics", http.MethodGet, path, nil, &out)
	return out, err
}

// Pageviews calls GET /api/websites/<id>/pageviews as the admin.
func (a *Admin) Pageviews(ctx context.Context, websiteID string, q PageviewsQuery) (Pageviews, error) {
	var out Pageviews
	path, err := pageviewsPath(websiteID, q)
	if err != nil {
		return out, err
	}
	err = a.call(ctx, "pageviews", http.MethodGet, path, nil, &out)
	return out, err
}
