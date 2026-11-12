# API metrics storage (schema `api`)

this stage reference for the tables, functions, rights and queries that hold
API latency metrics in the `platform` database. Migrations
`db/migrations/00015_api_metrics.sql` (tables, histogram functions) and
`00016_api_security.sql` (row-level security, rights, partition functions,
initial partitions). Queries: `db/queries/writer/api.sql` (package
`internal/db/writerq`) and `db/queries/jobs/api.sql` (package
`internal/db/jobsq`); regenerate with `make sqlc`. The histogram layout and
its Go side live in `internal/histo`.

## Histogram contract

- Canonical bounds in seconds: `0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25,
  0.5, 0.75, 1, 2.5, 5, 7.5, 10` (`histo.Bounds`). 15 buckets, stored as
  `bigint[15]`. Bucket 1 (SQL, 1-based) is `[0, 0.005]`, bucket *i* is
  `(bound[i-1], bound[i]]`, bucket 15 is the open bucket above 10 s.
- `histo.Rebucket(bounds, counts)` moves an OTLP explicit-bucket histogram
  onto the layout: each source bucket goes whole to the canonical bucket that
  contains its upper bound. `exact` is true only when every canonical bound
  is also a source bound (the canonical layout or a finer one that contains
  it). Otherwise the row is stored with `lossy = true` and the writer counts
  a `lossy_histogram` warning. Re-bucketing is linear, so the delta of two
  re-bucketed cumulative points equals the re-bucketed delta.
- `api.histogram_quantile(q, counts)` and `histo.Quantile(q, counts)`: linear
  interpolation inside the bucket that holds rank `q * total`; the first
  non-empty bucket that reaches the rank wins, so `q = 0` gives the lower
  bound of the first bucket with data. Open bucket: SQL returns
  `'Infinity'`, Go returns `+Inf` (`histo.IsOpen`); show it as `>10 s`,
  never as a number. Empty histogram: SQL `null`, Go `histo.ErrEmpty`.
  Errors: q outside [0, 1] or NaN (22023), not 15 buckets (2202E), a
  negative or null bucket (22023), total above int64 (22003). The two are
  tested to agree bit for bit on random histograms (`internal/histo`).
- `api.add_buckets(a, b)`: element-wise sum; different shapes (2202E), null
  elements (22004) and bigint overflow (22003) are errors.
- `api.sum_buckets(bigint[])`: aggregate over `add_buckets`, null inputs are
  skipped. Percentiles over a range are
  `api.histogram_quantile(q, api.sum_buckets(counts))`; percentiles are never
  averaged.

## Metric tables

`api.metrics_minute` (partitioned by day), `api.metrics_hourly`
(partitioned by month), `api.metrics_daily` (not partitioned, kept forever)
have the same shape:

| Column | Type | Rule |
|---|---|---|
| `tenant_id` | uuid | from the `scf.tenant.id` stamped by the collector |
| `bucket_start` | timestamptz | start of the UTC minute, hour or day (checked) |
| `service` | text | `service.name`, `''` when absent, at most 128 bytes |
| `method` | text | `http.request.method`, `''` when absent, at most 16 bytes |
| `route` | text | `http.route`, `{other}` above the cap, `''` when absent, at most 1024 bytes |
| `status_code` | smallint | `http.response.status_code`, `0` when absent, else 100 to 599 |
| `source` | text | `metric` (`http.server.request.duration`) or `span` (`scf.http.server.request.duration.from_spans`) |
| `counts` | bigint[] | 15 canonical buckets, none null or negative |
| `sum` | double precision | sum of observed values in seconds |
| `count` | bigint | must equal the sum of `counts` (checked) |
| `lossy` | boolean | some contribution was re-bucketed inexactly |

Primary key: `(tenant_id, bucket_start, service, method, route,
status_code, source)`. Absent attributes use `''` and `0` instead of null so
that the key stays unique. `error.type` is not a dimension. Metric and span
rows are never merged; queries choose per tenant and service.

Span-derived points carry the collector's receive time and no start time
(`docs/reference/collector.md`, "For this stage"); bucket them by that time.

## State and bookkeeping tables

