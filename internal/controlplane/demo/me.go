package demo

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/controlplane/terms"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
)

// DisplayName is the demo viewer's name in the console's account menu.
const DisplayName = "Demo"

// maxProjects bounds the demo's project list; demo projects are a handful
// an operator marks by hand
const maxProjects = 50

// Me is the answer of GET /api/demo/me: the shape of GET /api/me, so the
// console needs no second model of its user, and demo true.
type Me struct {
	auth.Me
	Demo bool `json:"demo"`
}

type me struct{ db *db.DB }

// get answers the demo viewer: no identity, the operator views without
// the admin pages (role operator), and the live demo projects with role
// viewer.
func (m *me) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	t, err := terms.Current()
	if err != nil {
		return err
	}
	var rows []systemq.CpTenant
	err = m.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = systemq.New(tx).ListDemoTenantsPage(ctx, systemq.ListDemoTenantsPageParams{RowLimit: maxProjects})
		return qerr
	})
	if err != nil {
		return err
	}
	role := "operator"
	out := Me{
		Me: auth.Me{
			User: auth.MeUser{
				ID: session.DemoUserID, DisplayName: DisplayName, IsOperator: true, OperatorRole: &role,
			},
			Tenants:        make([]auth.MeTenant, 0, len(rows)),
			RemovedTenants: []auth.MeRemovedTenant{},
			Terms:          auth.MeTerms{CurrentVersion: t.Version, Status: t.Status, URL: terms.URL},
		},
		Demo: true,
	}
	for _, row := range rows {
		out.Tenants = append(out.Tenants, auth.MeTenant{
			ID: row.ID, Slug: row.Slug, Name: row.Name, Status: row.Status, Role: tenants.RoleViewer,
			AcceptedTermsVersion: row.AcceptedTermsVersion,
			TermsCurrent:         row.AcceptedTermsVersion != nil && *row.AcceptedTermsVersion == t.Version,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
