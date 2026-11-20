---
title: "API metrics and web event capacity"
description: "Measured storage cost of API metric rows, extrapolated to the retention horizons, and the web event delivery rate."
icon: "gauge-high"
---

What API metric rows cost in PostgreSQL, measured, and the extrapolation to the
retention horizons: minute rows for 30 days (`API_MINUTE_RETENTION_DAYS`), hourly
rows for 36 months (`API_HOURLY_RETENTION_MONTHS`), daily rows forever. The table
shapes are in [api-storage.md](api-storage.md). Gateway throughput is in
[gateway-limits.md](gateway-limits.md); how fast accepted web events reach Umami is in "Web events
end to end" below.

## How the numbers are measured

- Tool: `internal/jobs/apicapacity`. `Measure` sums
  `pg_total_relation_size` (heap, TOAST and every index, including the primary
  key the upserts use) over the leaf partitions that hold rows and divides by
  the live row count. Partitions created ahead and still empty are reported
  separately (`empty_partitions`, `empty_bytes`): they cost a fixed few pages
  each, not anything per row.
- Against a running database: `jobs capacity` prints the same figures as JSON,
  with only the `PLATFORM_DB_*` keys of the jobs role set. Run `vacuum` on the
  three tables first, or dead tuples left by the writer's upserts count as
  bytes. The volume run below uses this command after its queue drains.
- Test: `go test -v -run TestMeasure ./internal/jobs/apicapacity/` writes real
  rows into a fresh PostgreSQL 18.6 with the platform migrations, rolls them up
  with the real `api-rollup` task, vacuums, and logs the figures below.

## Measured (2026-11-12, PostgreSQL 18.6)

Workload of the test: 10 tenants, 30 services, 1,864 distinct series (method,
normalized route of about 40 bytes, status code, source; a quarter
span-derived), each reporting every minute for one hour, with 1 to 4 non-empty
histogram buckets per row.

| Table | Rows | Bytes (non-empty partitions) | Bytes per row |
|---|---|---|---|
| `api.metrics_minute` | 111,840 | 56,688,640 | 506.9 |
| `api.metrics_hourly` | 1,864 | 983,040 | 527.4 |
| `api.metrics_daily` | 1,864 | 983,040 | 527.4 |

Empty partitions created ahead: 15 minute partitions in 245,760 bytes (16 KiB
each), 5 hourly partitions in 81,920 bytes. The hourly and daily figures come
from fewer rows, so page rounding makes them coarser than the minute figure;
the three tables have the same row shape.

Where the bytes go, per row: the 15-bucket `bigint[]` alone is 144 bytes
(120 of data, 24 of array header), the tuple header 24, the other columns
about 110, and the primary key index, which repeats every dimension column,
most of the rest.

## Extrapolation

Steady state of the design sizing: 2,000 active series, each
reporting every minute, so 2,000 × 1,440 = 2.88 million minute rows per day,
48,000 hourly rows and 2,000 daily rows per day. A month is the mean Gregorian
month (30.44 days).

| Store | Rows held | Size |
|---|---|---|
| `metrics_minute`, 30 days | 86.4 million | 40.8 GiB |
| `metrics_hourly`, 36 months | 52.6 million | 25.8 GiB |
| `metrics_daily`, per year kept | 0.73 million | 0.36 GiB |
| Total after 3 years | | about 67.7 GiB (66.6 GiB rolling plus 1.1 GiB of daily rows) |

The totals scale linearly with the number of series that report in a minute,
not with the number of requests: a series writes one minute row whether it
served one request or a thousand. 1,000,000 requests per day average
about 700 per minute, which can touch at most 700 series in any minute, so 2,000
series reporting every minute is an upper bound for that volume unless request
counts are spread over far more routes, services and status codes. The daily
caps (300 routes and 50 services per tenant per UTC day) bound the worst case
per tenant (`METRICSWRITER_ROUTE_CAP` and `METRICSWRITER_SERVICE_CAP`); routes
and services beyond them are stored as `{other}`.

The 24-month floor of `API_HOURLY_RETENTION_MONTHS` would hold 35.1 million
hourly rows, 17.2 GiB at the same rate.

## Volume run (2026-11-12, PostgreSQL 18.6)

