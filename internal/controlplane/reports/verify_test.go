package reports_test

import (
	"encoding/json"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/canonjson"
	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/controlplane/reports"
)

// a report stored in format version 1 keeps its layout (integrity_rules a
// number, no coverage or weekly active accounts) and still verifies.
func TestVersion1ReportStillVerifies(t *testing.T) {
	kp := keypair.MustRandom()
	v1 := map[string]any{
		"format": reports.Format, "version": 1, "id": "abcdefghijklmnopqrstuvwxyz",
		"issuer":       map[string]any{"name": "Veristar", "console": "https://app.veristar.app", "stellar_toml": "https://app.veristar.app/.well-known/stellar.toml", "signing_key": kp.Address()},
		"project":      map[string]any{"id": "p", "slug": "s", "name": "n"},
		"period":       map[string]any{"from": "2026-10-01", "to": "2026-10-31", "days": 31},
		"generated_at": "2026-11-16T09:00:00Z",
		"methods": map[string]any{"format": 1, "web_metrics": 1, "api_metrics": 2, "onchain": 1, "cohort": 1,
			"integrity_rules": 1, "route_normaliser": 1, "platform": "v0.3.0"},
		"web": map[string]any{"pageviews": 9}, "api": map[string]any{"requests": 0},
		"onchain": map[string]any{"ids": 0}, "integrity": map[string]any{"grade": nil},
		"cohort": map[string]any{"kpis": map[string]any{}}, "rules_met": []any{},
	}
	body, err := canonjson.Marshal(v1)
	require.NoError(t, err)
	sig, err := chain.SignSEP53(kp, body)
	require.NoError(t, err)
	file, err := json.Marshal(reports.File{Report: body, Signature: reports.Signature{Scheme: reports.SignatureScheme, Signer: kp.Address(), Value: sig}})
	require.NoError(t, err)
	v, err := reports.Verify(file)
	require.NoError(t, err)
	require.Equal(t, 1, v.Version)
	require.Equal(t, kp.Address(), v.Signer)
}

// a report stored in format version 2 (no onchain.contracts) still
// verifies after version 3.
func TestVersion2ReportStillVerifies(t *testing.T) {
	kp := keypair.MustRandom()
	v2 := map[string]any{
		"format": reports.Format, "version": 2, "id": "abcdefghijklmnopqrstuvwxy2",
		"issuer":       map[string]any{"name": "Veristar", "console": "https://app.veristar.app", "stellar_toml": "https://app.veristar.app/.well-known/stellar.toml", "signing_key": kp.Address()},
		"project":      map[string]any{"id": "p", "slug": "s", "name": "n"},
		"period":       map[string]any{"from": "2026-10-01", "to": "2026-10-31", "days": 31},
		"generated_at": "2026-11-16T09:00:00Z",
		"methods": map[string]any{"format": 2, "web_metrics": 1, "api_metrics": 2, "onchain": 1, "cohort": 1,
			"integrity_rules": []any{1, 2}, "route_normaliser": 1, "platform": "v0.4.0"},
		"web": map[string]any{"pageviews": 9, "has_data": true}, "api": map[string]any{"requests": 0, "has_data": false},
		"onchain": map[string]any{"ids": 1, "weekly_active_accounts": map[string]any{"average": nil}},
		"integrity": map[string]any{"grade": nil}, "cohort": map[string]any{"kpis": map[string]any{}}, "rules_met": []any{},
	}
	body, err := canonjson.Marshal(v2)
	require.NoError(t, err)
	sig, err := chain.SignSEP53(kp, body)
	require.NoError(t, err)
	file, err := json.Marshal(reports.File{Report: body, Signature: reports.Signature{Scheme: reports.SignatureScheme, Signer: kp.Address(), Value: sig}})
	require.NoError(t, err)
	v, err := reports.Verify(file)
	require.NoError(t, err)
	require.Equal(t, 2, v.Version)
}
