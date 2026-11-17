-- cp_system queries of the erasure run (control-plane-api section 10) and
-- the jobs task erasure-followup

-- name: ErasureHeartbeat :execrows
update cp.erasure_requests set heartbeat_at = now()
where id = @id and status = 'running';

-- name: CompleteErasureRequest :one
update cp.erasure_requests
set status = 'done', outcome = @outcome, message = @message, completed_at = now(),
    sessions_deleted = @sessions_deleted, events_deleted = @events_deleted,
    dlq_messages_deleted = @dlq_messages_deleted,
    affected_from = sqlc.narg(affected_from)::date, affected_to = sqlc.narg(affected_to)::date
where id = @id and status = 'running'
returning *;

-- name: FailErasureRequest :one
-- counts are kept: a run that fails halfway reports what it already deleted
update cp.erasure_requests
set status = 'failed', error = @error, completed_at = now(),
    sessions_deleted = @sessions_deleted, events_deleted = @events_deleted,
    dlq_messages_deleted = @dlq_messages_deleted,
    affected_from = sqlc.narg(affected_from)::date, affected_to = sqlc.narg(affected_to)::date
where id = @id and status = 'running'
returning *;

-- name: FailStaleErasureRequests :many
-- a run whose process stopped renewing its heartbeat cannot continue: a
-- visitor_today token existed only in that process's memory
update cp.erasure_requests
set status = 'failed', completed_at = now(),
    error = 'the control plane stopped while running this request; submit it again'
where status = 'running' and heartbeat_at < @stale_before
returning *;

-- name: ListErasureReaggregation :many
-- failed runs are included: they may have deleted sessions before failing
select * from cp.erasure_requests
where status in ('done', 'failed') and reaggregated_at is null and affected_from is not null
order by requested_at
limit @row_limit;

-- name: SetErasureReaggregated :execrows
update cp.erasure_requests set reaggregated_at = now()
where id = @id and reaggregated_at is null;

-- name: LockSiteResetTenant :exec
-- the shared agg lock of the tenant (jobsq.LockAggTenant), so the delete
-- below does not interleave with an integrity grade of the same tenant
select pg_advisory_xact_lock_shared(hashtextextended('agg.tenant:' || @tenant_key::text, 0));

-- name: DeleteSiteEdgeCounters :one
-- a site reset leaves no web data of the site: the gateways' outcome
-- counters, network class evidence, page signals and network keys go too.
-- cp_system deletes them; the jobs role may not delete edge counters
with h as (delete from cp.web_ingest_hourly x where x.site_id = @site_id::uuid returning 1),
     e as (delete from cp.web_evidence_daily x where x.site_id = @site_id::uuid returning 1),
     s as (delete from cp.web_signals_daily x where x.site_id = @site_id::uuid returning 1),
     n as (delete from cp.web_networks_daily x where x.site_id = @site_id::uuid returning 1)
select (select count(*) from h)::bigint as ingest, (select count(*) from e)::bigint as evidence,
       (select count(*) from s)::bigint as signals, (select count(*) from n)::bigint as networks;