`make api-volume` (`tests/apivolume`, build tag `apivolume`): 1,000,000
requests across 10 tenants and 30 services (3 per tenant, 20 routes and 4
status codes each, 2,400 series), recorded by the pinned Go SDK over 20
minutes, exported every 10 s (half delta, half cumulative, HTTP and gRPC)
through the built `scf-otelcol`, stored by the built `metricswriter` (batch
256), rolled up with `jobs run api-rollup`, then vacuumed and measured with
`jobs capacity`. The host had 10 CPUs and other load: its load average was
19.3 at the start, up to 23.8 during the run and 6.1 at the end.

| Result | Value |
|---|---|
| Stored `sum(count)` | 1,000,000, equal to requests sent, per tenant and in total |
| `API_METRICS` messages | 3,615 applied, 0 duplicates, 0 dead-lettered, 0 refused; 123 batches, none failed |
| Writer lag | at most 30 messages; zero 3 s after the last export |
| Rollup | 2,400 hourly rows, equal to the minute rows in every dimension and bucket; 435 ms |
| `metrics_minute` | 50,397 rows, 23,560,192 bytes, **467.5 bytes per row** |
| `metrics_hourly`, `metrics_daily` | 2,400 rows each, 375.5 bytes per row (few rows, so page rounding dominates) |
| Writer state | `series_state` 1,200 rows 2.6 MiB, `ingest_dedup` 3,615 rows 0.97 MiB, `resource_seen` 15 rows, `route_registry` 600 rows |
| Database growth | 29,474,816 bytes (10.5 MB to 40.0 MB) |

Each minute row was updated about six times (one export every 10 s), so the
figure includes what the upserts leave behind after `vacuum`; it is below the
506.9 bytes of the synthetic measurement above. At 467.5 bytes per row, the
30 days of minute rows for 2,000 series in the extrapolation come to 37.6 GiB
instead of 40.8 GiB. The table above keeps the higher figure as the planning
bound.

## Re-measuring

Re-run the measurement and `make api-volume` after any change to the metric
table shapes, and replace the tables above with the new figures and their date.

## Web events end to end

The gateway's rate ([gateway-limits.md](gateway-limits.md), "Capacity") is what a node
**accepts** into `WEB_EVENTS`. What reaches Umami is a second, separate rate: the replayer's.

**How delivery grows with the nodes.** `WEB_EVENTS` is split into 32 partitions by a hash of
the site id and the visitor token, each with its own durable consumer and its own lock. The
replayer on every core node owns a share of the partitions (11, 11 and 10 on three nodes) and
sends them to the Umami processes of its own node (`UMAMI_URL` in
`infra/ansible/roles/core/templates/compose.yml.j2`), so every node's Umami processes store
events at once ([../architecture.md](../architecture.md), "Web event partitions"). One visitor's
events stay in one partition, which has one owner at a time, and three things rely on that:

- **Order.** A visitor's events reach Umami in stream order: each visitor has one queue, one send
  at a time, and a failing event holds back only that visitor's later events. A new owner of a
  partition waits until its predecessor's unacknowledged messages can be redelivered.
- **Visit continuity.** The visitor's `visit_state` record (Umami's session id, the cache token of
  its last answer and the last acceptance time) is read before a send and written after it with a
  plain put (`internal/replayer/visit.go`). One owner per visitor means no other process
  overwrites it in between.
- **The duplicate guard.** `sent.<event id>` is checked before a send and written after Umami's
  200. Only the partition's owner holds the event, so no second process passes the check at the
  same time. A send is never cancelled by a hand-over or a lost lock: it runs until Umami
  answers or the 15 s send timeout ends it, so every 200 is recorded before the message can
  reach another owner. Umami has no idempotency key, so two cases remain in which it can store
  an event twice: a replayer killed between Umami's 200 and the `sent` write (at most one event
  per delivery slot), and a send that ends without an answer (the 15 s timeout or a broken
  connection) after Umami stored it, which is retried because its outcome is unknown. A
  hand-over whose bookkeeping writes still fail after 35 s (10 s for new sends, 15 s for the
  last answers, 10 s for their records) hands such a message back without its `sent` key.

Which Umami process, and which node, stores an event does not matter: every process uses the one
database and `APP_SECRET`, and Umami derives the session and visit from them and the event alone
([umami-contract.md](umami-contract.md) item 14).

