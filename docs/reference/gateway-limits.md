# Gateway limits

Settings of the collect gateway (`cmd/gateway`, `internal/gateway`). Every value
is a required environment key: the gateway refuses to start when one is missing,
so no limit runs at a value nobody chose. Change a value only with a recorded
reason in the table below.

## Starting values

| Key | Value | Meaning |
|---|---|---|
| `PROOF_BITS` | 12 | difficulty of the challenge; the highest tested value with a median solve under 150 ms at 6x CPU slowdown in all three engines ([browser-proof.md](browser-proof.md)) |
| `PROOF_REPLAY_LIMIT` | 50 | events one proof (one page load) may carry on one node |
| `IPCLUSTER_THRESHOLD` | 20 | distinct visitor tokens per site and truncated prefix per hour; the event that brings the set to 20 and every later one in the window go to the quarantine band |
| `RATE_SITE_PER_SECOND` / `RATE_SITE_BURST` | 200 / 400 | token bucket per site, per node |
| `RATE_PREFIX_PER_SECOND` / `RATE_PREFIX_BURST` | 20 / 60 | token bucket per site and keyed hash of the /24 or /48, per node |
| `GATEWAY_MAX_BODY` | 8192 | event body bytes; larger answers 413 |
| `CLIENT_IP_HEADER` | `X-Edge-Client-Ip` | the one header the local caddy sets (`deploy/caddy/edge.Caddyfile`) |
| `GATEWAY_SECURE_TRANSPORT` | `true` | browsers reach the gateway over https and send fetch metadata |

Other settings: `GATEWAY_NODE_ID` (unique per instance), `GATEWAY_ADDR` (for caddy),
`GATEWAY_OPS_ADDR` (`/healthz`, `/readyz`, `/metrics`, private), `DBIP_MMDB` and
`TRACKER_RELEASE_DIR` (both baked into the image), and the `NATS_*` keys.

Fixed in code: publish acknowledgement wait 2 s (`PublishAckWait`), readiness canary
every 1 s with a maximum age of 7 s (`CanaryMaxAge`, so `/readyz` fails within 10 s
of a queue outage), 200,000 entries each for the replay table, the ip-cluster sets
and each rate limit table.

Each limit is per node. With three edge nodes, a site can send three times the
site rate, and a captured proof can be replayed three times the replay limit.

Acceptance criterion, met on 2026-11-12: no event of the this stage and this stage
browser suites was refused, rate limited or quarantined against the real gateway
(`TestBrowserMatrix` in `tests/gateway`).

| Date | Key | Change | Reason |
|---|---|---|---|
| 2026-11-12 | all | starting values | the design |

## Responses

202 queued (only after the JetStream acknowledgement), 204 dropped for `Sec-GPC: 1`,
400 malformed (also a missing `User-Agent`), 403 origin, challenge, proof, replay or
fetch metadata, 404 unknown or inactive site, 410 revoked tracker version, 413 body
too large, 429 rate limited, 503 queue unavailable, registry not loaded or a missing
client address header. Every response to a request with `Origin` carries
`Access-Control-Allow-Origin` with that origin and `Vary: Origin`. The tracker files
carry `Access-Control-Allow-Origin: *`, `Cross-Origin-Resource-Policy: cross-origin`
and `Cache-Control: public, max-age=31536000, immutable`; there is no `latest` alias.

## Readiness

`/readyz` checks, in this order: a JetStream acknowledgement within 7 s, the registry
snapshot, today's proof key, today's daily secrets. The canary is a create of a
short lived key (`canary.<node>.<ns>`, ttl 30 s) in `edge_secrets`, not a message on
`WEB_EVENTS`: a work-queue message would reach the replayer as an event. An outage of
`WEB_EVENTS` alone therefore does not show in `/readyz`; event publishes then answer
503 directly.

A restart of a single NATS server loses the memory buckets (`edge_secrets`,
`visit_state`, `locks`) entirely, not just their keys. The gateway stays not ready
until `platformctl nats apply` recreates them, then recovers within a second
(measured: 202 ms).

## Throughput

Measured 2026-11-12 on the controller (Intel Core Ultra 5 225, 10 cores), one
gateway, one NATS server with file storage, all in Docker on one machine. Every
request is a full `POST /v1/e`: parse, proof, origin, rate limits, visitor token,
country, ip-cluster, sanitization and a JetStream publish waited for. Traffic is
spread over 20 sites and random 10.0.0.0/8 client addresses so the limiter does not
cap it.

| Tool | Load | Result |
|---|---|---|
| k6 2.3.0 (`TestThroughputK6`, docker image, gateway container directly) | constant arrival 2,500 req/s for 30 s | 74,723 requests, 2,490.6 req/s, 0 failed, every one queued; latency median 0.23 ms, p95 0.42 ms, p99 1.48 ms, max 246 ms. A second run with the host under load from other jobs (load average about 30): 2,499.1 req/s, p99 5.73 ms. During a load average near 50 one run crossed the 50 ms p99 threshold (81 ms), so the measurement needs a host that is not shared with other heavy work. |
| `go test -bench EventPath -benchtime 20000x` (in-process `gateway.Node`, loopback http, same nats) | 20 concurrent closed-loop clients, 400 sites | 20,000 requests, 4,214 req/s, every one 202; latency p50 3.27 ms, p99 22.67 ms (client, gateway and nats share the 10 cores) |

Target: at least 2,000 req/s with p99 under 50 ms on one instance. Met by both.
Under saturation (160 concurrent clients) the in-process node reached 7,721 req/s
with p99 79.8 ms, so latency at the target rate comes from queueing, not the path.

## Dropped stats

The `jobs` edgestats task needs a site's tenant from the site registry. If an
`EDGE_STATS` counter names a site that is still not in the registry after
every retry (about 36 seconds), that counter is dropped and logged at error level. The
`jobs_edgestats_dropped_total` metric counts the dropped site counts and
reports 0 from startup. `jobs_edgestats_unattributed_total{outcome}` breaks
the drops down by outcome. The other counters in the message are still
stored.
