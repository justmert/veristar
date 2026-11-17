package natsx_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/tests/fixtures/natsctr"
)

// every server configuration of the platform carries the same message size
// limit the collector's exporter checks against
func TestMaxPayloadConfigured(t *testing.T) {
	root := natsctr.RepoRoot(t)
	line := regexp.MustCompile(`(?m)^max_payload:\s*(\d+)\s*$`)
	for _, f := range []string{
		"infra/ansible/roles/edge/templates/nats.conf.j2",
		"deploy/compose/nats-dev.conf",
	} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		m := line.FindSubmatch(b)
		if m == nil {
			t.Errorf("%s sets no max_payload", f)
			continue
		}
		if string(m[1]) != strconv.Itoa(natsx.MaxPayload) {
			t.Errorf("%s: max_payload %s, want %d", f, m[1], natsx.MaxPayload)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "deploy/compose/dev.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`nats-dev\.conf:/etc/nats/nats\.conf:ro`).Match(b) {
		t.Error("dev.yml does not mount nats-dev.conf for the nats server")
	}
}