**Measured locally, 2026-11-17** (`TestDrainRateByReplayerNodes`, `internal/replayer`,
`REPLAYER_NODES_MEASURE`): a 3-node NATS cluster with the production topology at 3 replicas,
one PostgreSQL 18.6 container, and one Umami 3.5.0 process per replayer node capped at 1 core.
1, 2 or 3 replayers in the test process (128 workers and 900 in flight each, as the core role
sets) start on a queued backlog of page views of visitors with 6 events each; each run checks
that Umami holds every event exactly once. Every container and replayer ran on the same host,
which other work loaded at the same time; the load average on 10 cores is given per run.

| Backlog | Replayer nodes | Rate | Delivered per node | Umami CPU per process (cores) | Mean send | Host load (1 min) |
|---|---|---|---|---|---|---|
| 12,000 | 1 | 417/s | 12,000 | not recorded | not recorded | 9.9 to 11.4 |
| 12,000 | 2 | 523/s | 5,844, 6,156 | not recorded | not recorded | 11.4 to 11.7 |
| 12,000 | 3 | 637/s | 4,050, 4,182, 3,768 | not recorded | not recorded | 11.2 to 15.3 |
| 12,000 | 1 | 360/s | 12,000 | 0.98 | 349 ms | 14.9 to 15.9 |
| 12,000 | 2 | 586/s | 6,072, 5,928 | 0.81, 0.92 | 413 ms | 15.5 to 16.1 |
| 12,000 | 3 | 531/s | 4,002, 4,140, 3,858 | 0.51 to 0.61 | 608 ms | 16.1 to 16.0 |
| 30,000 | 1 | 238/s | 30,000 | 0.79 | 518 ms | 21.2 to 28.1 |
| 30,000 | 2 | 340/s | 15,126, 14,874 | 0.62, 0.68 | 688 ms | 27.2 to 35.3 |
| 30,000 | 3 | 447/s | 10,296, 10,416, 9,288 | 0.56 to 0.59 | 762 ms | 32.4 to 37.9 |

Reading:

- Each node delivers its own share: the partitions split the events within 10 % between the
  nodes in every run.
- With one node, its Umami process ran at or near its one-core cap (0.79 to 0.98). With two and
  three nodes the rate was 1.25 to 1.88 times one node's in the same series, and it rose from two
  to three nodes in two of the three series; the
  exception is the second 12,000 series, where three nodes drained 531/s against two nodes'
  586/s while the mean send rose to 608 ms. In the runs with two and three nodes no Umami process
  reached its cap, and in those that recorded it PostgreSQL used at most 0.27 cores, so on this host the limit was the time
  of each send under the shared CPU, not either service. On production nodes each replayer and
  its Umami processes have their own node's CPUs.
- The rates fall as the host load rises (417/s for one node at a load of 10, 238/s at 21 to 28),
  so they are figures of this host at that time, not of a production node.

Delivery to the Umami processes of one node, the step these node counts build on, was measured
on its own as well:

**One node, by Umami processes, measured locally, 2026-11-17** (`TestDrainRateByUmamiProcesses`, `internal/replayer`,
`REPLAYER_DRAIN_MEASURE=12000`): one NATS server, one PostgreSQL 18.6 container capped at 2 cores
(`REPLAYER_DRAIN_PG_CPUS=2`, the vCPUs of a production node), Umami 3.5.0 containers capped at 1
core each, one replayer in the test process. 12,000 page views of 2,000 visitors are queued, then
drained; each run checks that Umami holds all 12,000. The host was shared with other work, at a
load average of 30 to 46 on 10 cores during the runs, so these rates are lower bounds of the
software, not a node's figure.

| Umami processes | Replayer workers / in flight | Rate | Umami CPU per process (cores) | PostgreSQL CPU per event | Mean send |
|---|---|---|---|---|---|
| 2 | 128 / 900 | 425/s | 0.81, 0.78 | 0.51 ms | |
| 4 | 128 / 900 | 424/s | 0.40 to 0.52 | 0.52 ms | |
| 6 | 128 / 900 | 515/s | 0.31 to 0.44 | 0.55 ms | |
| 2 | 512 / 1,800 | 282/s | 0.58, 0.53 | 0.55 ms | 877 ms |
| 6 | 512 / 1,800 | 696/s | 0.41 to 0.59 | 0.49 ms | 325 ms |

Reading:

- No Umami process reached its one-core cap and PostgreSQL used at most 0.34 of its 2 cores, so
  on this host the bound was the time of each send under contention (325 to 877 ms), not either
  service's CPU. With more sends in flight, 6 processes drained 696/s.
- PostgreSQL's cost per stored event was steady at 0.49 to 0.55 ms of CPU in every run, whatever
  the contention.
