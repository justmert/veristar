package gateway

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/justmert/veristar/internal/registry"
)

// the request for sdk.js names no site: the tag's src is the same versioned
// path for every site. the page's origin is in the Referer header, which a
// browser sends for the script request under the default referrer policy
// (strict-origin-when-cross-origin: the origin only, as the request goes to
// another origin). ScriptAttribution matches that origin against the
// registered origins of the active sites, the same patterns the Origin check
// of events uses. the header is read in memory for the match and is never
// counted, logged or kept; only the site it names is.

// SiteSnapshots is the registry view the attribution needs: every site at
// once, to index the registered origins.
type SiteSnapshots interface {
	// SnapshotAt returns every site and the generation of that snapshot
	SnapshotAt() (map[string]registry.Site, uint64)
	// Generation moves whenever the snapshot changes
	Generation() uint64
	Loaded() bool
}

type originSite struct {
	pattern origin
	site    string
}

// ScriptAttribution maps a tracker file request to a site through an index
// of the active sites' origins. the index is built again whenever the
// registry snapshot changed, so a newly registered origin is attributed from
// its first request, and an unchanged registry costs no rebuild.
type ScriptAttribution struct {
	sites SiteSnapshots

	mu    sync.Mutex
	gen   uint64
	exact map[origin][]string
	wild  []originSite
}

// NewScriptAttribution returns an attribution over the registry snapshot.
func NewScriptAttribution(sites SiteSnapshots) *ScriptAttribution {
	return &ScriptAttribution{sites: sites}
}

// index returns the origin index of the current registry snapshot, building
// it when the snapshot changed since the last build.
func (a *ScriptAttribution) index() (map[origin][]string, []originSite) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exact != nil && a.sites.Generation() == a.gen {
		return a.exact, a.wild
	}
	snap, gen := a.sites.SnapshotAt()
	exact := map[origin][]string{}
	var wild []originSite
	for id, s := range snap {
		if !s.Active() {
			continue
		}
		for _, raw := range s.Origins {
			p, err := parseOrigin(raw, true)
			if err != nil {
				continue
			}
			if p.wildcard {
				wild = append(wild, originSite{pattern: p, site: id})
			} else {
				exact[p] = append(exact[p], id)
			}
		}
	}
	a.exact, a.wild, a.gen = exact, wild, gen
	return exact, wild
}

// Site returns the site of a tracker file request: the one active site
// whose registered origins match the origin of the Referer header. ok is
// false when the header is missing or is no http or https url, when no site
// matches, and when more than one site does, because a request that could
// belong to either cannot be counted for one of them.
func (a *ScriptAttribution) Site(h http.Header) (string, bool) {
	if !a.sites.Loaded() {
		return "", false
	}
	refs := h.Values("Referer")
	if len(refs) != 1 {
		return "", false
	}
	u, err := url.Parse(refs[0])
	if err != nil || u.Host == "" || u.User != nil {
		return "", false
	}
	o, err := parseOrigin(strings.ToLower(u.Scheme)+"://"+u.Host, false)
	if err != nil {
		return "", false
	}
	exact, wild := a.index()
	found := ""
	add := func(id string) bool {
		if found != "" && found != id {
			return false
		}
		found = id
		return true
	}
	for _, id := range exact[o] {
		if !add(id) {
			return "", false
		}
	}
	for _, w := range wild {
		if w.pattern.matches(o) && !add(w.site) {
			return "", false
		}
	}
	return found, found != ""
}

// ScriptCountable reports whether a tracker file request is one a browser
// made to run the script: fetch metadata names a script destination. a
// request without it (a crawler, a command-line client, a browser too old to
// send it) is served but not counted, so the share compares browsers with
// browsers.
func ScriptCountable(h http.Header) bool {
	return h.Get("Sec-Fetch-Dest") == "script"
}

// SendsGPC reports a Sec-GPC: 1 request, the global privacy control signal.
func SendsGPC(h http.Header) bool { return h.Get("Sec-GPC") == "1" }
