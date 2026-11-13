-- api metrics queries for metrics_writer (the design). every transaction
-- that runs them also advances the metricswriter fence through
-- natsx.CheckFence. table shapes: docs/reference/api-storage.md.

-- name: RecordIngestMessage :execrows
-- one row per applied API_METRICS message. zero rows means the message was
-- applied before (a redelivery), so nothing of it may be applied again.
insert into api.ingest_dedup (msg_id, tenant_id, applied_at)
values (@msg_id, @tenant_id, @applied_at::timestamptz)
on conflict (msg_id) do nothing;

-- name: GetSeriesStates :many
-- every known stream of the given series of one tenant, whatever its start
-- time, so the caller can tell a known stream from a new start time
select tenant_id, series_hash, start_unix_nano, last_unix_nano, last_count, last_sum, last_counts, last_seen_at, baseline
from api.series_state
where tenant_id = @tenant_id and series_hash = any(@series_hashes::bytea[]);

-- name: UpsertSeriesState :batchexec
-- stores the last cumulative point of a stream. last_counts are on the
-- canonical layout (15 buckets). baseline is true while the last point is
-- the baseline of a stream first met after the first-point threshold.
insert into api.series_state (tenant_id, series_hash, start_unix_nano, last_unix_nano,
                              last_count, last_sum, last_counts, last_seen_at, baseline)
values (@tenant_id, @series_hash, @start_unix_nano, @last_unix_nano,
        @last_count, @last_sum, @last_counts::bigint[], @seen_at::timestamptz, @baseline)
on conflict (tenant_id, series_hash, start_unix_nano) do update
  set last_unix_nano = excluded.last_unix_nano,
      last_count = excluded.last_count,
      last_sum = excluded.last_sum,
      last_counts = excluded.last_counts,
      last_seen_at = excluded.last_seen_at,
      baseline = excluded.baseline;

-- name: CountSeriesStates :one
-- the streams the writer follows for a tenant, for the per-tenant stream
-- cap
select count(*)::bigint as n from api.series_state where tenant_id = @tenant_id;

-- name: GetResourcesSeen :many
-- the resources of one tenant the writer has followed before, for the
-- first-point rule
select resource_hash, start_unix_nano, first_seen_at, last_seen_at
from api.resource_seen
where tenant_id = @tenant_id and resource_hash = any(@resource_hashes::bytea[]);

-- name: TouchResource :batchexec
-- records that a resource was seen; first_seen_at stays as first written
insert into api.resource_seen (tenant_id, resource_hash, start_unix_nano, first_seen_at, last_seen_at)
values (@tenant_id, @resource_hash, @start_unix_nano, @seen_at::timestamptz, @seen_at::timestamptz)
on conflict (tenant_id, resource_hash, start_unix_nano) do update
  set last_seen_at = greatest(api.resource_seen.last_seen_at, excluded.last_seen_at);

-- name: AddMinute :batchexec
-- adds one delta histogram to its minute row. bucket_start must be the start
-- of a utc minute and counts must have 15 buckets that sum to count; the
-- table's checks refuse anything else. a minute with no partition fails with
-- sqlstate 23514 (no partition of relation found for row).
insert into api.metrics_minute (tenant_id, bucket_start, service, method, route, status_code,
                                source, counts, sum, count, lossy)
values (@tenant_id, @bucket_start::timestamptz, @service, @method, @route, @status_code,
        @source, @counts::bigint[], @sum, @count, @lossy)
on conflict (tenant_id, bucket_start, service, method, route, status_code, source) do update
  set counts = api.add_buckets(api.metrics_minute.counts, excluded.counts),
      sum = api.metrics_minute.sum + excluded.sum,
      count = api.metrics_minute.count + excluded.count,
      lossy = api.metrics_minute.lossy or excluded.lossy;

-- name: LockRollupHour :exec
-- takes the shared lock of a (tenant, utc hour) for this transaction. the
-- writer takes it before it writes minute rows into the hour; the rollup
-- skips an hour whose lock it cannot take exclusively, so it never
-- recomputes an hour from minutes a writer has not committed yet (audit
-- A-2). writers share the lock, so they never wait for each other.
select pg_advisory_xact_lock_shared(api.rollup_lock_key(@tenant_id, @hour::timestamptz));

-- name: MarkRollupPending :batchexec
-- marks the utc hour of a written minute for the rollup. send it in the same
-- transaction as the minute rows of that hour.
insert into api.rollup_pending (tenant_id, hour)
values (@tenant_id, date_trunc('hour', @bucket_start::timestamptz, 'UTC'))
on conflict (tenant_id, hour) do nothing;

-- name: ListRoutes :many
-- the routes a tenant has admitted on one utc day, to load the cap state
select service, route
from api.route_registry
where tenant_id = @tenant_id and day = @day::date
order by service, route;

-- name: RegisterRoute :execrows
-- admits a route for the day. zero rows means it was admitted before.
insert into api.route_registry (tenant_id, day, service, route)
values (@tenant_id, @day::date, @service, @route)
on conflict (tenant_id, day, service, route) do nothing;

-- name: AddIngestWarning :exec
-- counts warnings of one kind (cardinality_overflow, lossy_histogram,
-- secret_in_path, span_derived) for a tenant and utc day
insert into api.ingest_warnings (tenant_id, day, kind, count)
values (@tenant_id, @day::date, @kind, @count)
on conflict (tenant_id, day, kind) do update
  set count = api.ingest_warnings.count + excluded.count;
