-- resubmission after rejection (control-plane-api 5.7). cp_system, after the
-- owner check: cp_tenant has no right on status or enrollment_answers.

-- name: ResubmitLockTenant :one
select * from cp.tenants where id = @id for update;

-- name: ResubmitTenant :one
-- the status condition is repeated here, so a tenant that is not rejected
-- can never be moved to pending by this statement
update cp.tenants
set enrollment_answers = @enrollment_answers,
    name = coalesce(sqlc.narg(name)::text, name),
    description = coalesce(sqlc.narg(description)::text, description),
    status = 'pending',
    status_reason = null,
    status_changed_at = now(),
    updated_at = now()
where id = @id and status = 'rejected'
returning *;