| Table | Key | Columns | Written by | Removed by |
|---|---|---|---|---|
| `api.series_state` | `(tenant_id, series_hash, start_unix_nano)` | `last_unix_nano`, `last_count`, `last_sum`, `last_counts bigint[15]` (canonical), `last_seen_at` | writer | retention, `last_seen_at` older than 7 days |
| `api.resource_seen` | `(tenant_id, resource_hash, start_unix_nano)` | `first_seen_at`, `last_seen_at` | writer | retention, `last_seen_at` older than 7 days |
| `api.ingest_dedup` | `msg_id` (the `Nats-Msg-Id`, 1 to 256 bytes) | `tenant_id` (from the subject), `applied_at` | writer | retention, `applied_at` older than 8 days |
| `api.route_registry` | `(tenant_id, day, service, route)` | `created_at` | writer | retention, days before a cutoff |
| `api.ingest_warnings` | `(tenant_id, day, kind)` | `count > 0`; kinds `cardinality_overflow`, `lossy_histogram`, `secret_in_path`, `span_derived` | writer | kept |
| `api.rollup_pending` | `(tenant_id, hour)` | `marked_at` | writer | rollup, when it claims the hour |

`series_hash` and `resource_hash` are 32 bytes (SHA-256). Start and point
times are unix nanoseconds (`bigint`), because timestamptz keeps only
microseconds and two replicas started within the same microsecond would
merge. `series_hash` is the caller's hash of tenant, resource hash, metric
name and attributes; the start time is a separate key column, so replicas
with equal attributes and different start times are separate streams, and
`GetSeriesStates` returns every start time of a series.

`api.rollup_pending` is how the rollup finds "every hour touched since the
last run": the writer marks the UTC hour of each minute row in the same
transaction (`MarkRollupPending`), the rollup deletes the marks it takes
(`ClaimRollupHours`) and recomputes those hours and their days in the same
transaction. A mark the writer adds while the rollup runs either waits for
the rollup's commit on the row lock and survives it, or is taken by it, so
no touched hour is missed.

## Partitions

- Names: `api.metrics_minute_pYYYYMMDD` (one UTC day), `api.metrics_hourly_pYYYYMM`
  (one UTC month).
- Created and dropped only through security definer functions owned by
  `platform_owner`, executable by `jobs`: `api.create_minute_partition(day)`,
  `api.create_hourly_partition(month)` (first day of the month),
  `api.drop_minute_partition(day)`, `api.drop_hourly_partition(month)` (each
  returns false when there was nothing to do), `api.minute_partitions()` and
  `api.hourly_partitions()` (days and months that exist, read from the
  partition bounds). Only the operator of a partitioned table may add or remove
  partitions, which is why these are functions.
- Every partition gets forced row-level security and the same `cp_tenant`
  policy as the parent (`internal/db.CheckPolicyCoverage` checks
  partitions), and no right for any application role. All access goes
  through the parent, where the parent's rights and policies apply.
- The migration creates minute partitions from 7 days back to 7 days ahead
  and hourly partitions from the month 31 days back to 3 months ahead, both
  in UTC at migration time. The partition job keeps that window.
- No default partition. A row outside every partition fails with SQLSTATE
  23514 ("no partition of relation ... found for row"); the writer must
  treat that as a permanent error for the point (time far outside the
  window), not retry it forever.
- Dropping a partition takes an access exclusive lock on the parent for a
  moment, so the retention job runs it in its own short transaction.
- 24-month minimum for hourly data: enforced by the retention job's
  configuration (`API_HOURLY_RETENTION_MONTHS`), not by the drop function.

## Rights

| Table | cp_tenant | cp_operator | metrics_writer | jobs |
|---|---|---|---|---|
| `metrics_minute` | select (own rows) | none | select, insert, update | select, insert, update, delete |
| `metrics_hourly`, `metrics_daily` | select (own rows) | select (all rows) | none | select, insert, update, delete |
| `ingest_warnings` | select (own rows) | none | select, insert, update | select, insert, update, delete |
| `series_state`, `resource_seen`, `ingest_dedup`, `route_registry` | none | none | select, insert, update | select, insert, update, delete |
| `rollup_pending` | select (own rows, 00021: hourly and daily answers read pending hours from minutes) | none | select, insert, update | select, insert, update, delete |

Every table has forced row-level security with exactly one `cp_tenant`
policy keyed on `cp.current_tenant_id()`; `cp_operator` has a select policy
on hourly and daily only; `metrics_writer` and `jobs` have policies over all
rows of the tables they use. `cp_system` has no table right in `api`; it has
schema usage only to call `api.purge_tenant` (migration 00017). Functions:
`add_buckets` for all four roles; `sum_buckets` and `histogram_quantile` for
`cp_tenant`, `cp_operator`, `jobs`; the partition functions for `jobs` only.

## Queries

