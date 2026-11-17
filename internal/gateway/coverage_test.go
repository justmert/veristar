package gateway

import (
	"maps"
	"net/http"
	"sync"
	"testing"

	"github.com/justmert/veristar/internal/registry"
)

// fakeSites is a registry snapshot whose generation moves on every change,
// as the watcher's does, and that counts how often the attribution read it
type fakeSites struct {
	mu    sync.Mutex
	sites map[string]registry.Site
	gen   uint64
	reads int
}

func (f *fakeSites) put(id string, s registry.Site) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sites == nil {
		f.sites = map[string]registry.Site{}
	}
	f.sites[id] = s
	f.gen++
}

func (f *fakeSites) SnapshotAt() (map[string]registry.Site, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return maps.Clone(f.sites), f.gen
}

func (f *fakeSites) Generation() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen
}

func (f *fakeSites) Loaded() bool { return true }

func referer(u string) http.Header {
	h := http.Header{}
	h.Set("Referer", u)
	return h
}

// a site registered after the index was built is attributed from its
// first tracker file request, and one that is no longer active is not
func TestScriptAttributionFollowsTheRegistry(t *testing.T) {
	sites := &fakeSites{}
	sites.put("site-a", registry.Site{Status: registry.StatusActive, Origins: []string{"https://a.example"}})
	a := NewScriptAttribution(sites)
	if id, ok := a.Site(referer("https://a.example/")); !ok || id != "site-a" {
		t.Fatalf("site a: %q %t", id, ok)
	}

	sites.put("site-b", registry.Site{Status: registry.StatusActive, Origins: []string{"https://b.example"}})
	if id, ok := a.Site(referer("https://b.example/page")); !ok || id != "site-b" {
		t.Fatalf("site b right after it was registered: %q %t", id, ok)
	}

	sites.put("site-a", registry.Site{Status: "suspended", Origins: []string{"https://a.example"}})
	if id, ok := a.Site(referer("https://a.example/")); ok {
		t.Fatalf("suspended site a still attributed: %q", id)
	}

	// an unchanged registry is not read again
	reads := sites.reads
	for range 100 {
		a.Site(referer("https://b.example/"))
	}
	if sites.reads != reads {
		t.Fatalf("the index was rebuilt %d times without a registry change", sites.reads-reads)
	}
}
