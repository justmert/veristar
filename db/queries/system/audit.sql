-- the operator audit list (control-plane-api 5.9), as cp_system: cp_operator
-- has no right on cp.audit_log or cp.users.

-- name: OperatorAuditPage :many
-- newest first, keyset over (at, id). every filter is optional; a null
-- parameter matches every row. action_like is an escaped like pattern.
select a.id, a.at, a.actor, a.action, a.target, a.detail,
       u.github_login as actor_login
from cp.audit_log a
left join cp.users u
  on a.actor like 'user:%' and u.id::text = substr(a.actor, 6) and u.deleted_at is null
where (sqlc.narg(tenant_id)::text is null
       or a.target = 'tenant:' || sqlc.narg(tenant_id)::text
       or a.detail ->> 'tenant_id' = sqlc.narg(tenant_id)::text)
  and (sqlc.narg(target)::text is null or a.target = sqlc.narg(target)::text)
  and (sqlc.narg(action)::text is null or a.action = sqlc.narg(action)::text)
  and (sqlc.narg(action_like)::text is null or a.action like sqlc.narg(action_like)::text)
  and (sqlc.narg(actor)::text is null or a.actor = sqlc.narg(actor)::text)
  and (sqlc.narg(from_at)::timestamptz is null or a.at >= sqlc.narg(from_at)::timestamptz)
  and (sqlc.narg(to_at)::timestamptz is null or a.at < sqlc.narg(to_at)::timestamptz)
  and (sqlc.narg(after_at)::timestamptz is null
       or (a.at, a.id) < (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::bigint))
order by a.at desc, a.id desc
limit @lim;
