-- tenant lifecycle actions, sites in review and the removal purge (plan
-- this stage stream a, control-plane-api 5.5, 5.6 and 5.13). all run as
-- cp_system: operator actions are cp_system work.

-- name: LifecycleLockTenant :one
-- the row lock serialises an action with the reconciler, which holds the
-- same lock for its whole run
select id, status, is_internal from cp.tenants where id = @id for update;

-- name: LifecycleSetTenantStatus :exec
update cp.tenants
set status = @status,
    status_reason = sqlc.narg(status_reason),
    approved_by = coalesce(sqlc.narg(approved_by), approved_by),
    status_changed_at = now(),
    updated_at = now()
where id = @id;

-- name: LifecycleActivatePendingSites :many
-- an approved tenant's sites go live with it (contract 5.5)
update cp.sites set status = 'active', status_reason = null, updated_at = now()
where tenant_id = @tenant_id and status = 'pending'
returning id;

-- name: LifecycleRemoveSites :many
-- domains stay on the rows: they drive the review rule of contract 5.6
update cp.sites set status = 'removed', updated_at = now()
where tenant_id = @tenant_id and status <> 'removed'
returning id;

-- name: LifecycleDeleteInvites :execrows
delete from cp.invites where tenant_id = @tenant_id;

-- name: LifecycleKeepAggregates :one
select value from cp.settings where key = 'removal_keep_aggregates';

-- name: LifecycleInsertRemoval :exec
insert into cp.tenant_removals (tenant_id, requested_by, reason, keep_aggregates)
values (@tenant_id, @requested_by, @reason, @keep_aggregates);

-- name: LifecycleTenantSummary :one
-- OperatorTenantSummary of contract 5.2
select t.id, t.slug, t.name, t.status, t.status_reason,
       (t.enrollment_answers ->> 'website')::text as website,
       t.created_at, t.status_changed_at,
       (select count(*) from cp.sites s
         where s.tenant_id = t.id and s.status <> 'removed')::bigint as site_count,
       (select count(*) from cp.sites s
         where s.tenant_id = t.id and s.status = 'pending' and t.status = 'active')::bigint as sites_in_review,
       (select count(*) from cp.sites s
         where s.tenant_id = t.id and s.status <> 'removed'
           and s.verified_at is not null and s.last_verify_error is null)::bigint as sites_verified,
       (select count(*) from cp.sites s
         where s.tenant_id = t.id and s.status <> 'removed'
           and s.stellar_toml ->> 'status' = 'found')::bigint as sites_with_stellar_toml
from cp.tenants t
where t.id = @id;

-- name: LifecycleLockSite :one
-- the tenant row is locked first, in the same order as the reconciler
select s.id, s.tenant_id, s.status, t.status as tenant_status, t.is_internal
from cp.sites s
join cp.tenants t on t.id = s.tenant_id
where s.id = @id
for update of t, s;

-- name: LifecycleSetSiteStatus :exec
update cp.sites
set status = @status, status_reason = sqlc.narg(status_reason), updated_at = now()
where id = @id;

-- name: LifecycleOperatorSite :one
-- OperatorSite of contract 5.2, through the view without verify_token
select v.id, v.tenant_id, v.name, v.domain, v.origins, v.status, v.status_reason,
       (v.status = 'pending' and t.status = 'active')::boolean as in_review,
       v.domain_previously_removed,
       v.verify_method, v.verified_at, v.last_verify_at, v.last_verify_error,
       v.stellar_toml, v.stellar_toml_checked_at, v.created_at, v.updated_at
from cp.operator_sites v
join cp.tenants t on t.id = v.tenant_id
where v.id = @id;

-- name: PurgeLockRemoval :one
-- the caller already holds the tenant row lock (ProvisionLockTenant)
select tenant_id, keep_aggregates, steps, completed_at
from cp.tenant_removals
where tenant_id = @tenant_id
for update;

-- name: PurgeSites :many
-- every site the tenant ever had, removed ones included: a site its owner
-- deleted earlier may still have a registry key or websites after a failed run
select id, domain, quarantine_website_id from cp.sites where tenant_id = @tenant_id order by id;

-- name: PurgeSetStep :exec
update cp.tenant_removals
set steps = jsonb_set(steps, array[@name::text], @step::jsonb, true)
where tenant_id = @tenant_id;

-- name: PurgeComplete :exec
update cp.tenant_removals set completed_at = now() where tenant_id = @tenant_id;

-- name: PurgeOrphanedUsers :many
-- members of the removed tenant who belong to no tenant that is not
-- removed: their umami user serves nobody any more
select m.user_id
from cp.tenant_members m
where m.tenant_id = @tenant_id
  and not exists (
    select 1 from cp.tenant_members o
    join cp.tenants t on t.id = o.tenant_id
    where o.user_id = m.user_id and t.status <> 'removed')
order by m.user_id;

-- name: PurgeWebIngest :execrows
delete from cp.web_ingest_hourly where tenant_id = @tenant_id;

-- name: RemovalKeepAggregatesForUpdate :one
select value from cp.settings where key = 'removal_keep_aggregates' for update;

-- name: SetRemovalKeepAggregates :exec
update cp.settings set value = to_jsonb(@keep::boolean) where key = 'removal_keep_aggregates';
