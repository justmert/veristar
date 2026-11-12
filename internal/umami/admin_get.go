package umami

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// read calls the provisioning reconciler needs. umami answers an unknown user
// or website on its GET route with 200 and the json literal null (the route
// passes the missing row to json()), and an unknown team with 404, so each
// lookup reports "absent" without an error and leaves every other status to
// the caller. deletes of unknown ids answer 500, which is why the reconciler
// looks before it deletes.

// GetUser calls GET /api/users/<id> (src/app/api/users/[userId]/route.ts).
// ok is false when no such user exists.
func (a *Admin) GetUser(ctx context.Context, id string) (u User, ok bool, err error) {
	if id == "" {
		return User{}, false, errors.New("umami get user: no user id")
	}
	var raw json.RawMessage
	if err := a.call(ctx, "get user", http.MethodGet, "/api/users/"+url.PathEscape(id), nil, &raw); err != nil {
		return User{}, false, err
	}
	if isNull(raw) {
		return User{}, false, nil
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return User{}, false, fmt.Errorf("umami get user: decode: %w", err)
	}
	if u.ID != id {
		return User{}, false, errors.New("umami get user: response is for another id")
	}
	return u, true, nil
}

// TeamDetail is a team with its memberships, as GET /api/teams/<id> returns
// it to an admin.
type TeamDetail struct {
	Team
	Members []TeamUser `json:"members"`
}

// GetTeam calls GET /api/teams/<id> (src/app/api/teams/[teamId]/route.ts),
// which includes the members. ok is false when no such team exists.
func (a *Admin) GetTeam(ctx context.Context, id string) (t TeamDetail, ok bool, err error) {
	if id == "" {
		return TeamDetail{}, false, errors.New("umami get team: no team id")
	}
	err = a.call(ctx, "get team", http.MethodGet, "/api/teams/"+url.PathEscape(id), nil, &t)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return TeamDetail{}, false, nil
	}
	if err != nil {
		return TeamDetail{}, false, err
	}
	if t.ID != id {
		return TeamDetail{}, false, errors.New("umami get team: response is for another id")
	}
	return t, true, nil
}

// page is umami's paged list answer (src/lib/prisma.ts pagedQuery).
type page[T any] struct {
	Data     []T `json:"data"`
	Count    int `json:"count"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// listPageSize is the page size of the admin list calls.
const listPageSize = 100

// listAll pages through an admin list route sorted by creation time,
// oldest first, so rows created while paging are appended after the pages
// already read and never shift an unread row onto a page already read.
func listAll[T any](ctx context.Context, a *Admin, op, path string, q url.Values) ([]T, error) {
	var out []T
	for p := 1; ; p++ {
		v := url.Values{}
		for k, vs := range q {
			v[k] = vs
		}
		v.Set("page", strconv.Itoa(p))
		v.Set("pageSize", strconv.Itoa(listPageSize))
		v.Set("orderBy", "createdAt")
		v.Set("sortDescending", "false")
		var pg page[T]
		if err := a.call(ctx, op, http.MethodGet, path+"?"+v.Encode(), nil, &pg); err != nil {
			return nil, err
		}
		if pg.Page != p || pg.PageSize != listPageSize {
			return nil, fmt.Errorf("umami %s: asked for page %d of %d, got page %d of %d", op, p, listPageSize, pg.Page, pg.PageSize)
		}
		out = append(out, pg.Data...)
		if len(pg.Data) < listPageSize || len(out) >= pg.Count {
			return out, nil
		}
	}
}

// ListUsers calls GET /api/admin/users (src/app/api/admin/users/route.ts)
// for every page: all users that are not deleted.
func (a *Admin) ListUsers(ctx context.Context) ([]User, error) {
	return listAll[User](ctx, a, "list users", "/api/admin/users", nil)
}

// FindTeamsByName calls GET /api/admin/teams with a search for name and
// returns the teams whose name equals it exactly. umami's search is a
// case-insensitive substring match, so the exact comparison happens here.
func (a *Admin) FindTeamsByName(ctx context.Context, name string) ([]Team, error) {
	if name == "" {
		return nil, errors.New("umami find teams: no name")
	}
	all, err := listAll[Team](ctx, a, "find teams", "/api/admin/teams", url.Values{"search": {name}})
	if err != nil {
		return nil, err
	}
	var out []Team
	for _, t := range all {
		if t.Name == name {
			out = append(out, t)
		}
	}
	return out, nil
}

// GetWebsite calls GET /api/websites/<id> (src/app/api/websites/[websiteId]/route.ts).
// ok is false when no such website exists.
func (a *Admin) GetWebsite(ctx context.Context, id string) (w Website, ok bool, err error) {
	if id == "" {
		return Website{}, false, errors.New("umami get website: no website id")
	}
	var raw json.RawMessage
	if err := a.call(ctx, "get website", http.MethodGet, "/api/websites/"+url.PathEscape(id), nil, &raw); err != nil {
		return Website{}, false, err
	}
	if isNull(raw) {
		return Website{}, false, nil
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Website{}, false, fmt.Errorf("umami get website: decode: %w", err)
	}
	if w.ID != id {
		return Website{}, false, errors.New("umami get website: response is for another id")
	}
	return w, true, nil
}

// WebsiteUpdate is the body of POST /api/websites/<id>: name and domain only.
type WebsiteUpdate struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

// UpdateWebsite calls POST /api/websites/<id>. an unknown id is a 400.
func (a *Admin) UpdateWebsite(ctx context.Context, id string, u WebsiteUpdate) (Website, error) {
	if id == "" || u.Name == "" || u.Domain == "" {
		return Website{}, errors.New("umami update website: id, name and domain are required")
	}
	var out Website
	if err := a.call(ctx, "update website", http.MethodPost, "/api/websites/"+url.PathEscape(id), u, &out); err != nil {
		return Website{}, err
	}
	if out.ID != id || out.Name != u.Name || out.Domain != u.Domain {
		return Website{}, errors.New("umami update website: response does not echo the change")
	}
	return out, nil
}

func isNull(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// Self returns the admin account's own user, through POST /api/auth/verify.
// the reconciler needs its id to tell the admin's team-owner membership of
// every tenant team from tenant members.
func (a *Admin) Self(ctx context.Context) (User, error) {
	var out User
	if err := a.call(ctx, "verify", http.MethodPost, "/api/auth/verify", nil, &out); err != nil {
		return User{}, err
	}
	if out.ID == "" || !out.IsAdmin {
		return User{}, errors.New("umami verify: admin session does not belong to an admin")
	}
	return out, nil
}
