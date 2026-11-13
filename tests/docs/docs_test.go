// Package docs checks the documentation set (the design): every relative
// link resolves, and every code block a guide includes from examples/ is
// byte for byte the code the examples run, so guides and tested code cannot
// drift.
//
//	go test ./tests/docs/                 check
//	go test ./tests/docs/ -args -update   rewrite included blocks from source
//
// The pages are the Mintlify site rooted at docs/ (docs/docs.json), so
// directives are MDX comments, each on the line right before a fenced code
// block:
//
//	{/* include examples/<path> */}              the whole file
//	{/* include examples/<path> region=<name> */} the lines between
//	                                             "docs:start <name>" and
//	                                             "docs:end <name>"
//	{/* include examples/<dir>/go.mod region=require */}
//	                                             the first require block
//	{/* snippet version=<x.y.z> [options] */}    the tracker tag the console
//	                                             builds for the example site
//
// Region markers follow apps/console/src/lib/otel-guides.ts: a comment line
// (//, #, <!--, /* or {/*) holding "docs:start <name>"; the marker lines are
// dropped, trailing blank lines removed and the common indentation stripped.
package docs

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/controlplane"
)

var update = flag.Bool("update", false, "rewrite included code blocks from their sources")

// the values every guide uses in its examples. analytics.example is a
// reserved example domain (rfc 2606), so it can never be mistaken for a real
// deployment; the guides say that the console shows each tenant's own values.
const (
	ExampleGateway = "https://in.analytics.example"
	ExampleSiteID  = "3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller information")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// markdownFiles lists the documentation: every .md and .mdx file under docs/
// plus the markdown files at the repository root and in each example
func markdownFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".mdx")) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"*.md", "examples/*/README.md"} {
		m, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m...)
	}
	slices.Sort(out)
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}

/* links */

var (
	fenceRe      = regexp.MustCompile("^\\s*(```|~~~)")
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
	// [text](target) and ![alt](target), target without spaces, optional title
	linkRe = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	// [label]: target reference definitions
	refDefRe = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*<?(\S+?)>?(?:\s+.*)?$`)
	hrefRe   = regexp.MustCompile(`\b(?:href|src)="([^"]+)"`)
	schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
	headRe   = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	anchorRe = regexp.MustCompile(`<a\s+(?:name|id)="([^"]+)"`)
	// links into this repository on github, by branch
	repoLinkRe = regexp.MustCompile(`^https://github\.com/justmert/veristar/(blob|tree)/main/([^?]+)$`)
)

// proseLines returns the lines outside fenced code blocks, with inline code
// removed, so example links inside code are not checked as links
func proseLines(text string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if fenceRe.MatchString(line) {
			inFence = !inFence
			out = append(out, "")
			continue
		}
		if inFence {
			out = append(out, "")
			continue
		}
		out = append(out, inlineCodeRe.ReplaceAllString(line, ""))
	}
	return out
}

