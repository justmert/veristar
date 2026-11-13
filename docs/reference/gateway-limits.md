# Gateway limits

Settings of the collect gateway (`cmd/gateway`, `internal/gateway`). Every value
is a required environment key: the gateway refuses to start when one is missing,
so no limit runs at a value nobody chose. Change a value only with a recorded
reason in the table below.

## Starting values

| Key | Value | Meaning |
|---|---|---|
| `PROOF_BITS` | 12 | difficulty of the challenge; the highest tested value with a median solve under 150 ms at 6x CPU slowdown in all three engines ([browser-proof.md](browser-proof.md)) |
| `PROOF_REPLAY_LIMIT` | 50 | events one proof (one page load) may carry on one node. only queued events count: an event refused after the proof check (fetch metadata, rate limit, user agent, queue) gives its use back. tracker 1.2.0 fetches a new challenge after 40 events and after a 403 `rejected_replay`, `rejected_challenge_expired` or `rejected_challenge_tag`, and sends that event once more |
| `IPCLUSTER_THRESHOLD` | 20 | distinct visitor tokens per site and truncated prefix per hour; the event that brings the set to 20 and every later one in the window go to the quarantine band |
| `VISITOR_EVENTS_PER_HOUR` | 600 | queued events per site and visitor token in an hour from the visitor's first event, per node; later events of that visitor in the window go to the quarantine band ([../security/bot-and-inflation.md](../security/bot-and-inflation.md) section 6) |
| `RATE_SITE_PER_SECOND` / `RATE_SITE_BURST` | 200 / 400 | token bucket per site, per node |
| `RATE_PREFIX_PER_SECOND` / `RATE_PREFIX_BURST` | 20 / 60 | token bucket per site and keyed hash of the /24 or /48, per node |
| (same keys) for `GET /v1/c` | 200 / 400 per site, 20 / 60 per site and prefix | the challenge route has its own buckets at the same rates. a page load takes one challenge and sends at least one event, so a visitor within the event limits is within the challenge limits too, and fetching challenges never uses up the event budget |
| `GATEWAY_MAX_BODY` | 8192 | event body bytes; larger answers 413 |
| `CLIENT_IP_HEADER` | `X-Edge-Client-Ip` | the one header the local caddy sets (`deploy/caddy/edge.Caddyfile`) |
| `GATEWAY_SECURE_TRANSPORT` | `true` | browsers reach the gateway over https and send fetch metadata |

Other settings: `GATEWAY_NODE_ID` (unique per instance), `GATEWAY_ADDR` (for caddy),
`GATEWAY_OPS_ADDR` (`/healthz`, `/readyz`, `/metrics`, private), `DBIP_MMDB` and
`TRACKER_RELEASE_DIR` (both baked into the image), and the `NATS_*` keys.

Fixed in code: publish acknowledgement wait 2 s (`PublishAckWait`), readiness canary
every 1 s with a maximum age of 7 s (`CanaryMaxAge`, so `/readyz` fails within 10 s
of a queue outage), 200,000 entries each for the replay table, the ip-cluster sets,
the visitor volume table and each rate limit table.

Each limit is per node. With three edge nodes, a site can send three times the
site rate, and a captured proof can be replayed three times the replay limit.

Acceptance criterion, met on 2026-11-12: no event of the this stage and this stage
browser suites was refused, rate limited or quarantined against the real gateway
(`TestBrowserMatrix` in `tests/gateway`).

| Date | Key | Change | Reason |
|---|---|---|---|
| 2026-11-12 | all | starting values | the design |
| 2026-11-13 | `VISITOR_EVENTS_PER_HOUR` | new, 600 | review: same-ip skepticism counts distinct visitor tokens only, so one address and one user agent could send at the prefix rate into the primary band; ten events a minute on average is far above a person's use of one site |
| 2026-11-13 | `PROOF_REPLAY_LIMIT` | counts queued events only | review: refused events used up a proof that a single-page app keeps for the whole tab (`TestRefusedEventsKeepTheProofAllowance` in `internal/gateway`) |
| 2026-11-13 | `RATE_SITE_*`, `RATE_PREFIX_*` | also applied to `GET /v1/c`, in separate buckets | this stage: the challenge route had no limit, so one client could request challenges without bound; checked after the origin and fetch metadata checks, so refused requests take no token (`TestChallengeRateLimits` in `tests/gateway`) |

## Responses

202 queued (only after the JetStream acknowledgement), 204 dropped for `Sec-GPC: 1`,
400 malformed (also a missing `User-Agent`), 403 origin, challenge, proof, replay or
fetch metadata, 404 unknown or inactive site, 410 revoked tracker version (the file,
and an event whose `sv` names a revoked release), 413 body too large, 429 rate limited (events and challenges), 503 queue unavailable, registry
not loaded or a missing client address header (events and challenges). Every response to a request with `Origin` carries
`Access-Control-Allow-Origin` with that origin and `Vary: Origin`. The tracker files
carry `Access-Control-Allow-Origin: *`, `Cross-Origin-Resource-Policy: cross-origin`
and `Cache-Control: public, max-age=31536000, immutable`; there is no `latest` alias.

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
