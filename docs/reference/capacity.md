---
title: "API metrics and web event capacity"
description: "Measured storage cost of API metric rows, extrapolated to the retention horizons, and the web event delivery rate."
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

**Why one replayer delivers for the whole cluster.** The replayer runs on every core node, but
only the holder of the `replayer` lock consumes `WEB_EVENTS`, through one durable consumer, and
it sends to the Umami processes of its own node (`UMAMI_URL` in
`infra/ansible/roles/core/templates/compose.yml.j2`). The single consumer is what keeps one
visitor's events in one place at a time, which three things rely on:

- **Order.** A visitor's events reach Umami in stream order: each visitor has one queue, one send
  at a time, and a failing event holds back only that visitor's later events.
- **Visit continuity.** The visitor's `visit_state` record (Umami's session id, the cache token of
  its last answer and the last acceptance time) is read before a send and written after it with a
  plain put (`internal/replayer/visit.go`). Two consumers handling the same visitor at once would
  overwrite each other's record, send a stale cache token or none, and split one visit into
  several.
- **The duplicate guard.** `sent.<event id>` is checked before a send and written after Umami's
  200. Two consumers that hold the same redelivered message at once would both pass the check
  and store the event twice.

Which Umami process stores an event does not matter: every process uses the one database and
`APP_SECRET`, and Umami derives the session and visit from them and the event alone
([umami-contract.md](umami-contract.md) item 14). So delivering to the Umami of the node that
consumes is safe; consuming one durable from several nodes at once is not. Delivery from every
node needs each visitor owned by exactly one node: the stream's subjects partitioned by visitor
(today the subject token is only the site id), one consumer and one lock per partition, a
predecessor wait per partition as the single lock has today, and a migration of the subject,
the NATS permissions, erasure's purge by subject and the DLQ replay. That design is not
implemented; until it is, the delivery rate is one node's.

**Measured locally, 2026-11-17** (`TestDrainRateByUmamiProcesses`, `internal/replayer`,
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

**What is not measured.** No end-to-end sustained rate of a production node has been measured:
the "about 420 events a second" of [availability.md](availability.md) is the 2026-11-13 drill's
280/s of one process scaled by its 1.5x for two, not a run. The production ingest figure of about
700 events a second per node was measured with proofs solved before the run, so it holds no
`GET /v1/c`; a real page load also fetches one challenge, which that figure does not include.

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
