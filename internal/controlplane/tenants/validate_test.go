package tenants

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Stellar DEX":                  "stellar-dex",
		"  Soroban -- Swap!! ":         "soroban-swap",
		"日本":                           "",
		"a" + strings.Repeat("-b", 40): strings.TrimRight(("a" + strings.Repeat("-b", 40))[:63], "-"),
	} {
		got := DeriveSlug(in)
		require.Equal(t, want, got, in)
		if got != "" {
			require.Regexp(t, slugRe, got)
		}
	}
}

func TestText(t *testing.T) {
	var f Fields
	require.Equal(t, "ok", Text(&f, "a", "  ok \n", 1, 10))
	Text(&f, "b", "x\x00y", 0, 10)
	Text(&f, "c", "line\nnext\ttab", 0, 20)
	Text(&f, "d", strings.Repeat("ü", 11), 0, 10)
	Text(&f, "e", "   ", 1, 10)
	require.Equal(t, map[string]string{"b": "invalid_format", "d": "too_long", "e": "required"}, f.codes)
}
