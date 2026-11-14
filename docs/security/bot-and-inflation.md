---
title: "Bot and inflation strategy"
description: "How the platform detects and limits bot traffic and artificial metric inflation."
---

This page describes how the platform detects and limits bot traffic and artificial metric
inflation. It builds on Umami's own bot detection, tells real browser JavaScript execution apart
from plain server-side posts, and treats many distinct visitor fingerprints from one address with
skepticism. Parameter names and values come from the code (`internal/config/gateway.go`,
`internal/gateway/`) and the deployed defaults (`infra/ansible/roles/edge/defaults/main.yml`,
`deploy/compose/dev.yml`), and are listed in
[../reference/gateway-limits.md](../reference/gateway-limits.md).

In short: the layers below raise the cost of forged traffic, refuse naive server-side posts and
keep suspicious bursts out of the primary numbers. Section 9 states the boundaries of each layer,
so you know what the numbers can and cannot tell you.

## 1. Where the checks run

```
browser ── sdk.js ── GET /v1/c (challenge) ── POST /v1/e (event) ──> edge Caddy ──> gateway
                                                                                   │
                                     JetStream WEB_EVENTS <── checks pass ─────────┘
                                             │
                                          replayer ──> Umami POST /api/send (isbot runs here)
```

The gateway (`internal/gateway/server.go`, `handleEvent`) checks every `POST /v1/e` in this
order, and stops at the first refusal:

| # | Check | Refusal |
|---|---|---|
| 1 | Body size (`GATEWAY_MAX_BODY`, 8192 bytes) and wire schema | 413, 400 |
| 2 | Site exists in the registry and is `active` | 404 |
| 3 | `Sec-GPC: 1` (Global Privacy Control) | 204, nothing queued |
| 4 | `Origin` is one of the site's registered origins | 403 |
| 5 | Challenge age, tag and proof of work | 403 |
| 6 | Replay limit for that proof on this node | 403 |
| 7 | Fetch metadata (`Sec-Fetch-Site`, `Sec-Fetch-Mode`, `Sec-Fetch-Dest`) | 403 |
| 8 | Rate limits per site and per address prefix | 429 |
| 9 | `User-Agent` present exactly once | 400 |
| 10 | Same-IP skepticism: event goes to the quarantine band or the primary band | (never refused) |

Checks 5 and 6 are one call (`ProofVerifier.Verify`, `internal/gateway/proof.go`). The replay
counter moves only for proofs that passed every other part of the check, so forged proofs cannot
use up a real visitor's allowance. `GET /v1/c` checks the site, `Origin` and fetch metadata
before it issues a challenge.

After the gateway, the replayer delivers the event to Umami with the visitor's original
`User-Agent`, and Umami's own `isbot` check runs (section 2).

## 2. Umami's `isbot` on the original user agent

Umami 3.5.0 runs `isbot` on the `User-Agent` of `POST /api/send`; for a bot it answers
`200 {"beep":"boop"}` and stores nothing. The replayer forwards the visitor's original
`User-Agent` header unchanged, and
Umami is deployed without `DISABLE_BOT_CHECK` (`infra/ansible/roles/core/templates/compose.yml.j2`,
`deploy/compose/dev.yml`). A bot answer is counted as `bot` on `EDGE_STATS` and in
`replayer_outcomes_total{outcome="bot"}`, so tenants and operators see how many events were
dropped this way.

Proved by: `TestBotUserAgentStoresNothing` (`tests/umami-compat/send_test.go`: Googlebot and
bingbot store no session and no pageview) and `TestWebPath` subtest `bots`
(`tests/webpath/webpath_test.go`: a crawler user agent through the whole chain, events answered
202 by the gateway, counted twice as `bot`, nothing in Umami).

Limit: `isbot` matches declared bots. A headless browser or script that sends an ordinary browser
user agent is not caught by it.

## 3. Server-issued challenge and proof

Full detail and measurements: [../reference/browser-proof.md](../reference/browser-proof.md) and
[../reference/wire-v1.md](../reference/wire-v1.md).

