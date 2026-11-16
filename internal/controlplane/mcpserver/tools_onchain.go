package mcpserver

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// the on-chain witness and "what scf sees" (contract 15 and 16.3), read
// through the console's own routes

func (s *Server) addOnchainTenantTools(srv *mcp.Server) {
	addTool(s, srv, &mcp.Tool{
		Name:  "get_onchain_activity",
		Title: "On-chain activity of the project",
		Description: "The Stellar accounts (G...) and Soroban contracts (C...) the project declared, whether the " +
			"project proved control of each, and their ledger activity per UTC day: operations, transactions and " +
			"distinct counterparties. Accounts are read from Horizon, contracts from the events they emit through " +
			"Stellar RPC, which keeps about seven days. A day without numbers carries the reason it is missing; it " +
			"is never a zero. Also returns the two-way link between the verified domain's stellar.toml and the " +
			"accounts' home domain.",
	}, nil, func(ctx context.Context, p Principal, in dayRangeIn) (any, error) {
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		var out json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/onchain", q, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	addTool(s, srv, &mcp.Tool{
		Name:  "get_scf_view",
		Title: "What SCF sees",
		Description: "The figures the SCF operator views show about this project over whole UTC days, from the " +
			"same queries: its row of the global web and API views, its on-chain summary and the daily series of " +
			"each. Use it to see the project exactly as reviewers do.",
	}, nil, func(ctx context.Context, p Principal, in dayRangeIn) (any, error) {
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		var out json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/scf-view", q, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
}

func (s *Server) addOnchainOperatorTools(srv *mcp.Server) {
	type tenantRangeIn struct {
		dayRangeIn
		TenantID string `json:"tenant_id" jsonschema:"tenant id from list_tenants"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_tenant_onchain",
		Title: "On-chain activity of one project",
		Description: "One project's declared Stellar accounts and Soroban contracts, the proof of control of each " +
			"(SEP-53 signature, manage-data entry, contract admin or deployer), their daily operations, " +
			"transactions and counterparties with the reason for any missing day, and the two-way stellar.toml " +
			"link. The global overviews and compares carry the same numbers as an onchain summary per project.",
	}, func(props map[string]*jsonschema.Schema) { uuidFormat(props, "tenant_id") }, func(ctx context.Context, p Principal, in tenantRangeIn) (any, error) {
		id, err := parseUUID("tenant_id", in.TenantID)
		if err != nil {
			return nil, err
		}
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		return s.operatorRead(ctx, p, "/tenants/"+id.String()+"/onchain", q)
	})
}
