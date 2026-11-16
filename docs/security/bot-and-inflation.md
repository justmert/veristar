---
title: "Bot and inflation strategy"
description: "How the platform grades the integrity of every site's web traffic from evidence it observes itself, and how it refuses and separates bot traffic."
---

Every site gets an **integrity grade** for every UTC day: **A**, **B** or **C**, with the reasons
that set it, in plain language. The grade rests on evidence the platform's own edge observes: the
class of each visitor's network, the number of distinct networks, the proof of work, the
quarantine band and the site's own baseline. The tenant's page cannot raise a grade; what the
page reports can only lower it. Operators see the grade in every web view, the project sees the
same grade and the same reasons on its own overview, and the machine API, the MCP tools and the
exports carry it.

Underneath the grade, four layers decide what each event is:

1. **Refuse** naive and malformed traffic at the edge (sections 3 to 6).
2. **Classify** the network every accepted event came from, at the edge (section 7).
3. **Separate** crowded networks and over-active visitors into a quarantine band, with
   thresholds that follow the network class and the site's size (section 8). Nothing is deleted.
4. **Grade** each site and day from what the first three layers counted (section 9).

Parameter names and values come from the code (`internal/config/gateway.go`,
`internal/gateway/`, `internal/integrity/`) and the deployed defaults
(`infra/ansible/roles/edge/defaults/main.yml`, `deploy/compose/dev.yml`), and are listed in
[../reference/gateway-limits.md](../reference/gateway-limits.md).

## 1. Evidence classes

Each number on a dashboard comes from one of these sources, and the source decides what it can
prove:

| Class | What it is | Who can fabricate it |
|---|---|---|
| **Platform-observed** | What the ingestion edge sees for itself: the network of each connection and its class, distinct networks, the proof of work, arrival volume, the quarantine and refusal counters | Only someone who pays for real distinct network endpoints and runs the challenge for each event |
| **Page-reported** | What the tracker says: path, referrer, title, custom events, and therefore page views, bounces and visit duration; from tracker 1.3.0 also whether a page runs inside a frame and whether a load was left without being visible | The tenant, because the tenant controls its page |

The grade uses platform-observed evidence. Page-reported numbers enter only as reasons that can
lower a grade (rules `page_volume`, `framed_share` and `never_visible_share`, section 9), never
as one that raises it. Visitors are counted
on the gateway's own visitor token, derived from the connection, so a page can add page views for
a real visitor but cannot add visitors.

## 2. Where the checks run

```
browser ── sdk.js ── GET /v1/c (challenge) ── POST /v1/e (event) ──> edge Caddy ──> gateway
                                                                                   │
                       network class, band, counters <──── checks pass ────────────┘
                                │                    │
                 EDGE_STATS (counters)       JetStream WEB_EVENTS
                                │                    │
              jobs: edgestats, integrity       replayer ──> Umami POST /api/send (isbot)
                                │
                     agg.web_integrity_daily ──> console, machine API, MCP, exports
```

The gateway (`internal/gateway/server.go`, `checkEvent`) checks every `POST /v1/e` in this order,
and stops at the first refusal:

| # | Check | Result |
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
| 10 | Network class of the client address | (never refused) |
| 11 | Same-network skepticism with class-aware thresholds, and the per-visitor volume bound: primary or quarantine band | (never refused) |

Checks 5 and 6 are one call (`ProofVerifier.Verify`, `internal/gateway/proof.go`). The replay
counter moves only for proofs that passed every other part of the check, so forged proofs cannot
use up a real visitor's allowance. `GET /v1/c` checks the site, `Origin` and fetch metadata
before it issues a challenge.

After the gateway, the replayer delivers the event to Umami with the visitor's original
`User-Agent`, and Umami's own `isbot` check runs (section 3).

## 3. Umami's `isbot` on the original user agent

