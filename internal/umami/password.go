package umami

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// PasswordKey derives the daily umami passwords of tenant users.
// it formats as a fixed marker, so it never reaches a log line.
type PasswordKey struct{ k []byte }

// PasswordKeyLen is the size of UMAMI_USER_PASSWORD_KEY after base64 decoding.
const PasswordKeyLen = 32

// NewPasswordKey wraps the 32 bytes of UMAMI_USER_PASSWORD_KEY (config.Key32).
// a shorter key would weaken every password, so it is refused.
func NewPasswordKey(k []byte) (PasswordKey, error) {
	if len(k) != PasswordKeyLen {
		return PasswordKey{}, fmt.Errorf("umami password key: %d bytes, want %d", len(k), PasswordKeyLen)
	}
	return PasswordKey{k: append([]byte(nil), k...)}, nil
}

func (PasswordKey) String() string   { return redacted }
func (PasswordKey) GoString() string { return redacted }

// Empty reports whether the key was never set.
func (p PasswordKey) Empty() bool { return len(p.k) == 0 }

// EpochDay is the utc day number of t: whole days since 1970-01-01. times
// before 1970 never occur here, so truncating division is exact.
func EpochDay(t time.Time) int64 {
	return t.Unix() / 86400
}

// Password returns the password of umami user userID for epoch day day:
// base64url(HMAC-SHA256(key, userID || 0x00 || day as 8 bytes big-endian)),
// unpadded. the separator keeps an id and a day from ever forming the same
// input as another pair. the result has 43 characters, inside umami's 8 to
// 255 limit. only the umami-rotate task sets it and provisioning sets it
// once at user creation; sso only logs in with it.
func (p PasswordKey) Password(userID string, day int64) (string, error) {
	if p.Empty() {
		return "", errors.New("umami password: no key")
	}
	if userID == "" || day <= 0 {
		return "", errors.New("umami password: user id and a positive day are required")
	}
	m := hmac.New(sha256.New, p.k)
	m.Write([]byte(userID))
	m.Write([]byte{0})
	var d [8]byte
	binary.BigEndian.PutUint64(d[:], uint64(day))
	m.Write(d[:])
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}
