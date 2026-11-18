package pins

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// an image reference pinned by digest anywhere in the tree: repository, tag
// and digest
var digestRef = regexp.MustCompile(`([a-z0-9][a-z0-9./_-]*):([A-Za-z0-9][A-Za-z0-9._-]*)@(sha256:[0-9a-f]{64})`)

// repositories pinned at more than one tag on purpose, with the tags allowed
var multiTag = map[string][]string{
	// the caddy image and its builder variant of the same release
	"caddy": {"2.11.9", "2.11.9-builder"},
}

// skipped: the changelog records the pins of past releases
var historical = []string{"CHANGELOG.md"}

// TestOneDigestPerImage fails when the same image is pinned at two versions
// or two digests in different files. dependabot watches one copy of most
// pins (deploy/compose, deploy/images, the examples), and the test suites,
// fixtures and docs hold their own copies; without this check a dependabot
// update moves one copy and the suites keep testing the old image.
func TestOneDigestPerImage(t *testing.T) {
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	type pin struct{ tag, digest string }
	seen := map[string]map[pin][]string{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if slices.Contains(historical, f) || strings.HasSuffix(f, "package-lock.json") || f == "pnpm-lock.yaml" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			// a submodule or a file removed from the work tree
			continue
		}
		for _, m := range digestRef.FindAllStringSubmatch(string(b), -1) {
			repo := strings.TrimPrefix(strings.TrimPrefix(m[1], "docker.io/"), "library/")
			if seen[repo] == nil {
				seen[repo] = map[pin][]string{}
			}
			p := pin{m[2], m[3]}
			if !slices.Contains(seen[repo][p], f) {
				seen[repo][p] = append(seen[repo][p], f)
			}
		}
	}
	if len(seen) < 10 {
		t.Fatalf("found %d pinned images; the pattern no longer matches the tree", len(seen))
	}
	for repo, pins := range seen {
		tags := map[string][]pin{}
		for p := range pins {
			tags[p.tag] = append(tags[p.tag], p)
		}
		for tag, ps := range tags {
			if len(ps) > 1 {
				var where []string
				for _, p := range ps {
					where = append(where, p.digest[:19]+" in "+strings.Join(pins[p], ", "))
				}
				slices.Sort(where)
				t.Errorf("%s:%s is pinned at %d digests: %s", repo, tag, len(ps), strings.Join(where, "; "))
			}
		}
		if len(tags) > 1 {
			var names []string
			for tag := range tags {
				names = append(names, tag)
			}
			slices.Sort(names)
			allowed := multiTag[repo]
			slices.Sort(allowed)
			if !slices.Equal(names, allowed) {
				var where []string
				for p, files := range pins {
					where = append(where, p.tag+" in "+strings.Join(files, ", "))
				}
				slices.Sort(where)
				t.Errorf("%s is pinned at %d versions; move every copy together: %s", repo, len(names), strings.Join(where, "; "))
			}
		}
	}
}
