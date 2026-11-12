package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUnknownCommandShowsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"nope"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "umami bootstrap") {
		t.Fatalf("usage missing: %q", errb.String())
	}
}

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), version) {
		t.Fatalf("version output %q", out.String())
	}
}

func TestBootstrapNamesMissingKeys(t *testing.T) {
	t.Setenv("UMAMI_URL", "")
	t.Setenv("UMAMI_ADMIN_PASSWORD", "")
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"umami", "bootstrap"}, &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, k := range []string{"UMAMI_URL", "UMAMI_ADMIN_PASSWORD"} {
		if !strings.Contains(errb.String(), k) {
			t.Errorf("stderr does not name %s: %s", k, errb.String())
		}
	}
}