// slug is github's heading anchor: link text instead of links, lower case,
// punctuation other than hyphens and underscores removed (backticks and
// asterisks of inline markup with it), spaces turned into hyphens
func slug(heading string) string {
	heading = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`).ReplaceAllString(heading, "$1")
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func anchors(text string) map[string]bool {
	set := map[string]bool{}
	seen := map[string]int{}
	for _, line := range proseLines(text) {
		if m := headRe.FindStringSubmatch(line); m != nil {
			s := slug(m[2])
			if n := seen[s]; n > 0 {
				set[fmt.Sprintf("%s-%d", s, n)] = true
			} else {
				set[s] = true
			}
			seen[s]++
		}
		for _, m := range anchorRe.FindAllStringSubmatch(line, -1) {
			set[m[1]] = true
		}
	}
	return set
}

// sitePage resolves a site route such as /onboarding/web/quickstart to the
// file under docs/ that mintlify serves for it: the path itself when it names
// a file, otherwise the path with .md or .mdx appended
func sitePage(docsDir, route string) string {
	p := filepath.Join(docsDir, filepath.FromSlash(route))
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	for _, ext := range []string{".mdx", ".md"} {
		if _, err := os.Stat(p + ext); err == nil {
			return p + ext
		}
	}
	return p
}

func TestRelativeLinks(t *testing.T) {
	root := repoRoot(t)
	docsDir := filepath.Join(root, "docs")
	anchorCache := map[string]map[string]bool{}
	anchorsOf := func(p string) map[string]bool {
		if a, ok := anchorCache[p]; ok {
			return a
		}
		a := anchors(read(t, p))
		anchorCache[p] = a
		return a
	}
	checked := 0
	for _, file := range markdownFiles(t, root) {
		for i, line := range proseLines(read(t, file)) {
			var targets []string
			for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[1])
			}
			if m := refDefRe.FindStringSubmatch(line); m != nil {
				targets = append(targets, m[1])
			}
			for _, m := range hrefRe.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[1])
			}
			for _, target := range targets {
				// the site serves only docs/, so pages link to the rest of the
				// repository on github; those links must name existing paths
				if m := repoLinkRe.FindStringSubmatch(target); m != nil {
					checked++
					p, _, _ := strings.Cut(m[2], "#")
					if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
						t.Errorf("%s:%d: %s: no %s in the repository", rel(root, file), i+1, target, p)
					}
					continue
				}
				if schemeRe.MatchString(target) || strings.HasPrefix(target, "//") {
					continue
				}
				checked++
				where := fmt.Sprintf("%s:%d: %s", rel(root, file), i+1, target)
				path, frag, _ := strings.Cut(target, "#")
				dest := file
				if path != "" {
					switch {
					case strings.HasPrefix(path, "/") && strings.HasPrefix(file, docsDir+string(filepath.Separator)):
						// a site route: the page's path under docs/ without its extension
						dest = sitePage(docsDir, path)
					case strings.HasPrefix(path, "/"):
						dest = filepath.Join(root, path)
					default:
						dest = filepath.Join(filepath.Dir(file), path)
					}
					if _, err := os.Stat(dest); err != nil {
						t.Errorf("%s: target does not exist", where)
						continue
					}
				}
				if frag != "" && (strings.HasSuffix(dest, ".md") || strings.HasSuffix(dest, ".mdx")) && !anchorsOf(dest)[frag] {
					t.Errorf("%s: no heading with anchor #%s in %s", where, frag, rel(root, dest))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no relative link found; the scanner is broken")
	}
	t.Logf("%d relative links checked", checked)
}

/* includes */

var goRequireRe = regexp.MustCompile(`(?ms)^require \(.*?^\)$`)

func goRequires(mod string) (string, error) {
	m := goRequireRe.FindString(mod)
	if m == "" {
		return "", errors.New("go.mod has no require block")
	}
	return m, nil
}

var (
	includeRe = regexp.MustCompile(`^\{/\* include (\S+)(?: region=(\S+))? \*/\}$`)
	snippetRe = regexp.MustCompile(`^\{/\* snippet version=(\d+\.\d+\.\d+)((?: [a-z-]+(?:=\S+)?)*) \*/\}$`)
	// the html comment form of a directive, which mdx cannot parse
	oldDirectiveRe = regexp.MustCompile(`^\s*<!--\s*(include|snippet)\b`)
)

// region returns the lines between the docs:start and docs:end markers of
// name, as otel-guides.ts region() does
func region(source, name string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	marker := func(kind string) int {
		re := regexp.MustCompile(`(//|#|<!--|/\*)\s*docs:` + kind + ` ` + regexp.QuoteMeta(name) + `(\s|$)`)
		for i, l := range lines {
			if re.MatchString(l) {
				return i
			}
		}
		return -1
	}
	start, end := marker("start"), marker("end")
	if start < 0 || end < 0 || end <= start+1 {
		return "", fmt.Errorf("docs region %q not found", name)
	}
	body := slices.Clone(lines[start+1 : end])
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	indent := -1
	for _, l := range body {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	for i, l := range body {
		if len(l) >= indent {
			body[i] = l[indent:]
		} else {
			body[i] = strings.TrimLeft(l, " \t")
		}
	}
	text := strings.Join(body, "\n")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("docs region %q is empty", name)
	}
	return text, nil
}

