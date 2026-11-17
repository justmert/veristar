---
title: "Threat model"
description: "What the platform protects, its trust boundaries, and the mitigation and test for each threat."
---

This page lists what the platform protects and where its trust boundaries are. For each threat it
gives the mitigation, the test or check that proves it, and the residual risk. Component names
follow [../architecture.md](../architecture.md) and the reference pages under `docs/reference/`.
Related documents:

- [bot-and-inflation.md](bot-and-inflation.md): the full bot and inflation strategy.
- [supply-chain.md](supply-chain.md): build, signing and dependency controls.
- [verify-tracker.md](verify-tracker.md): how a tenant verifies and self-hosts `sdk.js`.
- [SECURITY.md](SECURITY.md): how to report a vulnerability.
- [branch-protection.md](branch-protection.md): repository settings for `main`.
- [../runbooks/security-patch.md](../runbooks/security-patch.md): the 24 hour patch path.

Every test named below exists in the repository at the path given.

## 1. Assets

| Asset | Where it lives | Why it matters |
|---|---|---|
| Tenant web analytics (pageviews, visitors, referrers, events) | Umami database `umami`, one Umami team per tenant | Funding decisions use it; tenants must see only their own |
| Tenant API metrics | `platform` database, schema `api` | Same |
| Operator aggregates | `platform` schemas `agg`, `api` hourly and daily | Global view for SCF operators |
| Visitor IP addresses | Gateway memory only, for the length of one request | Personal data; must never be logged, queued or stored |
| Daily visitor secret, daily IP-cluster secret, daily proof key | NATS KV `edge_secrets`, memory storage, per-key TTL | Whoever holds the visitor secret can link visitor tokens to a person for that day |
| OTLP tokens (`sa_otlp_...`) | SHA-256 only, `cp.otlp_tokens` and `registry_otlp` | Write access to a tenant's API metrics |
| Operator API keys (`sa_op_...`) | SHA-256 only, `cp.operator_api_keys` | Read access to aggregates of every tenant (or one) |
| Console sessions (`__Host-sa_session`) | SHA-256 only, `cp.sessions` | A signed-in user's or operator's console access |
| Umami tokens of members | Issued by the control plane's login as the member's own Umami user, held in the memory of the control plane instance that logged in, for at most 5 minutes, never sent to a browser | View-only access to the tenant's Umami websites |
| Umami admin account, `UMAMI_USER_PASSWORD_KEY`, `OAUTH_STATE_KEY`, database and NATS credentials | SOPS-encrypted secrets (`infra/secrets/`), deployed by Ansible | Break-glass and service credentials |
| Tracker releases | `packages/tracker/releases/<version>/sdk.js`, `manifest.json`, `revoked.json` | Runs on every tenant page |
| Audit log | `cp.audit_log` | Record of every operator and tenant action |

## 2. Trust boundaries

1. **Browser to edge** (`in.<BASE_DOMAIN>`). Everything in the request is attacker-controlled:
   body, `Origin`, `Sec-Fetch-*`, `User-Agent`. Edge Caddy (`deploy/caddy/edge.Caddyfile`) sets
   `X-Edge-Client-Ip` from the TCP peer and removes `X-Forwarded-For`, `X-Real-Ip` and
   `Forwarded`. The gateway reads the address from that one header only and refuses the common
   proxy headers as sources (`internal/gateway/clientip.go`).
2. **Tenant server to edge** (`otlp.<BASE_DOMAIN>`). Authenticated by OTLP token; the content is
   still whatever the tenant sends.
3. **Browser to console** (`app.<BASE_DOMAIN>`). Session cookie, CSRF header and `Origin` check.
4. **Umami.** No public host serves it ([ADR 0005](../adr/0005-umami-as-engine-only.md)).
   The console's web dashboard reaches it only through the control plane, which reads on the
   private network as the member's own Umami user. Its interface is on each core node's
   loopback (`127.0.0.1:3300`) for platform administrators over SSH. `make test-caddy-closed`
   fails when an internet-facing Caddy site refers to Umami.
