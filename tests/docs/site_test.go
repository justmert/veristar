package docs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// the documentation site is mintlify, rooted at docs/ with docs/docs.json.
// mint validate and mint broken-links (make docs-site-check) build it; the
// tests below hold the rules that need no node toolchain, so a page added
// without front matter or outside the navigation fails here first.

// hiddenPages are published but deliberately left out of the navigation:
// research records and drill results that public pages link to as evidence.
// the audits and slo reports are not published at all (docs/.mintignore).
var hiddenPages = []string{
	"handover/redirect-research",
	"reference/the reference docs",
	"reference/the reference docs",
	"reference/drills/",
}

// mintIgnore reads docs/.mintignore. it supports the forms the file uses:
// "dir/" for a directory and a glob matched against the path or its base name.
func mintIgnore(t *testing.T, docsDir string) func(rel string) bool {
	t.Helper()
	var patterns []string
	for _, l := range strings.Split(read(t, filepath.Join(docsDir, ".mintignore")), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			patterns = append(patterns, l)
		}
	}
	return func(rel string) bool {
		for _, p := range patterns {
			if strings.HasSuffix(p, "/") {
				if strings.HasPrefix(rel, p) {
					return true
				}
				continue
			}
			if ok, _ := filepath.Match(p, rel); ok {
				return true
			}
			if !strings.Contains(p, "/") {
				if ok, _ := filepath.Match(p, filepath.Base(rel)); ok {
					return true
				}
			}
		}
		return false
	}
}

// sitePages lists the pages mintlify publishes, as routes (path under docs/
// without the extension). mintlify skips readme files on its own.
func sitePages(t *testing.T, docsDir string) map[string]string {
	t.Helper()
	ignored := mintIgnore(t, docsDir)
	pages := map[string]string{}
	err := filepath.WalkDir(docsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, docsDir+string(filepath.Separator)))
		if d.IsDir() {
			if p != docsDir && ignored(rel+"/") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(p)
		if ext != ".md" && ext != ".mdx" || ignored(rel) {
			return nil
		}
		if strings.EqualFold(strings.TrimSuffix(filepath.Base(p), ext), "readme") {
			return nil
		}
		pages[strings.TrimSuffix(rel, ext)] = p
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

// navPages collects every page and openapi file named in docs.json
func navPages(v any, pages, specs *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			switch k {
			case "pages":
				for _, p := range e.([]any) {
					if s, ok := p.(string); ok {
						*pages = append(*pages, s)
					} else {
						navPages(p, pages, specs)
					}
				}
			case "openapi":
				if s, ok := e.(string); ok {
					*specs = append(*specs, s)
				}
			default:
				navPages(e, pages, specs)
			}
		}
	case []any:
		for _, e := range x {
			navPages(e, pages, specs)
		}
	}
}

var (
	frontMatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	fmTitleRe     = regexp.MustCompile(`(?m)^title: "[^"]+"$`)
	fmDescRe      = regexp.MustCompile(`(?m)^description: "[^"]+"$`)
	htmlCommentRe = regexp.MustCompile(`<!--`)
	autolinkRe    = regexp.MustCompile(`<(https?://|mailto:)[^>\s]*>`)
)

func TestSiteNavigation(t *testing.T) {
	root := repoRoot(t)
	docsDir := filepath.Join(root, "docs")
	var cfg map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(docsDir, "docs.json"))), &cfg); err != nil {
		t.Fatalf("docs.json: %v", err)
	}
	var nav, specs []string
	navPages(cfg["navigation"], &nav, &specs)
	if len(nav) == 0 {
		t.Fatal("docs.json names no pages")
	}
	published := sitePages(t, docsDir)
	seen := map[string]bool{}
	for _, p := range nav {
		if seen[p] {
			t.Errorf("docs.json lists %s twice", p)
		}
		seen[p] = true
		if _, ok := published[p]; !ok {
			t.Errorf("docs.json lists %s, but docs/ publishes no such page", p)
		}
	}
	for _, s := range specs {
		if _, err := os.Stat(filepath.Join(docsDir, filepath.FromSlash(s))); err != nil {
			t.Errorf("docs.json names the openapi file %s: %v", s, err)
		}
	}
	routes := make([]string, 0, len(published))
	for r := range published {
		routes = append(routes, r)
	}
	slices.Sort(routes)
	for _, r := range routes {
		hidden := slices.ContainsFunc(hiddenPages, func(h string) bool {
			return r == h || strings.HasSuffix(h, "/") && strings.HasPrefix(r, h)
		})
		if !seen[r] && !hidden {
			t.Errorf("docs/%s is published but not in the docs.json navigation; add it there or to hiddenPages", r)
		}
	}
}

// TestSitePages holds what every published page needs: a title and a
// description in its front matter, and markdown that mdx can parse.
func TestSitePages(t *testing.T) {
	root := repoRoot(t)
	docsDir := filepath.Join(root, "docs")
	for route, file := range sitePages(t, docsDir) {
		text := read(t, file)
		fm := frontMatterRe.FindStringSubmatch(text)
		switch {
		case fm == nil:
			t.Errorf("docs/%s: no front matter", route)
		case !fmTitleRe.MatchString(fm[1]):
			t.Errorf("docs/%s: front matter has no title", route)
		case !fmDescRe.MatchString(fm[1]):
			t.Errorf("docs/%s: front matter has no description", route)
		}
		for i, l := range proseLines(text) {
			if htmlCommentRe.MatchString(l) {
				t.Errorf("docs/%s:%d: html comment; mdx needs {/* ... */}", route, i+1)
			}
			if autolinkRe.MatchString(l) {
				t.Errorf("docs/%s:%d: angle-bracket autolink; mdx needs [text](url)", route, i+1)
			}
		}
	}
}

// TestNoEmDash keeps the em dash out of the documentation and the site
// configuration.
func TestNoEmDash(t *testing.T) {
	root := repoRoot(t)
	files := append(markdownFiles(t, root), filepath.Join(root, "docs", "docs.json"))
	for _, file := range files {
		for i, l := range strings.Split(read(t, file), "\n") {
			if strings.ContainsRune(l, '—') {
				t.Errorf("%s:%d: em dash", rel(root, file), i+1)
			}
		}
	}
}