Umami 3.5.0 runs `isbot` on the `User-Agent` of `POST /api/send`; for a bot it answers
`200 {"beep":"boop"}` and stores nothing. The replayer forwards the visitor's original
`User-Agent` header unchanged, and Umami is deployed without `DISABLE_BOT_CHECK`
(`infra/ansible/roles/core/templates/compose.yml.j2`, `deploy/compose/dev.yml`). A bot answer is
counted as `bot` on `EDGE_STATS` and in `replayer_outcomes_total{outcome="bot"}`.

Proved by: `TestBotUserAgentStoresNothing` (`tests/umami-compat/send_test.go`) and `TestWebPath`
subtest `bots` (`tests/webpath/webpath_test.go`).

`isbot` matches declared bots; undeclared automation is the job of the layers below.

## 4. Server-issued challenge and proof

Full detail and measurements: [../reference/browser-proof.md](../reference/browser-proof.md) and
[../reference/wire-v1.md](../reference/wire-v1.md).

- Once per page load the tracker calls `GET /v1/c?w=<site id>`. The gateway answers
  `{w, iat, salt, bits, tag}`: `iat` is server time, `salt` 16 random bytes, `bits` the current
  `PROOF_BITS`, and `tag = HMAC-SHA256(proof_key, w|iat|salt|bits)` with a daily key held in the
  memory KV bucket `edge_secrets`.
- The tracker finds a `nonce` such that `SHA-256(w|iat|salt|nonce)` has at least `bits` leading
  zero bits (WebCrypto) and attaches the proof to every event of that page load. The challenge
  stays in page memory: no cookie, no browser storage.
- The gateway refuses a proof older than `ChallengeTTL` (6 hours), more than `MaxClockSkew`
  (30 seconds) in the future, with `bits` above `MaxProofBits` (24), with a wrong tag (which also
  covers a proof issued for another site), or with too little work.
- `PROOF_BITS` is **12**: the highest tested value whose median solve time stays under 150 ms at
  a 6x CPU slowdown in Chromium, Firefox and WebKit. Difficulty comes from the server and can be
  raised without a tracker release.

A request without a valid proof did not go through the challenge flow, so a plain HTTP client that
posts a JSON body is refused. Proof failures are counted per site, and their share is a grade
input (rule `proof_failures`).

Proved by: `TestChallengeRoundTrip`, `TestKeyRotationOverlap`, `TestKeyLost`, `TestKeyTTL`
(`internal/gateway/challenge_test.go`); `TestTrackerVectorBytes`, `TestTrackerVectorsVerify`,
`TestLeadingZeroBits` (`internal/gateway/proof_test.go`); `TestEventChecks` subtests
`naive post without proof is 403` and `forged tag is 403` (`tests/gateway/checks_test.go`);
`TestClocks` (`tests/gateway/clock_test.go`).

## 5. Origin and fetch-metadata checks

- **Origin.** The `Origin` header must equal one of the site's registered origins. Proved by
  `TestCheckOrigin`, `TestValidOriginPattern` (`internal/gateway/origin_test.go`) and
  `TestEventChecks` subtest `wrong origin is 403`.
- **Fetch metadata.** The tracker's `fetch` produces `Sec-Fetch-Site: cross-site` (or
  `same-site`), `Sec-Fetch-Mode: cors` and `Sec-Fetch-Dest: empty` in Chromium 156, Firefox 157
  and WebKit 27.2 (`tests/e2e/specs/headers.spec.ts`, recorded in `browser-proof.md`). The
  `SignalsChecker` (`internal/gateway/signals.go`) refuses any of these headers that is present
  with another value or repeated, and refuses missing headers when the user agent claims an engine
  known to send all three (Chromium 80, Firefox 90, Safari 16.4 or later) and
  `GATEWAY_SECURE_TRANSPORT` is `true`. Android WebViews, non-Safari iOS browsers and EdgeHTML
  were not measured, so they are never refused for missing headers. Proved by `TestSignals` and
  `TestSendsFetchMetadata` (`internal/gateway/signals_test.go`).

