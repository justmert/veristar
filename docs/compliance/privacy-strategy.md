---
title: "Privacy strategy"
description: "How the platform minimises personal data: what is held in memory, queued and stored, and for how long."
---

How the service keeps personal data to a minimum: what it holds in memory,
queues and stores, for how long, and which controls tenants have. Legal
statements cite the legal sources register (`sources.md`); technical statements
point to the code or test that shows them. This page is not legal advice. Last
updated 2026-11-16.

## 1. Roles

- Each **tenant** (an enrolled project) is the **controller** of the data of its
  website visitors and API clients.
- The **hosting operator**, the party that runs the service and hosts its data,
  is each tenant's **processor**. It processes that data only to provide the
  service, on the tenant's documented instructions, under the terms in
  `terms.md` and the processing terms in `dpa-template.md` (GDPR Art. 28(3)(a),
  S10).
- **SCF** does not run the service. Each tenant, by accepting `terms.md`
  section 1.1 at enrollment, instructs the hosting operator to make aggregate
  statistics about its project available to SCF. They come from the same
  sources as the operator's global views and machine API: `agg.*`,
  `api.metrics_hourly`, `api.metrics_daily` and counters, never raw events,
  sessions or Umami (role `cp_operator`, tested in
  `internal/db/boundaries_test.go`).
- CNIL's self-assessment tool excludes reuse of the data "for the provider's
  own account" unless the data was effectively anonymised first (S2). The
  aggregates, including those SCF receives, are exact counts per site and day,
  not rounded, and this page makes no claim that they are anonymous in the CNIL
  sense. That is
  why France is "not as built" in `jurisdictions.md`.

## 2. Data inventory per component

"Memory" means the data exists only in a process's memory or in a NATS
memory-backed store and never reaches a disk. "Queued" means a NATS JetStream
file-backed stream. "Stored" means PostgreSQL.

| Component | Data | Where | How long |
|---|---|---|---|
| Edge Caddy | client address, `User-Agent`, request line | memory | the request; access logs delete the remote address, client address, `User-Agent`, `X-Forwarded-For`, `X-Real-Ip` and `Forwarded` fields (`deploy/caddy/edge.Caddyfile`) |
| Gateway | client address (in `X-Edge-Client-Ip` from Caddy) | memory | the request. It is truncated to /24 (IPv4) or /48 (IPv6) for the country and autonomous system lookups, compared whole against the Tor exit list, and is never logged, queued or stored (`internal/gateway/clientip.go`, `internal/gateway/network.go`, `docs/reference/geo.md`) |
| Gateway | network class of the event (`access`, `organisation`, `hosting`, `anonymiser`, `unknown`) and, per site and UTC day, the network as an HMAC of its autonomous system number under the daily IP-cluster secret, cut to 64 bits | memory, then counted on `EDGE_STATS` | the request; the class leaves only as a per-site count, the network key once per site and day; the autonomous system number itself never leaves the gateway |
| Gateway | networks reported per site and day, as keys | memory | the UTC day, in a bounded table |
| Gateway | daily visitor secret, daily IP-cluster secret, daily proof key | `edge_secrets`, memory bucket | until the next UTC midnight (per-key TTL), then gone; gateways zero their copies at midnight (`internal/gateway/visitor.go`) |
| Gateway | IP-cluster sets: keyed HMAC of the truncated network, visitor tokens | memory | a sliding hour |
| `WEB_EVENTS` | accepted event: site, band, visitor token, user agent (cut to 500 UTF-16 code units), country code, hostname, path with allowlisted query, referrer scheme and host, title, screen, language, event name and data | queued | until the replayer delivers it, at most 72 hours |
| `WEB_DLQ` | events Umami refused permanently, same fields | queued | at most 7 days; erasure deletes matching messages |
| `visit_state` | per site and visitor token: Umami session id, cache token, last event time; duplicate-guard keys | memory bucket | 24 hours after the last write |
| Umami (`umami` database) | `session` (browser, OS, device, screen, language, country), `website_event` (time, path, query, referrer domain, title, event name, hostname; the referrer path is always `/` and its query empty, section 7), `event_data` | stored | `WEB_RAW_RETENTION_MONTHS`, default 25 (section 4) |
| `agg.web_daily`, `agg.web_daily_dim` | per site, band and UTC day: page views, visitors, visits, bounces, total time; ranked referrers, countries, devices, browsers, OSes | stored | as long as the site exists; removal follows `removal_keep_aggregates` |
| `cp.web_evidence_daily` | per site and UTC day: accepted events per network class | stored | as long as the site exists; removal follows `removal_keep_aggregates` |
| `cp.web_networks_daily` | per site and UTC day: the network keys reported by the gateways, for the distinct network count | stored | until the day leaves the regrade window (four days), then deleted by the `integrity` task; deleted in every removal purge |
| `agg.web_integrity_daily` | per site and UTC day: the integrity grade, its reasons and the counts it was computed from (events per class, distinct networks, quarantined and proof-failed requests, visitors, page views, baseline) | stored | as long as the site exists; removal follows `removal_keep_aggregates` |
| OTLP collector | tenant metrics and spans; logs are accepted and discarded | memory | the request; spans become duration histograms and are dropped (`collector/`) |
| `API_METRICS` | filtered metric batches: tenant, service, normalized route, method, status class and code, histogram buckets | queued | until stored, at most 7 days |
| `api.*` | one-minute, hourly and daily rows of the same dimensions | stored | minutes `API_MINUTE_RETENTION_DAYS`, hours `API_HOURLY_RETENTION_MONTHS` (at least 24), days as long as the tenant exists |
| Control plane (`cp.*`) | GitHub numeric id and login, and the Stellar account address of a wallet sign-in, of console users; session token hashes; tenants, sites, OTLP token hashes, audit log, erasure requests | stored | while the account or tenant exists; the audit log is append-only |
| Control plane (`cp.wallet_challenges_used`) | SHA-256 of the nonce of each used wallet sign-in challenge, with its expiry; no address and no account | stored | until the challenge expires (at most five minutes); the next wallet verification after that deletes it |

