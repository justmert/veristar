-- operator tenant detail parts that cp_operator may not read (control-plane-api
-- 5.1 and 5.4): users and members, as cp_system.

-- name: OperatorUserRef :one
select id, github_id, github_login from cp.users where id = @id;

-- name: OperatorTenantMembers :many
select u.github_id, u.github_login, m.role, m.created_at
from cp.tenant_members m
join cp.users u on u.id = m.user_id
where m.tenant_id = @tenant_id
order by lower(u.github_login), u.id;

-- name: OperatorSyncFlags :many
-- startup sync of contract 5.1: is_operator follows OPERATOR_GITHUB_IDS for
-- every user, and the changed rows come back for the audit
update cp.users
set is_operator = (github_id = any(@operator_ids::bigint[]))
where is_operator <> (github_id = any(@operator_ids::bigint[]))
returning id, is_operator;

-- name: ReviewMatchRemovedDomain :one
-- the review rule of contract 5.6 for a domain about to be registered: the
-- first site of a removed tenant (other than tenant_id) whose domain equals,
-- is a parent of, or is a subdomain of domain. it is the same predicate as
-- cp.operator_sites.domain_previously_removed, evaluated before the insert,
-- because cp_tenant cannot see other tenants' sites.
select o.domain, o.tenant_id
from cp.sites o
join cp.tenants t on t.id = o.tenant_id
where t.status = 'removed' and o.tenant_id <> @tenant_id
  and (o.domain = @domain::text
       or right(@domain::text, length(o.domain) + 1) = '.' || o.domain
       or right(o.domain, length(@domain::text) + 1) = '.' || @domain::text)
order by o.domain, o.tenant_id
limit 1;
