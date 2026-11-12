# Privacy strategy

> **Status: draft, pending legal review an operator input.** Not legal advice. Last
> updated 2026-11-12. Legal statements cite `sources.md` (S1 to S11); technical
> statements point to the code or test that shows them. A statement that is
> neither sourced nor tested does not belong here.

## 1. Roles

- Each **tenant** (an enrolled project) is the **controller** of the data of its
  website visitors and API clients.
- The **service operator** (today SCF, after handover the community operator)
  is the tenant's **processor**. It processes that data only to provide the
  service, under the terms in `terms.md` and the processing terms in
  `dpa-template.md` (GDPR Art. 28, S10).
- **SCF operators** see aggregates only: the global views and the machine API
  read `agg.*`, `api.metrics_hourly`, `api.metrics_daily` and counters, never
  raw events, sessions or Umami (the design and 4.8; role `cp_operator`, tested in
  `internal/db/boundaries_test.go`).
- Open question for counsel (the design): whether SCF's evaluative use of
  cross-tenant aggregates is reuse "for the provider's own account". CNIL's
  self-assessment tool lists that as excluded unless the data was effectively
  anonymised first (S2). The aggregates are counts per site and day, not
  rounded; whether they are anonymous in the CNIL sense is not settled here.

## 2. Data inventory per component

"Memory" means the data exists only in a process's memory or in a NATS
memory-backed store and never reaches a disk. "Queued" means a NATS JetStream
file-backed stream. "Stored" means PostgreSQL.

| Component | Data | Where | How long |
|---|---|---|---|
| Edge Caddy | client address, `User-Agent`, request line | memory | the request; access logs delete the remote address, client address, `User-Agent`, `X-Forwarded-For`, `X-Real-Ip` and `Forwarded` fields (`deploy/caddy/edge.Caddyfile`) |
| Gateway | client address (in `X-Edge-Client-Ip` from Caddy) | memory | the request. It is truncated to /24 (IPv4) or /48 (IPv6) for the country lookup and is never logged, queued or stored (`internal/gateway/clientip.go`, `docs/reference/geo.md`) |
| Gateway | daily visitor secret, daily IP-cluster secret, daily proof key | `edge_secrets`, memory bucket | until the next UTC midnight (per-key TTL), then gone; gateways zero their copies at midnight (`internal/gateway/visitor.go`) |
| Gateway | IP-cluster sets: keyed HMAC of the truncated network, visitor tokens | memory | a sliding hour |
| `WEB_EVENTS` | accepted event: site, band, visitor token, user agent (cut to the this stage limit), country code, hostname, path with allowlisted query, referrer, title, screen, language, event name and data | queued | until the replayer delivers it, at most 72 hours |
| `WEB_DLQ` | events Umami refused permanently, same fields | queued | at most 7 days; erasure deletes matching messages |
| `visit_state` | per site and visitor token: Umami session id, cache token, last event time; duplicate-guard keys | memory bucket | 24 hours after the last write |
| Umami (`umami` database) | `session` (browser, OS, device, screen, language, country), `website_event` (time, path, query, referrer path, query and domain, title, event name, hostname), `event_data` | stored | `WEB_RAW_RETENTION_MONTHS`, default 25 (section 4) |
| `agg.web_daily`, `agg.web_daily_dim` | per site, band and UTC day: page views, visitors, visits, bounces, total time; ranked referrers, countries, devices, browsers, OSes | stored | as long as the site exists; removal follows `removal_keep_aggregates` |
| OTLP collector | tenant metrics and spans; logs are accepted and discarded | memory | the request; spans become duration histograms and are dropped (`collector/`) |
| `API_METRICS` | filtered metric batches: tenant, service, normalized route, method, status class and code, histogram buckets | queued | until stored, at most 7 days |
| `api.*` | one-minute, hourly and daily rows of the same dimensions | stored | minutes `API_MINUTE_RETENTION_DAYS`, hours `API_HOURLY_RETENTION_MONTHS` (at least 24), days as long as the tenant exists |
| Control plane (`cp.*`) | GitHub numeric id and login of console users, session token hashes, tenants, sites, OTLP token hashes, audit log, erasure requests | stored | while the account or tenant exists; the audit log is append-only |

No component stores an IP address. The privacy audit test (`tests/privacy/`)
sends real web and API traffic from marker addresses on a dedicated Docker
network and scans both databases, every JetStream stream and KV bucket, every
service log, the Caddy logs and every container volume for them.

## 3. IP address and visitor token design

1. The gateway reads the client address from the one header Caddy sets and
   keeps it in memory for the request.
2. Country: the /24 or /48 network is looked up in the local DB-IP Country
   Lite file. Only the ISO country code leaves the gateway. Region and city are
   never looked up.
3. Visitor token: `vt = hex(HMAC-SHA256(daily_secret, site_id || 0x00 || ip ||
   0x00 || user_agent))`. The secret is 32 random bytes per
   UTC day, created atomically in the memory bucket `edge_secrets` with a TTL
   that ends at the next UTC midnight. No copy is written to disk.
4. The replayer sends Umami the constant address `192.0.2.1` (TEST-NET-1) and
   passes `vt` as Umami's client salt, so Umami's session id depends on the
   token and never on a real address.
5. After midnight nobody can recompute a token of the previous day, so stored
   sessions cannot be linked back to an address and user agent. The CJEU held
   in C-413/23 P that pseudonymised data is personal data for whoever can
   re-identify it (S8). Applying that to a destroyed salt is our inference,
   recorded in S8 for counsel. During the day the token is computable by the
   gateways, which is why same-day erasure exists (section 6).

Consequences, stated in the tenant docs: visitors are unique per UTC day; a
person visiting on two days counts twice; a full NATS cluster restart during a
day replaces the day's secrets, so visitors seen before it count again.