No component stores an IP address or an autonomous system number. The privacy audit test (`tests/privacy/`)
sends real web and API traffic from marker addresses on a dedicated Docker
network and scans both databases, every JetStream stream and KV bucket, every
service log, the Caddy logs and every container volume for them.

## 3. IP address and visitor token design

1. The gateway reads the client address from the one header Caddy sets and
   keeps it in memory for the request.
2. Country: the /24 or /48 network is looked up in the local DB-IP Country
   Lite file. Only the ISO country code leaves the gateway. Region and city are
   never looked up.
3. Network class: the /24 or /48 network is looked up in the local DB-IP ASN
   Lite file, and the autonomous system's category in the local ipverse
   as-metadata table; the whole address is compared with the local list of
   Tor exit addresses. Only the class leaves the request, as a count per site
   (`evidence_net_<class>`). For the distinct network count the gateway sends
   each network once per site and UTC day as
   `HMAC-SHA256(daily_ipcluster_secret, "asn:" || number)`, cut to 64 bits.
   The secret is the one of the same-network check, memory only and gone at
   the next UTC midnight, so a stored key cannot be linked to a network
   afterwards, and the keys are deleted once their day is graded for the last
   time. The class and the keys describe a network shared by many people,
   never a person; they serve fraud detection and are reduced to counts and a
   grade per site and day (`docs/security/bot-and-inflation.md` sections 7 to
   9).
4. Visitor token: `vt = hex(HMAC-SHA256(daily_secret, site_id || 0x00 || ip ||
   0x00 || user_agent))`. The secret is 32 random bytes per
   UTC day, created atomically in the memory bucket `edge_secrets` with a TTL
   that ends at the next UTC midnight. No copy is written to disk.
5. The replayer sends Umami the constant address `192.0.2.1` (TEST-NET-1) and
   passes `vt` as Umami's client salt, so Umami's session id depends on the
   token and never on a real address.
