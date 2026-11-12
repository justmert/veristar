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