- A `WEB_EVENTS` message of these page views takes **690 bytes** of the store (8,280,000 bytes for
  12,000 messages).

**Measured on production, 2026-11-18** (release `v0.4.0`, three `Standard_B2as_v2` nodes, the
replayer lock on `production-node-3`, its two Umami processes delivering, the Patroni leader on
`production-node-2`). A load client built on `tests/gateway/gwclient` ran on an operator
workstation inside `admin_cidrs`, 58 ms from the region, and sent page views of the internal probe
site through each node's Caddy (`in.veristar.app` dialled per node), with production's gateway
limits unchanged. **Challenges are included**: each page load fetched one `GET /v1/c`, solved it
and sent six page views with it, as the tracker does. The load came from one address, so the
per-site and prefix limits (20 a second per node, [gateway-limits.md](gateway-limits.md)) cap it
near 60 a second, and the same-network threshold put the visitors after the twentieth of the hour
in the quarantine band, which Umami stores the same way. A sampler read the `WEB_EVENTS` consumer
of the replayer every 2 s from its NATS leader, and afterwards every acknowledged path was looked
up in Umami's `website_event`.

| Run | Sent | Answered 202 | Stored in Umami | Consumer backlog | Send to stored |
|---|---|---|---|---|---|
| 1 client, 1,500 visitors, 1,500 challenges | 9,000 in 199.6 s, **45.1/s** | 9,000 | 9,000 (114 primary, 8,886 quarantine), 1,500 sessions | `num_pending` 0 in all 141 samples, at most 5 unacknowledged | every event in the second it was sent (`created_at` has 1 s resolution), at most 0.3 s later |
| 3 clients (one per node), 500 visitors, 1,500 challenges | 9,000 in 162.0 s, **55.5/s** | 9,000 | 9,000 (all quarantine), 500 sessions, each visitor one session across the three nodes | `num_pending` 0 in all 112 samples, at most 10 unacknowledged | every event in the second it was sent |

Client latency of `POST /v1/e` was p50 55 ms and p99 61 to 66 ms including the 58 ms round trip.
On the delivering node, `docker stats` averaged 10 to 11 % of a core per Umami process, 6 % for
the replayer and 6 % for its PostgreSQL replica during run 1; `top` showed a median of 59 % idle.

**Result: the cluster delivers 55.5 web events a second end to end, challenges included, with no
backlog**, above the expected peak of 50 a second. This is a sustained rate at the level one
address can offer, not the ceiling: the queue never held an event, so the delivery rate the
replayer reaches under a backlog was not reached. Measuring that ceiling needs a backlog, either
the replayers stopped while events queue and then started again, or the gateway limits raised for
load from several addresses as in [gateway-limits.md](gateway-limits.md), "Capacity". Release
`0.4.0` does not include partitioned replay: in this run one replayer delivered for the whole
cluster to the Umami processes of its own node.

**Partitioned delivery measured on production, 2026-11-20** (release `v0.4.7`, three
`Standard_B2as_v2` nodes, two Umami processes per node, 128 replayer workers and 900 in flight
per node, the Patroni leader on `production-node-1`). The same load client and Umami lookup as on
2026-11-18, against the internal probe site with production's gateway limits unchanged and
challenges included. Each round:

1. `docker compose stop replayer` on every core node.
2. Three clients, one per node's Caddy, each sent 8,000 page views at 19 a second (1,334 page
   loads of 6 page views, one challenge each, one visitor per page load), so 24,000 queued in
   `WEB_EVENTS` in 421 s. Every event was answered 202.
3. `docker compose start replayer` on the round's nodes at once. A sampler on `production-node-1`
   read the `WEB_EVENTS` stream and its 32 partition consumers from their NATS leaders about every
   1.1 s until the stream held no message and no consumer had one pending or unacknowledged. The
   delivered count is the backlog at the start plus what arrived during the drain (live traffic
   and the prober, 15 to 18 events).
4. The replayers of the other nodes started, and ownership returned to 11, 10 and 11.

`docker stats` sampled every node about every 5 s; the CPU figures are averages over the drain
from 15 s after the start. The mean send is `replayer_send_duration_seconds` for answered sends
over the drain. Every acknowledged path was then looked up in Umami's `website_event`.