5. **Machine clients to the machine API** (`/api/metrics/v1/`). Operator API key.
6. **Private network.** Gateway, collector, replayer, metricswriter, jobs, control plane, Umami,
   PostgreSQL and NATS. Each service connects to NATS as its own user with publish and subscribe
   allow lists (`internal/natsx/topology.go`) and to PostgreSQL as its own role.
   **Transport:** every hop between nodes is encrypted and authenticated with certificates of an internal CA,
   one per environment. The CA key lives only in the SOPS file `infra/secrets/<env>/ca.enc.yaml`,
   which only `playbooks/internal-ca.yml` reads on the controller; nodes receive their own key
   and certificate from `tls.enc.yaml` ([../deploy/ansible.md](../deploy/ansible.md) section 6
   lists every link). NATS client connections are TLS 1.3 from the first byte; NATS routes,
   etcd peers and clients, the Patroni REST API, pgBackRest, the metrics relay, the exporters
   and Alertmanager (web and gossip) require a client certificate of the internal CA;
   PostgreSQL admits `hostssl` lines only and every client (services, Umami, replication,
   `pg_rewind`, `postgres_exporter`) uses `verify-full`. Services refuse a CA with a weaker
   `sslmode` and never fall back to plain text. NATS and PostgreSQL clients still log in with
   their per-service password on top of TLS; they present no client certificate.
   Proved by: `TestLoadNATSWithTLS`, `TestLoadNATSRefusesBrokenTLS`, `TestLoadPostgresTLS`
   (`internal/config/tls_test.go`); `TestConnectWithTLS` (`internal/natsx/tls_test.go`, real
   NATS 2.15.3: plain, wrong-CA and non-TLS-first clients refused); `TestTLSOnlyServer`
   (`internal/db/tls_test.go`, real PostgreSQL 18.6 with `hostssl` only: plain text,
   foreign CA and wrong name refused); `tests/localconverge/tls-audit.sh` on the local
   converge (capture of the private network, refusal of a host without a certificate, the
   end-to-end suite).
   Residual risk: NATS monitoring is plain HTTP on each edge node's loopback and Compose
   network (not on the private network); the operators' client certificate, if leaked, reads
   every monitoring endpoint until the CA is rotated (there is no revocation list); traffic
   inside one node's Compose network (Caddy to its upstreams, the replayer, control plane and
   jobs to Umami, the relay to the exporters behind it) is not encrypted, and observing it
   needs code execution on that node; and Alertmanager's gossip TLS is marked experimental upstream.
7. **Upstream code.** Umami image, Go modules, npm packages, base images, GitHub Actions.

## 3. Threats

### 3.1 Tracker compromise

An attacker changes the `sdk.js` that tenants load, to steal data from tenant pages or inject
code.

Built:

- Releases are immutable files under `packages/tracker/releases/<version>/sdk.js`, listed in
  `manifest.json` with `sha384` and an `integrity` value. There is no `latest` alias. The gateway
  serves only those committed files with `Cache-Control: public, max-age=31536000, immutable`, and
  answers 410 for a version in `revoked.json` (`docs/reference/gateway-limits.md`, responses).
- The snippet the console hands out carries `integrity` and `crossorigin`; the control plane
  refuses a manifest entry whose integrity value is malformed (`internal/controlplane/snippet.go`).
  A browser refuses a changed file whose hash does not match.
- Tenants may self-host the same bytes with the same integrity hash ([verify-tracker.md](verify-tracker.md)).
- `pnpm --filter tracker verify-releases` checks that released files never change, that each
  rebuilds byte-identical, and the size budget (`packages/tracker/scripts/verify-releases.mjs`,
  run by the `releases` job of `.github/workflows/tracker.yml`).

Proved by: `TestReleasesServeCommittedTracker` and `TestReleasesRevokedAndTampered`
(`internal/gateway/releases_test.go`); `TestTrackerVersionsAndRevocation`,
`TestLoadTrackerRefusesBadInput` and `TestSnippetMatchesTypeScript`
(`internal/controlplane/snippet_test.go`); the `verify-releases` script.

Residual risk: a tenant who copies the snippet without `integrity` gets no browser-side check.
Write access to `main` is the control over new releases, and every push to `main` runs the
release and supply-chain checks ([branch-protection.md](branch-protection.md)). A compromised edge
node can serve different bytes for a new version, but a browser with the integrity attribute
still refuses them.

### 3.2 Cross-tenant data leak

A member of tenant A reads or changes tenant B's data.

Built:

- Console API: every route under `/api/tenants/{tenant_id}` and `/api/sites/{site_id}` checks
  membership first and answers 404 to non-members, then runs as `cp_tenant` inside
  `db.WithTenant`, where row-level security is forced on every tenant table and no application
  role has `BYPASSRLS` ([../reference/control-plane-api.md](../reference/control-plane-api.md) section 1.3).
- Web dashboard (contract section 18): the membership check, the site resolved among the
  tenant's own sites under `cp_tenant`, and the aggregates read under row-level security.
  The Umami reads go out as the member's own Umami user, never as the admin, and only for one
  of the checked site's two websites (`internal/controlplane/umamiread`), so Umami's own team
  check is a second gate behind the control plane's.
- Umami: each tenant is one Umami team, each user is `view-only` with team role
  `team-view-only`. No public host serves Umami, so no project holds an Umami token or reaches
  its routes.
- The tenant id of API metrics comes from the OTLP credential. `tenantguard` replaces resource
  attributes with an allowlisted set, so a forged `scf.tenant.id` is dropped
  (`collector/tenantguardprocessor/guard.go`, function `stamp`).
- Operators see aggregates only and get no Umami access.

Proved by:

- `TestUmamiTenantIsolation` (`tests/isolation/umami/isolation_test.go`, build tag `isolation`):
  user B calls every operation of Umami's OpenAPI contract with team A's ids on the private
  network.
- `TestWebDashboardParity` subtest `isolation` (`internal/controlplane/webdash/live_test.go`)
  and the gate's web dashboard cases (`tests/isolation/console/gate_test.go`).
- `TestConsole` subtests `Isolation` and `WrongTenantSeesNothing`
  (`tests/isolation/console/console_test.go`) and `TestRemovedTenantIsGoneForFormerMembers`
  (`tests/isolation/console/removal_test.go`).
- `TestTenantIsolation`, `TestNoTenantSetSeesNothing`, `TestTenantSeesOnlyItsMembers`
  (`internal/db/isolation_test.go`); `TestPolicyCoverage` and `TestOpenRefusesPrivilegedLogin`
  (`internal/db/migrate_test.go`); `TestGrantMatrix` and `TestRoleAttributes`
  (`internal/db/privileges_test.go`); `TestOperatorBoundaries`, `TestMetricsWriterBoundaries`,
  `TestJobsBoundaries` (`internal/db/boundaries_test.go`).
- `TestViewOnlyTeamMemberReadsButCannotChange`, `TestViewOnlyCannotReadOtherTeam` and
  `TestOpenAPIRouteListUnchanged` (`tests/umami-compat/access_test.go`).
- `TestCaddyfilesNameNoUmami` and `TestAssembledConfigsReachNoUmami`
  (`deploy/caddy/umami_closed_test.go`): no internet-facing Caddy site reaches Umami.
- Collector: `TestCollector` subtest `http cumulative with forged tenant and secret seed`
  (`tests/collector/collector_test.go`) and `TestMetricsFilteredAndStamped`
  (`collector/tenantguardprocessor/guard_test.go`).
- Browser level: `tests/e2e-console/specs/console-isolation.spec.ts`.

The isolation gate (`make isolation`, the `isolation` job of `.github/workflows/ci.yml`) runs
these suites together on every push to `main` and every pull request
([branch-protection.md](branch-protection.md)).

Residual risk: a new Umami version can change what a `view-only` team member may read;
`TestViewOnlyCannotReadOtherTeam` and `TestOpenAPIRouteListUnchanged` fail on such a change,
which forces a review before an upgrade.

### 3.3 Tenant metric inflation

A tenant inflates its own numbers to look more used than it is.

Web: covered in full in [bot-and-inflation.md](bot-and-inflation.md). In short, the challenge
and proof, `Origin` and fetch-metadata checks, the per-node replay limit, rate limits, same-IP
quarantine and Umami's `isbot` raise the cost and catch naive server posts. They do not stop a
scripted client or real browsers on residential proxies.

