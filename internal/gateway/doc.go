// Package gateway is the collect gateway behind in.<BASE_DOMAIN> (plan
// section 4.4 and this stage). it accepts web events from the tracker, checks
// them and queues them in jetstream.
//
// the checks are small units with no http server of their own, so each one
// can be tested alone and the server wires them in the order of section 4.4
// step 6:
//
//   - KeyStore holds the daily proof key from the edge_secrets bucket.
//   - Challenger issues the challenge of GET /v1/c.
//   - ProofVerifier checks tag, age, work and the per-node replay limit.
//   - CheckOrigin matches the Origin header against a site's origins.
//   - SignalsChecker checks Sec-Fetch-* consistency.
//
// every check fails with a *RejectError whose Reason is the stats outcome
// (reasons.go), so the stats publisher and the server share one set of names.
// the proof shows that a request went through the challenge flow, not that a
// browser ran the page; docs/reference/browser-proof.md says why.
package gateway
