package umami

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"testing"
	"time"
)

func TestPasswordDerivation(t *testing.T) {
	raw := make([]byte, PasswordKeyLen)
	for i := range raw {
		raw[i] = byte(i)
	}
	if _, err := NewPasswordKey(raw[:31]); err == nil {
		t.Fatal("31-byte key accepted")
	}
	k, err := NewPasswordKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 99 // the key keeps its own copy
	const user = "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10"
	day := EpochDay(time.Date(2026, 11, 12, 23, 59, 59, 0, time.UTC))
	if day != 20769 {
		t.Fatalf("epoch day %d", day)
	}
	if EpochDay(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)) != day+1 {
		t.Fatal("midnight does not start a new day")
	}
	pw, err := k.Password(user, day)
	if err != nil {
		t.Fatal(err)
	}
	// the documented construction, computed independently
	m := hmac.New(sha256.New, func() []byte {
		b := make([]byte, 32)
		for i := range b {
			b[i] = byte(i)
		}
		return b
	}())
	m.Write([]byte(user))
	m.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0x51, 0x21}) // separator, then 20769 as 8 bytes big-endian
	if want := base64.RawURLEncoding.EncodeToString(m.Sum(nil)); pw != want {
		t.Fatalf("password %q, want %q", pw, want)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(pw) {
		t.Fatalf("password %q is not 43 url-safe characters", pw)
	}
	next, _ := k.Password(user, day+1)
	other, _ := k.Password("0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a11", day)
	if next == pw || other == pw {
		t.Fatal("password does not depend on the day and the user")
	}
	if _, err := (PasswordKey{}).Password(user, day); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := k.Password("", day); err == nil {
		t.Fatal("empty user accepted")
	}
	if s := fmt.Sprintf("%v %+v %#v", k, k, k); s != "[redacted] [redacted] [redacted]" {
		t.Fatalf("key printed as %q", s)
	}
}