6. After midnight nobody can recompute a token of the previous day, so stored
   sessions cannot be linked back to an address and user agent. The CJEU held
   in C-413/23 P that pseudonymised data is personal data for whoever can
   re-identify it (S8). That nobody can re-identify a token once its salt is
   destroyed is an inference, recorded as such in S8. During the day the token is computable by the
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
| `agg.web_integrity_daily`, `cp.web_evidence_daily` | while the site exists | removal purge (`agg.purge_tenant`, `lifecycle.WebIngestPurger`) |
| `cp.web_networks_daily` | four days | `jobs` task `integrity`; removal purge |
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
`_prisma_migrations`, nothing else (same test). It runs outside the jobs
daemon: the core role installs a systemd timer (`retain-web.timer`) that runs
it once a day.

Raw retention bounds what a tenant dashboard covers: with the default of 25
months, Umami shows 24 full months.

## 5. Umami features that are off, and how

Umami 3.5.0 has no global switch for these features, so they are disabled by
not shipping their client code and by blocking
their routes:

| Feature | How it is off |
|---|---|
| Session replay, heatmaps, web vitals, error tracking | the tracker (`packages/tracker`) contains no recorder, heatmap, vitals or error code; `/recorder.js` and `/script.js` are denied on `web.<BASE_DOMAIN>`; Umami's collect routes are reachable only on the private network |
| Identify and distinct ids | the tracker has no identify call; the wire format has no id field (`docs/reference/wire-v1.md`) |
| Links, pixels, shares, boards | `/api/links*`, `/api/pixels*`, `/api/share*`, `/api/boards*`, `/share/*`, `/q/*`, `/p/*` are denied for every method |
| Direct collection | `/api/send`, `/api/batch`, `/api/record` are denied publicly; only the replayer reaches them |
| Region and city | the gateway never looks them up; Umami receives only a country header with the constant address |
| Telemetry, update checks, favicon lookups | `PRIVATE_MODE=1`, `DISABLE_TELEMETRY=1`, `DISABLE_UPDATES=1` (outbound traffic tested in `tests/webpath/egress_test.go`) |

The deny list lives in `deploy/caddy/web.Caddyfile` and
`docs/reference/umami-contract.md`.

## 6. Objection and consent controls for tenants

- **Global Privacy Control.** The tracker sends nothing when
  `navigator.globalPrivacyControl` is true, and the gateway drops any event
  that arrives with `Sec-GPC: 1` (`internal/gateway/server.go`).
- **Opt-out flag.** The tracker sends nothing when the browser's
  `localStorage` holds `umami.disabled`. A tenant offers its visitors an
  opt-out link that sets this key. The tracker never writes browser storage.
- **What the tracker reads from browser storage, and when.** Exactly this,
  from `packages/tracker/src/index.ts` and `wallets-kit.ts`:
  - On every site: the value of `umami.disabled`. In default mode it is read
    when the page loads and again before every event. In consent mode
    (`data-auto-track="false"`) nothing is read until the site calls
    `umami.start()`, and then before every event.
  - Only on a site that turns on Wallets Kit capture with the script tag
    (`data-wallets-kit="true"`), and only once the tracker has started: every
    500 ms, and on each `storage` event raised by another tab of the site, the
    tracker lists the names of the page's `localStorage` keys to find the
    kit's two keys. It reads the value of `@StellarWalletsKit/selectedModuleId`
    (the wallet id, for example `xbull`) and checks only whether
    `@StellarWalletsKit/activeAddress` exists; it never reads that value or
    the value of any other key. Nothing it reads leaves the browser except the
    wallet events of `docs/onboarding/web/wallets-kit.md`: the wallet id, never
    an address. "Started" means page load in default mode, and only when
    neither Global Privacy Control nor the opt-out flag is set; in consent
    mode it means the site's `umami.start()` call. From tracker 1.2.0 that
    call starts the reading only when neither Global Privacy Control, the
    opt-out flag nor an honoured Do Not Track is set (1.1.0 started it
    regardless, although it sent no event); tested by `never reads the kit's
    storage when start() is called for a gpc visitor`
    (`packages/tracker/test/wallets-kit.test.ts`).
  - With the npm adapter (`attachWalletsKit`) the tracker reads no kit
    storage: it subscribes to the events of the kit the dapp set up itself.
  - Nothing else. The challenge and proof stay in page memory.

  Reading the kit's keys is access to information stored on the visitor's
  device (EDPB Guidelines 2/2023, `sources.md` S1), which is why Wallets Kit
  capture is off unless the tenant turns it on for its own site, and why a
  tenant that turns it on must include it in its own assessment.
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

