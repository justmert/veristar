package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/justmert/veristar/internal/config"
)

// every missing key is reported exactly once, although two loaders read the
// same environment
func TestMissingConfigReportedOnce(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, config.FromMap(nil), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	for _, key := range []string{"NATS_URL", "NATS_REPLICAS", "GATEWAY_NODE_ID", "PROOF_BITS", "TRACKER_RELEASE_DIR"} {
		if n := strings.Count(stderr.String(), key+" is required"); n != 1 {
			t.Errorf("%s reported %d times: %s", key, n, stderr.String())
		}
	}
}
