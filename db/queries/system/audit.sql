-- the operator audit list (control-plane-api 5.9), as cp_system: cp_operator
-- has no right on cp.audit_log or cp.users.

-- name: OperatorAuditPage :many
-- newest first, keyset over (at, id). every filter is optional; a null
-- parameter matches every row. action_like is an escaped like pattern.
-- q_like is the escaped search pattern of q ('%' around it): it matches a
-- row whose action, actor, target or detail holds it (the trigram index of
-- 00052 serves this expression), and a row whose actor, target or tenant
-- names a live account, project or site whose login, address, name, slug
-- or domain holds it.
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
  and (sqlc.narg(q_like)::text is null
       or (a.action || ' ' || a.actor || ' ' || a.target || ' ' || a.detail::text) ilike sqlc.narg(q_like)::text
       or a.actor = any(array(
            select 'user:' || su.id::text from cp.users su
            where su.deleted_at is null
              and (su.github_login ilike sqlc.narg(q_like)::text
                   or su.stellar_address ilike sqlc.narg(q_like)::text
                   or su.passkey_address ilike sqlc.narg(q_like)::text)))
       or a.target = any(array(
            select 'user:' || su.id::text from cp.users su
            where su.deleted_at is null
              and (su.github_login ilike sqlc.narg(q_like)::text
                   or su.stellar_address ilike sqlc.narg(q_like)::text
                   or su.passkey_address ilike sqlc.narg(q_like)::text)
            union all
            select 'tenant:' || st.id::text from cp.tenants st
            where st.name ilike sqlc.narg(q_like)::text or st.slug ilike sqlc.narg(q_like)::text
            union all
            select 'site:' || ss.id::text from cp.sites ss
            where ss.name ilike sqlc.narg(q_like)::text or ss.domain ilike sqlc.narg(q_like)::text))
       or a.detail ->> 'tenant_id' = any(array(
            select st.id::text from cp.tenants st
            where st.name ilike sqlc.narg(q_like)::text or st.slug ilike sqlc.narg(q_like)::text)))
  and (sqlc.narg(after_at)::timestamptz is null
       or (a.at, a.id) < (sqlc.narg(after_at)::timestamptz, sqlc.narg(after_id)::bigint))
order by a.at desc, a.id desc
limit @lim;