- Once per page load the tracker calls `GET /v1/c?w=<site id>`. The gateway answers
  `{w, iat, salt, bits, tag}`: `iat` is server time, `salt` 16 random bytes, `bits` the current
  `PROOF_BITS`, and `tag = HMAC-SHA256(proof_key, w|iat|salt|bits)` with a daily key held in the
  memory KV bucket `edge_secrets`.
- The tracker finds a `nonce` such that `SHA-256(w|iat|salt|nonce)` has at least `bits` leading
  zero bits (WebCrypto, batches of 64) and attaches the proof to every event of that page load.
  The challenge stays in page memory: no cookie, no browser storage.
- The gateway refuses a proof older than `ChallengeTTL` (6 hours of server time), more than
  `MaxClockSkew` (30 seconds) in the future, with `bits` above `MaxProofBits` (24), with a wrong
  tag (which also covers a proof issued for another site, since the tag covers `w`), or with
  too little work. The previous day's key is still accepted inside the 6 hour window.
- `PROOF_BITS` is **12**. It is the highest tested value whose median solve time stays under
  150 ms at a 6x CPU slowdown in Chromium, Firefox and WebKit. Because difficulty and time come
  from the server, difficulty can be raised without a tracker release.

What this distinguishes: a request without a valid proof did not go through the challenge flow,
so a plain HTTP client that posts a JSON body is refused. This separates browser JavaScript
execution from plain server-side posts. Section 9 covers clients that run the challenge flow
themselves.

Proved by: `TestChallengeRoundTrip`, `TestKeyRotationOverlap`, `TestKeyLost`, `TestKeyTTL`
(`internal/gateway/challenge_test.go`); `TestTrackerVectorBytes`, `TestTrackerVectorsVerify`,
`TestLeadingZeroBits` (`internal/gateway/proof_test.go`, shared vectors with the TypeScript
tracker); `TestEventChecks` subtests `naive post without proof is 403` and `forged tag is 403`
(`tests/gateway/checks_test.go`); `TestClocks` (`tests/gateway/clock_test.go`).

## 4. Origin and fetch-metadata checks

- **Origin.** The `Origin` header must equal one of the site's registered origins (1 to 20 bare
  origins under the site's domain, `https` only except `localhost` and `127.0.0.1` for testing).
  Proved by `TestCheckOrigin`, `TestValidOriginPattern` (`internal/gateway/origin_test.go`) and
  `TestEventChecks` subtest `wrong origin is 403`.
- **Fetch metadata.** The tracker's `fetch` (`keepalive`, `credentials: 'omit'`, `text/plain`, no
  custom headers) produces `Sec-Fetch-Site: cross-site` (or `same-site` when the tenant shares the
  base domain), `Sec-Fetch-Mode: cors` and `Sec-Fetch-Dest: empty` in Chromium 156, Firefox 157
  and WebKit 27.2 (`tests/e2e/specs/headers.spec.ts`, recorded in `browser-proof.md`). The
  `SignalsChecker` (`internal/gateway/signals.go`) refuses any of these headers that is present
  with another value or repeated. It refuses missing headers only when the user agent claims an
  engine known to send all three (Chromium 80 or later, Firefox 90 or later, Safari 16.4 or
  later) and `GATEWAY_SECURE_TRANSPORT` is `true`. Android WebViews and non-Safari iOS browsers
  (`; wv)`, `CriOS/`, `FxiOS/`, `EdgiOS/`, `OPiOS/`) and EdgeHTML were not measured, so they are
  never refused for missing headers. Proved by `TestSignals` and `TestSendsFetchMetadata`
  (`internal/gateway/signals_test.go`).

These checks stop requests that come from the wrong page and naive clients that send nothing.
They are headers, and any non-browser client can set them (section 9).

## 5. Replay limit

A captured proof (one page load) may carry at most `PROOF_REPLAY_LIMIT` events on one node.
The default is **50**. The count is kept per proof tag in a bounded least recently used table of
200,000 entries per node (`replayEntries`, `internal/gateway/node.go`).

- The limit is per node and in memory. With three edge nodes, one proof can be used up to
  3 x `PROOF_REPLAY_LIMIT` (150) times in total.
