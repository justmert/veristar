// Package mcpserver is the model context protocol server of the control
// plane (contract section 12, docs/guides/mcp.mdx). ai clients connect to
// https://app.<BASE_DOMAIN>/mcp over streamable http with a bearer
// credential that acts as one console user: an mcp access key made in the
// console, or an oauth access token from the authorization flow of the mcp
// specification, which this package also serves. every tool is read-only.
// tenant tools call the console's own read handlers in process as that
// user, and umami as that user's umami user, so membership, row-level
// security and range limits are the console's own; operator tools need the
// operator flag, read again on every request.
package mcpserver

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
)

// secret prefixes. every secret is the prefix plus 40 uniformly random
// base62 characters, about 238 bits; the database holds only its sha-256.
const (
	// KeyPrefix starts an mcp access key made in the console.
	KeyPrefix = "sa_mcp_"
	// AccessTokenPrefix starts an oauth access token.
	AccessTokenPrefix = "sa_mcpat_" //nolint:gosec // a prefix, not a credential
	// RefreshTokenPrefix starts an oauth refresh token.
	RefreshTokenPrefix = "sa_mcprt_" //nolint:gosec // a prefix, not a credential
	// codePrefix starts an authorization code.
	codePrefix = "sa_mcpac_"
	// clientPrefix starts a registered client id, which is public.
	clientPrefix = "mcpc_"
)

const (
	secretRandomLen = 40
	clientRandomLen = 32
	// keyDisplayLen is what the console shows of a key: the prefix and four
	// random characters, enough to tell keys apart, far too little to guess
	keyDisplayLen = len(KeyPrefix) + 4
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// randomString draws n characters of alphabet from crypto/rand without
// modulo bias.
func randomString(n int) (string, error) {
	var b strings.Builder
	b.Grow(n)
	m := big.NewInt(int64(len(alphabet)))
	for range n {
		i, err := rand.Int(rand.Reader, m)
		if err != nil {
			return "", fmt.Errorf("mcpserver: random: %w", err)
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String(), nil
}

// newSecret is prefix plus 40 random characters, with its hash.
func newSecret(prefix string) (plain string, hash [32]byte, err error) {
	r, err := randomString(secretRandomLen)
	if err != nil {
		return "", hash, err
	}
	plain = prefix + r
	return plain, sha256.Sum256([]byte(plain)), nil
}

// parseSecret returns the hash of s when it has the exact form prefix plus
// 40 base62 characters, so nothing else ever reaches the database.
func parseSecret(s, prefix string) ([32]byte, bool) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok || len(rest) != secretRandomLen {
		return [32]byte{}, false
	}
	for i := range len(rest) {
		if strings.IndexByte(alphabet, rest[i]) < 0 {
			return [32]byte{}, false
		}
	}
	return sha256.Sum256([]byte(s)), true
}

// validClientID reports whether s has the form of a registered client id.
func validClientID(s string) bool {
	rest, ok := strings.CutPrefix(s, clientPrefix)
	if !ok || len(rest) != clientRandomLen {
		return false
	}
	for i := range len(rest) {
		if strings.IndexByte(alphabet, rest[i]) < 0 {
			return false
		}
	}
	return true
}