| Replayer nodes | Backlog | Start to empty | Rate | Rate once every partition was consuming | Delivered per node | Umami CPU per process (cores) | Leader PostgreSQL CPU (cores) | Mean send |
|---|---|---|---|---|---|---|---|---|
| 1 (`node-2`) | 24,064 | 143.0 s | **168.4/s** | 171.3/s | 24,084 | 0.49, 0.46 | 0.24 | 662 ms |
| 2 (`node-2`, `node-3`) | 24,045 | 118.4 s | **203.3/s** | 258.3/s (the last 15,228, from 60.2 s) | 13,522, 10,547 | 0.32, 0.32; 0.27, 0.30 | 0.26 | 653, 609 ms |
| 3 | 24,057 | 114.7 s | **209.9/s** | 269.6/s (the last 15,173, from 59.5 s) | 7,084, 6,627, 10,362 | 0.20 to 0.24 | 0.33 | 954, 813, 693 ms |

The delivered counts are `replayer_outcomes_total{outcome="delivered"}` of each replayer since its
start, which includes the live traffic of those minutes.

| Round | Answered 202 | Stored in Umami | Missing | Stored twice | Dead-lettered |
|---|---|---|---|---|---|
| 1 node | 24,000 | 24,000 (112 primary, 23,888 quarantine), 4,005 sessions | 0 | 0 | 0 |
| 2 nodes | 24,000 | 24,000 (all quarantine), 4,002 sessions | 0 | 0 | 0 |
| 3 nodes | 24,000 | 24,000 (all quarantine), 4,002 sessions | 0 | 0 | 0 |

Reading:

- **One node delivers about 170 events a second** from a backlog, at a mean send of 662 ms with its
  two Umami processes at about half a core each.
- **Hand-over at a cold start.** The replayer that starts first sees only itself in its first
  member list and takes all 32 partitions; the others get their share after the 10 s settle and
  the 15 s minimum hold, and wait up to the 30 s ack wait for the messages handed back. In the
  rounds with two and three nodes the last partition began consuming 60.2 s and 59.5 s after the
  start, and the first node delivered alone until then (122 to 188 a second in the 30 s windows
  of that time). Every hand-over finished the sends in flight before handing back the rest
  (16 and 21 partitions), and no event was stored twice.
- **Once every partition had its owner**, two nodes delivered 258 a second and three 270 a second,
  1.5 and 1.6 times one node. The mean send rose from 662 ms with one node to 954 ms on the
  leader's node with three, while no Umami process averaged more than half a core and the
  leader's PostgreSQL 0.33 cores. Every stored event goes through the one Patroni leader, which
  is the part all nodes share.
- **Against the expected peak of 50 a second**, one node delivers 3.4 times it and the cluster 4.2
  times it from a cold start, 5.4 times it once all three consume. A one-hour backlog at the
  expected peak (180,000 events) drains in about 14 minutes at 270 a second while 50 a second
  keep arriving (180,000 / 220 a second = 818 s).
- During each round the stopped replayers made `ReplayerNotConsuming` and `ServiceDown` page, and
  both resolved when the replayers started; `ReplayerLagHigh`, `WebEventsBacklogGrowing` and
  `ReplayerPartitionsUnowned` stayed pending. The status page stayed green: web freshness allows
  10 minutes.

**What is not measured.** Ingest and delivery at the same time above 60 events a second on
production: in these rounds the backlog was built with the replayers stopped, so delivery did
not share the nodes' CPUs with ingest from several addresses. A backlog larger than 24,000
events. More Umami processes, workers or sends in flight per node on production, the levers
[../runbooks/capacity.md](../runbooks/capacity.md) describes. The production ingest figure of
about 700 events a second per node was measured with proofs solved before the run, so it holds
no `GET /v1/c`.

**What a backlog costs.** Above the delivery rate, events wait in `WEB_EVENTS`:

- At 72 hours (max age, discard old) the stream drops the oldest events without a trace.
  `WebEventsNearDiscard` pages at 48 hours ([../runbooks/alerts.md](../runbooks/alerts.md)).
- Before that, the backlog can fill the store. Every NATS server keeps a replica in its file store
  (`edge_nats_max_file_store`, 20G per server), shared with every other stream, and a full store
  refuses new messages on every stream, so ingestion answers 503. At the measured 690 bytes per
  message, 20 GB holds about 29 million such events; a backlog that grows by more than about 110
  events a second fills it before the 72 hours run out (29 million / 259,200 s). Both are
  arithmetic on the measured message size, not a run, and real events with a title or event data
  are larger.
