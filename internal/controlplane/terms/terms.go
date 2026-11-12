// Package terms parses the terms of use embedded from
// docs/compliance/terms.md (contract 4.1.5). the version in its front matter
// is the one tenants accept.
package terms

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/justmert/stellar-analytics/docs/compliance"
	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
)

// Terms is one version of the terms of use.
type Terms struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	// Markdown is the document without its front matter.
	Markdown string `json:"markdown"`
}

// draft versions are YYYY-MM-DD-draft.N, final ones YYYY-MM-DD.N
var versionRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(-draft)?\.\d+$`)

// Parse reads the front matter (version, status) and the body of a terms
// document. a missing or malformed version is an error, so the control
// plane refuses to start rather than accept an unversioned text.
func Parse(doc []byte) (Terms, error) {
	s := strings.ReplaceAll(string(doc), "\r\n", "\n")
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return Terms{}, errors.New("terms: document has no front matter")
	}
	fm, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return Terms{}, errors.New("terms: front matter is not closed")
	}
	var t Terms
	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "version":
			t.Version = strings.TrimSpace(v)
		case "status":
			t.Status = strings.TrimSpace(v)
		}
	}
	if !versionRe.MatchString(t.Version) {
		return Terms{}, fmt.Errorf("terms: front matter version %q is missing or malformed", t.Version)
	}
	if t.Status != "draft" && t.Status != "final" {
		return Terms{}, fmt.Errorf("terms: front matter status %q must be draft or final", t.Status)
	}
	if (t.Status == "draft") != strings.Contains(t.Version, "-draft") {
		return Terms{}, fmt.Errorf("terms: status %s does not match version %s", t.Status, t.Version)
	}
	t.Markdown = string(bytes.TrimLeft([]byte(body), "\n"))
	return t, nil
}

var current = sync.OnceValues(func() (Terms, error) { return Parse(compliance.TermsMarkdown) })

// Current returns the embedded terms. the control plane calls it at startup
// and stops on an error, so later calls cannot fail.
func Current() (Terms, error) { return current() }

// MustCurrent returns the embedded terms after startup checked them.
func MustCurrent() Terms {
	t, err := current()
	if err != nil {
		panic(err)
	}
	return t
}

// URL is where the console shows the terms.
const URL = "/terms"

// Routes serves GET /terms (public).
func Routes(r chi.Router) {
	r.Method(http.MethodGet, "/terms", httpx.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
		t, err := Current()
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, t)
		return nil
	}))
}