## 4. Retention per store

| Store | Retention | Mechanism |
|---|---|---|
| Client address | the request | never written |
| `edge_secrets` | until next UTC midnight | per-key TTL, memory storage |
| `visit_state` | 24 hours after the last write | bucket TTL, memory storage |
| `WEB_EVENTS` | until delivered, at most 72 hours | work-queue stream, max age |
| `WEB_DLQ` | at most 7 days | max age; erasure deletes earlier |
| Umami raw rows | `WEB_RAW_RETENTION_MONTHS` (default 25) | `jobs retain-web` as role `jobs_retention` (`internal/jobs/retainweb`) |
| `agg.web_daily*` | while the site exists | removal purge (`agg.purge_tenant`) |
| `api.metrics_minute` | `API_MINUTE_RETENTION_DAYS` | `jobs` task `api-retention` |
| `api.metrics_hourly` | `API_HOURLY_RETENTION_MONTHS`, at least 24 | `jobs` task `api-retention` |
| Control plane sessions | 7 days absolute, 24 hours idle | `jobs` task `session-prune` |

`jobs retain-web` deletes Umami `website_event`, `event_data` and the disabled
features' per-event tables by `created_at`, then sessions that have no event
left and their `session_data` and `session_link` rows. It reads
`_prisma_migrations` first and refuses to run unless the database holds exactly
the 27 migrations of the pinned Umami 3.5.0 image with their checksums, and
the columns it uses exist (tested in `internal/jobs/retainweb/retainweb_test.go`).
The role `jobs_retention` has select and delete on those tables and select on
`_prisma_migrations`, nothing else (same test). It is not scheduled by the jobs
daemon; deployments run it once a day (this stage adds the timer).

Raw retention bounds what a tenant dashboard covers: with the default of 25
months, Umami shows 24 full months.

## 5. Umami features that are off, and how

Umami 3.5.0 has no global switch for these features (the design, correction
8), so they are disabled by not shipping their client code and by blocking
their routes:

| Feature | How it is off |
|---|---|
| Session replay, heatmaps, web vitals, error tracking | the tracker (`packages/tracker`) contains no recorder, heatmap, vitals or error code; `/recorder.js` and `/script.js` are denied on `web.<BASE_DOMAIN>`; Umami's collect routes are reachable only on the private network |
| Identify and distinct ids | the tracker has no identify call; the wire format has no id field (`docs/reference/wire-v1.md`) |
| Links, pixels, shares, boards | `/api/links*`, `/api/pixels*`, `/api/share*`, `/api/boards*`, `/share/*`, `/q/*`, `/p/*` are denied for every method |
| Direct collection | `/api/send`, `/api/batch`, `/api/record` are denied publicly; only the replayer reaches them |
| Region and city | the gateway never looks them up; Umami receives only a country header with the constant address |
| Telemetry, update checks, favicon lookups | `PRIVATE_MODE=1`, `DISABLE_TELEMETRY=1`, `DISABLE_UPDATES=1` |

The deny list lives in `deploy/caddy/web.Caddyfile` and
`docs/reference/umami-contract.md`.

## 6. Objection and consent controls for tenants

- **Global Privacy Control.** The tracker sends nothing when
  `navigator.globalPrivacyControl` is true, and the gateway drops any event
  that arrives with `Sec-GPC: 1` (`internal/gateway/server.go`).
- **Opt-out flag.** The tracker sends nothing when the browser's
  `localStorage` holds `umami.disabled`. This is the only storage the tracker
  reads, and it never writes any. A tenant offers its visitors an opt-out link
  that sets this key.
- **Do Not Track.** With `data-do-not-track="true"` the tracker also honours
  the browser's Do Not Track setting.
- **Deferred start.** With `data-auto-track="false"` the tracker loads, sends
  nothing and fetches no challenge until the site calls `umami.start()`, for
  example after consent (`docs/reference/wire-v1.md`).
- **Erasure** (`erasure.md`): one visitor's visit of the current UTC day, one
  session at any time during raw retention, or a whole site.

## 7. Redaction and its limits

Before an event is queued, the gateway (`internal/redact`,
`internal/gateway/sanitize.go`):

- replaces all ten SEP-23 strkey types (G, M, C, W, L, B, T, X, P, S),
  email addresses and federation addresses in the path, kept query values,
  referrer, title and event data; S secret seeds become `{redacted-secret}`;
- drops every query parameter outside the allowlist (`utm_source`,
  `utm_medium`, `utm_campaign`, `utm_content`, `utm_term`, `ref`);
- drops URL fragments except a `#/` hash route, without its own query.

It cannot recognise every identifier a tenant puts in a path, title or event:
an order number, a user name or a database id in a URL passes through. The
terms forbid personal data in URLs, titles, referrers and event data, and
session erasure and `site_reset` remain available for the whole raw retention
period.

## 8. Known gaps

- **Referrer detail.** Umami stores the referrer's path and query (after
  redaction and the allowlist), not only its host. CNIL's self-assessment tool
  lists "referrer limited to the host" among its criteria (S2), so this
  deployment does not meet that criterion as built.
- **Exact counts.** Dashboards show exact counts. CNIL's tool recommends
  rounding reports to the nearest ten (S2); Umami's dashboards do not.
- **Erasure race.** See `erasure.md`, "Limits".
- **NATS file blocks.** An acknowledged `WEB_EVENTS` message is removed from
  the stream, but its bytes can stay in a JetStream file block until the block
  is compacted or expires (at most 72 hours). Erasure deletes `WEB_DLQ`
  messages with secure delete, which overwrites them.
- **No production deployment yet.** Phases 15 and 16 build the hosting and
  backups. `data-residency.md` states what they must satisfy.
