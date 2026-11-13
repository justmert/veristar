---
title: "API metrics capacity"
description: "Measured storage cost of API metric rows, extrapolated to the retention horizons."
---

this stage measurement of what API metric rows cost in PostgreSQL, and the
extrapolation to the retention horizons: minute rows for 30 days
(`API_MINUTE_RETENTION_DAYS`), hourly rows for 36 months
(`API_HOURLY_RETENTION_MONTHS`), daily rows forever. Table shapes are in
`docs/reference/api-storage.md`.

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
  bytes. The this stage volume run uses this command after its queue drains.
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

Steady state from the plan's sizing (this stage): 2,000 active series, each
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
served one request or a thousand. The RFP's 1,000,000 requests per day average
about 700 per minute, which can touch at most 700 series in any minute, so 2,000
series reporting every minute is an upper bound for that volume unless request
counts are spread over far more routes, services and status codes. The daily
caps (300 routes and 50 services per tenant per UTC day) bound the worst case
per tenant.

The 24-month floor of `API_HOURLY_RETENTION_MONTHS` would hold 35.1 million
hourly rows, 17.2 GiB at the same rate.

## Volume run (2026-11-12, PostgreSQL 18.6)

`make api-volume` (`tests/apivolume`, build tag `apivolume`): 1,000,000
requests across 10 tenants and 30 services (3 per tenant, 20 routes and 4
status codes each, 2,400 series), recorded by the pinned Go SDK over 20
minutes, exported every 10 s (half delta, half cumulative, HTTP and gRPC)
through the built `scf-otelcol`, stored by the built `metricswriter` (batch
256), rolled up with `jobs run api-rollup`, then vacuumed and measured with
`jobs capacity`. The host is shared: its load average was 19.3 at the start,
up to 23.8 during the run and 6.1 at the end, on 10 CPUs.

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

Re-run the measurement after any change to the metric table shapes, and after
the this stage volume run (1,000,000 requests across 10 tenants and 30 services
within 60 minutes) with `jobs capacity`, and replace the tables above with the
new figures and their date.
