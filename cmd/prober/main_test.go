package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/justmert/stellar-analytics/internal/config"
)

func TestRunNamesMissingKeys(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), nil, config.FromMap(map[string]string{}), &out, &errb); code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, k := range []string{"PROBER_INGEST_URL", "PROBER_OTLP_TOKEN", "PROBER_NODES", "PROBER_GATUS_TOKEN"} {
		if !strings.Contains(errb.String(), k) {
			t.Errorf("error does not name %s: %s", k, errb.String())
		}
	}
	if code := run(context.Background(), []string{"bogus"}, config.FromMap(nil), &out, &errb); code != 2 {
		t.Fatalf("unknown command exit %d", code)
	}
}
