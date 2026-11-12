package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/justmert/stellar-analytics/internal/config"
)

// a replayer without configuration exits before connecting and names every
// missing key in one error.
func TestServeNamesEveryMissingKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, config.FromMap(nil), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code %d", code)
	}
	for _, k := range []string{"NATS_URL", "REPLAYER_NODE_ID", "REPLAYER_OPS_ADDR", "UMAMI_URL", "REPLAYER_WORKERS", "REPLAYER_MAX_IN_FLIGHT"} {
		if !strings.Contains(stderr.String(), k) {
			t.Errorf("error does not name %s: %s", k, stderr.String())
		}
	}
}

func TestUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"bogus"}, config.FromMap(nil), &stdout, &stderr); code != 2 {
		t.Fatalf("exit code %d", code)
	}
	if code := run(context.Background(), []string{"version"}, config.FromMap(nil), &stdout, &stderr); code != 0 || stdout.Len() == 0 {
		t.Fatalf("version: code %d, output %q", code, stdout.String())
	}
}
