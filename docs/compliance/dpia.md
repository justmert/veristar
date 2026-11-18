---
title: "Data protection impact assessment"
sidebarTitle: "DPIA"
description: "The data protection impact assessment for cookieless web analytics and API request metrics."
---

The service, as processor, prepares this assessment to help tenants
(controllers) with their own assessment under GDPR Art. 35. Each tenant decides
with its counsel whether a DPIA is mandatory for it. This page is not legal
advice. Last updated 2026-11-18.

## 1. Description

Cookieless web analytics and API request metrics for projects in the Stellar
ecosystem, built on Umami and operated as a shared service. Data flows and the
inventory are in `privacy-strategy.md` section 2; residency in
`data-residency.md`.

## 2. Necessity and proportionality

| Principle | How the design meets it | Evidence |
|---|---|---|
| Purpose limitation | audience measurement for the tenant; SCF receives project aggregates only, on the tenant's instruction (`terms.md` section 1.1) | `cp_operator` role rights, `internal/db/boundaries_test.go` |
| Data minimisation | no IP stored, no region or city, query allowlist, strkey and email redaction, Umami features off, API traces and logs discarded | `privacy-strategy.md` sections 3, 5 and 7; `tests/privacy/` |
| Storage limitation | raw web rows deleted after 25 months by default; queues and memory stores expire in hours or days | `internal/jobs/retainweb`, `privacy-strategy.md` section 4 |
| Accuracy | not relevant to statistics; visitors are unique per UTC day by design | `privacy-strategy.md` section 3 |
| Data subject rights | same-day visitor erasure, session erasure, site reset; objection through GPC, opt-out flag, Do Not Track, consent mode | `erasure.md`, `privacy-strategy.md` section 6 |

## 3. Risks to data subjects

| Risk | Likelihood | Severity | Measures | Residual |
|---|---|---|---|---|
| Linking a visit to a person through the IP address | low | medium | address never stored or logged; token secret destroyed daily; constant address sent to Umami | low; during the day the gateways can recompute a token from a known address and user agent |
| Personal data in URLs, titles or event data | medium | medium | redaction of strkeys, emails, secret seeds; query allowlist; terms forbid personal data there | medium: free-form identifiers pass redaction (`privacy-strategy.md` section 7) |
| Cross-site tracking | low | medium | token includes the site id and a daily secret; no cookies; no identify | low |
| Cross-tenant data access | low | high | row-level security on every tenant table; Umami users limited to their team with view-only roles; isolation tests | low |
| Operator reuse of data | low | medium | operators read visitor and API client data only as aggregates, through the `cp_operator` role; beyond them they see only project metadata, members and the audit log, to review projects | low if the aggregates are anonymous under the CNIL criterion (S2, `privacy-strategy.md` section 1) |
| Breach of the database | low | medium | private network, no public database port, one role per service, signed images, SSH open only to the operator's admin address ranges (`../security/threat-model.md`) | stored rows hold no IP address and no visitor identifier that lasts beyond one UTC day |
| Retention beyond need | low | low | `jobs retain-web` on a daily systemd timer, expiring queues | low |

## 4. Consultation

No supervisory authority has been consulted. Tenants that conclude a DPIA
shows high residual risk must consult their authority before use (Art. 36).

## 5. Security measures (summary for Art. 28 and Art. 32)

- Transport encryption on every public endpoint (Caddy, TLS).
- Ingestion edge and core on a private network; no public host serves Umami,
  so its collect, admin and dashboard routes are reachable only on the
  private network (`deploy/caddy/umami_closed_test.go` checks the Caddy
  configuration).
- One database role per service with the least rights it needs, no role with
  `BYPASSRLS`, forced row-level security on every tenant table.
- One NATS user per service with subject allow lists (`internal/natsx/topology.go`).
- Secrets for daily visitor tokens in memory only.
- Audit log, append-only, for every change in the control plane.
- Signed release images, dependency gates and the 24-hour patch procedure
  (`../security/supply-chain.md`, `../runbooks/security-patch.md`).
- Backups encrypted by pgBackRest (AES-256) before upload, with point-in-time restore exercised on production
  (`../deploy/backups.md`, `../reference/drills/2026-11-13-azure.md`).

