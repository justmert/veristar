package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	"github.com/justmert/veristar/internal/db/tenantq"
)

// the snippet is the script tag tenants paste into their pages. it must be
// byte-identical to snippet() in packages/tracker/src/snippet.ts
// (control-plane-api 4.3.6); snippet_test.go runs both and compares.

var trackerVersionRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// TrackerRelease is one manifest entry, as the snippet route shows it.
type TrackerRelease struct {
	Version   string `json:"version"`
	Released  string `json:"released"`
	Bytes     int    `json:"bytes"`
	GzipBytes int    `json:"gzip_bytes"`
	Integrity string `json:"-"`
}

// errors of Tracker.Snippet; the route maps them to 422 and 410
var (
	ErrUnknownTrackerVersion = errors.New("tracker version is not in the release manifest")
	ErrTrackerRevoked        = errors.New("tracker version is revoked")
)

// Tracker builds snippets from the release directory (TRACKER_RELEASE_DIR):
// manifest.json is immutable per release and read once, revoked.json is the
// one file an operator edits, so it is read on every call and a revocation
// takes effect without a restart.
type Tracker struct {
	dir          string
	ingestOrigin string
	releases     []TrackerRelease // newest first
	byVersion    map[string]TrackerRelease
}

// LoadTracker reads the manifest and checks ingestOrigin (INGEST_ORIGIN)
// the way snippet.ts does, so a bad value stops the process at start
// instead of producing tags the browser cannot load.
func LoadTracker(dir, ingestOrigin string) (*Tracker, error) {
	if err := checkBareOrigin(ingestOrigin); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // dir is TRACKER_RELEASE_DIR from the operator, not request input
	if err != nil {
		return nil, fmt.Errorf("controlplane: read tracker manifest: %w", err)
	}
	var m struct {
		Versions []struct {
			Version   string `json:"version"`
			Released  string `json:"released"`
			Bytes     int    `json:"bytes"`
			GzipBytes int    `json:"gzipBytes"`
			Integrity string `json:"integrity"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("controlplane: tracker manifest: %w", err)
	}
	if len(m.Versions) == 0 {
		return nil, errors.New("controlplane: tracker manifest lists no versions")
	}
	t := &Tracker{dir: dir, ingestOrigin: ingestOrigin, byVersion: map[string]TrackerRelease{}}
	for _, e := range m.Versions {
		if !trackerVersionRe.MatchString(e.Version) {
			return nil, fmt.Errorf("controlplane: tracker manifest version %q is not x.y.z", e.Version)
		}
		if _, dup := t.byVersion[e.Version]; dup {
			return nil, fmt.Errorf("controlplane: tracker manifest lists %s twice", e.Version)
		}
		// the integrity value is copied into html unescaped, so it must hold
		// nothing but the base64 of a sha-384 digest
		if !integrityRe.MatchString(e.Integrity) {
			return nil, fmt.Errorf("controlplane: tracker %s has a malformed integrity value", e.Version)
		}
		if e.Released == "" || e.Bytes <= 0 || e.GzipBytes <= 0 {
			return nil, fmt.Errorf("controlplane: tracker %s lacks released, bytes or gzipBytes", e.Version)
		}
		r := TrackerRelease{Version: e.Version, Released: e.Released, Bytes: e.Bytes, GzipBytes: e.GzipBytes, Integrity: e.Integrity}
		t.byVersion[e.Version] = r
		t.releases = append(t.releases, r)
	}
	sort.Slice(t.releases, func(i, j int) bool { return versionLess(t.releases[j].Version, t.releases[i].Version) })
	if _, err := t.revoked(); err != nil {
		return nil, err
	}
	return t, nil
}

var integrityRe = regexp.MustCompile(`^sha384-[A-Za-z0-9+/]{64}$`)

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// checkBareOrigin mirrors snippet.ts: new URL(origin).origin must equal the
// input and the scheme must be http or https.
func checkBareOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.Scheme+"://"+u.Host != o || u.Host != strings.ToLower(u.Host) || u.User != nil {
		return fmt.Errorf("controlplane: ingest origin %q must be a bare http or https origin", o)
	}
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") || strings.HasSuffix(u.Host, ":") {
		// a browser drops the default port from an origin, so the value would
		// not equal its own serialisation
		return fmt.Errorf("controlplane: ingest origin %q must not name the default port", o)
	}
	return nil
}

func (t *Tracker) revoked() (map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join(t.dir, "revoked.json"))
	if err != nil {
		return nil, fmt.Errorf("controlplane: read revoked tracker list: %w", err)
	}
	var f struct {
		Revoked []struct {
			Version string `json:"version"`
		} `json:"revoked"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("controlplane: revoked tracker list: %w", err)
	}
	if f.Revoked == nil {
		return nil, errors.New("controlplane: revoked tracker list has no revoked array")
	}
	set := map[string]bool{}
	for _, v := range f.Revoked {
		if _, ok := t.byVersion[v.Version]; !ok {
			return nil, fmt.Errorf("controlplane: revoked tracker version %q was never released", v.Version)
		}
		set[v.Version] = true
	}
	return set, nil
}

// Available returns every non-revoked release, newest first.
func (t *Tracker) Available() ([]TrackerRelease, error) {
	rev, err := t.revoked()
	if err != nil {
		return nil, err
	}
	out := make([]TrackerRelease, 0, len(t.releases))
	for _, r := range t.releases {
		if !rev[r.Version] {
			out = append(out, r)
		}
	}
	return out, nil
}

// ScriptURL is scriptUrl() of snippet.ts.
func (t *Tracker) ScriptURL(version string) string {
	return t.ingestOrigin + "/v/" + version + "/sdk.js"
}

// Snippet returns the tag for a site and the release it uses. no value is
// html-escaped, as in snippet.ts: the site id is a canonical uuid, the url
// is built from a checked origin and an x.y.z version, and the integrity
// value was checked at load, so none can hold a quote or angle bracket.
func (t *Tracker) Snippet(siteID uuid.UUID, version string) (string, TrackerRelease, error) {
	r, ok := t.byVersion[version]
	if !ok {
		return "", TrackerRelease{}, ErrUnknownTrackerVersion
	}
	rev, err := t.revoked()
	if err != nil {
		return "", TrackerRelease{}, err
	}
	if rev[version] {
		return "", TrackerRelease{}, ErrTrackerRevoked
	}
	tag := `<script defer src="` + t.ScriptURL(version) + `" integrity="` + r.Integrity +
		`" crossorigin="anonymous" data-website-id="` + siteID.String() + `"></script>`
	return tag, r, nil
}

// SnippetResponse is the body of GET /api/sites/{site_id}/snippet.
type SnippetResponse struct {
	SiteID    uuid.UUID        `json:"site_id"`
	Version   string           `json:"version"`
	Src       string           `json:"src"`
	Integrity string           `json:"integrity"`
	Tag       string           `json:"tag"`
	NPM       *json.RawMessage `json:"npm"`
	Versions  []TrackerRelease `json:"versions"`
}

// SnippetRoutes serves GET /api/sites/{site_id}/snippet (contract 4.3.6).
type SnippetRoutes struct {
	scope   *tenants.Scope
	tracker *Tracker
}

// NewSnippetRoutes returns the route. npmPackage is TRACKER_NPM_PACKAGE.
// the shape of the npm answer (in particular its import line) is defined
// only once the package is published on npm, so a configured package
// name is refused here instead of answering with a guessed import.
func NewSnippetRoutes(scope *tenants.Scope, tracker *Tracker, npmPackage string) (*SnippetRoutes, error) {
	if scope == nil || tracker == nil {
		return nil, errors.New("controlplane: snippet route needs a scope and a tracker")
	}
	if npmPackage != "" {
		return nil, errors.New("controlplane: TRACKER_NPM_PACKAGE is set, but the npm snippet is defined only once the tracker is published on npm")
	}
	return &SnippetRoutes{scope: scope, tracker: tracker}, nil
}

// Routes registers the path under a router mounted at /api.
func (h *SnippetRoutes) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/sites/{site_id}/snippet", httpx.HandlerFunc(h.get))
}

