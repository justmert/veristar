package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/natsx"
)

// a syntactically valid bcrypt hash; nats-server only compares it, so the
// test needs no password behind it
const testHash = "$2a$11$abcdefghijklmnopqrstuuJ0ZcVq2y8M0v3o7xGQ2e3s6B0s8b1yK"

func TestNATSAuthConfig(t *testing.T) {
	hashes := map[string]string{}
	for _, u := range natsx.Users() {
		hashes[u.Name] = testHash
	}
	in, err := json.Marshal(hashes)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, natsAuthConfig(bytes.NewReader(in), &out))
	require.True(t, strings.HasPrefix(out.String(), "authorization: "))
	require.Contains(t, out.String(), `"user": "gateway"`)
	require.Contains(t, out.String(), testHash)

	// a plain password is refused, so no usable credential reaches the file
	hashes["gateway"] = "plain-password"
	in, err = json.Marshal(hashes)
	require.NoError(t, err)
	require.ErrorContains(t, natsAuthConfig(bytes.NewReader(in), &out), "not a bcrypt hash")

	// a missing user is refused by RenderAuthorization
	delete(hashes, "gateway")
	in, err = json.Marshal(hashes)
	require.NoError(t, err)
	require.ErrorContains(t, natsAuthConfig(bytes.NewReader(in), &out), "no credential for nats user gateway")
}
