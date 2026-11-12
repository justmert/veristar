package gateway

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
)

// the release directory is packages/tracker/releases, baked into the image:
// manifest.json lists every immutable <version>/sdk.js with its size and
// sha384, revoked.json is the one mutable file.

var releaseVersionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type manifestEntry struct {
	Version   string `json:"version"`
	File      string `json:"file"`
	Bytes     int    `json:"bytes"`
	SHA384    string `json:"sha384"`
	Integrity string `json:"integrity"`
}

type revokedFile struct {
	Revoked []struct {
		Version string `json:"version"`
		Reason  string `json:"reason"`
	} `json:"revoked"`
}

// Releases holds every released tracker file in memory. the files are
// checked against the manifest at load, so a corrupted image fails at start
// instead of serving bytes whose integrity hash the snippets would refuse.
type Releases struct {
	dir     string
	files   map[string][]byte
	revoked atomic.Pointer[map[string]bool]
}

// LoadReleases reads and checks the release directory.
func LoadReleases(dir string) (*Releases, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // dir is TRACKER_RELEASE_DIR from the operator, not request input
	if err != nil {
		return nil, fmt.Errorf("gateway: read tracker manifest: %w", err)
	}
	var m struct {
		Versions []manifestEntry `json:"versions"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("gateway: tracker manifest: %w", err)
	}
	if len(m.Versions) == 0 {
		return nil, errors.New("gateway: tracker manifest lists no versions")
	}
	r := &Releases{dir: dir, files: map[string][]byte{}}
	for _, e := range m.Versions {
		if !releaseVersionRe.MatchString(e.Version) {
			return nil, fmt.Errorf("gateway: tracker manifest version %q is not x.y.z", e.Version)
		}
		if e.File != e.Version+"/sdk.js" {
			return nil, fmt.Errorf("gateway: tracker manifest entry %s names file %q", e.Version, e.File)
		}
		if _, dup := r.files[e.Version]; dup {
			return nil, fmt.Errorf("gateway: tracker manifest lists %s twice", e.Version)
		}
		// the version matched x.y.z above, so it cannot leave dir
		body, err := os.ReadFile(filepath.Join(dir, e.Version, "sdk.js")) //nolint:gosec // dir is operator config, version is checked
		if err != nil {
			return nil, fmt.Errorf("gateway: read tracker %s: %w", e.Version, err)
		}
		sum := sha512.Sum384(body)
		if len(body) != e.Bytes || hex.EncodeToString(sum[:]) != e.SHA384 ||
			"sha384-"+base64.StdEncoding.EncodeToString(sum[:]) != e.Integrity {
			return nil, fmt.Errorf("gateway: tracker %s does not match its manifest entry", e.Version)
		}
		r.files[e.Version] = body
	}
	if err := r.ReloadRevoked(); err != nil {
		return nil, err
	}
	return r, nil
}

// ReloadRevoked reads revoked.json again. on error the previous list stays,
// so a broken edit never un-revokes a version.
func (r *Releases) ReloadRevoked() error {
	b, err := os.ReadFile(filepath.Join(r.dir, "revoked.json"))
	if err != nil {
		return fmt.Errorf("gateway: read revoked tracker list: %w", err)
	}
	var f revokedFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("gateway: revoked tracker list: %w", err)
	}
	if f.Revoked == nil {
		return errors.New("gateway: revoked tracker list has no revoked array")
	}
	set := map[string]bool{}
	for _, v := range f.Revoked {
		if _, ok := r.files[v.Version]; !ok {
			return fmt.Errorf("gateway: revoked tracker version %q was never released", v.Version)
		}
		if v.Reason == "" {
			return fmt.Errorf("gateway: revoked tracker version %s has no reason", v.Version)
		}
		set[v.Version] = true
	}
	r.revoked.Store(&set)
	return nil
}

// Lookup returns the file of a version. ok is false for a version that was
// never released; revoked is true for one listed in revoked.json.
func (r *Releases) Lookup(version string) (body []byte, revoked, ok bool) {
	body, ok = r.files[version]
	if !ok {
		return nil, false, false
	}
	if (*r.revoked.Load())[version] {
		return nil, true, true
	}
	return body, false, true
}
