-- cp_tenant queries of the otlp token routes (control-plane-api 6). the hash
-- column holds the sha-256 of the token; the token itself is never stored.

-- name: LockTenantStatus :one
-- the token create checks the status under the row lock, so a suspension
-- that commits meanwhile waits for the insert and its reconcile sees the token
select status from cp.tenants where id = cp.current_tenant_id() for update;

-- name: CountActiveOTLPTokens :one
select count(*) from cp.otlp_tokens where status = 'active';

-- name: InsertOTLPToken :one
insert into cp.otlp_tokens (tenant_id, name, environment, prefix, hash, created_by)
values (cp.current_tenant_id(), @name, @environment, @prefix, @hash, @created_by)
returning id, tenant_id, name, environment, prefix, status, created_by, created_at, revoked_at, last_seen_at;

-- name: ListOTLPTokensPage :many
select id, tenant_id, name, environment, prefix, status, created_by, created_at, revoked_at, last_seen_at
from cp.otlp_tokens
where (@include_revoked::boolean or status <> 'revoked')
  and (sqlc.narg(after_created_at)::timestamptz is null
       or (created_at, id) > (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
order by created_at, id
limit @row_limit;

-- name: GetOTLPToken :one
select id, tenant_id, name, environment, prefix, status, created_by, created_at, revoked_at, last_seen_at
from cp.otlp_tokens
where id = @id;

-- name: LockOTLPToken :one
-- the revoke reads the hash to flip the registry key in the same transaction
select id, status, hash from cp.otlp_tokens where id = @id for update;

-- name: SetOTLPTokenRevoked :exec
update cp.otlp_tokens
set status = 'revoked', revoked_at = now()
where id = @id and status = 'active';
