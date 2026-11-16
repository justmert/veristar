package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/controlplane/umamiread"
	"github.com/justmert/veristar/internal/umami"
)

// web analytics tools read umami's rest api on the private network as the
// principal's own umami user (package umamiread). a site id from the client
// is first read through the console's site route as the principal, which
// answers only for a site of the principal's tenant; only then is the umami
// website id derived from it, so no website id a client sends ever reaches
// umami.

// siteRef is the part of the console's site object the web tools need.
type siteRef struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	Name                string     `json:"name"`
	Domain              string     `json:"domain"`
	Status              string     `json:"status"`
	QuarantineWebsiteID *uuid.UUID `json:"quarantine_website_id"`
	CollectUTM          bool       `json:"collect_utm"`
}

// website is one checked umami website of a site.
type website struct {
	site siteRef
	view string
	id   string
}

// resolveSite checks a client's site id through the console's site route.
func (s *Server) resolveSite(ctx context.Context, p Principal, rawSite, view string) (website, error) {
	siteID, err := parseUUID("site_id", rawSite)
	if err != nil {
		return website{}, err
	}
	switch view {
	case "":
		view = "primary"
	case "primary", "quarantine":
	default:
		return website{}, invalid("view must be primary or quarantine")
	}
	var site siteRef
	if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/sites/"+siteID.String(), nil, &site); err != nil {
		return website{}, err
	}
	if site.ID != siteID || site.TenantID != p.TenantID {
		return website{}, fmt.Errorf("mcpserver: site route answered site %s of tenant %s for %s", site.ID, site.TenantID, siteID)
	}
	switch site.Status {
	case "removed":
		return website{}, &toolError{Code: outcomeNotFound, Message: "this site was removed"}
	case "pending":
		return website{}, &toolError{Code: "provisioning_pending", Message: "this site is in review and has no analytics yet"}
	}
	w := website{site: site, view: view, id: site.ID.String()}
	if view == "quarantine" {
		if site.QuarantineWebsiteID == nil {
			return website{}, &toolError{Code: "provisioning_pending", Message: "the quarantine view of this site is not set up yet"}
		}
		w.id = site.QuarantineWebsiteID.String()
	}
	return w, nil
}

// umamiRead reads a website route of w as the principal's umami user.
func (s *Server) umamiRead(ctx context.Context, p Principal, w website, sub string, q url.Values, out any) error {
	err := s.web.Read(ctx, p.User.ID, umamiread.Website{Tenant: p.TenantID, Site: w.site.ID, ID: w.id}, sub, q, out)
	if err == nil {
		return nil
	}
	if _, ok := umamiread.FailureCode(err); ok {
		return umamiSessionError(err)
	}
	var ue *umamiread.UmamiError
	if !errors.As(err, &ue) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch ue.Status() {
	case 404:
		return &toolError{Code: outcomeNotFound, Message: "umami does not know this report for the site"}
	case 400:
		var ae *umami.APIError
		errors.As(err, &ae)
		return &toolError{Code: outcomeInvalid, Message: "umami refused the parameters: " + ae.Message}
	case 401, 403:
		return &toolError{Code: outcomeForbidden, Message: "umami refused access to this site for this user"}
	}
	return &toolError{Code: outcomeUnavailable, Message: "web analytics are unreachable right now; try again shortly"}
}

// umamiSessionError maps the member session failure codes to tool errors.
func umamiSessionError(err error) error {
	code, ok := umamiread.FailureCode(err)
	if !ok {
		return err
	}
	switch code {
	case umamiread.CodeNotFound:
		return &toolError{Code: outcomeNotFound, Message: "not found: the site is unknown or not part of this project"}
	case umamiread.CodeProvisioningPending:
		return &toolError{Code: "provisioning_pending", Message: "web analytics for this site are still being set up; try again in a few minutes"}
	case umamiread.CodeUmamiUnavailable:
		return &toolError{Code: outcomeUnavailable, Message: "web analytics are unreachable right now; try again shortly"}
	}
	return fmt.Errorf("mcpserver: umami session: %s", code)
}
