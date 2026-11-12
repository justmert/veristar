package gateway

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeRelease(t *testing.T, dir, version, body, revoked string) {
	t.Helper()
	sum := sha512.Sum384([]byte(body))
	if err := os.MkdirAll(filepath.Join(dir, version), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, version, "sdk.js"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"versions":[{"version":%q,"file":%q,"bytes":%d,"sha384":%q,"integrity":%q}]}`,
		version, version+"/sdk.js", len(body), hex.EncodeToString(sum[:]),
		"sha384-"+base64.StdEncoding.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "revoked.json"), []byte(revoked), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReleasesServeCommittedTracker(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "packages", "tracker", "releases")
	r, err := LoadReleases(dir)
	if err != nil {
		t.Fatal(err)
	}
	body, revoked, ok := r.Lookup("1.0.0")
	if !ok || revoked || len(body) == 0 {
		t.Fatalf("1.0.0: ok=%v revoked=%v bytes=%d", ok, revoked, len(body))
	}
	for _, v := range []string{"latest", "1.0", "../1.0.0", "9.9.9"} {
		if _, _, ok := r.Lookup(v); ok {
			t.Errorf("%q resolved to a release", v)
		}
	}
}

func TestReleasesRevokedAndTampered(t *testing.T) {
	dir := t.TempDir()
	writeRelease(t, dir, "1.2.3", "console.log(1)", `{"revoked":[{"version":"1.2.3","reason":"test"}]}`)
	r, err := LoadReleases(dir)
	if err != nil {
		t.Fatal(err)
	}
	if body, revoked, ok := r.Lookup("1.2.3"); !ok || !revoked || body != nil {
		t.Fatalf("revoked version: ok=%v revoked=%v", ok, revoked)
	}
	// a broken edit keeps the previous list, so nothing is un-revoked
	if err := os.WriteFile(filepath.Join(dir, "revoked.json"), []byte(`{"revoked":[{"version":"7.7.7","reason":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.ReloadRevoked(); err == nil {
		t.Fatal("unknown revoked version accepted")
	}
	if _, revoked, _ := r.Lookup("1.2.3"); !revoked {
		t.Fatal("failed reload un-revoked the version")
	}

	if err := os.WriteFile(filepath.Join(dir, "1.2.3", "sdk.js"), []byte("console.log(2)"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReleases(dir); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered file loaded: %v", err)
	}
}
