-- api metrics queries for the jobs role (the design): rollups,
-- partitions and retention. every transaction that runs them also advances
-- the jobs fence through natsx.CheckFence. table shapes:
-- docs/reference/api-storage.md.

-- name: ClaimRollupHours :many
-- takes up to max_hours pending hours, oldest first. the marks are deleted
-- here, so the caller must recompute those hours (and their days) in the
-- same transaction; a rollback puts the marks back.
delete from api.rollup_pending p
where (p.tenant_id, p.hour) in (
  select r.tenant_id, r.hour
  from api.rollup_pending r
  order by r.hour, r.tenant_id
  limit @max_hours::int
  for update skip locked)
returning p.tenant_id, p.hour;

-- name: DeleteHourlyRows :execrows
-- first half of recomputing one tenant's hour: the old rows go, so a
-- dimension that has no minute rows any more does not survive
delete from api.metrics_hourly
where tenant_id = @tenant_id and bucket_start = sqlc.arg(hour)::timestamptz;

-- name: RollupHour :execrows
-- second half: one hourly row per dimension from the minute rows. sums of
-- floats are taken in time order, so a re-run gives the same bits.
-- metric and span rows stay apart because source is a dimension.
insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code,
                                source, counts, sum, count, lossy)
select m.tenant_id, sqlc.arg(hour)::timestamptz, m.service, m.method, m.route, m.status_code, m.source,
       api.sum_buckets(m.counts order by m.bucket_start), sum(m.sum order by m.bucket_start),
       sum(m.count)::bigint, bool_or(m.lossy)
from api.metrics_minute m
where m.tenant_id = @tenant_id
  and m.bucket_start >= sqlc.arg(hour)::timestamptz
  and m.bucket_start < sqlc.arg(hour)::timestamptz + interval '1 hour'
group by m.tenant_id, m.service, m.method, m.route, m.status_code, m.source;

-- name: DeleteDailyRows :execrows
delete from api.metrics_daily
where tenant_id = @tenant_id and bucket_start = sqlc.arg(day)::timestamptz;

-- name: RollupDay :execrows
-- one daily row per dimension from the hourly rows of the utc day starting
-- at day. run it after the hours of that day were recomputed.
insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code,
                               source, counts, sum, count, lossy)
select h.tenant_id, sqlc.arg(day)::timestamptz, h.service, h.method, h.route, h.status_code, h.source,
       api.sum_buckets(h.counts order by h.bucket_start), sum(h.sum order by h.bucket_start),
       sum(h.count)::bigint, bool_or(h.lossy)
from api.metrics_hourly h
where h.tenant_id = @tenant_id
  and h.bucket_start >= sqlc.arg(day)::timestamptz
  and h.bucket_start < sqlc.arg(day)::timestamptz + interval '1 day'
group by h.tenant_id, h.service, h.method, h.route, h.status_code, h.source;

-- name: CreateMinutePartition :one
-- false when the day's partition exists already
select api.create_minute_partition(@day::date)::boolean as created;

-- name: CreateHourlyPartition :one
-- month is the first day of the month; false when it exists already
select api.create_hourly_partition(@month::date)::boolean as created;

-- name: DropMinutePartition :one
-- false when the day has no partition
select api.drop_minute_partition(@day::date)::boolean as dropped;

-- name: DropHourlyPartition :one
select api.drop_hourly_partition(@month::date)::boolean as dropped;

-- name: ListMinutePartitions :many
-- the utc days that have a minute partition, oldest first
select d::date as day from api.minute_partitions() as d;

-- name: ListHourlyPartitions :many
-- the first days of the months that have an hourly partition, oldest first
select m::date as month from api.hourly_partitions() as m;

-- name: PruneIngestDedup :execrows
-- message ids applied before the cutoff. the caller passes a cutoff past
-- API_METRICS' 7-day max age (8 days), so no pruned id can arrive again.
delete from api.ingest_dedup where applied_at < @cutoff::timestamptz;

-- name: PruneSeriesState :execrows
-- streams idle since before the cutoff (7 days)
delete from api.series_state where last_seen_at < @cutoff::timestamptz;

-- name: PruneResourceSeen :execrows
-- resources idle since before the cutoff (7 days)
delete from api.resource_seen where last_seen_at < @cutoff::timestamptz;

-- name: PruneRouteRegistry :execrows
-- cap state of days before the given utc day; only the current day's cap
-- is enforced, so older days are kept just long enough for late data
delete from api.route_registry where day < @before::date;
