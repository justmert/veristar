-- sign-in, sessions and /api/me for the console. cp_system, because
-- these run before any tenant is known.

-- name: SignInUser :one
-- the numeric github id is the identity, the login can change. is_operator
-- follows OPERATOR_GITHUB_IDS at every sign-in, so removing an id from the
-- setting takes effect at that person's next sign-in.
insert into cp.users (github_id, github_login, is_operator, last_login_at)
values (@github_id, @github_login, @is_operator, now())
on conflict (github_id) do update
  set github_login = excluded.github_login,
      is_operator = excluded.is_operator,
      last_login_at = excluded.last_login_at
returning *;

-- name: GetUser :one
select * from cp.users where id = @id;

-- name: ResolveInvites :many
-- turns the open invites for a github account into memberships and deletes
-- them, in one statement. the account id is matched, never the login,
-- which can change hands (00025). an invite for a tenant the user already
-- belongs to is consumed without changing the existing role.
with resolved as (
  delete from cp.invites
  where github_id = @github_id and expires_at > now()
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
