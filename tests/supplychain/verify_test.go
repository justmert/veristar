//go:build supplychain

// Package supplychain proves the deploy-time image verification of
// infra/ansible/playbooks/deploy.yml (playbooks/tasks/verify-images.yml)
// against a real registry and real cosign signatures (the design).
//
// TEST-ONLY SIGNING: production images are signed keyless by
// release-images.yml with github's oidc identity, which cannot run outside
// github actions. this test signs with an ephemeral key pair made for one run
// and verifies with sa_signature_mode=test-ephemeral-key, which the task file
// accepts only for an inventory under tests/. the key is deleted with the
// test directory. what is proven here is the refusal logic around cosign:
// release and registry checks, unsigned digests, a foreign signer, a tag that
// moved, and that the result pins the verified digests.
//
//	go test -tags supplychain -count=1 -v ./tests/supplychain/
//
// needs docker, cosign 3.1.4 on PATH and the ansible venv (make ansible-venv,
// ANSIBLE_VENV to override its location).
package supplychain

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// infra/local/env.sh pins the same registry image for the local converge
const registryImage = "registry:3.0.0@sha256:6c5666b861f3505b116bb9aa9b25175e71210414bd010d92035ff64018f9457e"

// the platform_images keys of vars/pins.yml, as release-images.yml names them
var images = []string{"gateway", "collector", "caddy", "console", "controlplane", "replayer",
	"metricswriter", "jobs", "platformctl", "postgres-ha", "prober"}

type world struct {
	t        *testing.T
	dir      string
	registry string // host:port/sa
	tag      string
	key, pub string
	other    string // a second key pair's private key, a foreign signer
}

