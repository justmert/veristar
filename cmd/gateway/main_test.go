package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
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

// SIGHUP runs the node's reload, which covers the geo file, the network
// data and the revoked tracker list; a failure is logged and the loop keeps
// answering later signals
func TestReloadOnHUP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hup := make(chan os.Signal)
	results := []error{errors.New("bad file"), nil}
	calls := make(chan struct{}, 2)
	var logs syncBuffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	done := make(chan struct{})
	go func() {
		reloadOnHUP(ctx, hup, func() error {
			err := results[0]
			results = results[1:]
			calls <- struct{}{}
			return err
		}, log)
		close(done)
	}()
	hup <- syscall.SIGHUP
	<-calls
	hup <- syscall.SIGHUP
	<-calls
	cancel()
	<-done
	out := logs.String()
	if !strings.Contains(out, "reload failed") || !strings.Contains(out, "bad file") || !strings.Contains(out, "list reloaded") {
		t.Fatalf("logs: %s", out)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
