package identity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	addrA = "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN"
	addrB = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"
	// a soropass account address (passkey identity)
	addrC = "CBH2J5X4WBV2KT6NSUE47DRFBUHTVJOBJTBGGNELJ2DZXTDB3AYRAXAO"
)

func ptr[T any](v T) *T { return &v }

func TestOperatorsIs(t *testing.T) {
	o := Operators{GitHub: map[int64]bool{7: true, 8: false}, Stellar: map[string]bool{addrA: true}}
	require.True(t, o.Is(ptr(int64(7)), nil))
	require.False(t, o.Is(ptr(int64(8)), nil))
	require.True(t, o.Is(nil, ptr(addrA)))
	require.True(t, o.Is(ptr(int64(9)), ptr(addrA)))
	require.False(t, o.Is(ptr(int64(9)), ptr(addrB)))
	require.False(t, o.Is(nil, nil))
	require.ElementsMatch(t, []int64{7}, o.GitHubIDs())
	require.ElementsMatch(t, []string{addrA}, o.StellarAddresses())
	require.False(t, Operators{}.Is(ptr(int64(7)), ptr(addrA)))

	// a listed C-address makes a passkey account an operator, whichever
	// address slot carries it
	p := Operators{Stellar: map[string]bool{addrC: true}}
	require.True(t, p.Is(nil, nil, ptr(addrC)))
	require.True(t, p.Is(nil, ptr(addrA), ptr(addrC)))
	require.False(t, p.Is(nil, ptr(addrA), nil))
	require.False(t, p.Is(nil))
}

func TestValidContractAddress(t *testing.T) {
	require.True(t, ValidContractAddress(addrC))
	require.True(t, ValidPersonAddress(addrC))
	require.True(t, ValidPersonAddress(addrA))
	require.False(t, ValidContractAddress(addrA))
	require.False(t, ValidContractAddress(addrC[:55]+"A"))
	require.False(t, ValidPersonAddress("MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAGZFQ"))
	require.False(t, ValidPersonAddress(""))
}

func TestValidAddress(t *testing.T) {
	require.True(t, ValidAddress(addrA))
	require.True(t, ValidAddress(addrB))
	// checksum, muxed account, seed, contract, lowercase
	require.False(t, ValidAddress(addrA[:55]+"A"))
	require.False(t, ValidAddress("MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAGZFQ"))
	require.False(t, ValidAddress("SBA5JEQJ4UM5GXJYEP4XMBHGQEIB4AHQN3D4G3CT6Z3CJAYSV4OOUO3T"))
	require.False(t, ValidAddress("CA3D5KRYM6CB7OWQ6TWYRR3Z4T7GNZLKERYNZGGA5SOAOPIFY6YQGAXE"))
	require.False(t, ValidAddress(""))
}

func TestDisplayName(t *testing.T) {
	require.Equal(t, "octocat", DisplayName(ptr("octocat"), ptr(addrA)))
	require.Equal(t, "GCB6…WOQN", DisplayName(nil, ptr(addrA)))
	require.Equal(t, "GCB6…WOQN", DisplayName(ptr(""), ptr(addrA)))
	require.Empty(t, DisplayName(nil, nil))
	require.Equal(t, "CBH2…AXAO", DisplayName(nil, nil, ptr(addrC)))
	require.Equal(t, "GCB6…WOQN", DisplayName(nil, ptr(addrA), ptr(addrC)))
	require.Empty(t, DisplayName(nil, nil, nil))
	require.Equal(t, "short", ShortAddress("short"))
}
