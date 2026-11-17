---
title: "Gateway limits"
description: "Every limit of the collect gateway, with its starting value and the reason for it."
---

Settings of the collect gateway (`cmd/gateway`, `internal/gateway`). Every value
is a required environment key: the gateway refuses to start when one is missing,
so no limit runs at a value nobody chose. Change a value only with a recorded
reason in the table below.

## Starting values

| Key | Value | Meaning |
|---|---|---|
| `PROOF_BITS` | 12 | difficulty of the challenge; the highest tested value with a median solve under 150 ms at 6x CPU slowdown in all three engines ([browser-proof.md](browser-proof.md)) |
| `PROOF_REPLAY_LIMIT` | 50 | events one proof (one page load) may carry on one node. only queued events count: an event refused after the proof check (fetch metadata, rate limit, user agent, queue) gives its use back. tracker 1.2.0 fetches a new challenge after 40 events and after a 403 `rejected_replay`, `rejected_challenge_expired` or `rejected_challenge_tag`, and sends that event once more |
| `IPCLUSTER_THRESHOLD` | 20 | the floor of the same-network threshold: distinct visitor tokens per site and truncated prefix per hour. access, organisation and unknown networks use `max(20, scale / 100)`, hosting networks and Tor exits `max(5, scale / 1000)`, where `scale` is the site's typical daily visitors from its registry entry; every threshold is capped at 2000. the event that reaches the threshold and every later one in the window go to the quarantine band ([../security/bot-and-inflation.md](../security/bot-and-inflation.md) section 8) |
| `VISITOR_EVENTS_PER_HOUR` | 600 | queued events per site and visitor token in an hour from the visitor's first event, per node; later events of that visitor in the window go to the quarantine band ([../security/bot-and-inflation.md](../security/bot-and-inflation.md) section 6) |
| `RATE_SITE_PER_SECOND` / `RATE_SITE_BURST` | 200 / 400 | token bucket per site, per node |
| `RATE_PREFIX_PER_SECOND` / `RATE_PREFIX_BURST` | 20 / 60 | token bucket per site and keyed hash of the /24 or /48, per node |
| (same keys) for `GET /v1/c` | 200 / 400 per site, 20 / 60 per site and prefix | the challenge route has its own buckets at the same rates. a page load takes one challenge and sends at least one event, so a visitor within the event limits is within the challenge limits too, and fetching challenges never uses up the event budget |
| `GATEWAY_MAX_BODY` | 8192 | event body bytes; larger answers 413 |
| `CLIENT_IP_HEADER` | `X-Edge-Client-Ip` | the one header the local caddy sets (`deploy/caddy/edge.Caddyfile`) |
| `GATEWAY_SECURE_TRANSPORT` | `true` | browsers reach the gateway over https and send fetch metadata |

Other settings: `GATEWAY_NODE_ID` (unique per instance), `GATEWAY_ADDR` (for caddy),
`GATEWAY_OPS_ADDR` (`/healthz`, `/readyz`, `/metrics`, private), `DBIP_MMDB` and
`TRACKER_RELEASE_DIR` (both baked into the image), and the `NATS_*` keys. The
network data (`dbip-asn-lite.mmdb`, `asn-classes.csv`, `tor-exits.txt`) is read
from the directory of `DBIP_MMDB` ([geo.md](geo.md)).