func (h *SnippetRoutes) get(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a, siteID, err := h.scope.Site(r, "site_id")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if len(q["version"]) > 1 {
		return httpx.BadQuery("version is given more than once")
	}
	var site tenantq.CpSite
	if err := h.scope.InTenant(ctx, a, func(tx pgx.Tx) error {
		var qerr error
		site, qerr = tenantq.New(tx).GetSite(ctx, siteID)
		return qerr
	}); err != nil {
		return tenants.NotFoundOnNoRows(err)
	}
	if site.Status == "removed" {
		return httpx.Conflict(httpx.CodeSiteRemoved, "the site is removed")
	}
	avail, err := h.tracker.Available()
	if err != nil {
		return err
	}
	version := q.Get("version")
	if _, given := q["version"]; !given {
		if len(avail) == 0 {
			return httpx.E(http.StatusGone, httpx.CodeTrackerRevoked, "every tracker version is revoked")
		}
		version = avail[0].Version
	}
	tag, rel, err := h.tracker.Snippet(site.ID, version)
	switch {
	case errors.Is(err, ErrUnknownTrackerVersion):
		return httpx.Validation("version is not a released tracker version",
			map[string]string{"version": httpx.FieldUnknownTrackerVersion})
	case errors.Is(err, ErrTrackerRevoked):
		return httpx.E(http.StatusGone, httpx.CodeTrackerRevoked, "tracker version "+version+" is revoked")
	case err != nil:
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, SnippetResponse{
		SiteID: site.ID, Version: rel.Version, Src: h.tracker.ScriptURL(rel.Version),
		Integrity: rel.Integrity, Tag: tag, NPM: nil, Versions: avail,
	})
	return nil
}