- reduces the referrer to its scheme and host (`https://news.example/a?b#c`
  becomes `https://news.example`); a same-origin path becomes empty
  (`SanitizeReferrer`, tested in `internal/gateway/sanitize_test.go` and
  end to end in `tests/gateway`);
- replaces all ten SEP-23 strkey types (G, M, C, W, L, B, T, X, P, S),
  email addresses and federation addresses in the path, kept query values,
  referrer host, title and event data; S secret seeds become `{redacted-secret}`;
- drops every query parameter of the page URL. Only on a site whose owner
  turned on UTM collection (the site setting `collect_utm`, off by default;
  `docs/reference/control-plane-api.md` 3.1) does it keep `utm_source`,
  `utm_medium`, `utm_campaign`, `utm_content`, `utm_term` and `ref`
  (`CampaignParams`, tested in `internal/gateway/sanitize_test.go` and through
  the registry in `internal/gateway/utm_node_test.go`);
- drops URL fragments except a `#/` hash route, without its own query.

It cannot recognise every identifier a tenant puts in a path, title or event:
an order number, a user name or a database id in a URL passes through. The
terms forbid personal data in URLs, titles, referrers and event data, and
session erasure and `site_reset` remain available for the whole raw retention
period.

## 8. CNIL self-assessment criteria: what the code does

The primary text of CNIL's July 2025 self-assessment tool, quoted in S2,
says that the technical measures it names are recommendations: a provider may
use an alternative if it shows the alternative meets the criterion. The
conditions of the exemption are those of délibération 2020-091 art. 5 (S2).

| Criterion (tool, section 4) | CNIL's recommended measure | What the service does | Met? |
|---|---|---|---|
| No import of outside data | referrer, if collected, limited to the host | the gateway keeps only `scheme://host` of the referrer before queueing (section 7); the referrers metric is unchanged, since it always listed hosts | yes, by the recommended measure |
| No import of outside data | no collection of UTM or campaign identifiers in URLs | by default the gateway drops every query parameter, UTM and `ref` included (section 7). A site owner can turn on `collect_utm` for one site; the gateway then keeps `utm_*` and `ref` for that site only, and Umami reports them | yes by default, by the recommended measure; no for a site that turns `collect_utm` on |
| All reports and exports contain only anonymous statistics | aggregation and presentation rounded to the nearest ten, or a documented anonymity analysis (WP29) | exact counts in Umami's dashboards, read API and exports | no (section 9) |
| No filter combination isolates a user | none named | Umami's filters and Sessions page can show one visitor's pages of a day | no (section 9) |
| No tracking of a single user's navigation | session replay disabled | replay is off (section 5), but the Sessions page lists one session's page views | replay: yes; the criterion as a whole: no |

### Why reports are not rounded to ten

1. **Where tenants see numbers.** Every tenant web report is Umami's own UI,
   read API and CSV export, run from the official image unmodified
   ([ADR 0002](../adr/0002-overlay-not-fork.md)). The console shows tenants
   no web counts, so rounding there would change nothing a tenant sees.
   Rounding in Umami would mean patching the image or rewriting every JSON and
   CSV answer of dozens of report routes in the web proxy.