// exampleTag is the console's tag for the example site, with the changes a
// guide tells the tenant to make. options, in the order given:
//
//	wallets-kit        adds data-wallets-kit="true"
//	auto-track-off     adds data-auto-track="false"
//	self-hosted=<path> serves sdk.js from <path> on the tenant's site and adds
//	                   data-host-url with the gateway origin
func exampleTag(root, version string, options []string) (string, error) {
	tr, err := controlplane.LoadTracker(filepath.Join(root, "packages", "tracker", "releases"), ExampleGateway)
	if err != nil {
		return "", err
	}
	tag, _, err := tr.Snippet(uuid.MustParse(ExampleSiteID), version)
	if err != nil {
		return "", err
	}
	const closing = "></script>"
	open := strings.TrimSuffix(tag, closing)
	for _, o := range options {
		name, value, _ := strings.Cut(o, "=")
		switch name {
		case "wallets-kit":
			open += ` data-wallets-kit="true"`
		case "auto-track-off":
			open += ` data-auto-track="false"`
		case "self-hosted":
			if !strings.HasPrefix(value, "/") {
				return "", fmt.Errorf("self-hosted needs an absolute path, got %q", value)
			}
			open = strings.Replace(open, `src="`+tr.ScriptURL(version)+`"`, `src="`+value+`"`, 1)
			open += ` data-host-url="` + ExampleGateway + `"`
		default:
			return "", fmt.Errorf("unknown snippet option %q", o)
		}
	}
	return open + closing, nil
}

// block is one directive and the fenced code block after it
type block struct {
	line      int // 1-based line of the directive
	open      int // index of the opening fence line
	close     int // index of the closing fence line
	directive string
	want      string
	err       error
}

// guideBlocks finds every directive in a markdown file and the fenced block
// that follows it, and computes what the block must contain
func guideBlocks(root, text string) ([]block, error) {
	lines := strings.Split(text, "\n")
	var out []block
	for i, l := range lines {
		inc := includeRe.FindStringSubmatch(l)
		snip := snippetRe.FindStringSubmatch(l)
		if inc == nil && snip == nil {
			if oldDirectiveRe.MatchString(l) {
				return nil, fmt.Errorf("line %d: directive %q is an html comment; mdx needs {/* ... */}", i+1, l)
			}
			if strings.HasPrefix(strings.TrimSpace(l), "{/* include") || strings.HasPrefix(strings.TrimSpace(l), "{/* snippet") {
				return nil, fmt.Errorf("line %d: malformed directive %q", i+1, l)
			}
			continue
		}
		b := block{line: i + 1, directive: l}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "```") {
			return nil, fmt.Errorf("line %d: directive is not followed by a fenced code block", i+1)
		}
		b.open = i + 1
		b.close = -1
		for j := i + 2; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "```") {
				b.close = j
				break
			}
		}
		if b.close < 0 {
			return nil, fmt.Errorf("line %d: unclosed code block", i+1)
		}
		switch {
		case inc != nil:
			path := inc[1]
			if !strings.HasPrefix(path, "examples/") {
				b.err = fmt.Errorf("include %s: only files under examples/ may be included", path)
				break
			}
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				b.err = err
				break
			}
			switch {
			case inc[2] == "":
				b.want = strings.TrimRight(string(src), "\n")
			case inc[2] == "require" && strings.HasSuffix(path, "/go.mod"):
				// go mod tidy moves comment lines, so a go.mod cannot hold
				// markers; its first require block (the direct dependencies)
				// is taken instead, as otel-guides.ts goRequires() does
				b.want, b.err = goRequires(string(src))
			default:
				b.want, b.err = region(string(src), inc[2])
			}
		default:
			b.want, b.err = exampleTag(root, snip[1], strings.Fields(snip[2]))
		}
		out = append(out, b)
	}
	return out, nil
}

