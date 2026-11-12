package session

import (
	"context"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/stretchr/testify/require"
)

// the store refuses anything but a sha-256 hash, so a manager configured
// without HashTokenInStore fails loudly instead of storing cookie values
func TestStoreRefusesRawTokens(t *testing.T) {
	s := NewStore(nil, scs.GobCodec{})
	ctx := context.Background()
	raw := "dGhpcyBpcyBhIHJhdyBjb29raWUgdmFsdWUgb2YgMzIgYnl0ZXMhIQ" // not 32 bytes
	_, _, err := s.FindCtx(ctx, raw)
	require.Error(t, err)
	require.Error(t, s.CommitCtx(ctx, raw, nil, time.Now()))
	require.Error(t, s.DeleteCtx(ctx, "short"))
}