Fixed in code: publish acknowledgement wait 2 s (`PublishAckWait`), readiness canary
every 1 s with a maximum age of 7 s (`CanaryMaxAge`, so `/readyz` fails within 10 s
of a queue outage), 200,000 entries each for the replay table, the ip-cluster sets,
the visitor volume table and each rate limit table. Coverage counting of `GET /v/*/sdk.js`:
a token bucket per site and keyed hash of the /24 or /48, burst 60 and one token every
10 s, per node (`DefaultScriptCountRate`). It decides only whether a request counts toward
`script_requests` and `script_gpc`; the file is always served and stays cacheable, and
requests past the bucket are counted in `gateway_script_requests_not_counted_total`
([../compliance/privacy-strategy.md](../compliance/privacy-strategy.md) section 6, "Forged
requests").

Each limit is per node. With three edge nodes, a site can send three times the
site rate, and a captured proof can be replayed three times the replay limit.

Acceptance criterion, met on 2026-11-12: no event of the browser test suites
was refused, rate limited or quarantined against the real gateway
(`TestBrowserMatrix` in `tests/gateway`).

| Date | Key | Change | Reason |
|---|---|---|---|
| 2026-11-12 | all | starting values | first version of the gateway |
| 2026-11-13 | `VISITOR_EVENTS_PER_HOUR` | new, 600 | same-IP skepticism counts distinct visitor tokens only, so one address and one user agent could send at the prefix rate into the primary band; ten events a minute on average is far above a person's use of one site |
| 2026-11-13 | `PROOF_REPLAY_LIMIT` | counts queued events only | refused events used up a proof that a single-page app keeps for the whole tab (`TestRefusedEventsKeepTheProofAllowance` in `internal/gateway`) |
| 2026-11-13 | `RATE_SITE_*`, `RATE_PREFIX_*` | also applied to `GET /v1/c`, in separate buckets | the challenge route had no limit, so one client could request challenges without bound; checked after the origin and fetch metadata checks, so refused requests take no token (`TestChallengeRateLimits` in `tests/gateway`) |
| 2026-11-17 | script count bucket | new, 60 at once and one per 10 s per site and prefix | the tracker file route has no proof or origin check, so forged `Referer`, `Sec-Fetch-Dest` and `Sec-GPC` headers from one network could move a site's unmeasured share toward 0% or 100% (`TestForgedScriptRequestsAreCountedWithinAPrefixLimit` in `internal/gateway`) |

## Responses

202 queued (only after the JetStream acknowledgement), 204 dropped for `Sec-GPC: 1`,
400 malformed (also a missing `User-Agent`), 403 origin, challenge, proof, replay or
fetch metadata, 404 unknown or inactive site, 410 revoked tracker version (the file,
and an event whose `sv` names a revoked release), 413 body too large, 429 rate limited (events and challenges), 503 queue unavailable, registry
not loaded or a missing client address header (events and challenges). Every response to a request with `Origin` carries
`Access-Control-Allow-Origin` with that origin and `Vary: Origin`. The tracker files
carry `Access-Control-Allow-Origin: *`, `Cross-Origin-Resource-Policy: cross-origin`
and `Cache-Control: public, max-age=31536000, immutable`; there is no `latest` alias.
The node's Caddy compresses them with zstd or gzip when the browser accepts either
(`Content-Encoding`, `Vary: Accept-Encoding`); the integrity hash covers the decoded file, so
subresource integrity is unaffected. No other route is compressed
(`TestEdgeCompressesTheTrackerFile` in `deploy/caddy`).

## Readiness

`/readyz` checks, in this order: a JetStream acknowledgement within 7 s, that
`edge_secrets` is not known to be missing, the registry snapshot, today's proof key,
today's daily secrets. The canary is a create of a
short lived key (`canary.<node>.<ns>`, ttl 30 s) in `edge_secrets`, not a message on
`WEB_EVENTS`: a work-queue message would reach the replayer as an event. An outage of
`WEB_EVENTS` alone therefore does not show in `/readyz`; event publishes then answer
503 directly.

A restart of a single NATS server loses the memory buckets (`edge_secrets`,
`visit_state`, `locks`) entirely, not just their keys; a full restart of the 3-node
cluster keeps their definitions but empties them. Each service recreates the buckets it
uses itself, with exactly the topology config from `internal/natsx`: the gateway
`edge_secrets`, the replayer `visit_state` and `locks`, jobs and the metricswriter
`locks`. They check at
startup, every second, and at once when a bucket operation fails as a missing stream
would. Their NATS users may create exactly those `KV_<bucket>` streams and nothing
else. No operator step is needed (`tests/recovery`, build tag `recoveryit`).

An emptied `edge_secrets` is the accepted trade-off: the day's secrets and proof key are
new, so visitors seen earlier that day are counted again and challenges issued before
the restart stop verifying once the 10 s key refresh ran. Every gateway compares its
loaded daily secrets with the bucket every 10 s and switches to the stored ones, so a
gateway that kept running agrees with one started after the restart. Secrets are never
persisted.

Measured 2026-11-12 (`tests/recovery`), times after NATS was healthy again:

| | single server | 3-node cluster |
|---|---|---|
| gateway accepts an event | 0.2 s | 0.0 s |
| replayer delivers it to Umami | 2.2 s | 3.3 s |
| jobs writes its outcome counter | 2.3 s | 7.4 s |
| all gateways on the same daily secret | 7.7 s | 7.5 s |

## Throughput

Measured 2026-11-12 on a 10-core Intel Core Ultra 5 225 host, one
gateway, one NATS server with file storage, all in Docker on one machine. Every
request is a full `POST /v1/e`: parse, proof, origin, rate limits, visitor token,
country, ip-cluster, sanitization and a JetStream publish waited for. Traffic is
spread over 20 sites and random 10.0.0.0/8 client addresses so the limiter does not
cap it.

| Tool | Load | Result |
|---|---|---|
| k6 2.3.0 (`TestThroughputK6`, `make gateway-load`, docker image, gateway container directly) | constant arrival 2,500 req/s for 30 s | 74,723 requests, 2,490.6 req/s, 0 failed, every one queued; latency median 0.23 ms, p95 0.42 ms, p99 1.48 ms, max 246 ms. A second run with the host under load from other jobs (load average about 30): 2,499.1 req/s, p99 5.73 ms. During a load average near 50 one run crossed the 50 ms p99 threshold (81 ms), so the measurement needs a host that is not shared with other heavy work. |
| `go test -bench EventPath -benchtime 20000x` (in-process `gateway.Node`, loopback http, same nats) | 20 concurrent closed-loop clients, 400 sites | 20,000 requests, 4,214 req/s, every one 202; latency p50 3.27 ms, p99 22.67 ms (client, gateway and nats share the 10 cores) |

Target for one gateway instance on its own: at least 2,000 req/s with p99 under 50 ms. Met by both.

`make gateway-load` is a manual target and not part of `make gateway-it` or the nightly e2e
workflow. On GitHub's standard runner for private repositories (2 vCPUs, 7 GB) k6, the gateway and
NATS share two cores; two runs there sent 2,400 and 2,221 req/s with p99 174 ms and 222 ms
(e2e runs 43437576423 and 43454017432), which measures the runner, not the gateway. Run it on a host
with cores to spare for k6 before a release that touches the event path.

Under saturation (160 concurrent clients) the in-process node reached 7,721 req/s
with p99 79.8 ms, so latency at the target rate comes from queueing, not the path.

A production node shares its two vCPUs with TLS, NATS and the core services; what it supports
is in "Capacity" below.

## Capacity

**Supported ingest rate: about 700 events a second per node at p99 50 ms, about 2,100 a second
across the three production nodes.** This is what the gateways accept into `WEB_EVENTS`, with
proofs solved before the run: the `GET /v1/c` each page load also makes is not part of it. It is
not the rate at which events reach Umami. One replayer delivers for the whole cluster to the
Umami processes of one node, so an accepted rate above that node's delivery rate builds a backlog
that the queue keeps for at most 72 hours ([capacity.md](capacity.md), "Web events end to end"). The expected peak of the availability statement is 50 events a
second, so the cluster has about 40 times headroom against it, and one node alone serves the
10x burst (500 a second) at p99 under 50 ms. The cluster figure is three times the per-node
measurement: Traffic Manager spreads `in.veristar.app` over the three nodes, and each node's
gateway queues on its own NATS server. Each `WEB_EVENTS` message is also stored on the two other
servers (R3), so at the full cluster rate every node carries the replicas of the other nodes'
events as well; the single-node runs carried only the replicas of their own.

One production node (`Standard_B2as_v2`, 2 vCPUs, 8 GB, converged: Caddy with TLS, the gateway,
NATS with R3 file streams, PostgreSQL, Umami and the observability stack share the two vCPUs),
measured through the node's Caddy with the `make gateway-load` profile: k6 2.3.0 on a
workstation outside the region, `in.veristar.app` mapped to `production-node-2`, proofs solved beforehand, the probe
site, `RATE_SITE_*` and `RATE_PREFIX_*` raised to 10,000 a second for the runs and restored
after. Latency is node-side: Caddy's `caddy_http_request_duration_seconds` histogram for 202
answers on the event route, read before and after each 30 s run,
p99 interpolated inside its bucket. Client latency adds
the 58 ms round trip.

| Offered rate | Before: release `v0.2.0` | After: release `v0.2.1` |
|---|---|---|
| 500/s | p99 46.3 ms | 499/s achieved, p99 about 45 ms (99.50 % within 50 ms) |
| 600/s | | 599/s, p99 about 55 ms (98.91 %) |
| 700/s | | 699/s, p99 about 50 ms (99.00 %) |
| 800/s | p99 98.5 ms | 795/s, p99 about 68 ms (98.52 %) |
| 1,200/s | p99 1,259 ms | 1,179/s, p99 about 193 ms (85.03 %) |
| 2,500/s | 829/s achieved, p99 2,497 ms | **1,665/s** achieved, p99 about 496 ms |

Every request of every run was answered 202 (0.00 to 0.01 % client errors, all timeouts).
**About 700 a second per node at p99 50 ms** (was about 500), and the
saturated rate went from 829 to 1,665 a second.

What changed between the two columns:

- the event route `POST /v1/e` writes no access log line (`log_skip` in
  `deploy/caddy/edge.Caddyfile`): the JSON encoding through the filter, the write and Docker's
  log copy were about a quarter of Caddy's CPU, and `dockerd` with `containerd-shim` another
  14 % of a core at 1,200 a second;
- Caddy keeps up to 1,024 idle connections to the gateway (default 32), so a burst no longer
  opens and closes a TCP connection per request;
- Caddy and the gateway run with `GOGC=200` and `GODEBUG=adaptivestackstart=1`
  (`sa_ingest_gogc`, `infra/ansible/vars/pins.yml`);
- accelerated networking on the three NICs (`infra/tofu/modules/node`).

None of them changes what is checked, logged elsewhere or stored: 202 still follows the
JetStream acknowledgement of the R3 stream, and every other route keeps its filtered log.

The after runs were made with the replayer's leadership on another node. In a first pass the
replayer on the measured node was delivering the backlog of earlier runs to Umami (one test
visitor, so one event at a time, about 70 a second), which took about a third of a vCPU in NATS,
Umami, the replayer and PostgreSQL and gave p99 74 ms at 500, 87 at 800, 234 at 1,200 and
1,037/s achieved at 2,500. A real node also hosts its share of that delivery work, so the
numbers above are the ingest path alone.

CPU of the node at 1,200 a second (`perf record -a`, whole node; `/proc/stat`: user 49 %,
system 19 %, softirq 16 %, idle 14 %):

| Process | Share of both vCPUs | Of it in the kernel |
|---|---|---|
| Caddy (TLS, HTTP/2, the proxy to the gateway) | 31 % | 11 % |
| NATS (TLS client and route connections, R3 replication, file store) | 22 % | 13 % |
| gateway | 20 % | 7 % |
| everything else (PostgreSQL, Prometheus, etcd, Docker) | 16 % | |
| idle | 11 % | |

The remaining cost is spread thin: no single function in the kernel profile is above 1.1 %
(`nft_do_chain`), and most of the kernel time is TCP sends of small TLS records on the
client, Caddy to gateway and NATS connections. The gateway's own histogram at 800 a second has
97.2 % of events within 25 ms and 99.6 % within 50 ms, so the tail is CPU contention on two
vCPUs shared by three busy processes, not one slow step. TLS between the containers and the R3
acknowledgement before 202 stay: they are what keeps the path private and durable.

**Scaling.** The rate grows with nodes (each one adds its own Caddy, gateway and NATS server)
and with vCPUs per node. The runs bound the CPU an event costs at 1.15 ms of vCPU at saturation
(1,665 a second on both vCPUs) and 2.2 ms all in at 700 a second, including the fixed load of the
other services; on two vCPUs p99 stayed at 50 ms up to about 77 % busy. By those figures a node
with 8 dedicated vCPUs (`Standard_D8as_v6`) serves at least 2,800 a second at p99 under 50 ms
(extrapolated, not measured). `Standard_B2as_v2` is burstable: sustained load above its baseline
uses CPU credits, so the runs above show its burst rate.

## Dropped stats

The `jobs` edgestats task needs a site's tenant from the site registry. If an
`EDGE_STATS` counter names a site that is still not in the registry after
every retry (about 36 seconds), that counter is dropped and logged at error level. The
`jobs_edgestats_dropped_total` metric counts the dropped site counts and
reports 0 from startup. `jobs_edgestats_unattributed_total{outcome}` breaks
the drops down by outcome. The other counters in the message are still
stored.