- A proof pushed out of the table by 200,000 newer ones starts counting again
  (`internal/gateway/proof.go`, `NewProofVerifier`).
- A gateway restart forgets the table.

Proved by: `TestReplayLimit` (`internal/gateway/proof_test.go`) and `TestEventChecks` subtest
`replay past the limit is 403`.

## 6. Same-IP skepticism and quarantine bands

- **Fingerprint.** The visitor token is
  `vt = hex(HMAC-SHA256(daily_secret, site_id || 0x00 || ip || 0x00 || user_agent))`. Distinct
  user agents from one address, or distinct addresses in one prefix, give distinct tokens.
- **Key.** Per site and per keyed hash of the truncated address (IPv4 /24, IPv6 /48) under a
  second daily secret. The network itself is never sent or stored.
- **Window and threshold.** `IPClusterWindow` is one hour. When the set for a key reaches
  `IPCLUSTER_THRESHOLD` distinct tokens (default **20**), the event that brings it to 20 and every
  later event for that key go to the quarantine band, until one window has passed since the
  threshold was last reached (`internal/gateway/ipcluster.go`).
- **Across nodes.** Each gateway publishes every token that is new to it on the NATS core subject
  `edge.ipcluster` (at most `IPCLUSTER_THRESHOLD` messages per key and window) and merges what
  peers publish, so all three nodes count the same set. At most 200,000 keys per node
  (`clusterKeys`), least recently used evicted. Nothing is persisted.
- **Bands.** Every site has two Umami websites: the primary (id equals the site id) and a
  quarantine website. Quarantined events go to the quarantine website. They are never deleted
  and never mixed into primary numbers. Tenants open the quarantine view through
  `GET /api/sso/umami?site=<id>&view=quarantine`, and the per-site counters include
  `quarantined`.

Proved by: `TestEdgeSecretsCluster` subtests `quarantine starts at exactly the threshold across
three gateways`, `a node publishes at most threshold tokens per key and window` and `the key
bound evicts the least recently used set` (`internal/gateway/visitor_test.go`, implemented in
`internal/gateway/ipcluster_test.go`); `TestWebPath` subtest `quarantine`
(`tests/webpath/webpath_test.go`, real browsers through the whole chain).

### False positives on shared addresses

Many real people share one address or one /24:

- **Carrier-grade NAT.** Mobile carriers and some home ISPs put thousands of subscribers behind a
  few addresses.
- **VPN exits** and privacy relays.
- **Offices and campuses** behind one NAT.

When more than 20 distinct visitor tokens reach one site from one such prefix in an hour, real
visitors are quarantined too. This is expected and accepted: quarantine is a separate band shown
next to the primary data, never deletion, so the tenant and operators can still see and judge
those visits. A popular site with a lot of mobile traffic sees a quarantine band above zero.
Change `IPCLUSTER_THRESHOLD` only with a recorded reason in
[../reference/gateway-limits.md](../reference/gateway-limits.md).

### One visitor's volume

Same-IP skepticism counts distinct visitor tokens, so one scripted client with one address and
one user agent stays one token and is never caught by it. A second count closes that gap: each
gateway counts queued events per site and visitor token in a window of one hour from the
visitor's first event. Past `VISITOR_EVENTS_PER_HOUR` (default **600**, ten a minute on
average) every further event of that visitor in the window goes to the quarantine band and is
counted as `quarantined` (`internal/gateway/volume.go`). The count is per node, in a bounded
table of 200,000 visitors, and nothing is persisted; a visitor spread over three nodes can send
three times the limit into the primary band.

Proved by: `TestOneVisitorsVolumeIsQuarantined` (`internal/gateway/node_test.go`).

## 7. Rate limits

Token buckets in each gateway, per node (`internal/gateway/ratelimit.go`):

| Key | Rate / burst | Meaning |
|---|---|---|
| `RATE_SITE_PER_SECOND` / `RATE_SITE_BURST` | 200 / 400 | per site |
| `RATE_PREFIX_PER_SECOND` / `RATE_PREFIX_BURST` | 20 / 60 | per site and keyed hash of the /24 or /48 |

