-- queries for cp_system (db.WithSystem): no row filter, every tenant.

-- name: UpsertUser :one
-- github sign-in: the numeric github id is the identity, the login can change
insert into cp.users (github_id, github_login, last_login_at)
values (@github_id, @github_login, now())
on conflict (github_id) where deleted_at is null do update
  set github_login = excluded.github_login, last_login_at = excluded.last_login_at
returning *;

-- name: CreateTenant :one
insert into cp.tenants (slug, name, description, created_by)
values (@slug, @name, @description, @created_by)
returning *;

-- name: AddTenantMember :exec
insert into cp.tenant_members (tenant_id, user_id, role)
values (@tenant_id, @user_id, @role);

-- name: InsertAuditLog :one
insert into cp.audit_log (actor, action, target, detail)
values (@actor, @action, @target, @detail)
returning id, at;

-- name: GetSetting :one
select value from cp.settings where key = @key;
