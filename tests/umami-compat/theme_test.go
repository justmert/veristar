//go:build umamicompat

package umamicompat

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// the veristar theme on umami's dashboard (docs/adr/0004) depends on names in
// umami's html, css and javascript, never on its code. this test reads them
// from the running image and fails when an upgrade renames, drops or adds one,
// so the theme can never break silently:
//
//   - theme.css names the umami version it was reviewed for, and it must be
//     the running one, as with the reviewed route list;
//   - every --zen-* and tailwind property theme.css sets is still defined by
//     umami's css, and the selectors and classes it builds on still exist;
//   - umami still writes data-theme and the dark class, and still paints the
//     map and chart colours the stylesheet answers to;
//   - the html still has one </head> for the stylesheet link;
//   - the icon paths umami links, and the ones its manifest and browserconfig
//     name, are exactly the paths the web caddy serves from the theme, and
//     umami still has a file at each of them.

const (
	themeCSS   = "../../deploy/caddy/umami-theme/site/_veristar/theme.css"
	caddyfile  = "../../deploy/caddy/web.Caddyfile"
	themeLink  = `<link rel="stylesheet" href="/_veristar/theme.css">`
	themeMatch = "@theme path "
)

func (e *env) fetch(t *testing.T, path string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx(t), http.MethodGet, e.base.String()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %v", path, resp.StatusCode, err)
	}
	return string(b)
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// themeIcons is the @theme matcher of web.Caddyfile without its /_veristar/*
// prefix: the umami paths caddy answers from the theme.
func themeIcons(t *testing.T) []string {
	t.Helper()
	for _, line := range strings.Split(readFile(t, caddyfile), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), themeMatch); ok {
			var out []string
			for _, p := range strings.Fields(rest) {
				if p != "/_veristar/*" {
					out = append(out, p)
				}
			}
			sort.Strings(out)
			return out
		}
	}
	t.Fatal("web.Caddyfile has no @theme path matcher")
	return nil
}

func TestThemeReviewedForThisVersion(t *testing.T) {
	e := setup(t)
	var doc struct {
		Info struct{ Version string } `json:"info"`
	}
	if err := json.Unmarshal([]byte(e.fetch(t, "/openapi.json")), &doc); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`umami-version: ([0-9.]+)`).FindStringSubmatch(readFile(t, themeCSS))
	if m == nil {
		t.Fatal("theme.css names no umami-version")
	}
	if m[1] != doc.Info.Version {
		t.Fatalf("theme.css was reviewed for umami %s, the image is %s; review the theme "+
			"(docs/runbooks/umami-upgrade.md) and move its umami-version", m[1], doc.Info.Version)
	}
}

func TestThemeNamesStillExist(t *testing.T) {
	e := setup(t)
	_, website := e.site(t)
	page := e.fetch(t, "/websites/"+website)

	if n := strings.Count(page, "</head>"); n != 1 {
		t.Errorf("the html has %d </head>, the caddy rewrite needs exactly one", n)
	}
	if strings.Contains(page, "/_veristar/") {
		t.Error("umami's own html names /_veristar/, which the web caddy reserves")
	}

	// umami stays credited in the title of every page (adr 0004), which the
	// theme leaves alone
	if !regexp.MustCompile(`<title>[^<]*\| Umami</title>`).MatchString(page) {
		t.Error("umami's page title no longer ends in \"| Umami\"; adr 0004 relies on it as the visible credit")
	}

	// umami's css, as the page links it
	var css strings.Builder
	for _, m := range regexp.MustCompile(`<link rel="stylesheet" href="(/_next/static/[^"]+\.css)"`).FindAllStringSubmatch(page, -1) {
		css.WriteString(e.fetch(t, m[1]))
	}
	if css.Len() == 0 {
		t.Fatal("the page links no stylesheet of umami's")
	}
	umamiCSS := css.String()
	theme := readFile(t, themeCSS)
	props := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--(?:zen|radius)-[a-z0-9-]+):`).FindAllStringSubmatch(theme, -1) {
		props[m[1]] = true
	}
	if len(props) < 40 {
		t.Fatalf("found only %d umami properties in theme.css", len(props))
	}
	for p := range props {
		if !regexp.MustCompile(`[{;]` + regexp.QuoteMeta(p) + `:`).MatchString(umamiCSS) {
			t.Errorf("umami's css no longer defines %s, which theme.css sets", p)
		}
	}
	for _, s := range []string{
		// the selectors zen defines its tokens on, which theme.css outranks
		"[data-theme=light]{", "[data-theme=dark],.dark{", "[data-palette=neutral]{",
		// the classes theme.css restyles
		".bg-primary{", ".bg-status-error{", ".text-white{",
		// umami's global.css: the page font and ground come from zen's tokens
		"font-family:var(--zen-font-family)", "background-color:var(--zen-surface-raised)",
	} {
		if !strings.Contains(umamiCSS, s) {
			t.Errorf("umami's css no longer has %q, which theme.css builds on", s)
		}
	}

	// umami's javascript, as the page loads it
	var js strings.Builder
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`/_next/static/chunks/[A-Za-z0-9_.~-]+\.js`).FindAllString(page, -1) {
		if !seen[m] {
			seen[m] = true
			js.WriteString(e.fetch(t, m))
		}
	}
	for _, s := range []string{
		// zen marks the mode on <html>; theme.css keys light mode on it
		`setAttribute("data-theme"`, `classList.add("dark")`,
		// the chart and map colours theme.css answers to (src/lib/constants.ts)
		`line:"#d9d9d9"`, `fill:"#f9f9f9"`, `fill:"#191919"`,
		// the danger button whose text colour theme.css changes
		`bg-status-error text-white`,
		// umami's logo, which theme.css swaps for the veristar mark
		// (src/components/svg/Logo.tsx), and the delta chips' grounds
		// (src/components/metrics/ChangeLabel.tsx)
		`viewBox:"0 0 428 389.11"`,
		`background:"var(--zen-status-success-bg)"`, `background:"var(--zen-status-error-bg)"`,
	} {
		if !strings.Contains(js.String(), s) {
			t.Errorf("umami's javascript no longer has %q, which theme.css builds on", s)
		}
	}

	// the icon paths: what umami links and names, against what caddy serves
	linked := map[string]bool{}
	for _, m := range regexp.MustCompile(`<link rel="(?:icon|apple-touch-icon|manifest|mask-icon)"[^>]*href="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		linked[m[1]] = true
	}
	var manifest struct {
		Icons []struct{ Src string } `json:"icons"`
	}
	if err := json.Unmarshal([]byte(e.fetch(t, "/site.webmanifest")), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, i := range manifest.Icons {
		linked[i.Src] = true
	}
	for _, m := range regexp.MustCompile(`src="([^"]+)"`).FindAllStringSubmatch(e.fetch(t, "/browserconfig.xml"), -1) {
		linked[m[1]] = true
	}
	// umami's manifest names the browserconfig only by convention (a
	// browser asks for /browserconfig.xml itself)
	linked["/browserconfig.xml"] = true
	var got []string
	for p := range linked {
		got = append(got, p)
	}
	sort.Strings(got)
	want := themeIcons(t)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("umami's icon paths changed; update @theme in web.Caddyfile and the theme's files.\numami: %v\ncaddy: %v", got, want)
	}
	for _, p := range want {
		e.fetch(t, p)
	}
}