`writerq` (metrics_writer; run with `db.WithRole(ctx, db.RoleMetricsWriter,
...)` after `natsx.CheckFence`):

| Query | Kind | Purpose |
|---|---|---|
| `RecordIngestMessage` | execrows | dedup by `Nats-Msg-Id`; 0 rows = already applied, skip the message |
| `GetSeriesStates` | many | known streams of a tenant's series hashes, all start times |
| `UpsertSeriesState` | batchexec | store the last cumulative point |
| `GetResourcesSeen` | many | resources seen before, for the first-point rule |
| `TouchResource` | batchexec | record a resource (keeps `first_seen_at`) |
| `AddMinute` | batchexec | add a delta histogram to its minute row (`api.add_buckets`) |
| `MarkRollupPending` | batchexec | mark the UTC hour of a minute for the rollup |
| `ListRoutes` | many | routes admitted for a tenant and day (cap state) |
| `RegisterRoute` | execrows | admit a route; 0 rows = admitted before |
| `AddIngestWarning` | exec | add to a warning counter |

`jobsq` (jobs; run with `db.WithRole(ctx, db.RoleJobs, ...)` after
`natsx.CheckFence`):

| Query | Kind | Purpose |
|---|---|---|
| `ClaimRollupHours` | many | delete and return up to N pending hours, oldest first (`skip locked`) |
| `DeleteHourlyRows` + `RollupHour` | execrows | recompute one tenant's hour from minutes |
| `DeleteDailyRows` + `RollupDay` | execrows | recompute one tenant's UTC day from hours |
| `CreateMinutePartition`, `CreateHourlyPartition` | one | create ahead; false when present |
| `DropMinutePartition`, `DropHourlyPartition` | one | drop expired; false when absent |
| `ListMinutePartitions`, `ListHourlyPartitions` | many | existing days and months |
| `PruneIngestDedup`, `PruneSeriesState`, `PruneResourceSeen` | execrows | rows older or idle before a cutoff |
| `PruneRouteRegistry` | execrows | cap state of days before a given day |

The rollup sums floats in time order (`sum(sum order by bucket_start)`), so
running it twice over the same minutes gives the same bits. Delete and
insert are two statements because one statement cannot both delete a row and
insert a row with the same key.

## Jobs tasks

| Task | Every | Package | Does |
|---|---|---|---|
| `api-rollup` | 5 min | `internal/jobs/apirollup` | claims up to 500 pending hours per transaction until none is left; recomputes each hour, then each day containing one. Refuses (and keeps the marks) when a pending hour's minute partition is gone, because replacing its hourly rows would erase them |
| `api-partition` | 1 h | `internal/jobs/apipartition` | creates minute partitions for today and 7 days ahead, hourly partitions for this month and 3 ahead |
| `api-retention` | 1 h | `internal/jobs/apiretention` | drops minute partitions of days before today minus `API_MINUTE_RETENTION_DAYS` and hourly partitions of months before this month minus `API_HOURLY_RETENTION_MONTHS` (each drop its own transaction); an expired day with hours still in `rollup_pending` waits for the rollup (`RollupPendingInDay`, `db/queries/jobs/apiretention.sql`). Prunes `ingest_dedup` after 8 days, `series_state` and `resource_seen` after 7 idle days, `route_registry` days older than 8 days (a backlog can deliver points up to 7 days old) |

Both retention keys are required. `API_HOURLY_RETENTION_MONTHS` below 24 is
refused at startup by `config.LoadAPIRetention` and by `apiretention.New`;
`API_MINUTE_RETENTION_DAYS` must be 2 or more. `jobs capacity` measures the
tables (`docs/reference/capacity.md`).

## Tenant purge

`00017_api_purge.sql` adds `api.purge_tenant(tenant, keep_aggregates)`, a
security definer function and the only api right of `cp_system`. The
lifecycle purger `api` (`lifecycle.APIPurger`) calls it in the reconciler's
cp_system transaction (`PurgeAPITenant` in `db/queries/system/api_purge.sql`).
It refuses a tenant whose `cp.tenants.status` is not `removed`. It always
deletes `metrics_minute`, `series_state`, `resource_seen`, `route_registry`
and `rollup_pending` rows; `metrics_hourly`, `metrics_daily` and
`ingest_warnings` unless `keep_aggregates`. `ingest_dedup` rows stay until
retention removes them, so a message still queued on `API_METRICS` cannot be
applied again. Because every api table forces row-level security, the
function's owner has delete and select policies on those tables and a select
policy on `cp.tenants` limited to removed tenants.