API metrics: the tenant controls its own servers and therefore the OTLP data it sends. Nothing
can tell real requests from invented ones. Built limits: per-tenant request and item rate limits
in `tenantguard` (`COLLECTOR_RATE_REQUESTS_PER_SECOND` 50, burst 100;
`COLLECTOR_RATE_ITEMS_PER_SECOND` 50,000, burst 200,000, per tenant and node, from
`infra/ansible/roles/edge/defaults/main.yml`), daily route and service cardinality caps in the
metricswriter (routes beyond the cap become `other` with a `cardinality_overflow` warning,
`internal/metricswriter/store.go`), and dashboards that label span-derived data.

Proved by: `TestRateLimit` and `TestLimiterSharedAcrossSignals`
(`collector/tenantguardprocessor/guard_test.go`); `TestCollector` subtest
`rate limit 429 then sdk retry succeeds`; the metricswriter subtest
`routes beyond the daily cap become other with a warning` (`internal/metricswriter/it_test.go`).

Residual risk: API metrics are self-reported. The platform can bound volume and cardinality but
cannot verify that requests happened. Operators read API numbers as the tenant's own claim.

### 3.4 Sabotage by a rival

A rival hurts another tenant's numbers or standing.

Vectors and what limits them:

- **Inflating the victim's site** so it looks manipulated. Site ids are public (they are in the
  snippet), so anyone can fetch a challenge from an allowed origin header and post. The same
  limits as 3.3 apply. Quarantined traffic lands in a separate Umami website, never in the
  primary numbers, and per-site outcome counters (`accepted`, `quarantined`, `bot`,
  `rejected_<reason>`) are visible to the tenant and operators
  ([../reference/control-plane-api.md](../reference/control-plane-api.md) section 4.3.7).
- **Starving the victim's rate limit.** `RATE_SITE_PER_SECOND` (200, burst 400) is one bucket per
  site per node, shared by all visitors of the site. A refusal by the per-prefix bucket
  (`RATE_PREFIX_PER_SECOND` 20, burst 60) does not take a site token
  (`TestRateLimitPrefixRefusalKeepsSiteTokens`, `internal/gateway/ratelimit_test.go`), so one
  address prefix cannot drain the site bucket. An attacker with more than about ten prefixes per
  node can, and then real visitors of that site get 429 on that node.
- **Pushing real visitors into quarantine.** An attacker sharing an address prefix with real
  visitors (same /24 or /48) can send 20 distinct visitor tokens and quarantine that prefix for
  the site for an hour. Real visitors elsewhere are unaffected.
- **Lowering the victim's integrity grade with forged page signals.** The never-visible and
  framed shares of the grade ([bot-and-inflation.md](bot-and-inflation.md) section 9) are counted
  from what the tracker reports. Outside a browser, `Origin` is a plain header, the site id is
  public and one solved proof is reused up to the replay limit, so a script can send never-visible
  notices or page views with `fr: true` for any site. Mitigation: a page view counts toward
  `signal_views` and `signal_framed` only when it is queued in the primary band, and a notice
  counts toward `signal_never_visible` only when the per-visitor volume bound and the same-network
  threshold would have kept it there; a notice that fails them is counted in Prometheus only. One
  visitor adds at most `VISITOR_EVENTS_PER_HOUR` signals per hour and node, and one prefix at
  most its same-network threshold of visitors, each within that bound. A rival spread over many networks, each under
  the limits, can still lower the grade to B or C; the reasons name the share that did it, and an
  operator reads them before relying on a grade.
