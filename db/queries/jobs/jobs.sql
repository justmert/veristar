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

-- name: AddEvidenceCounts :exec
-- adds one message's network class counters to their daily rows, like
-- AddIngestCounts: parallel arrays, each (site, class) once
insert into cp.web_evidence_daily (site_id, tenant_id, day, class, count)
select unnest(@site_ids::uuid[]), unnest(@tenant_ids::uuid[]), @day::date,
       unnest(@classes::text[]), unnest(@counts::bigint[])
on conflict (site_id, day, class) do update
  set count = cp.web_evidence_daily.count + excluded.count;

-- name: AddSignalCounts :exec
-- adds one message's page signal counters (integrity.Signals) to their
-- daily rows, like AddEvidenceCounts
insert into cp.web_signals_daily (site_id, tenant_id, day, signal, count)
select unnest(@site_ids::uuid[]), unnest(@tenant_ids::uuid[]), @day::date,
       unnest(@signals::text[]), unnest(@counts::bigint[])
on conflict (site_id, day, signal) do update
  set count = cp.web_signals_daily.count + excluded.count;

-- name: AddCoverageCounts :exec
-- adds one message's tracker file counters to the sites' daily coverage rows.
-- parallel arrays, each site once
insert into agg.web_coverage_daily (site_id, tenant_id, day, script_requests, gpc_requests)
select unnest(@site_ids::uuid[]), unnest(@tenant_ids::uuid[]), @day::date,
       unnest(@script_requests::bigint[]), unnest(@gpc_requests::bigint[])
on conflict (site_id, day) do update
  set script_requests = agg.web_coverage_daily.script_requests + excluded.script_requests,
      gpc_requests = agg.web_coverage_daily.gpc_requests + excluded.gpc_requests;

-- name: AddNetworkSightings :exec
-- records the networks one message reports. a network that another node, or
-- a redelivery, reported before is already in the set
insert into cp.web_networks_daily (site_id, tenant_id, day, network)
select unnest(@site_ids::uuid[]), unnest(@tenant_ids::uuid[]), @day::date, unnest(@networks::text[])
on conflict (site_id, day, network) do nothing;
