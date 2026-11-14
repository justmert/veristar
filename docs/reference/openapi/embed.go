// Package openapi carries the openapi documents of the machine apis into the
// binaries that serve them, so the served contract is byte for byte the
// reviewed file and never read from disk at run time.
package openapi

import _ "embed"

// MetricsV1 is docs/reference/openapi/metrics-v1.yaml, the contract of
// /api/metrics/v1/.
//
//go:embed metrics-v1.yaml
var MetricsV1 []byte
