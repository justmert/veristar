package terms

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedTerms(t *testing.T) {
	tm, err := Current()
	require.NoError(t, err)
	require.Equal(t, "2026-11-17-draft.5", tm.Version)
	require.Equal(t, "draft", tm.Status)
	require.True(t, strings.HasPrefix(tm.Markdown, "# Terms of use"))
	require.NotContains(t, tm.Markdown, "version:")
	require.NotContains(t, tm.Markdown, "\u2014", "no em dash in the terms")
	// the roles stay apart: the hosting operator is the processor, SCF
	// receives project statistics on the tenant's instruction
	require.Contains(t, tm.Markdown, "the hosting operator is the **processor**")
	require.Contains(t, tm.Markdown, "### 1.1 Project statistics for SCF")
	require.NotContains(t, tm.Markdown, "Today that is the Stellar\n  Community Fund")
}

func TestParseRefuses(t *testing.T) {
	for name, doc := range map[string]string{
		"no front matter":  "# Terms\n",
		"unclosed":         "---\nversion: 2026-11-12-draft.1\nstatus: draft\n# Terms\n",
		"no version":       "---\nstatus: draft\n---\n# Terms\n",
		"bad version":      "---\nversion: v1\nstatus: draft\n---\n# Terms\n",
		"bad status":       "---\nversion: 2026-11-12-draft.1\nstatus: maybe\n---\n# Terms\n",
		"status mismatch":  "---\nversion: 2026-11-12.1\nstatus: draft\n---\n# Terms\n",
		"status mismatch2": "---\nversion: 2026-11-12-draft.1\nstatus: final\n---\n# Terms\n",
	} {
		_, err := Parse([]byte(doc))
		require.Error(t, err, name)
	}
	tm, err := Parse([]byte("---\r\nversion: 2027-01-01.2\r\nstatus: final\r\n---\r\n\r\n# Terms\r\n"))
	require.NoError(t, err)
	require.Equal(t, "2027-01-01.2", tm.Version)
	require.Equal(t, "# Terms\n", tm.Markdown)
}