func TestIncludedSnippets(t *testing.T) {
	root := repoRoot(t)
	total := 0
	for _, file := range markdownFiles(t, root) {
		text := read(t, file)
		blocks, err := guideBlocks(root, text)
		if err != nil {
			t.Errorf("%s: %v", rel(root, file), err)
			continue
		}
		if len(blocks) == 0 {
			continue
		}
		lines := strings.Split(text, "\n")
		changed := false
		// from the end, so rewriting a block keeps earlier indexes valid
		for k := len(blocks) - 1; k >= 0; k-- {
			b := blocks[k]
			total++
			if b.err != nil {
				t.Errorf("%s:%d: %v", rel(root, file), b.line, b.err)
				continue
			}
			got := strings.Join(lines[b.open+1:b.close], "\n")
			if got == b.want {
				continue
			}
			if *update {
				lines = slices.Concat(lines[:b.open+1], strings.Split(b.want, "\n"), lines[b.close:])
				changed = true
				continue
			}
			t.Errorf("%s:%d: code block differs from %s\n--- in the guide\n%s\n--- in the source\n%s",
				rel(root, file), b.line, b.directive, got, b.want)
		}
		if changed {
			if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("rewrote %s", rel(root, file))
		}
	}
	if total == 0 {
		t.Fatal("no include directive found; the scanner is broken")
	}
	t.Logf("%d included blocks checked", total)
}

// TestOnboardingCodeIsIncluded keeps the rule of the design: code in the
// onboarding guides comes from examples/ or the tag generator, never typed
// into the page. only shell commands (sh) and plain text output (text) may
// stand without a directive.
func TestOnboardingCodeIsIncluded(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "onboarding")
	files := 0
	for _, file := range markdownFiles(t, root) {
		if !strings.HasPrefix(file, dir+string(filepath.Separator)) {
			continue
		}
		files++
		lines := strings.Split(read(t, file), "\n")
		inFence := false
		for i, l := range lines {
			if !strings.HasPrefix(l, "```") {
				continue
			}
			if inFence {
				inFence = false
				continue
			}
			inFence = true
			lang := strings.TrimSpace(strings.TrimPrefix(l, "```"))
			if lang == "sh" || lang == "text" {
				continue
			}
			prev := ""
			if i > 0 {
				prev = lines[i-1]
			}
			if !includeRe.MatchString(prev) && !snippetRe.MatchString(prev) {
				t.Errorf("%s:%d: code block without an include or snippet directive", rel(root, file), i+1)
			}
		}
	}
	if files == 0 {
		t.Fatal("no onboarding guide found")
	}
}

// TestOnboardingStyle holds the writing rules the guides share: no em dash,
// and the example values instead of anything that could pass for a real
// deployment.
func TestOnboardingStyle(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", "onboarding")
	for _, file := range markdownFiles(t, root) {
		if !strings.HasPrefix(file, dir+string(filepath.Separator)) {
			continue
		}
		for i, l := range strings.Split(read(t, file), "\n") {
			if strings.ContainsRune(l, '\u2014') {
				t.Errorf("%s:%d: em dash", rel(root, file), i+1)
			}
			if bytes.Contains([]byte(l), []byte("<BASE_DOMAIN>")) {
				t.Errorf("%s:%d: write the example domain analytics.example instead of <BASE_DOMAIN>", rel(root, file), i+1)
			}
		}
	}
}

func TestRegionMatchesConsoleRule(t *testing.T) {
	src := "a\n  // docs:start x (note)\n    one\n\n      two\n\n  // docs:end x\nb"
	got, err := region(src, "x")
	if err != nil {
		t.Fatal(err)
	}
	if want := "one\n\n  two"; got != want {
		t.Fatalf("region = %q, want %q", got, want)
	}
	if _, err := region("// docs:start y\n// docs:end y", "y"); err == nil {
		t.Fatal("an empty region must be an error")
	}
	if _, err := region("<!-- docs:start z -->\n<p>\n<!-- docs:end z -->", "z"); err != nil {
		t.Fatalf("html markers: %v", err)
	}
	if _, err := region("{/* docs:start j */}\n<b />\n{/* docs:end j */}", "j"); err != nil {
		t.Fatalf("jsx markers: %v", err)
	}
}