Over either: 429. A refusal by the prefix bucket does not take a site token. Each table holds at
most 200,000 buckets. Limits are per node, so a site can send three times the site rate across
three edge nodes. `GET /v1/c` has its own buckets at the same rates, checked after the origin
and fetch metadata checks, so fetching challenges never uses up the event budget and refused
challenge requests take no token ([../reference/gateway-limits.md](../reference/gateway-limits.md);
`TestChallengeRateLimits` in `tests/gateway`).

Proved by: `TestRateLimitPrefixBurstAndRefill`, `TestRateLimitSite`,
`TestRateLimitPrefixRefusalKeepsSiteTokens`, `TestRateLimitClockBackwards`,
`TestRateLimitBoundedKeys`, `TestRateLimitConfig`, `TestRateLimitConcurrent`
(`internal/gateway/ratelimit_test.go`). Acceptance: no event of the browser test suites is
refused, rate limited or quarantined (`TestBrowserMatrix`, `tests/gateway`).

## 8. Visibility

Every outcome is counted per site on `EDGE_STATS` and stored hourly (`cp.web_ingest_hourly`):
`accepted`, `quarantined`, `bot`, `dlq` and `rejected_<reason>` (for example
`rejected_origin`, `rejected_challenge_tag`, `rejected_proof_work`, `rejected_replay`,
`rejected_signals`, `rejected_rate_limited`; full list in `internal/gateway/reasons.go`). Tenants see them under
`GET /api/sites/{site_id}/ingest`; operators see them in the tenant detail. A sudden rise in
`quarantined` or `rejected_replay` is the signal that someone is forging traffic. A Sec-GPC drop is counted as `rejected_gpc`; it is a
privacy choice, not a sign of forgery.

## 9. Boundaries of the checks

- **The proof does not prove browser execution.** A scripted non-browser client can fetch a
  challenge, solve 12 bits in milliseconds, and forge `Origin`, `Sec-Fetch-Site`,
  `Sec-Fetch-Mode`, `Sec-Fetch-Dest` and `User-Agent`. The gateway then accepts its events. This
  is tested on purpose: `TestEventChecks` subtest
  `scripted client with forged headers is accepted` (`tests/gateway/checks_test.go`) posts from a
  Go client and expects 202. The proof raises the cost of forged traffic and catches naive server
  posts; a determined actor with a scripted client gets through it.
- **Real browsers on residential proxies pass every layer.** A headless or real browser with an
  ordinary user agent runs the real tracker, so the proof, `Origin` and fetch metadata are
  genuine; `isbot` does not match; and spreading the traffic over many residential addresses in
  different prefixes keeps each prefix under `IPCLUSTER_THRESHOLD` and the prefix rate limit.
- **Replay** is per node and in memory: up to 3 x `PROOF_REPLAY_LIMIT` events per proof across
  three nodes, more if the table evicts the entry or a gateway restarts. Only queued events
  count; an event refused by a later check gives its use back.
- **Volume per visitor is bounded, not stopped.** One address and one user agent can send
  `VISITOR_EVENTS_PER_HOUR` (600) events an hour per node into the primary band, three times that
  across three nodes, before the rest go to quarantine (section 6). Page views, custom events
  and session duration can therefore still be raised by one client within that bound, while
  visitors stay at 1. A client that rotates its user agent becomes several visitors and then
  meets same-IP skepticism instead.
- **Same-IP skepticism has false positives** on shared addresses (section 6) and false negatives
  for traffic spread over many prefixes.
- **API metrics are self-reported.** OTLP data comes from the tenant's own servers; the collector
  can rate limit and cap cardinality but cannot verify that requests happened
  ([threat-model.md](threat-model.md) section 3.3).
- **No first-party proxy.** Tenants send events straight from the browser. Proxying them through
  the tenant's own server turns every event into a server-side post from one address, which the
  proof and same-IP checks treat as suspicious.

Together the layers raise the cost of manipulation and make it visible. When you compare projects,
read the primary numbers together with the quarantine band and the outcome counters.
