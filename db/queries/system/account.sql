-- console account deletion (contract 4.1.6), as cp_system: the account's
-- rows span every tenant it belongs to.

-- name: AccountLockUser :one
-- the account row, locked so two deletions of one account run one after
-- the other and the second sees the first's result
select id, github_id, stellar_address, passkey_address, is_operator, deleted_at from cp.users where id = @id for update;

-- name: AccountLockTenants :many
-- every tenant the account belongs to, locked in id order. a reconcile run
-- holds the same row lock for its whole length, so it either finished
-- before this deletion or reads the memberships after it commits.
select t.id from cp.tenants t
where t.id in (select m.tenant_id from cp.tenant_members m where m.user_id = @user_id)
order by t.id
for update of t;

-- name: AccountLockOwners :exec
-- the owner rows of those tenants, locked as members.setRole and
-- members.remove lock them, so a concurrent demotion of the other owner
-- cannot leave a tenant without one
select m.user_id from cp.tenant_members m
where m.role = 'owner'
  and m.tenant_id in (select x.tenant_id from cp.tenant_members x where x.user_id = @user_id)
order by m.tenant_id, m.user_id
for update of m;

-- name: AccountSoleOwnedTenants :many
-- tenants that are not removed and whose only owner is the account
select t.id, t.name, t.status from cp.tenant_members m
join cp.tenants t on t.id = m.tenant_id
where m.user_id = @user_id and m.role = 'owner' and t.status <> 'removed'
  and not exists (
    select 1 from cp.tenant_members o
    where o.tenant_id = m.tenant_id and o.role = 'owner' and o.user_id <> m.user_id)
order by lower(t.name), t.id;

-- name: AccountDeleteMemberships :many
-- the tenant_members_mcp_revoke trigger revokes the account's mcp
-- credentials for each tenant, with their audit rows
delete from cp.tenant_members where user_id = @user_id
returning tenant_id, role;

-- name: AccountDeleteInvites :execrows
-- open and expired invites addressed to the account's github id or stellar
-- address: they name the person, who can be invited again after signing in
-- anew. a null parameter matches nothing.
delete from cp.invites
where github_id = sqlc.narg(github_id)::bigint or stellar_address = sqlc.narg(stellar_address)::text
   or stellar_address = sqlc.narg(passkey_address)::text;

-- name: AccountDeleteMCPCodes :execrows
delete from cp.mcp_oauth_codes where user_id = @user_id;

-- name: AccountDeleteMCPCredentials :execrows
-- access keys and oauth grants; the grants' tokens go with them (cascade)
delete from cp.mcp_credentials where user_id = @user_id;

-- name: AccountDeleteSessions :execrows
delete from cp.sessions where user_id = @user_id::uuid;

-- name: AccountEmpty :exec
-- clears what identifies the person (00031, 00034). is_operator goes to false in
-- the same statement, which fires users_mcp_revoke for any operator
-- credential left
update cp.users
set github_id = null, github_login = null, stellar_address = null, umami_username = null,
    passkey_address = null, passkey_credential_id = null, passkey_public_key = null,
    is_operator = false, last_login_at = null, deleted_at = now()
where id = @id and deleted_at is null;

-- name: AccountDeleteRoster :many
-- the roster rows of the account's identities (00041): a deleted account
-- is no operator. operators_last_admin refuses the delete when it would
-- leave no active operator admin.
delete from cp.operators
where (sqlc.narg(github_id)::bigint is not null and github_id = sqlc.narg(github_id)::bigint)
   or (sqlc.narg(stellar_address)::text is not null and stellar_address = sqlc.narg(stellar_address)::text)
   or (sqlc.narg(passkey_address)::text is not null and stellar_address = sqlc.narg(passkey_address)::text)
returning id;
