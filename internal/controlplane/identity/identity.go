// Package identity holds what the console knows about how a person signs in:
// a github account, a stellar account (a G-address), or both linked on one
// cp.users row (contract 4.1.7 and 4.1.8). it decides the operator flag from
// both identities and the display name of an account.
package identity

import (
	"github.com/stellar/go-stellar-sdk/strkey"
)

// Operators are the configured operator identities: OPERATOR_GITHUB_IDS and
// OPERATOR_STELLAR_ADDRESSES (contract 5.1).
type Operators struct {
	GitHub  map[int64]bool
	Stellar map[string]bool
}

// Is reports whether an account with these identities is an operator: one
// listed identity is enough. a nil identity matches nothing.
func (o Operators) Is(githubID *int64, address *string) bool {
	if githubID != nil && o.GitHub[*githubID] {
		return true
	}
	return address != nil && o.Stellar[*address]
}

// GitHubIDs returns the listed github ids, for the startup sync.
func (o Operators) GitHubIDs() []int64 {
	out := make([]int64, 0, len(o.GitHub))
	for id, ok := range o.GitHub {
		if ok {
			out = append(out, id)
		}
	}
	return out
}

// StellarAddresses returns the listed addresses, for the startup sync.
func (o Operators) StellarAddresses() []string {
	out := make([]string, 0, len(o.Stellar))
	for a, ok := range o.Stellar {
		if ok {
			out = append(out, a)
		}
	}
	return out
}

// ValidAddress reports whether s is a stellar account id (G-address) with a
// correct checksum. muxed (M...) addresses and other strkey kinds are not
// accounts a person signs in with.
func ValidAddress(s string) bool {
	return strkey.IsValidEd25519PublicKey(s)
}

// ShortAddress is the first and last four characters of an address with an
// ellipsis between, as wallets show one. the console's shortAddress
// (apps/console/src/lib/stellar-wallet) uses the same form.
func ShortAddress(a string) string {
	if len(a) <= 12 {
		return a
	}
	return a[:4] + "…" + a[len(a)-4:]
}

// DisplayName is how the console names an account: the github login when
// one is linked, the short address of a wallet-only account, and "" for an
// account with neither (a deleted one).
func DisplayName(githubLogin, address *string) string {
	if githubLogin != nil && *githubLogin != "" {
		return *githubLogin
	}
	if address != nil {
		return ShortAddress(*address)
	}
	return ""
}
