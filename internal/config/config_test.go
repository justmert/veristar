package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestMissingKeyIsNamed(t *testing.T) {
	_, err := LoadUmamiAdmin(FromMap(map[string]string{
		"UMAMI_URL": "http://umami:3000",
	}))
	if err == nil {
		t.Fatal("expected an error for the missing password")
	}
	var ke *KeyError
	if !errors.As(err, &ke) || ke.Key != "UMAMI_ADMIN_PASSWORD" {
		t.Fatalf("error does not name UMAMI_ADMIN_PASSWORD: %v", err)
	}
	if !strings.Contains(err.Error(), "UMAMI_ADMIN_PASSWORD is required") {
		t.Fatalf("message does not name the key: %q", err.Error())
	}
}

func TestEveryMissingKeyIsReported(t *testing.T) {
	_, err := LoadUmamiAdmin(FromMap(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, key := range []string{"UMAMI_URL", "UMAMI_ADMIN_PASSWORD"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %q", key, err.Error())
		}
	}
}

func TestEmptyValueCountsAsMissing(t *testing.T) {
	e := FromMap(map[string]string{"A": "   "})
	_ = e.String("A")
	if err := e.Err(); err == nil || !strings.Contains(err.Error(), "A is required") {
		t.Fatalf("blank value accepted: %v", err)
	}
}

func TestInvalidValuesAreNamed(t *testing.T) {
	e := FromMap(map[string]string{
		"U": "umami:3000",
		"D": "soon",
		"N": "70000",
		"S": "short",
	})
	_ = e.URL("U")
	_ = e.Duration("D")
	_ = e.Int("N", 1, 65535)
	_ = e.Secret("S", 16)
	err := e.Err()
	for _, want := range []string{
		"U must be an absolute http or https url",
		"D must be a positive duration",
		"N must be an integer between 1 and 65535",
		"S must be at least 16 characters",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestValidValues(t *testing.T) {
	e := FromMap(map[string]string{
		"UMAMI_URL":            "http://localhost:3000",
		"UMAMI_ADMIN_PASSWORD": "0123456789abcdef",
		"D":                    "30s",
		"N":                    "8080",
	})
	c, err := LoadUmamiAdmin(e)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL.String() != "http://localhost:3000" || c.Password.Reveal() != "0123456789abcdef" {
		t.Fatalf("unexpected config: %v %v", c.URL, c.Password.Reveal())
	}
	if d := e.Duration("D"); d != 30*time.Second {
		t.Fatalf("duration: %v", d)
	}
	if n := e.Int("N", 1, 65535); n != 8080 {
		t.Fatalf("int: %d", n)
	}
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestSecretNeverPrints(t *testing.T) {
	s := Secret{v: "hunter2hunter2hunter2"}
	var b strings.Builder
	fmt.Fprintf(&b, "%v %s %+v %#v", s, s, s, s)
	slog.New(slog.NewJSONHandler(&b, nil)).Info("x", "pw", s)
	if strings.Contains(b.String(), "hunter2") {
		t.Fatalf("secret leaked: %s", b.String())
	}
}
