package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// integrity tools (contract 12.4 and 17): the console's integrity routes,
// the same report for a project and for operators

const integrityDescription = "The integrity grade of web traffic over whole UTC days: A, B or C per site and day " +
	"with the reasons that lowered it, computed by the platform from what its edge observes (the network class of " +
	"each visitor's network, the distinct networks, the proof of work, the quarantine band) and checked against the " +
	"site's own baseline. The tracker on the page can only lower a grade, never raise it. The range grade is C when " +
	"at least a quarter of the graded site days are C, B when at least a quarter are B or C, A otherwise. Days with " +
	"too little traffic carry no grade."

func (s *Server) addIntegrityTenantTools(srv *mcp.Server) {
	addTool(s, srv, &mcp.Tool{
		Name:        "get_web_integrity",
		Title:       "Integrity grade of the project's web traffic",
		Description: integrityDescription + " This is the same report SCF operators see for the project.",
	}, nil, func(ctx context.Context, p Principal, in dayRangeIn) (any, error) {
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		var out json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/integrity", q, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
}

func (s *Server) addIntegrityOperatorTools(srv *mcp.Server) {
	type in struct {
		dayRangeIn
		TenantID string `json:"tenant_id" jsonschema:"tenant id from list_tenants"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_tenant_integrity",
		Title: "Integrity grade of one project",
		Description: integrityDescription + " get_global_web_overview carries each project's range grade and sorts " +
			"by it (sort integrity); this tool returns one project's reasons per site and day.",
	}, func(props map[string]*jsonschema.Schema) { uuidFormat(props, "tenant_id") }, func(ctx context.Context, p Principal, x in) (any, error) {
		id, err := parseUUID("tenant_id", x.TenantID)
		if err != nil {
			return nil, err
		}
		q := url.Values{}
		if err := x.values(q); err != nil {
			return nil, err
		}
		q.Set("tenant", id.String())
		return s.operatorRead(ctx, p, "/web/integrity", q)
	})
}