- **Moving the victim's unmeasured-share estimate.** The tracker file route has no proof or
  origin check, and `Referer`, `Sec-Fetch-Dest` and `Sec-GPC` are plain headers outside a
  browser, so a script can claim script loads of any site with or without Global Privacy
  Control. Mitigation: each gateway counts at most 60 such requests per site and truncated
  prefix at once and one per 10 seconds after that; the file is still served
  ([../compliance/privacy-strategy.md](../compliance/privacy-strategy.md) section 6, "Forged
  requests"). A rival with many networks can still move the share of a small site. The share
  is an estimate shown beside the numbers and is not an input of the integrity grade.
- **Claiming the victim's domain.** Sites carry a domain ownership check and a stellar.toml signal
  ([../reference/control-plane-api.md](../reference/control-plane-api.md) sections 5.8 and 5.12). An unverified claim is
  visible to operators. Even an accepted claim gives the rival only its own site id; events of
  the victim's snippet carry the victim's site id.
- **Getting the victim removed.** Removal is an operator decision with a required reason and an
  audit row (section 5.5 of the contract).

Proved by: `TestRateLimitSite`, `TestRateLimitPrefixBurstAndRefill`,
`TestRateLimitPrefixRefusalKeepsSiteTokens`, `TestRateLimitBoundedKeys`
(`internal/gateway/ratelimit_test.go`); `TestForgedPageSignalsDoNotMoveTheGrade`
(`internal/gateway/signals_node_test.go`); `TestForgedScriptRequestsAreCountedWithinAPrefixLimit`
(`internal/gateway/signals_node_test.go`); `TestEdgeSecretsCluster` subtest
`quarantine starts at exactly the threshold across three gateways`
(`internal/gateway/visitor_test.go`); `TestWebPath` subtest `quarantine`
(`tests/webpath/webpath_test.go`); `TestVerifyRoutes`, `TestCheckDNS`, `TestCheckFile`
(`internal/controlplane/domaincheck/`); `TestRemovedDomainReview`
(`tests/enrollment/review_test.go`).

Residual risk: a resourced rival can distort a victim's numbers or rate-limit its visitors for
as long as the attack lasts. Detection is by the outcome counters; there is no automatic
attribution of an attack.

### 3.5 Token theft

| Credential | What a thief gets | Built mitigations | Proved by | Residual |
|---|---|---|---|---|
| OTLP token `sa_otlp_` + 40 base62 | Write API metrics as that tenant. No read access: no route accepts an OTLP token for reading. | Stored only as SHA-256, prefix kept for display; shown once; format checked before hashing; revocation writes `registry_otlp` before commit, and a failed write rolls back (contract 6.4); suspended and removed tenants refused; `last_seen_at` shows use. | `TestOTLPTokens`, `TestGenerate`, `TestRegistryStatus` (`internal/controlplane/otlptokens/`); `TestBearerParsing`, `TestAuthenticateAgainstRegistry`, `TestFailsClosedWithoutSnapshot` (`collector/tenantauthextension/auth_test.go`); `TestCollector` subtest `unknown and revoked tokens`; `tests/e2e-console/specs/otlp-tokens.spec.ts` | Tokens do not expire. A thief can pollute the tenant's API data until the tenant revokes. |
| Operator API key `sa_op_` + 40 base62 | Read aggregates of every tenant (global key) or one tenant (scoped key). No write, no raw data. | SHA-256 only; shown once; lifetime 1 to 365 days, default 90; revocation and expiry apply to the very next request; 10 requests per second, burst 20, per key and instance; usage counted per hour; key works only under `/api/metrics/v1/` and a session cookie does not work there (contract 9.1 to 9.3). | `TestMachineAPI` subtests `a revoked key is refused on the very next request`, `an expired key is refused`, `a key works nowhere else and a cookie not here`, `rate limit answers 429 with Retry-After`, `no plaintext key is stored anywhere`, `a scoped key sees only its tenant`; `TestKeyFormat`, `TestKeyLimiter` (`internal/controlplane/operator/machine_test.go`) | A stolen global key reads every tenant's aggregates until revoked or expired. |
| Console session `__Host-sa_session` | The user's console access; for an operator, operator actions. | `HttpOnly`, `Secure`, `SameSite=Lax`, host-only with the `__Host-` prefix, so no other host of the domain can set or overwrite it (the sign-in state and challenge cookies carry the prefix too); absolute lifetime 7 days counted from the sign-in, kept across token renewals; idle 24 hours; DB holds SHA-256 only; token renewed at sign-in and at a step-up; sign-out, a roster suspension, removal or passkey reset and account deletion delete the rows, and a request still in flight never writes a deleted row back (its commit only updates the token it arrived with, and a renewed token replaces the old row only while that row exists); every state change needs `X-Requested-With: sa-console` and `Origin` equal to `CONSOLE_ORIGIN`; console CSP `default-src 'none'; script-src 'self'` with `frame-ancestors 'none'` (`deploy/caddy/app.Caddyfile`). | `TestSessionCookieFlags`, `TestSignInRenewsToken`, `TestLogoutInvalidates`, `TestCSRFOnLogout`, `TestStateFailures`, `TestPKCEFailure` (`internal/controlplane/auth/`); `TestCSRF` (`internal/controlplane/httpx/`); `TestStoreRefusesRawTokens`, `TestRevokedSessionsStayRevoked`, `TestLifetimeIsAbsolute` (`internal/controlplane/session/`); `tests/e2e-console/specs/console-session.spec.ts`; `TestAppCaddyServesConsoleAndApi` (`deploy/caddy/app_test.go`) | Malware on the user's machine can still take the cookie. There is no device binding. |
| Umami token of a member | View-only Umami access to that member's teams. | Never leaves the control plane: obtained on the private network with the member's daily derived password, kept in the memory of each control plane instance for at most 5 minutes per member and site, used only for the website read routes `internal/umami.ReadWebsite` allows; Umami passwords rotate daily, so every token dies within about 24 hours; a removed member loses the console route at once (membership read on every request) and gets 401 from Umami as soon as the team membership is gone. | `TestReader` (`internal/controlplane/umamiread`); `TestProvisioningLive` (`internal/controlplane/provision/live_test.go`) | A control plane memory dump during the token's life exposes it. |

### 3.6 Umami and dependency vulnerabilities

Built:

- Umami runs unmodified as `ghcr.io/umami-software/umami:3.5.0` pinned by digest
  (`deploy/compose/dev.yml`, `upstream_images` in Ansible). Base images are pinned by digest
  (`deploy/images/*.Dockerfile`). GitHub Actions are pinned by commit SHA
  (`.github/workflows/*.yml`).
- Umami is never exposed to the internet: no Caddy site proxies to it, so its Next.js
  application, its middleware (CVE-2025-29927 needs a request from outside) and its share and
  report routes have no public path. Only the replayer, the control plane and jobs reach it on
  the private network.
- Umami has no outbound access in normal operation, which the `TestWebPath` subtest
  `egress from boot to here` checks.
- Go services run as `nonroot` on distroless images; Caddy as user 10001
  (`deploy/images/`).

- Detection: the hourly `security.yml` workflow runs Trivy against the deployed digests,
  `govulncheck`, `pnpm audit` and an upstream advisory check; Dependabot opens update pull
  requests; gitleaks and the licence gate run on every pull request
  ([supply-chain.md](supply-chain.md) sections 5 to 7).
- `make umami-upgrade VERSION=` runs `tests/umami-compat/`, the isolation suite and the
  end-to-end suite before rollout. The patch path is
  [../runbooks/security-patch.md](../runbooks/security-patch.md).
- Read-only container filesystems and dropped capabilities: the edge, core and observability
  compose templates under `infra/ansible/roles/*/templates/` set `read_only: true`,
  `cap_drop: [ALL]` and `no-new-privileges` on every service, with a small `/tmp` tmpfs. The
  backup repository template of the local converge (`roles/backup`) and the development stack
  `deploy/compose/dev.yml` do not set them; neither runs in staging or production.

Residual risk: Umami has published advisories without a stated fixed version, so the advisory
check never relies on "fixed in" wording and the patch path does not depend on it either.

### 3.7 Ingest denial of service

Built:

- Event bodies are capped at 8 KiB (`GATEWAY_MAX_BODY` 8192; 413 above). OTLP HTTP bodies at
  4 MB on the wire in edge Caddy, and the collector checks size again after decompression.
- Per-site and per-prefix token buckets on events; per-tenant request and item buckets in the
  collector; `memory_limiter` in the collector.
- Every in-memory table is bounded: 200,000 entries each for the replay table, the IP-cluster
  sets and each rate limit table (`internal/gateway/node.go`, `ratelimit.go`).
- Ingestion does not depend on PostgreSQL or Umami: events are queued in JetStream (R3, 72 hours
  for `WEB_EVENTS`), so a database outage does not drop events.
- Three nodes, one in each availability zone of the region, behind health-checked DNS
  ([../reference/availability.md](../reference/availability.md)).

Proved by: `TestRateLimitBoundedKeys`, `TestRateLimitConcurrent`
(`internal/gateway/ratelimit_test.go`); `TestEventChecks` subtest `oversized body is 413`
(`tests/gateway/checks_test.go`); `TestCollector` subtests `oversize and decompression bombs`
and `nats stopped gives 503 and recovers without a gap`; `TestQueueOutage`
(`tests/gateway/cluster_test.go`); `TestWebPath` subtests `outage umami stopped` and
`outage postgres stopped`; `TestThroughputK6` (`tests/gateway/throughput_test.go`,
`make gateway-load`). The measured throughput per node and per cluster is in
[../reference/gateway-limits.md](../reference/gateway-limits.md).

Residual risk: there is no rate limit on `GET /v1/c` (challenge) in the gateway or in edge Caddy;
the limiter runs only on `POST /v1/e`. A challenge costs one HMAC and is cheap, but the route is
unbounded. Neither edge Caddy nor core Caddy has a rate limiter; volumetric attacks are left to
the network provider. The sign-in start and callback routes limit each client on its own (a keyed
hash of the address core Caddy passes, held only in memory) under a per-instance ceiling, and each
OAuth state reaches the code exchange once (`internal/controlplane/auth/ratelimit.go`, control
plane API 4.1.1). A client is an IPv4 address or an IPv6 /48, so one site's whole allocation is
one client. One client cannot lock others out; a flood from about 100 or more addresses or /48s
can still hold the ceiling (50 starts per second per instance), but a person who had been idle
then takes from a separate reserve that busy keys cannot reach. Only a flood with a
fresh address or /48 for every request, at more than the reserve's 5 per second, delays
sign-in for everyone.

### 3.8 Operator credential compromise

An attacker takes over an operator's GitHub account or console session.

What an operator can do: approve, reject, suspend, resume and remove tenants; change enrollment
mode and the removal setting; create and revoke operator API keys; read aggregates. An operator
admin also changes the operator roster. An operator cannot read raw events or Umami data and has
no Umami admin.

Built:

- Operators are rows of the roster `cp.operators`. `cp.users.is_operator` is derived from the
  roster by database triggers in the transaction that changes either side, and read per request;
  anything else is 403 `operator_required`. A suspension or removal takes effect at the person's
  next request, ends their console sessions and revokes their operator MCP credentials.
- The operator area needs a passkey assertion recorded on the session within
  `OPERATOR_STEP_UP_MAX_AGE` (12 hours by default); otherwise 401 `step_up_required`. A stolen
  session cookie or a taken-over GitHub account or wallet alone does not open the area. The step-up uses
  the passkey verification of passkey sign-in (`internal/controlplane/passkey`): the assertion must
  name the console's relying party and origin, so a look-alike site cannot relay it, must sign a
  challenge the console issued for this session and purpose, and must carry user verification. A
  step-up renews the session token, and a challenge is accepted once (it is removed from the
  session and its hash stored in `cp.wallet_challenges_used`).
- Step-up keys are the passkeys registered in the operator area and the account's sign-in
  passkey only when an active roster row names that passkey's address. A passkey linked to an
  account the roster names by GitHub or wallet signs in but does not step up, so a passkey linked
  from a stolen session, before or after the account became an operator, never opens the area.
- On an operator account, every link and unlink of a sign-in method needs a fresh step-up
  (401 `step_up_required`), the GitHub link flow included; unlinking a sign-in passkey that is
  the account's last step-up key answers 409 `last_passkey`, as deleting the last registered
  passkey does.
- A first passkey can be registered without a step-up only inside an enrollment window
  (`OPERATOR_ENROLL_WINDOW`, 24 hours by default) that an admin's addition or reset, or the start of
  the control plane for a deploy-settings identity without a passkey, opens; only from a session
  signed in after the window opened; and only while the account has no passkey. The first
  registration closes the window. Later ones need a step-up.
- Roster changes need the operator admin role and a step-up, carry a reason, and are audited. The
  constraint trigger `operators_last_admin` keeps one active operator admin whatever statement
  runs. Deploy-settings rows cannot be suspended, removed or demoted in the console.
- Machine API keys and operator MCP credentials do not need the step-up: they authenticate with
  their own key, scoped and revocable, which only a stepped-up operator session can create or
  approve.
- Every action writes an audit row; `cp_system` cannot update or delete `cp.audit_log`.
- Actions need a reason (remove, reject, suspend) and are refused as 409 on a repeat.
- Sign-in is GitHub OAuth with PKCE S256 and state and no scopes requested, a SEP-10 wallet
  challenge, or a passkey assertion (soropass, contract 4.1.9): relying party id the console host,
  origin exactly `CONSOLE_ORIGIN`, user verification required, and a one-use challenge.

Proved by: `TestStepUp` (`internal/controlplane/stepup/stepup_test.go`); `TestRosterRoutes`,
`TestRosterDerivesOperatorFlag` and `TestBootstrap` (`internal/controlplane/operator/roster_test.go`);
`TestGlobalRoutesNeedAnOperator` (`internal/controlplane/operator/global_umami_test.go`);
`TestAuditLogAppendOnly` (`internal/db/boundaries_test.go`); `TestOperatorFlag`,
`TestOperatorSessionAloneCannotGainStepUpKey`, `TestLinkedPasskeyIsNoStepUpKey`,
`TestOperatorChangesIdentitiesAfterStepUp` and `TestUnlinkLastStepUpPasskey`
(`internal/controlplane/auth/`); the isolation gate's operator routes without a step-up
(`tests/isolation/console/gate_test.go`); `tests/e2e-console/specs/operator-roster.spec.ts` with a
Chromium virtual authenticator.

Residual risk: a synced passkey is as safe as the platform account that syncs it; an operator
who wants a key that never leaves one device registers a security key with a PIN. A new
passkey's key is trusted because it signs the console's challenge; no attestation ties it to an
authenticator model. Inside an open enrollment
window, a person who takes over the operator's GitHub account and signs in first registers the
first passkey; the window is short, the registration is audited, and the Operators page shows the
passkey count of every row. One operator alone can remove a tenant, and removal purges the
tenant's Umami websites and data; there is no second approval and no undo inside the application.
Recovery is a point-in-time restore from the pgBackRest backups
([../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md)).
A compromised operator session that holds a step-up can also mint a global API key and read
every tenant's aggregates until it is revoked.

### 3.9 Edge node compromise

An attacker gets code execution on an edge server.

Exposure: the node sees raw client addresses in memory, holds the day's secrets from
`edge_secrets`, and the gateway and collector NATS users on the node can publish to
`WEB_EVENTS` and `API_METRICS`, bypassing every check. The node holds no database credentials: the edge tier has no dependency on PostgreSQL.

Built: separate NATS users with allow lists per service (`internal/natsx/topology.go`), tested by
`TestGatewayEdgePermissions` (`internal/gateway/visitor_test.go`); secrets never touch disk
(memory KV); SSH accepts only the operator's `admin_cidrs`, in the network security group and
the host firewall.

Residual risk: a compromised edge node can inject arbitrary events and API metrics for any
tenant and can link visitor tokens to addresses for the current day.

### 3.10 Server-side request forgery through domain checks

The control plane and `jobs` fetch files from tenant-supplied domains (domain verification and
`stellar.toml`). Built: `internal/netguard` resolves the host, refuses every non-public address
(including IANA special-purpose ranges and NAT64), connects only to the checked address and
re-checks before `connect`, caps size, time and redirects, and ignores proxy environment
variables. Proved by `TestRefused`, `TestRefusedAddresses`, `TestRefusedNames`,
`TestDNSRebinding`, `TestRedirects`, `TestOversize`, `TestSlow`, `TestIgnoresProxyEnvironment`
(`internal/netguard/guard_test.go`). Residual risk: none known beyond bugs in that package.

### 3.11 Personal data leaking into storage or logs

Built: the client IP never leaves gateway memory; Caddy log filters delete addresses, user agent
and forwarding headers (edge) and all request headers, query strings and response headers
(app); the replayer sends Umami the constant `192.0.2.1`; `internal/redact` replaces all ten
SEP-23 strkey types, email and federation addresses, with S secret seeds becoming
`{redacted-secret}`; the referrer is cut to scheme and host. Proved by `TestNoAddressAnywhere`
(`tests/gateway/privacy_test.go`), `TestNoMarkerAddressAnywhere` (`tests/privacy/privacy_test.go`),
`TestEdgeAccessLogHasNoClientIdentity` (`deploy/caddy/edge_test.go`),
`TestAppCaddyServesConsoleAndApi` (`deploy/caddy/app_test.go`, access log part),
`TestClientIPNeverPrints` (`internal/gateway/clientip_test.go`), `TestWebPath`
subtest `privacy dump`, `TestCollector` subtest `secret seed absent from collector logs`, and
`internal/redact/redact_test.go`. Details: `docs/compliance/`.
