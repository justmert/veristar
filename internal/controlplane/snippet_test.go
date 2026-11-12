package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func releaseDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "packages", "tracker", "releases"))
	require.NoError(t, err)
	return dir
}

// tsSnippets runs snippet() of packages/tracker/src/snippet.ts in node for
// every manifest version and returns version to tag. node strips the
// typescript types itself, so the file runs exactly as committed.
func tsSnippets(t *testing.T, siteID, origin string) map[string]string {
	t.Helper()
	node, err := exec.LookPath("node")
	require.NoError(t, err, "node 26 must be on PATH: the byte-equality check runs the typescript snippet builder")
	src, err := filepath.Abs(filepath.Join("..", "..", "packages", "tracker", "src", "snippet.ts"))
	require.NoError(t, err)
	script := `const m = await import(process.argv[1]);
const out = {};
for (const r of m.releases()) {
  if (m.isRevoked(r.version)) continue;
  out[r.version] = m.snippet({ siteId: process.argv[2], version: r.version, origin: process.argv[3] });
}
process.stdout.write(JSON.stringify(out));`
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, src, siteID, origin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	require.NoError(t, err, stderr.String())
	var tags map[string]string
	require.NoError(t, json.Unmarshal(b, &tags))
	return tags
}

func TestSnippetMatchesTypeScript(t *testing.T) {
	dir := releaseDir(t)
	for _, origin := range []string{"https://in.example.org", "http://localhost:8080"} {
		tr, err := LoadTracker(dir, origin)
		require.NoError(t, err)
		avail, err := tr.Available()
		require.NoError(t, err)
		require.NotEmpty(t, avail)
		for range 3 {
			site := uuid.Must(uuid.NewV7())
			want := tsSnippets(t, site.String(), origin)
			require.Len(t, want, len(avail), "go and typescript disagree on the non-revoked versions")
			for _, r := range avail {
				got, rel, err := tr.Snippet(site, r.Version)
				require.NoError(t, err)
				require.Equal(t, want[r.Version], got, "version %s origin %s", r.Version, origin)
				require.Equal(t, r.Integrity, rel.Integrity)
			}
		}
	}
}

// writeReleases makes a release directory with the given manifest and
// revoked list, for the cases the committed release set does not cover.
func writeReleases(t *testing.T, manifest, revoked string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "revoked.json"), []byte(revoked), 0o600))
	return dir
}

const twoReleases = `{"versions":[
 {"version":"1.0.0","released":"2026-11-12","bytes":10,"gzipBytes":5,"integrity":"sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j"},
 {"version":"1.10.0","released":"2026-12-01","bytes":11,"gzipBytes":6,"integrity":"sha384-AAAAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j"},
 {"version":"1.2.0","released":"2026-11-20","bytes":12,"gzipBytes":7,"integrity":"sha384-BBBBlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j"}]}`

func TestTrackerVersionsAndRevocation(t *testing.T) {
	dir := writeReleases(t, twoReleases, `{"revoked":[]}`)
	tr, err := LoadTracker(dir, "https://in.example.org")
	require.NoError(t, err)
	avail, err := tr.Available()
	require.NoError(t, err)
	// numeric order, not string order: 1.10.0 is newer than 1.2.0
	require.Equal(t, []string{"1.10.0", "1.2.0", "1.0.0"}, []string{avail[0].Version, avail[1].Version, avail[2].Version})

	_, _, err = tr.Snippet(uuid.New(), "9.9.9")
	require.ErrorIs(t, err, ErrUnknownTrackerVersion)

	// revoked.json is read per call, so a revocation applies without a restart
	require.NoError(t, os.WriteFile(filepath.Join(dir, "revoked.json"), []byte(`{"revoked":[{"version":"1.10.0","reason":"bug"}]}`), 0o600))
	_, _, err = tr.Snippet(uuid.New(), "1.10.0")
	require.ErrorIs(t, err, ErrTrackerRevoked)
	avail, err = tr.Available()
	require.NoError(t, err)
	require.Equal(t, "1.2.0", avail[0].Version)

	// a broken revoked list is an error, never an empty list
	require.NoError(t, os.WriteFile(filepath.Join(dir, "revoked.json"), []byte(`{}`), 0o600))
	_, _, err = tr.Snippet(uuid.New(), "1.2.0")
	require.Error(t, err)
}

func TestLoadTrackerRefusesBadInput(t *testing.T) {
	good := writeReleases(t, twoReleases, `{"revoked":[]}`)
	for _, o := range []string{"https://in.example.org/", "https://IN.example.org", "ftp://in.example.org",
		"https://in.example.org:443", "in.example.org", "https://u@in.example.org"} {
		_, err := LoadTracker(good, o)
		require.Error(t, err, o)
	}
	for name, m := range map[string]string{
		"empty":     `{"versions":[]}`,
		"version":   `{"versions":[{"version":"v1","released":"x","bytes":1,"gzipBytes":1,"integrity":"sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j"}]}`,
		"integrity": `{"versions":[{"version":"1.0.0","released":"x","bytes":1,"gzipBytes":1,"integrity":"sha384-\"><x"}]}`,
		"duplicate": `{"versions":[{"version":"1.0.0","released":"x","bytes":1,"gzipBytes":1,"integrity":"sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j"},{"version":"1.0.0","released":"x","bytes":1,"gzipBytes":1,"integrity":"sha384-G7jAlHsp/0HhreHeIKFZIr1Udyb8lXfTJPPZcLC8YcVCAQ9Mr/OS3JOMW4n4OT6j"}]}`,
	} {
		_, err := LoadTracker(writeReleases(t, m, `{"revoked":[]}`), "https://in.example.org")
		require.Error(t, err, name)
	}
	_, err := LoadTracker(writeReleases(t, twoReleases, `{"revoked":[{"version":"7.0.0"}]}`), "https://in.example.org")
	require.Error(t, err, "revoking an unreleased version")
}
