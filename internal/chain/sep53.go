// Package chain reads public stellar ledger data for the on-chain witness
// (control-plane-api section 15): daily activity of the accounts and
// contracts a project declares, the proofs that the project controls them,
// and the home domain of its accounts. every endpoint is configured by the
// operator; nothing here talks to a tenant-supplied host.
package chain

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// sep53Prefix is the fixed prefix of sep-53 (sign and verify messages):
// the signer hashes prefix and message with sha-256 and signs the hash
// with ed25519, so a signed message can never be a transaction.
const sep53Prefix = "Stellar Signed Message:\n"

// SEP53Hash is the 32 bytes a sep-53 signer signs for message.
func SEP53Hash(message string) [32]byte {
	return sha256.Sum256([]byte(sep53Prefix + message))
}

// ErrBadSignature is a signature that does not verify, or is not a
// signature at all.
var ErrBadSignature = errors.New("chain: the signature does not verify for this account and message")

// VerifySEP53 checks a sep-53 signature of message by account (a g
// address). wallets return the 64-byte signature base64 encoded; hex is
// accepted too, because some wallets and the stellar cli print it so.
func VerifySEP53(account, message, signature string) error {
	if !strkey.IsValidEd25519PublicKey(account) {
		return errors.New("chain: sep-53 needs a g account")
	}
	sig, ok := decodeSignature(strings.TrimSpace(signature))
	if !ok {
		return ErrBadSignature
	}
	kp, err := keypair.ParseAddress(account)
	if err != nil {
		return err
	}
	h := SEP53Hash(message)
	if kp.Verify(h[:], sig) != nil {
		return ErrBadSignature
	}
	return nil
}

func decodeSignature(s string) ([]byte, bool) {
	if len(s) == 128 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, true
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 64 {
			return b, true
		}
	}
	return nil, false
}
