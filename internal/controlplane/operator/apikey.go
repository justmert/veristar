package operator

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// operator api keys: sa_op_ plus 40 uniformly random base62
// characters, about 238 bits. only the sha-256 of the whole key is stored,
// with a short display prefix, so the database never holds a usable key.

// KeyPrefix starts every operator api key.
const KeyPrefix = "sa_op_"

// keyRandomLen is the number of base62 characters after KeyPrefix.
const keyRandomLen = 40

// keyDisplayLen is how much of a key is stored for display: the prefix and
// the first four random characters, enough to tell keys apart in a list
// and far too little to guess the other 36.
const keyDisplayLen = len(KeyPrefix) + 4

const keyAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// NewKey is a freshly generated key. Plain is shown to the operator once
// and then dropped; only Hash and Display are stored.
type NewKey struct {
	Plain   string
	Hash    [32]byte
	Display string
}

// GenerateKey makes a key from crypto/rand. rand.Int draws without modulo
// bias, so every character is uniform over the alphabet.
func GenerateKey() (NewKey, error) {
	var b strings.Builder
	b.Grow(len(KeyPrefix) + keyRandomLen)
	b.WriteString(KeyPrefix)
	n := big.NewInt(int64(len(keyAlphabet)))
	for range keyRandomLen {
		i, err := rand.Int(rand.Reader, n)
		if err != nil {
			return NewKey{}, fmt.Errorf("operator: api key random: %w", err)
		}
		b.WriteByte(keyAlphabet[i.Int64()])
	}
	plain := b.String()
	return NewKey{Plain: plain, Hash: sha256.Sum256([]byte(plain)), Display: plain[:keyDisplayLen]}, nil
}

// errKeyFormat is returned for an authorization value that cannot be a key.
var errKeyFormat = errors.New("not an operator api key")

// ParseBearer reads an Authorization header value and returns the sha-256
// of the key it carries. only the exact form "Bearer sa_op_<40 base62>" is
// accepted (the scheme is case-insensitive, rfc 9110 section 11.1); anything
// else is refused before the database is asked.
func ParseBearer(header string) ([32]byte, error) {
	scheme, key, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return [32]byte{}, errKeyFormat
	}
	if !validKey(key) {
		return [32]byte{}, errKeyFormat
	}
	return sha256.Sum256([]byte(key)), nil
}

func validKey(key string) bool {
	rest, ok := strings.CutPrefix(key, KeyPrefix)
	if !ok || len(rest) != keyRandomLen {
		return false
	}
	for i := range len(rest) {
		if strings.IndexByte(keyAlphabet, rest[i]) < 0 {
			return false
		}
	}
	return true
}