These checks stop requests from the wrong page and naive clients. A client that sets every header
itself passes them (`TestEventChecks` subtest `scripted client with forged headers is accepted`);
it is then classified by its network and counted like any other event (sections 7 to 9).

## 6. Replay limit and rate limits

A captured proof may carry at most `PROOF_REPLAY_LIMIT` events (**50**) on one node, counted per
proof tag in a bounded table of 200,000 entries (`replayEntries`, `internal/gateway/node.go`).
Proved by `TestReplayLimit` (`internal/gateway/proof_test.go`) and `TestEventChecks` subtest
`replay past the limit is 403`.

Token buckets in each gateway, per node (`internal/gateway/ratelimit.go`):

| Key | Rate / burst | Meaning |
|---|---|---|
| `RATE_SITE_PER_SECOND` / `RATE_SITE_BURST` | 200 / 400 | per site |
| `RATE_PREFIX_PER_SECOND` / `RATE_PREFIX_BURST` | 20 / 60 | per site and keyed hash of the /24 or /48 |

Over either: 429. `GET /v1/c` has its own buckets at the same rates. Proved by
`TestRateLimitPrefixBurstAndRefill`, `TestRateLimitSite`,
`TestRateLimitPrefixRefusalKeepsSiteTokens`, `TestRateLimitBoundedKeys`
(`internal/gateway/ratelimit_test.go`) and `TestChallengeRateLimits` (`tests/gateway`).

## 7. Network class, observed at the edge

The gateway classifies the network of every accepted event from the connection's own address,
never from anything the page sends (`internal/gateway/network.go`):

| Class | Meaning | Source |
|---|---|---|
| `access` | residential broadband and mobile carriers | autonomous system category `isp`, or network role `access_provider` |
| `organisation` | business, education and government networks | categories `business`, `education_research`, `government_admin` |
| `hosting` | cloud, hosting and content networks, where most VPN, proxy and relay exits live | category `hosting` |
| `anonymiser` | published Tor exit addresses | Tor exit lists |
| `unknown` | everything the open data cannot place, including private ranges | none |

The data is open, pinned by month and baked into the gateway image next to the country file
(`scripts/fetch-network.sh`, [../reference/geo.md](../reference/geo.md)):

- **DB-IP IP to ASN Lite** (CC BY 4.0) maps the address's /24 or /48 to its autonomous system.
- **ipverse as-metadata** (CC0 1.0) gives each autonomous system a category and a network role,
  from the commit current on the first day of the month.
- **Tor Project exit lists** (CC0) give every address that exited Tor during the month before.

The address is looked up in memory and then forgotten. Only the class leaves the request: it is
counted per site as `evidence_net_<class>` on `EDGE_STATS` and stored as a daily count per site
(`cp.web_evidence_daily`). For the distinct network count, the gateway reports each network once
per site and UTC day as an HMAC of the autonomous system number under the gateways' daily secret,
cut to 64 bits (`network_<key>`). The secret is held in memory only and is gone after midnight,
so a stored key can never be linked to a network; the jobs task deletes the keys once their day is
graded for the last time. No address and no autonomous system number is stored anywhere.

Category data describes each network's primary function and is imperfect by its own account, which
is one reason the class separates and grades, and never deletes.

Proved by: `TestNetworkClassifiesFromOpenData`, `TestNetworkRefusesBrokenFiles`,
`TestNetworkReloadKeepsDataOnFailure` (`internal/gateway/network_test.go`); `TestEdgeSecretsCluster`
subtest `network keys are daily and differ from prefix keys` (`internal/gateway/visitor_test.go`); `TestNetworkClassAtTheEdge`
(`tests/gateway/network_test.go`: hosting, access and Tor addresses through the real gateway
image, with the counters on `EDGE_STATS` holding classes and keys only).

## 8. Same-network skepticism and the quarantine band

- **Fingerprint.** The visitor token is
  `vt = hex(HMAC-SHA256(daily_secret, site_id || 0x00 || ip || 0x00 || user_agent))`.
