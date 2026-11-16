-- api metrics queries for the jobs role: rollups,
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

-- name: TryLockRollupHour :one
-- takes the exclusive lock of a claimed (tenant, utc hour) for this
-- transaction without waiting. false means a writer transaction that writes
-- minute rows into the hour is still open; the caller puts the mark back and
-- leaves the hour to a later run.
select pg_try_advisory_xact_lock(api.rollup_lock_key(@tenant_id, @hour::timestamptz))::boolean as locked;

-- name: RestoreRollupMark :exec
-- puts back a claimed mark whose hour was not recomputed
insert into api.rollup_pending (tenant_id, hour) values (@tenant_id, @hour::timestamptz)
on conflict (tenant_id, hour) do nothing;

-- name: MarkDriftedHours :many
-- the consistency check behind the rollup: every (tenant, utc
-- hour) between from and to that has minute rows, whose hourly rows
-- disagree with them on the request count, and that no mark covers, is
-- marked again so the next batch recomputes it. hours without minute rows
-- are never marked: a purge that keeps aggregates deletes the minutes on
-- purpose, and recomputing would erase the hourly rows it kept. it returns
-- what it marked; any row means a mark was lost, which the rollup reports.
insert into api.rollup_pending (tenant_id, hour)
select m.tenant_id, m.hour
from (
  select mm.tenant_id, date_trunc('hour', mm.bucket_start, 'UTC') as hour, sum(mm.count)::bigint as n
  from api.metrics_minute mm
  where mm.bucket_start >= sqlc.arg(from_hour)::timestamptz and mm.bucket_start < sqlc.arg(to_hour)::timestamptz
  group by 1, 2
) m
left join (
  select hh.tenant_id, hh.bucket_start as hour, sum(hh.count)::bigint as n
  from api.metrics_hourly hh
  where hh.bucket_start >= sqlc.arg(from_hour)::timestamptz and hh.bucket_start < sqlc.arg(to_hour)::timestamptz
  group by 1, 2
) h on h.tenant_id = m.tenant_id and h.hour = m.hour
where m.n is distinct from h.n
  and not exists (select 1 from api.rollup_pending p where p.tenant_id = m.tenant_id and p.hour = m.hour)
on conflict (tenant_id, hour) do nothing
returning tenant_id, hour;

-- name: DeleteHourlyRows :execrows
-- first half of recomputing one tenant's hour: the old rows go, so a
-- dimension that has no minute rows any more does not survive
delete from api.metrics_hourly
where tenant_id = @tenant_id and bucket_start = sqlc.arg(hour)::timestamptz;

-- name: RollupHour :execrows
-- second half: one hourly row per dimension from the minute rows. sums of
-- floats are taken in time order, so a re-run gives the same bits.
-- metric and span rows stay apart because source is a dimension, and
-- application and infrastructure rows because traffic_class is one.
insert into api.metrics_hourly (tenant_id, bucket_start, service, method, route, status_code,
                                source, traffic_class, counts, sum, count, lossy)
select m.tenant_id, sqlc.arg(hour)::timestamptz, m.service, m.method, m.route, m.status_code, m.source,
       m.traffic_class,
       api.sum_buckets(m.counts order by m.bucket_start), sum(m.sum order by m.bucket_start),
       sum(m.count)::bigint, bool_or(m.lossy)
from api.metrics_minute m
where m.tenant_id = @tenant_id
  and m.bucket_start >= sqlc.arg(hour)::timestamptz
  and m.bucket_start < sqlc.arg(hour)::timestamptz + interval '1 hour'
group by m.tenant_id, m.service, m.method, m.route, m.status_code, m.source, m.traffic_class;

-- name: DeleteDailyRows :execrows
delete from api.metrics_daily
where tenant_id = @tenant_id and bucket_start = sqlc.arg(day)::timestamptz;

-- name: RollupDay :execrows
-- one daily row per dimension from the hourly rows of the utc day starting
-- at day. run it after the hours of that day were recomputed. the day is 24
-- hours, not interval '1 day': on timestamptz that is calendar arithmetic in
-- the session time zone and gives 23 or 25 hours on a daylight saving day.
insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code,
                               source, traffic_class, counts, sum, count, lossy)
select h.tenant_id, sqlc.arg(day)::timestamptz, h.service, h.method, h.route, h.status_code, h.source,
       h.traffic_class,
       api.sum_buckets(h.counts order by h.bucket_start), sum(h.sum order by h.bucket_start),
       sum(h.count)::bigint, bool_or(h.lossy)
from api.metrics_hourly h
where h.tenant_id = @tenant_id
  and h.bucket_start >= sqlc.arg(day)::timestamptz
  and h.bucket_start < sqlc.arg(day)::timestamptz + interval '24 hours'
group by h.tenant_id, h.service, h.method, h.route, h.status_code, h.source, h.traffic_class;

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

-- name: EvictIdleSeriesStates :one
-- bounds the stored streams of each tenant (migration 00029)
select api.evict_idle_series_states(@idle_before::timestamptz, @max_stored::bigint)::bigint as evicted;

-- name: PruneResourceSeen :execrows
-- resources idle since before the cutoff (7 days)
delete from api.resource_seen where last_seen_at < @cutoff::timestamptz;

-- name: PruneRouteRegistry :execrows
-- cap state of days before the given utc day; only the current day's cap
-- is enforced, so older days are kept just long enough for late data
delete from api.route_registry where day < @before::date;
