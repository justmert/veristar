-- queries for cp_tenant (db.WithTenant). row-level security limits every
-- statement to the tenant in app.tenant_id; inserts take the tenant from the
-- same setting, so a handler cannot place a row in another tenant.

-- name: GetTenant :one
select * from cp.tenants where id = cp.current_tenant_id();

-- name: ListMembers :many
select m.user_id, m.role, u.github_id, u.github_login, u.stellar_address, u.passkey_address
from cp.tenant_members m
join cp.users u on u.id = m.user_id
order by coalesce(u.github_login, u.stellar_address, u.passkey_address);

-- name: CreateSite :one
insert into cp.sites (id, tenant_id, name, domain, origins, verify_token)
values (@id, cp.current_tenant_id(), @name, @domain, @origins, @verify_token)
returning *;

-- name: ListSites :many
select * from cp.sites order by created_at, id;

-- name: CreateOTLPToken :one
insert into cp.otlp_tokens (tenant_id, name, environment, prefix, hash, created_by)
values (cp.current_tenant_id(), @name, @environment, @prefix, @hash, @created_by)
returning id, tenant_id, name, environment, prefix, status, created_by, created_at;

-- name: ListOTLPTokens :many
select id, name, environment, prefix, status, created_by, created_at, revoked_at, last_seen_at
from cp.otlp_tokens
order by created_at, id;

-- name: RevokeOTLPToken :execrows
update cp.otlp_tokens
set status = 'revoked', revoked_at = now()
where id = @id and status = 'active';
