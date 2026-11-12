-- cp_tenant queries of the erasure routes (control-plane-api section 10)

-- name: InsertErasureRequest :one
insert into cp.erasure_requests (tenant_id, site_id, kind, status, requested_by, day, session_id, heartbeat_at)
values (cp.current_tenant_id(), @site_id, @kind, 'running', @requested_by,
        sqlc.narg(day)::date, sqlc.narg(session_id)::uuid, now())
returning *;

-- name: ListErasureRequestsPage :many
-- newest first: the console shows the latest request on top
select * from cp.erasure_requests
where (sqlc.narg(site_id)::uuid is null or site_id = sqlc.narg(site_id)::uuid)
  and (sqlc.narg(before_requested_at)::timestamptz is null
       or (requested_at, id) < (sqlc.narg(before_requested_at)::timestamptz, sqlc.narg(before_id)::uuid))
order by requested_at desc, id desc
limit @row_limit;

-- name: GetErasureRequest :one
select * from cp.erasure_requests where id = @id;