- **Key.** Per site and per keyed hash of the truncated address (IPv4 /24, IPv6 /48) under a
  second daily secret. The network itself is never sent or stored.
- **Threshold by network class and site size.** A hosting network legitimately carries almost no
  visitors; a mobile carrier's address prefix legitimately carries many, because carrier-grade NAT
  puts thousands of subscribers behind few addresses. So the threshold of distinct visitor tokens
  per prefix and hour follows both the class and the site's typical traffic
  (`integrity.PrefixThreshold`):

  | Class | Threshold |
  |---|---|
  | `access`, `organisation`, `unknown` | `max(IPCLUSTER_THRESHOLD, scale / 100)` |
  | `hosting`, `anonymiser` | `max(ceil(IPCLUSTER_THRESHOLD / 4), scale / 1000)` |

  `scale` is the site's typical daily visitors (the median of up to 28 earlier days) rounded down
  to a power of two, published to the gateways in the site's registry entry. With the default
  `IPCLUSTER_THRESHOLD` of **20**: a new site quarantines a hosting prefix at its fifth distinct
  visitor in an hour and an access prefix at its twentieth; a site with 8,192 typical daily
  visitors carries 81 distinct visitors per access prefix and hour, and 8 per hosting prefix.
  Every threshold is capped at 2,000. A popular, mobile-first site is therefore not quarantined
  for its audience's carrier, and a burst of fake visitors cannot raise its own threshold,
  because the scale comes from earlier days.
- **Window.** One hour. From the event that reaches the threshold, every event for that key goes
  to the quarantine band until one window has passed since the threshold was last reached
  (`internal/gateway/ipcluster.go`).
- **Across nodes.** Each gateway publishes every token that is new to it on `edge.ipcluster` and
  merges what peers publish, so every node counts the same set and applies the threshold it
  computes for the key.
- **One visitor's volume.** Past `VISITOR_EVENTS_PER_HOUR` (**600**) events in an hour, every
  further event of that visitor token goes to the quarantine band (`internal/gateway/volume.go`).
- **Bands.** Every site has two Umami websites: the primary and a quarantine website.
  Quarantined events go to the quarantine website; they are never deleted and never mixed into
  primary numbers. Tenants open the quarantine view through
  `GET /api/sso/umami?site=<id>&view=quarantine`.

Proved by: `TestPrefixThreshold` and `TestScaleOf` (`internal/integrity/integrity_test.go`);
`TestEdgeSecretsCluster` subtests `quarantine starts at exactly the threshold across three
gateways`, `thresholds follow the network class and the site's scale` and `the key bound evicts
the least recently used set` (`internal/gateway/visitor_test.go`); `TestNetworkClassAtTheEdge`
(`tests/gateway/network_test.go`); `TestThreeInstances` (`tests/gateway/cluster_test.go`);
`TestOneVisitorsVolumeIsQuarantined` (`internal/gateway/node_test.go`); `TestWebPath` subtest
`quarantine` (`tests/webpath/webpath_test.go`).

## 9. Integrity grade

The jobs task `integrity` (`internal/jobs/integrity`) runs hourly. For every site it grades today
and the three days before it, the window `aggregate-web` reads again, from:

| Input | Source | Evidence class |
|---|---|---|
| Events by network class | `cp.web_evidence_daily` | platform-observed |
| Distinct networks | `cp.web_networks_daily` | platform-observed |
| Accepted, quarantined and proof-failed requests | `cp.web_ingest_hourly` | platform-observed |
| Primary visitors, and their median over up to 28 earlier days | `agg.web_daily` | platform-observed |
| Primary page views | `agg.web_daily` | page-reported, can only lower |
| Page views of trackers that report page signals, the framed part, never-visible notices | `cp.web_signals_daily` | page-reported, can only lower (rule version 2) |

Rule version 2 (`internal/integrity/grade.go`), applied since tracker 1.3.0, starts every day at
A. Each rule can only lower the grade:

| Code | Lowers to | When |
|---|---|---|
| `hosting_share` | B / C | 25% / 50% of classified events come from hosting networks or anonymisers |
| `quarantine_share` | B / C | 10% / 30% of accepted events went to the quarantine band |
| `few_networks` | B | at least 100 visitors and fewer than one network per 100 visitors |
| `spike` | B | at least 100 visitors and 10 times the baseline median, with 7 or more baseline days |
| `proof_failures` | B | half or more of the challenge-backed requests failed their proof |
| `page_volume` | B | the page reports 50 or more page views per visitor |
| `framed_share` | B / C | from 50 page views of trackers that report signals, 25% / 50% of them inside a frame (version 2) |
| `never_visible_share` | B | from 50 page views and notices together, 30% of them are loads left without being visible (version 2) |

Rule version 1 is the same set without the last two rules. `GradeVersion` still applies it, so a
day graded under version 1 is reproduced exactly from its stored inputs; version 1 does not read
the signal columns, which hold 0 for days graded before them.

**Page signals.** A page loaded in a hidden frame on an unrelated, busy site has the tenant's
origin, so it passes the origin check and every later one. Tracker 1.3.0 reports the frame
truthfully: every event of a framed page carries `fr: true`, and a page that is left before it was
ever visible sends a never-visible notice instead of a page view
([../reference/wire-v1.md](../reference/wire-v1.md), "Pages a person can see"). The gateway
counts, per site and UTC day, the accepted page views of releases that report signals
(`signal_views`), the framed part (`signal_framed`) and the accepted notices
(`signal_never_visible`). The framed share is taken of `signal_views` only, so pages still on an
older release neither dilute nor raise it. A site built to be embedded shows a steady framed share
and its reason explains the grade; a frame used to inflate a site shows nearly all views framed.
The signals are the page's own claims: leaving them out (an older release, a modified copy) only
removes a reason, and sending more of them only lowers the site's own grade. A notice needs the
site's origin and a solved proof like any event, so it cannot be sent for another site from
outside its pages.
A day with fewer than 50 accepted events has no grade; its reason `too_little_traffic` says so.
The thresholds leave room for real audiences: Safari users behind iCloud Private Relay and people
on company VPNs arrive from hosting networks, so a share of hosting traffic is normal and only a
large share lowers the grade.

**Over a range** (a month, a funding round), a project's grade combines its graded site days:
**C** when at least a quarter of them are C, **B** when at least a quarter are B or C, **A**
otherwise. A single bad day does not define a month, and the reasons list how many days each rule
applied to, so a single bad day stays visible.

Every stored day keeps its inputs, its reasons and the rule version that graded it
(`agg.web_integrity_daily`), so a later change of rules is a new version and never rewrites how a
closed evaluation was graded.

Where the grade appears:

- **Operators:** a sortable Integrity column in the global web table with an Integrity filter
  (`integrity_at_or_below`: `C` lists the projects graded C, `B` those graded B or C, `A` every
  graded project), the grade and reasons in compare and in the tenant detail,
  `GET /api/operator/web/integrity` for one project's reasons per site and day
  ([../reference/control-plane-api.md](../reference/control-plane-api.md) section 17).
- **The project:** the same report on its own overview (`GET /api/tenants/{tenant_id}/integrity`),
  so builders see exactly what SCF sees.
- **Machines:** `GET /api/metrics/v1/web/integrity` per site and day, with the same
  `integrity_at_or_below` filter on the site day's grade, and an `integrity` object on every
  `/web/daily` item.
- **MCP:** `get_web_integrity` for a project, `get_tenant_integrity` for operators; the global web
  overview carries each project's grade, sorts by it and takes `integrity_at_or_below`.
- **Exports:** `integrity_grade` columns in the web project, compare and project overview CSV
  exports; the web project export takes the list's Integrity filter.

### Appealing a grade

The grade is automatic: the rules above compute it, and no one sets or edits it by hand. Operators
decide what a grade means for a project. If you think your project's grade misreads your traffic,
appeal it:

1. Open your project's overview or **What SCF sees** in the console and select **Appeal this
   grade** next to the grade. It appears whenever the range has a grade.
2. The link opens a new issue on the **Integrity appeal** form of the public tracker, filled in
   with your project slug, each site's domain and grade, the period, the range grade, the reason
   codes with their day counts and the rule set version. It carries no personal data and no
   visitor data.
3. Add what the operator needs to know, such as a launch, a campaign or an audience on company
   VPNs, with dates in UTC, and submit the issue.

An operator reads the appeal next to the evidence of each site day and answers on the issue
within the [support targets](../support.md). The appeal does not change the stored grade; the
operator's decision is recorded on the issue. When an appeal shows that a rule misreads a kind of
traffic, the fix is a new rule set version, which grades later days and leaves closed
evaluations as they were graded.

Proved by: `TestGradeRules`, `TestPageReportedNeverRaises`, `TestCombine`
(`internal/integrity/integrity_test.go`); `TestGrader` (`internal/jobs/integrity`);
`TestGradeSignalRules`, `TestRuleVersions` (`internal/integrity/integrity_test.go`);
`TestPageSignalsOfTracker13` (`internal/gateway/signals_node_test.go`); `TestPageSignals`
(`tests/webpath/signals_test.go`, Chromium, Firefox and WebKit through the real gateway);
`TestIntegrityViews`, `TestIntegrityRankSQL` (`internal/controlplane/operator/integrity_test.go`);
`TestMachineAPI` subtest `integrity rows page in key order and daily rows carry the day's grade`
(`internal/controlplane/operator/machine_test.go`).

## 10. Visibility

Every outcome is counted per site on `EDGE_STATS` and stored hourly (`cp.web_ingest_hourly`):
`accepted`, `quarantined`, `bot`, `dlq` and `rejected_<reason>` (full list in
`internal/gateway/reasons.go`). Tenants see them under `GET /api/sites/{site_id}/ingest`;
operators see them in the tenant detail. The evidence counters (`evidence_net_<class>`,
`network_<key>`, `signal_<name>`) and the tracker file counters of the coverage estimate
(`script_requests`, `script_gpc`) travel on the same stream into their own daily tables and never
mix with these outcomes. A Sec-GPC drop is counted as `rejected_gpc`; it is a privacy choice, not a sign of
forgery. The visits a privacy signal keeps out of the numbers are estimated per site from the
requests for the tracker file ([../compliance/privacy-strategy.md](../compliance/privacy-strategy.md),
section 6), so a reviewer comparing projects knows the size of the gap.

## 11. What the grade establishes

- **Visitors from distinct access networks are the strongest web number.** Faking them means
  paying for real distinct residential or mobile endpoints and running the challenge for every
  event. An A grade says the traffic came from the kind and spread of networks real audiences
  use, at the site's usual scale.
- **Real browsers on residential connections pass every layer**, and so do real people paid to
  visit. No technique tells them apart from organic visitors; conversion into corroborated use
  does, which is why the grade sits next to the project's on-chain activity in the operator
  views.
- **The TLS handshake is not an input of rule versions 1 and 2.** The edge terminates TLS in Caddy, which
  does not expose the ClientHello to the gateway; a handshake check against the claimed browser
  family becomes a new rule version when a maintained way to pass it exists.
- **Thresholds are per node where the counters are per node.** Replay and per-visitor volume are
  counted per node and in memory; same-network sets are shared across nodes.
- **API metrics are self-reported.** OTLP data comes from the tenant's own servers; the collector
  rate limits and caps cardinality ([threat-model.md](threat-model.md) section 3.3). The grade
  covers web traffic.
- **No first-party proxy.** Proxying events through the tenant's own server turns every event
  into a server-side post from one hosting address, which the checks classify as such.

When you compare projects, read the primary numbers together with the integrity grade and its
reasons; the investigation steps are in
[../runbooks/metric-inflation.md](../runbooks/metric-inflation.md).