func repoRoot(t *testing.T) string {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func run(t *testing.T, dir string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func must(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, env, name, args...)
	require.NoError(t, err, "%s %v: %s", name, args, out)
	return out
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func setup(t *testing.T) *world {
	out := must(t, "", nil, "cosign", "version")
	require.Contains(t, out, "v3.1.4", "cosign 3.1.4 is required")
	w := &world{t: t, dir: t.TempDir(), tag: "v0.0.0-supplychain-test"}
	port := freePort(t)
	name := fmt.Sprintf("sa-supplychain-registry-%d", port)
	must(t, "", nil, "docker", "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:5000", port), registryImage)
	t.Cleanup(func() { _, _ = run(t, "", nil, "docker", "rm", "-f", name) })
	w.registry = fmt.Sprintf("127.0.0.1:%d/sa", port)
	for i := 0; i < 50; i++ {
		if _, err := run(t, "", nil, "curl", "-fsS", fmt.Sprintf("http://127.0.0.1:%d/v2/", port)); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	// the ephemeral key pairs: empty password, made in the test directory
	for _, sub := range []string{"release", "foreign"} {
		d := filepath.Join(w.dir, sub)
		require.NoError(t, os.MkdirAll(d, 0o700))
		must(t, d, []string{"COSIGN_PASSWORD="}, "cosign", "generate-key-pair")
	}
	w.key = filepath.Join(w.dir, "release", "cosign.key")
	w.pub = filepath.Join(w.dir, "release", "cosign.pub")
	w.other = filepath.Join(w.dir, "foreign", "cosign.key")
	return w
}

// push builds a one-file image from scratch (no base image pull) whose
// content names it, pushes it and returns its digest.
func (w *world) push(name, tag, content string) string {
	t := w.t
	d := filepath.Join(w.dir, "img-"+name+"-"+content)
	require.NoError(t, os.MkdirAll(d, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(d, "f"), []byte(content), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(d, "Dockerfile"), []byte("FROM scratch\nCOPY f /f\n"), 0o600))
	ref := w.registry + "/" + name + ":" + tag
	must(t, "", nil, "docker", "build", "-q", "-t", ref, d)
	t.Cleanup(func() { _, _ = run(t, "", nil, "docker", "rmi", ref) })
	must(t, "", nil, "docker", "push", "-q", ref)
	out := must(t, "", nil, "docker", "buildx", "imagetools", "inspect", "--format", "{{json .Manifest}}", ref)
	var m struct{ Digest string }
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	require.True(t, strings.HasPrefix(m.Digest, "sha256:"), out)
	return m.Digest
}

func (w *world) sign(key, name, digest string) {
	must(w.t, "", []string{"COSIGN_PASSWORD="}, "cosign", "sign", "--yes", "--key", key,
		"--use-signing-config=false", "--tlog-upload=false", "--allow-http-registry",
		w.registry+"/"+name+"@"+digest)
}

type entry struct {
	Repo   string `json:"repo"`
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
}

func (w *world) manifest(release string, imgs map[string]entry) string {
	b, err := json.Marshal(map[string]any{"schema": 1, "release": release, "images": imgs})
	require.NoError(w.t, err)
	p := filepath.Join(w.dir, fmt.Sprintf("manifest-%d.json", time.Now().UnixNano()))
	require.NoError(w.t, os.WriteFile(p, b, 0o600))
	return p
}

// verify runs the verification play and returns its output, the verified
// references when it passed, and whether it passed.
func (w *world) verify(manifest string, extra ...string) (string, map[string]string, bool) {
	t := w.t
	root := repoRoot(t)
	venv := os.Getenv("ANSIBLE_VENV")
	if venv == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		venv = filepath.Join(home, ".local", "share", "stellar-analytics", "ansible-venv")
	}
	out := filepath.Join(w.dir, fmt.Sprintf("verified-%d.json", time.Now().UnixNano()))
	inv := "inventory/hosts.yml"
	// an extra "-i <path>" replaces the test inventory instead of adding to it
	if len(extra) >= 2 && extra[0] == "-i" {
		inv, extra = extra[1], extra[2:]
	}
	args := []string{"-i", inv, "verify.yml",
		"-e", "sa_image_registry=" + w.registry, "-e", "sa_image_tag=" + w.tag,
		"-e", "sa_release_manifest=" + manifest, "-e", "sa_signature_test_key=" + w.pub,
		"-e", "verify_out=" + out}
	args = append(args, extra...)
	text, err := run(t, filepath.Join(root, "tests", "supplychain"),
		[]string{"ANSIBLE_CONFIG=" + filepath.Join(root, "infra", "ansible", "ansible.cfg")},
		filepath.Join(venv, "bin", "ansible-playbook"), args...)
	if err != nil {
		return text, nil, false
	}
	b, rerr := os.ReadFile(out)
	require.NoError(t, rerr)
	var got map[string]string
	require.NoError(t, json.Unmarshal(b, &got))
	return text, got, true
}

func TestDeployVerification(t *testing.T) {
	w := setup(t)
	signed := map[string]entry{}
	for _, n := range images {
		tag := w.tag
		if n == "postgres-ha" {
			tag = "18.6-" + w.tag
		}
		d := w.push(n, tag, n+"-release")
		w.sign(w.key, n, d)
		signed[n] = entry{Repo: w.registry + "/" + n, Tag: tag, Digest: d}
	}
	clone := func() map[string]entry {
		c := map[string]entry{}
		for k, v := range signed {
			c[k] = v
		}
		return c
	}

	t.Run("signed release passes and pins digests", func(t *testing.T) {
		out, got, ok := w.verify(w.manifest(w.tag, signed))
		require.True(t, ok, out)
		require.Len(t, got, len(images))
		for _, n := range images {
			e := signed[n]
			require.Equal(t, e.Repo+":"+e.Tag+"@"+e.Digest, got[strings.ReplaceAll(n, "-", "_")])
		}
	})

	t.Run("unsigned digest is refused", func(t *testing.T) {
		m := clone()
		d := w.push("gateway", "unsigned", "gateway-unsigned")
		m["gateway"] = entry{Repo: m["gateway"].Repo, Tag: "unsigned", Digest: d}
		out, _, ok := w.verify(w.manifest(w.tag, m))
		require.False(t, ok)
		require.Contains(t, out, "no signatures found", out)
	})

	t.Run("manifest digest that differs from the signed tag is refused", func(t *testing.T) {
		// a signed image of another build named under this release's tag entry
		m := clone()
		d := w.push("jobs", "other-build", "jobs-other-build")
		w.sign(w.key, "jobs", d)
		m["jobs"] = entry{Repo: m["jobs"].Repo, Tag: m["jobs"].Tag, Digest: d}
		out, _, ok := w.verify(w.manifest(w.tag, m))
		require.False(t, ok)
		require.Contains(t, out, "the manifest says "+d, out)
	})

	t.Run("signature by another key is refused", func(t *testing.T) {
		m := clone()
		d := w.push("replayer", "foreign", "replayer-foreign")
		w.sign(w.other, "replayer", d)
		m["replayer"] = entry{Repo: m["replayer"].Repo, Tag: "foreign", Digest: d}
		out, _, ok := w.verify(w.manifest(w.tag, m))
		require.False(t, ok)
		require.Contains(t, out, "replayer", out)
	})

	t.Run("manifest of another release is refused", func(t *testing.T) {
		out, _, ok := w.verify(w.manifest("v9.9.9", signed))
		require.False(t, ok)
		require.Contains(t, out, "does not describe release", out)
	})

	t.Run("image from another registry is refused", func(t *testing.T) {
		m := clone()
		m["caddy"] = entry{Repo: "ghcr.io/somebody/caddy", Tag: w.tag, Digest: m["caddy"].Digest}
		out, _, ok := w.verify(w.manifest(w.tag, m))
		require.False(t, ok)
		require.Contains(t, out, "does not describe release", out)
	})

	t.Run("incomplete manifest is refused", func(t *testing.T) {
		m := clone()
		delete(m, "console")
		out, _, ok := w.verify(w.manifest(w.tag, m))
		require.False(t, ok)
		require.Contains(t, out, "completely", out)
	})

	t.Run("manifest without the prober is refused", func(t *testing.T) {
		m := clone()
		delete(m, "prober")
		out, _, ok := w.verify(w.manifest(w.tag, m))
		require.False(t, ok)
		require.Contains(t, out, "completely", out)
	})

	t.Run("missing manifest is refused", func(t *testing.T) {
		out, _, ok := w.verify("")
		require.False(t, ok)
		require.Contains(t, out, "deployed only by verified digest", out)
	})

	t.Run("tag moved to another signed image is refused", func(t *testing.T) {
		// the release tag of the collector now names a different (also
		// signed) build; the manifest still has the original digest
		d := w.push("collector", signed["collector"].Tag, "collector-moved")
		w.sign(w.key, "collector", d)
		out, _, ok := w.verify(w.manifest(w.tag, signed))
		require.False(t, ok)
		require.Contains(t, out, "points to", out)
	})

	t.Run("test key mode outside tests is refused", func(t *testing.T) {
		// the same play from the production inventory directory: the assert on
		// inventory_dir must stop it before any verification
		root := repoRoot(t)
		inv := filepath.Join(root, "infra", "ansible", "inventories", "staging", "supplychain-test-hosts.yml")
		require.NoError(t, os.WriteFile(inv, []byte("all:\n  hosts:\n    localhost:\n      ansible_connection: local\n      ansible_python_interpreter: \"{{ ansible_playbook_python }}\"\n"), 0o600))
		t.Cleanup(func() { _ = os.Remove(inv) })
		out, _, ok := w.verify(w.manifest(w.tag, signed), "-i", inv, "-e", "sa_signature_mode=test-ephemeral-key")
		require.False(t, ok)
		require.Contains(t, out, "allowed only for an inventory under tests/", out)
	})
}
