package onchain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// the two-way link holds for a proven account on a public network file only.
func TestLinkNeedsVerifiedAccountAndPublicNetwork(t *testing.T) {
	const account = "GDWQ7YV27AVTL25BDOLZD6E44VCQLCEBJXBOOWA2R3VLKCUIZHSXZLDE"
	toml := func(network *string) []byte {
		b, err := json.Marshal(map[string]any{
			"status": "found", "network": network, "org_url": "https://example.org", "org_url_same_domain": true,
			"accounts": []string{account},
		})
		require.NoError(t, err)
		return b
	}
	home := "example.org"
	now := time.Now()
	verified := idRow{Strkey: account, Kind: "account", HomeDomain: &home, VerifiedAt: &now}
	unverified := verified
	unverified.VerifiedAt = nil
	public, testnet := "public", "testnet"
	for _, tc := range []struct {
		name    string
		network *string
		id      idRow
		want    bool
	}{
		{"verified, no passphrase", nil, verified, true},
		{"verified, public", &public, verified, true},
		{"verified, testnet", &testnet, verified, false},
		{"declared only", nil, unverified, false},
	} {
		site := siteRow{ID: uuid.New(), Domain: "example.org", Verified: true, Toml: toml(tc.network)}
		l, err := buildLink([]siteRow{site}, []idRow{tc.id})
		require.NoError(t, err)
		require.Equal(t, tc.want, l.TwoWay, tc.name)
		require.Equal(t, tc.want, l.Sites[0].Accounts[0].TwoWay, tc.name)
	}
}
