-- cp_system queries of the tenants, members and sites routes
-- (control-plane-api 1.3, 4.2, 4.3). they answer "may this user act on that
-- tenant" before any tenant-scoped work, and cover the few steps that span
-- tenants (enrollment, the caller's tenant list, member lookup by login).

-- name: GetMembership :one
-- the per-request membership check. removed tenants are filtered by the
-- caller, so the status is returned rather than hidden here.
select m.role, t.status, t.accepted_terms_version
from cp.tenant_members m
join cp.tenants t on t.id = m.tenant_id
where m.tenant_id = @tenant_id and m.user_id = @user_id;

-- name: GetSiteTenantID :one
select tenant_id from cp.sites where id = @id;

-- name: TenantsCreatedSince :one
-- the oldest creation in the window tells the client when it may retry;
-- with no rows the value is unused
select count(*) as created, coalesce(min(created_at), now())::timestamptz as oldest
from cp.tenants where created_by = @created_by and created_at >= @since;

-- name: EnrollTenant :one
insert into cp.tenants (slug, name, description, status, enrollment_answers, accepted_terms_version, created_by)
values (@slug, @name, @description, @status, @enrollment_answers, @accepted_terms_version, @created_by)
returning *;

-- name: ListUserTenantsPage :many
-- the caller's tenants for GET /api/tenants, keyset over (created_at, id).
-- the cursor columns are nullable so the first page passes nulls.
select t.*, m.role
from cp.tenant_members m
join cp.tenants t on t.id = m.tenant_id
where m.user_id = @user_id
  and t.status <> 'removed'
  and (sqlc.narg(after_created_at)::timestamptz is null
       or (t.created_at, t.id) > (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
order by t.created_at, t.id
limit @row_limit;

-- name: FindUserByGitHubID :one
-- the account the inviting owner named, resolved from the login through github
-- first: a stored login can be stale after a rename, the id cannot
select id, github_id, github_login, stellar_address, passkey_address, created_at
from cp.users
where github_id = @github_id and deleted_at is null;

-- name: FindUserByStellarAddress :one
-- the account the inviting owner named by its G-address
select id, github_id, github_login, stellar_address, passkey_address, created_at
from cp.users
where stellar_address = @stellar_address and deleted_at is null;

-- name: FindUserByPasskeyAddress :one
-- the account the inviting owner named by its soropass C-address
select id, github_id, github_login, stellar_address, passkey_address, created_at
from cp.users
where passkey_address = @passkey_address and deleted_at is null;

-- name: RefreshUserLogin :exec
-- the login github reported for the account just now; member lists show
-- it instead of the one stored at the last sign-in
update cp.users set github_login = @github_login
where github_id = @github_id and github_login <> @github_login;
