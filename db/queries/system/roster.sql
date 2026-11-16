-- the operator roster (00036) as cp_system: the guard, the operators page,
-- the startup bootstrap and the step-up enrollment window.

-- name: RosterEntriesForUser :many
-- the roster rows matching an account's identities. an account can match
-- two rows, one per identity.
select * from cp.operators
where (sqlc.narg(github_id)::bigint is not null and github_id = sqlc.narg(github_id)::bigint)
   or (sqlc.narg(stellar_address)::text is not null and stellar_address = sqlc.narg(stellar_address)::text)
order by created_at, id;

-- name: ListRoster :many
-- every row with the live account that holds its identity, if one exists
select o.*, u.id as user_id, u.github_login as account_github_login, u.last_login_at,
       (select count(*) from cp.operator_passkeys k where k.user_id = u.id)::bigint as passkeys
from cp.operators o
left join cp.users u
  on u.deleted_at is null
 and ((o.github_id is not null and u.github_id = o.github_id)
      or (o.stellar_address is not null and u.stellar_address = o.stellar_address))
order by o.role desc, o.created_at, o.id;

-- name: LockRosterEntry :one
select * from cp.operators where id = @id for update;

-- name: LockActiveAdmins :many
-- every active admin row, locked: a change that could remove the last one
-- takes these locks first, so two such changes run one after the other
select id from cp.operators where role = 'operator_admin' and status = 'active' order by id for update;

-- name: AddRosterEntry :one
insert into cp.operators (github_id, github_login, stellar_address, role, created_by,
                          enroll_opened_at, enroll_until)
values (sqlc.narg(github_id), sqlc.narg(github_login), sqlc.narg(stellar_address), @role, sqlc.narg(created_by),
        @enroll_opened_at::timestamptz, @enroll_until::timestamptz)
returning *;

-- name: SetRosterStatus :one
update cp.operators
set status = @status, status_reason = sqlc.narg(status_reason), updated_at = now()
where id = @id
returning *;

-- name: SetRosterRole :one
update cp.operators set role = @role, updated_at = now() where id = @id returning *;

-- name: DeleteRosterEntry :one
delete from cp.operators where id = @id returning *;

-- name: OpenEnrollment :one
update cp.operators
set enroll_opened_at = @opened_at::timestamptz, enroll_until = @until::timestamptz, updated_at = now()
where id = @id
returning *;

-- name: CloseEnrollment :execrows
-- the first passkey of the account closes the window of every row it holds
update cp.operators
set enroll_opened_at = null, enroll_until = null, updated_at = now()
where enroll_until is not null
  and ((sqlc.narg(github_id)::bigint is not null and github_id = sqlc.narg(github_id)::bigint)
    or (sqlc.narg(stellar_address)::text is not null and stellar_address = sqlc.narg(stellar_address)::text));

-- name: BootstrapRosterGitHub :one
-- the startup bootstrap of one github id: an active operator admin. xmax = 0
-- tells an inserted row from an updated one.
insert into cp.operators (github_id, role)
values (@github_id, 'operator_admin')
on conflict (github_id) where github_id is not null do update
  set role = 'operator_admin', status = 'active', status_reason = null,
      updated_at = case when cp.operators.role <> 'operator_admin' or cp.operators.status <> 'active'
                        then now() else cp.operators.updated_at end
returning *, (xmax = 0)::boolean as inserted;

-- name: BootstrapRosterStellar :one
insert into cp.operators (stellar_address, role)
values (@stellar_address, 'operator_admin')
on conflict (stellar_address) where stellar_address is not null do update
  set role = 'operator_admin', status = 'active', status_reason = null,
      updated_at = case when cp.operators.role <> 'operator_admin' or cp.operators.status <> 'active'
                        then now() else cp.operators.updated_at end
returning *, (xmax = 0)::boolean as inserted;

-- name: RosterIdentityPasskeys :one
-- the passkeys of the live account holding a roster row's identity; 0
-- when no account holds it yet
select coalesce((
  select count(*) from cp.operator_passkeys k
  join cp.users u on u.id = k.user_id
  where u.deleted_at is null
    and ((sqlc.narg(github_id)::bigint is not null and u.github_id = sqlc.narg(github_id)::bigint)
      or (sqlc.narg(stellar_address)::text is not null and u.stellar_address = sqlc.narg(stellar_address)::text))
), 0)::bigint;

-- name: RosterIdentityUser :one
-- the live account holding a roster row's identity
select id from cp.users
where deleted_at is null
  and ((sqlc.narg(github_id)::bigint is not null and github_id = sqlc.narg(github_id)::bigint)
    or (sqlc.narg(stellar_address)::text is not null and stellar_address = sqlc.narg(stellar_address)::text));

-- name: RecomputeOperatorFlags :many
-- every live account whose flag disagrees with the roster, set right; the
-- startup bootstrap audits each one
update cp.users u
set is_operator = cp.roster_active(u.github_id, u.stellar_address)
where u.deleted_at is null and u.is_operator is distinct from cp.roster_active(u.github_id, u.stellar_address)
returning u.id, u.is_operator;

-- name: CountActiveAdmins :one
select count(*)::bigint from cp.operators where role = 'operator_admin' and status = 'active';
