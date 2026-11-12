# API metrics capacity

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

## Re-measuring

Re-run the measurement after any change to the metric table shapes, and after
the this stage volume run (1,000,000 requests across 10 tenants and 30 services
within 60 minutes) with `jobs capacity`, and replace the tables above with the
new figures and their date.
