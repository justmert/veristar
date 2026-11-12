-- queries for the jobs role (db.WithRole). every transaction that runs them
-- also advances the jobs fence through natsx.CheckFence.

-- name: RecordStatsMessage :execrows
-- one row per EDGE_STATS message id. zero rows means the message was applied
-- before, so its counters must not be added again.
insert into cp.stats_dedup (message_id)
values (@message_id)
on conflict (message_id) do nothing;

-- name: AddIngestCounts :exec
-- adds one message's counters to their hourly rows. the arrays are parallel
-- and of equal length (set-returning functions in one select list advance
-- together), and hold each (site, outcome) once, because one statement
-- cannot update the same row twice.
insert into cp.web_ingest_hourly (site_id, tenant_id, hour, outcome, count)
select unnest(@site_ids::uuid[]), unnest(@tenant_ids::uuid[]), @hour::timestamptz,
       unnest(@outcomes::text[]), unnest(@counts::bigint[])
on conflict (site_id, hour, outcome) do update
  set count = cp.web_ingest_hourly.count + excluded.count;

-- name: PruneStatsDedup :execrows
-- removes message ids applied before the cutoff. the caller passes a cutoff
-- past the stream's max age, so no pruned id can be delivered again.
delete from cp.stats_dedup
where applied_at < @cutoff::timestamptz;
