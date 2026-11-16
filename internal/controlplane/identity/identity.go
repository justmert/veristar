// Package identity holds what the console knows about how a person signs in:
// a github account, a stellar account (a G-address), a soropass passkey (its
// account's C-address), or any of them linked on one cp.users row (contract
// 4.1.7 to 4.1.9). it decides the operator flag from every identity and the
// display name of an account.
package identity

import (
	"github.com/stellar/go-stellar-sdk/strkey"
)

// Operators are the configured operator identities: OPERATOR_GITHUB_IDS and
// OPERATOR_STELLAR_ADDRESSES (contract 5.1). a listed G-address names a
// wallet account, a listed C-address a passkey account.
type Operators struct {
	GitHub  map[int64]bool
	Stellar map[string]bool
}

// Is reports whether an account with these identities is an operator: one
// listed identity is enough. addresses are the account's stellar address
// and passkey address; a nil identity matches nothing.
func (o Operators) Is(githubID *int64, addresses ...*string) bool {
	if githubID != nil && o.GitHub[*githubID] {
		return true
	}
	for _, a := range addresses {
		if a != nil && o.Stellar[*a] {
			return true
		}
	}
	return false
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

// ValidContractAddress reports whether s is a contract address (C-address)
// with a correct checksum, the identity of a passkey account.
func ValidContractAddress(s string) bool {
	_, err := strkey.Decode(strkey.VersionByteContract, s)
	return err == nil
}

// ValidPersonAddress reports whether s names an account a person signs in
// with: a wallet's G-address or a passkey account's C-address.
func ValidPersonAddress(s string) bool {
	return ValidAddress(s) || ValidContractAddress(s)
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
// one is linked, else the short form of the first address given (the
// wallet's, then the passkey account's), and "" for an account with none (a
// deleted one).
func DisplayName(githubLogin *string, addresses ...*string) string {
	if githubLogin != nil && *githubLogin != "" {
		return *githubLogin
	}
	for _, a := range addresses {
		if a != nil {
			return ShortAddress(*a)
		}
	}
	return ""
}
