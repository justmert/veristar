-- sign-in, sessions and /api/me for the console. cp_system, because
-- these run before any tenant is known.

-- name: SignInUser :one
-- the numeric github id is the identity, the login can change. is_operator
-- is set right after by SetOperatorFlag, from both identities of the row. a
-- deleted account's emptied row never matches (00031), so signing in again
-- after deleting the account makes a new row.
insert into cp.users (github_id, github_login, last_login_at)
values (@github_id, @github_login, now())
on conflict (github_id) where deleted_at is null do update
  set github_login = excluded.github_login,
      last_login_at = excluded.last_login_at
returning *;

-- name: SignInStellarUser :one
-- wallet sign-in (4.1.7): the G-address whose key signed the challenge is
-- the identity
insert into cp.users (stellar_address, last_login_at)
values (@stellar_address, now())
on conflict (stellar_address) where deleted_at is null and stellar_address is not null do update
  set last_login_at = excluded.last_login_at
returning *;

-- name: SetOperatorFlag :one
-- is_operator follows OPERATOR_GITHUB_IDS and OPERATOR_STELLAR_ADDRESSES at
-- every sign-in, link and unlink, so removing an entry takes effect at that
-- person's next sign-in (and at the next start, operator.SyncFlags)
update cp.users set is_operator = @is_operator
where id = @id and deleted_at is null
returning *;

-- name: LinkGitHub :one
-- adds a github identity to an account that has none (4.1.8). a github id
-- that already names another live account fails on users_github_id_live_idx
update cp.users set github_id = @github_id, github_login = @github_login
where id = @id and deleted_at is null and github_id is null
returning *;

-- name: LinkStellar :one
-- adds a stellar identity to an account that has none (4.1.8). an address
-- that already names another live account fails on
-- users_stellar_address_live_idx
update cp.users set stellar_address = @stellar_address
where id = @id and deleted_at is null and stellar_address is null
returning *;

-- name: UnlinkGitHub :one
-- removes the github identity while a stellar identity remains, so an
-- account always keeps a way to sign in
update cp.users set github_id = null, github_login = null
where id = @id and deleted_at is null and github_id is not null and stellar_address is not null
returning *;

-- name: UnlinkStellar :one
update cp.users set stellar_address = null
where id = @id and deleted_at is null and stellar_address is not null and github_id is not null
returning *;

-- name: UseWalletChallenge :execrows
-- 0 rows: the challenge was used before (4.1.7)
insert into cp.wallet_challenges_used (nonce_hash, expires_at)
values (@nonce_hash, @expires_at)
on conflict (nonce_hash) do nothing;

-- name: PruneWalletChallenges :exec
-- an expired challenge is refused by its time bounds already
delete from cp.wallet_challenges_used where expires_at <= now();

-- name: GetUser :one
-- a deleted account is no user: its sessions are gone, and this keeps a
-- session row written in a race with the deletion from loading it
select * from cp.users where id = @id and deleted_at is null;

-- name: ResolveInvites :many
-- turns the open invites for the account's github id or stellar address
-- into memberships and deletes them, in one statement. the github account
-- id is matched, never the login, which can change hands (00025). a null
-- parameter matches nothing. an invite for a tenant the user already
-- belongs to is consumed without changing the existing role.
with resolved as (
  delete from cp.invites
  where (github_id = sqlc.narg(github_id)::bigint or stellar_address = sqlc.narg(stellar_address)::text)
    and expires_at > now()
  returning id, tenant_id, role
), added as (
  insert into cp.tenant_members (tenant_id, user_id, role)
  select tenant_id, @user_id::uuid, role from resolved
  on conflict (tenant_id, user_id) do nothing
  returning tenant_id
)
select r.id, r.tenant_id, r.role, (a.tenant_id is not null)::boolean as added
from resolved r left join added a on a.tenant_id = r.tenant_id
order by r.id;

-- name: ListUserTenants :many
-- every tenant of the user except removed ones, for /api/me
select t.id, t.slug, t.name, t.status, t.accepted_terms_version, m.role
from cp.tenant_members m
join cp.tenants t on t.id = m.tenant_id
where m.user_id = @user_id and t.status <> 'removed'
order by t.name, t.id;

-- name: FindSession :one
-- expired rows are invisible here; the jobs task sessionprune deletes them
select data from cp.sessions where token_hash = @token_hash and expiry > now();

-- name: CommitSession :exec
insert into cp.sessions (token_hash, user_id, data, expiry)
values (@token_hash, @user_id, @data, @expiry)
on conflict (token_hash) do update
  set user_id = excluded.user_id, data = excluded.data, expiry = excluded.expiry;

-- name: DeleteSession :exec
delete from cp.sessions where token_hash = @token_hash;

-- name: PruneSessions :execrows
-- run by the jobs task session-prune; @cutoff is the task's clock, so tests
-- can move it
delete from cp.sessions where expiry <= @cutoff;

-- name: ListUserRemovedTenants :many
-- removed projects of the user for /api/me removed_tenants (contract 5.10),
-- newest removal first; member rows stay on removal so former members learn
-- why the project is gone
select t.id, t.name, t.status_reason, t.status_changed_at
from cp.tenant_members m
join cp.tenants t on t.id = m.tenant_id
where m.user_id = @user_id and t.status = 'removed'
order by t.status_changed_at desc, t.id
limit 20;
