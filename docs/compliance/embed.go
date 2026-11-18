// Package compliance embeds the terms of use the control plane serves, so
// the text a project accepts is the text in the binary.
package compliance

import _ "embed"

// TermsMarkdown is docs/compliance/terms.md, front matter included.
//
//go:embed terms.md
var TermsMarkdown []byte
