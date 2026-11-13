// Package otlptokens serves the otlp token routes (control-plane-api 6)
// and holds the rule that turns a token row and its tenant's status into
// the registry_otlp entry the collector reads. the routes, the reconciler
// and the removal purge all publish through Entry, so they never disagree.
package otlptokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/registry"
)

// Prefix starts every token.
const Prefix = "sa_otlp_"

// randomLen is the number of base62 characters after Prefix.
const randomLen = 40

// displayLen is how much of a token is stored for display: the prefix and
// the first four random characters, 24 bits, enough to tell tokens apart
// and far too little to guess the other 36 characters.
const displayLen = len(Prefix) + 4

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// statuses of the cp.otlp_tokens check
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// tenant status that lets a token through
const tenantActive = "active"

// Generated is a new token. Plain is returned to the caller once and then
// dropped; only Hash and Display are stored.
type Generated struct {
	Plain   string
	Hash    [32]byte
	Display string
}

// HexHash is the registry key of the token.
func (g Generated) HexHash() string { return hex.EncodeToString(g.Hash[:]) }

// Generate makes a token of 40 uniformly random base62 characters.
// rand.Int draws without modulo bias.
func Generate() (Generated, error) {
	var b strings.Builder
	b.Grow(len(Prefix) + randomLen)
	b.WriteString(Prefix)
	max := big.NewInt(int64(len(base62)))
	for range randomLen {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return Generated{}, fmt.Errorf("otlptokens: random: %w", err)
		}
		b.WriteByte(base62[n.Int64()])
	}
	plain := b.String()
	return Generated{Plain: plain, Hash: sha256.Sum256([]byte(plain)), Display: plain[:displayLen]}, nil
}

// RegistryStatus is the status the collector sees for a token. a revoked
// token is revoked whatever its tenant does; an active token takes traffic
// only while its tenant is active, otherwise the tenant's status (pending,
// rejected, suspended, removed) is published, which the collector refuses.
func RegistryStatus(tokenStatus, tenantStatus string) string {
	if tokenStatus != StatusActive {
		return StatusRevoked
	}
	if tenantStatus != tenantActive {
		return tenantStatus
	}
	return registry.StatusActive
}

// Entry builds the registry_otlp entry of a token row.
func Entry(tenantID, tokenID uuid.UUID, hash []byte, tokenStatus, tenantStatus string) (registry.OTLPToken, error) {
	if len(hash) != sha256.Size {
		return registry.OTLPToken{}, errors.New("otlptokens: stored hash is not 32 bytes")
	}
	return registry.OTLPToken{
		Hash:     hex.EncodeToString(hash),
		TenantID: tenantID.String(),
		TokenID:  tokenID.String(),
		Status:   RegistryStatus(tokenStatus, tenantStatus),
	}, nil
}
