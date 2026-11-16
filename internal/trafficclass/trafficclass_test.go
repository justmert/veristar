package trafficclass

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/justmert/veristar/internal/routenorm"
)

type corpusCase struct {
	line                         int
	method, path, ua, syn, class string
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	f, err := os.Open("testdata/corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	dash := func(s string) string {
		if s == "-" {
			return ""
		}
		return s
	}
	var out []corpusCase
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 5 {
			t.Fatalf("corpus line %d has %d columns", n, len(cols))
		}
		out = append(out, corpusCase{n, cols[0], dash(cols[1]), dash(cols[2]), dash(cols[3]), cols[4]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCorpus runs every case the way tenantguard does: the raw path goes
// through routenorm first, then the classifier sees the normalized route.
func TestCorpus(t *testing.T) {
	cases := loadCorpus(t)
	if len(cases) < 150 {
		t.Fatalf("corpus has %d cases, at least 150 are required", len(cases))
	}
	seen := map[Class]int{}
	for _, c := range cases {
		if !Valid(c.class) {
			t.Fatalf("line %d: unknown class %q", c.line, c.class)
		}
		route := ""
		if c.path != "" {
			route, _ = routenorm.Normalize(c.path)
		}
		got := Classify(Request{Method: c.method, Route: route, UserAgent: c.ua, Synthetic: c.syn})
		if string(got) != c.class {
			t.Errorf("line %d: Classify(%s %q -> %q, ua %q, synthetic %q) = %s, want %s",
				c.line, c.method, c.path, route, c.ua, c.syn, got, c.class)
		}
		seen[got]++
	}
	for _, c := range []Class{ClassApp, ClassPreflight, ClassHealth, ClassProbe} {
		if seen[c] < 10 {
			t.Errorf("the corpus holds %d cases of %s, at least 10 are required", seen[c], c)
		}
	}
}

func TestInfrastructure(t *testing.T) {
	if ClassApp.Infrastructure() {
		t.Error("app counted as infrastructure")
	}
	for _, c := range []Class{ClassPreflight, ClassHealth, ClassProbe} {
		if !c.Infrastructure() || !Valid(string(c)) {
			t.Errorf("%s is not a valid infrastructure class", c)
		}
	}
	if Valid("") || Valid("bot") || Valid("APP") {
		t.Error("Valid accepts a value that is not a class")
	}
}

// a normalized route classifies the same as the route it came from, so a
// tenant that sends http.route templates and one that sends raw paths get
// the same answer.
func TestStableUnderNormalization(t *testing.T) {
	for _, c := range loadCorpus(t) {
		if c.path == "" {
			continue
		}
		once, _ := routenorm.Normalize(c.path)
		twice, _ := routenorm.Normalize(once)
		a := Classify(Request{Method: c.method, Route: once, UserAgent: c.ua, Synthetic: c.syn})
		b := Classify(Request{Method: c.method, Route: twice, UserAgent: c.ua, Synthetic: c.syn})
		if a != b {
			t.Errorf("line %d: %s on %q, %s on %q", c.line, a, once, b, twice)
		}
	}
}

func FuzzClassify(f *testing.F) {
	f.Add("GET", "/healthz", "kube-probe/1.31", "")
	f.Add("OPTIONS", "/a", "", "test")
	f.Fuzz(func(t *testing.T, method, route, ua, syn string) {
		c := Classify(Request{Method: method, Route: route, UserAgent: ua, Synthetic: syn})
		if !Valid(string(c)) {
			t.Fatalf("class %q", c)
		}
	})
}
