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
			"accounts' home domain. " +
			"Weekly active accounts follow the SCF Growth Hack definition: the distinct accounts that initiated at least " +
			"one operation involving the project's verified accounts or contracts in an ISO week (UTC); a week " +
			"without a count carries the reason it is missing and is never a zero. Each verified contract carries its " +
			"calls per function over the range; get_contract_functions gives them per day.",
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
	type idRangeIn struct {
		dayRangeIn
		ChainID string `json:"chain_id" jsonschema:"declared id from get_onchain_activity"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_contract_functions",
		Title: "Calls per function of a contract",
		Description: "Calls into one declared Soroban contract per function and UTC day: successful calls, failed calls and " +
			"the distinct accounts that initiated them, split by caller: account (the operation invokes the contract) or " +
			"contract (another contract calls it during an invocation). Failed is null for calls of accounts: the " +
			"transactions read all succeeded, because a failed transaction keeps no contract events. Only verified contracts " +
			"have calls; an unverified contract or an account answers with every day missing and the reason. A day whose " +
			"calls by a caller were not read has null numbers, never zeros. At most 64 functions per day keep their own " +
			"row; the rest add up under {other}. Take chain_id from the ids of get_onchain_activity.",
	}, func(props map[string]*jsonschema.Schema) { uuidFormat(props, "chain_id") }, func(ctx context.Context, p Principal, in idRangeIn) (any, error) {
		id, err := parseUUID("chain_id", in.ChainID)
		if err != nil {
			return nil, err
		}
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		var out json.RawMessage
		if err := s.read(ctx, p, "/tenants/"+p.TenantID.String()+"/onchain/ids/"+id.String()+"/functions", q, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	addTool(s, srv, &mcp.Tool{
		Name:  "get_scf_view",
		Title: "What SCF sees",
		Description: "The figures the SCF operator views show about this project over whole UTC days, from the " +
			"same queries: its row of the global web and API views, its on-chain summary with the weekly active " +
			"accounts, and the daily series of each. Use it to see the project exactly as reviewers do.",
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
			"link, and the weekly active accounts per ISO week. The global overviews and compares carry the same " +
			"numbers as an onchain summary per project. " +
			"Weekly active accounts follow the SCF Growth Hack definition: the distinct accounts that initiated at least " +
			"one operation involving the project's verified accounts or contracts in an ISO week (UTC); a week " +
			"without a count carries the reason it is missing and is never a zero.",
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
	type tenantIDRangeIn struct {
		dayRangeIn
		TenantID string `json:"tenant_id" jsonschema:"tenant id from list_tenants"`
		ChainID  string `json:"chain_id" jsonschema:"declared id from get_tenant_onchain"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_tenant_contract_functions",
		Title: "Calls per function of a project's contract",
		Description: "Calls into one declared Soroban contract per function and UTC day: successful calls, failed calls and " +
			"the distinct accounts that initiated them, split by caller: account (the operation invokes the contract) or " +
			"contract (another contract calls it during an invocation). Failed is null for calls of accounts: the " +
			"transactions read all succeeded, because a failed transaction keeps no contract events. Only verified contracts " +
			"have calls; an unverified contract or an account answers with every day missing and the reason. A day whose " +
			"calls by a caller were not read has null numbers, never zeros. At most 64 functions per day keep their own " +
			"row; the rest add up under {other}. Take chain_id from the ids of get_tenant_onchain.",
	}, func(props map[string]*jsonschema.Schema) { uuidFormat(props, "tenant_id", "chain_id") }, func(ctx context.Context, p Principal, in tenantIDRangeIn) (any, error) {
		id, err := parseUUID("tenant_id", in.TenantID)
		if err != nil {
			return nil, err
		}
		chainID, err := parseUUID("chain_id", in.ChainID)
		if err != nil {
			return nil, err
		}
		q := url.Values{}
		if err := in.values(q); err != nil {
			return nil, err
		}
		return s.operatorRead(ctx, p, "/tenants/"+id.String()+"/onchain/ids/"+chainID.String()+"/functions", q)
	})
}
