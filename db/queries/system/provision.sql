-- queries for the provisioning reconciler, sso and the umami password
-- rotation. all run as cp_system.

-- name: ProvisionLockTenant :one
-- one reconcile per tenant at a time: the row lock is held for the whole run,
-- across the umami calls, so the control plane and the jobs task never create
-- the same team or website twice
select id, status, umami_team_id from cp.tenants where id = @id for update;

-- name: ProvisionSetTeam :exec
update cp.tenants set umami_team_id = @umami_team_id, updated_at = now() where id = @id;

-- name: ProvisionMembers :many
-- the users of a tenant who need an umami user and team membership
select u.id, u.github_id
from cp.tenant_members m
join cp.users u on u.id = m.user_id
where m.tenant_id = @tenant_id
order by u.id;

-- name: ProvisionSites :many
select id, name, domain, origins, quarantine_website_id, status, collect_utm
from cp.sites
where tenant_id = @tenant_id
order by id;

-- name: ProvisionTenantIDs :many
-- every tenant, for the periodic reconcile of all tenants
select id from cp.tenants order by id;

-- name: SSOSite :one
-- the site an sso request names, with what the membership check needs
select s.id, s.tenant_id, s.status, s.quarantine_website_id, t.status as tenant_status, t.umami_team_id
from cp.sites s
join cp.tenants t on t.id = s.tenant_id
where s.id = @id;

-- name: SSOMember :one
-- the caller's github id when the caller is a member of the tenant
select u.github_id
from cp.tenant_members m
join cp.users u on u.id = m.user_id
where m.tenant_id = @tenant_id and m.user_id = @user_id;

-- name: UmamiPasswordDay :one
-- the utc epoch day of the last completed password rotation; no row before
-- the first one
select (value #>> '{}')::bigint as day from cp.settings where key = 'umami_password_day';

-- name: SetUmamiPasswordDay :exec
insert into cp.settings (key, value) values ('umami_password_day', to_jsonb(@day::bigint))
on conflict (key) do update set value = excluded.value;

-- name: ProvisionSiteBaselines :many
-- the latest baseline of each site through the security definer function
-- of 00033 (cp_system has no right in agg itself)
select b.site_id::uuid as site_id, b.baseline_visitors::bigint as baseline_visitors
from agg.site_baselines(@tenant_id::uuid) as b;