2. **Rounding alone would not meet the criterion.** The criterion is that
   every report is anonymous for any combination of filters, and the next
   row asks that no single user's navigation can be followed. Umami's
   Sessions and realtime pages show the pages of one visitor's day to a
   `team-view-only` member (`docs/reference/umami-contract.md`, item 10).
   A rounded overview next to those pages is still not anonymous output.
   Blocking those pages would also remove the session ids a tenant needs for
   session erasure (`erasure.md`).
3. **No overstated claim.** France therefore stays "not as built"
   (`jurisdictions.md`): tenants with French visitors use consent mode unless
   their counsel decides otherwise. No anonymity analysis is claimed.

## 9. Limits

- **Exact counts and single-visitor views.** Section 8: no rounding, and
  Umami's Sessions page shows one visitor's pages of a day.
- **UTM parameters.** Off by default: the gateway drops them unless the
  site's owner turns on `collect_utm` (section 7), which keeps the default at
  CNIL's recommended measure. A site that turns it on no longer meets that
  measure; the console says so next to the switch.
- **Erasure race.** See `erasure.md`, "Limits".
- **NATS file blocks.** An acknowledged `WEB_EVENTS` message is removed from
  the stream, but its bytes can stay in a JetStream file block until the block
  is compacted or expires (at most 72 hours). Erasure deletes `WEB_DLQ`
  messages with secure delete, which overwrites them.

## 10. AI clients (MCP)

Tenant members and operators can connect an AI client to the MCP server
(`docs/security/mcp.md`, contract section 12). This adds no new data and no
new store; it is another way for the same people to read what the console
already shows them.

- **Who receives data.** The AI client a member or operator connects, and the
  vendor that runs it, receives the tool answers that person asks for. The member chose
  the client and approved the connection; the vendor processes the answers
  under the member's or the tenant's own terms with that vendor, not as a
  sub-processor of the service. The service sends nothing to a client that
  was not asked for in a tool call.
- **What a project connection can receive.** Exactly what the project's
  members see in the console and in Umami: aggregates and rankings of page
  views, visitors, referrers, countries, devices, browsers and events; event
  data the tenant's own site sent; Umami's session list (browser, operating
  system, device, screen, language, country, region and city as Umami holds
  them, page and event counts, first and last activity, and the session id
  derived from the daily visitor token); and API request counts, status codes
  and latency buckets. No IP address or user agent exists in any store, so
  none can be returned.
- **What an operator connection can receive.** The operator views' daily
  aggregates and project metadata (section 1): never sessions or raw events.
- **Read-only.** No tool changes, exports in bulk or deletes data. Results are
  capped at 512 KiB per call and paged.
- **Revocation.** A connection ends when its member leaves the project, the
  project is suspended or removed, the operator loses operator rights, or the
  member revokes it on the project's AI page. Data a client already received
  stays with that client and its vendor; tenants decide whether to connect a
  client with that in mind, as for any export from their dashboard.
- **Records kept.** For each credential: its hash, name, user, project,
  creation, expiry, last use and revocation. For each tool call: an audit row
  with the tool, outcome, duration, user and project, never the arguments or
  the answer.

## 11. On-chain activity

A project can declare the Stellar accounts and Soroban contracts it operates
(contract section 15). Ledger data is public: every Horizon or Stellar RPC
client reads the same records. Veristar stores:

- the declared ids, their kind and when they were declared;
- the proof of control of each: the method, when it held, the verified account
  a contract was proven through, and the transaction hash or storage key a
  contract proof read. A pending challenge holds a random nonce for 30
  minutes. No signature is stored;
- per id, the account's home domain and the read state of the daily job;
- per id and UTC day, three counts: operations, transactions and distinct
  counterparties.

No counterparty address, transaction, amount or memo is stored, so no store
holds a list of who interacted with a project. An id can belong to a person
(a wallet); it is declared by the project's own members, it is public on the
ledger already, and it is shown to the project's members and to operators only.
Removing an id hides its rows; removing the project deletes its read state and,
unless `removal_keep_aggregates`, its daily counts.

Requests to the ledger APIs carry public ids only, never data about an end
user of a project, so the API providers receive no personal data and are not
sub-processors.

