package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/config"
	"github.com/justmert/stellar-analytics/internal/obs"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

func TestNATSApplyAndCheck(t *testing.T) {
	srv := natsctr.StartSingle(t)
	env := func(replicas string) *config.Env {
		return config.FromMap(map[string]string{"NATS_URL": srv.URL, "NATS_REPLICAS": replicas})
	}
	// the connection's closed handler logs from its own goroutine
	logs := &lockedBuffer{}
	log := obs.NewLogger(logs, slog.LevelInfo)
	ctx := context.Background()

	require.Error(t, runNATSWith(ctx, []string{"check"}, env("1"), log), "check must fail before apply")
	require.NoError(t, runNATSWith(ctx, []string{"apply"}, env("1"), log))
	logs.Reset()
	require.NoError(t, runNATSWith(ctx, []string{"apply"}, env("1"), log))
	require.NotContains(t, logs.String(), `"action":"create"`)
	require.NotContains(t, logs.String(), `"action":"update"`)
	require.Equal(t, 13, strings.Count(logs.String(), `"action":"unchanged"`))
	require.NoError(t, runNATSWith(ctx, []string{"check"}, env("1"), log))

	// three replicas cannot be placed on one server; the error is not hidden
	require.Error(t, runNATSWith(ctx, []string{"apply"}, env("3"), log))
	require.Error(t, runNATSWith(ctx, []string{"nope"}, env("1"), log))
	require.Error(t, runNATSWith(ctx, []string{"apply"}, config.FromMap(nil), log))
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.Reset()
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
