-- cp_tenant queries of the tenant and member routes. row-level security
-- limits every statement to app.tenant_id.

-- name: LockTenant :one
-- serialises the member and site limit checks of one tenant, so two
-- concurrent requests cannot both pass a count of 49. it returns the status
-- as of the lock: an operator action that committed while the request
-- waited is seen here, not the status read before the lock
select status from cp.tenants where id = cp.current_tenant_id() for update;

-- name: UpdateTenantProfile :one
update cp.tenants
set name = coalesce(sqlc.narg(name)::text, name),
    description = coalesce(sqlc.narg(description)::text, description),
    updated_at = now()
where id = cp.current_tenant_id()
returning *;

-- name: AcceptTenantTerms :one
update cp.tenants
set accepted_terms_version = @version, updated_at = now()
where id = cp.current_tenant_id()
returning *;

-- name: InsertTenantAudit :exec
-- no returning clause: cp_tenant may append to the audit log but not read it
insert into cp.audit_log (actor, action, target, detail)
values (@actor, @action, @target, @detail);

-- name: ListTenantMembers :many
select m.user_id, u.github_id, u.github_login, m.role, m.created_at
from cp.tenant_members m
join cp.users u on u.id = m.user_id
order by lower(u.github_login), m.user_id;

-- name: GetTenantMember :one
select m.user_id, u.github_id, u.github_login, m.role, m.created_at
from cp.tenant_members m
join cp.users u on u.id = m.user_id
where m.user_id = @user_id;

-- name: InsertTenantMember :one
insert into cp.tenant_members (tenant_id, user_id, role)
values (cp.current_tenant_id(), @user_id, @role)
returning user_id, role, created_at;

-- name: SetTenantMemberRole :one
update cp.tenant_members set role = @role where user_id = @user_id
returning user_id, role, created_at;

-- name: DeleteTenantMember :execrows
delete from cp.tenant_members where user_id = @user_id;

-- name: LockTenantOwners :many
-- row locks on every owner membership, so two concurrent demotions or
-- removals cannot both see a second owner and leave the tenant with none
select user_id from cp.tenant_members where role = 'owner' for update;

-- name: ListOpenInvites :many
select * from cp.invites where expires_at > now() order by created_at, id;

-- name: CountMembersAndOpenInvites :one
select (select count(*) from cp.tenant_members)
     + (select count(*) from cp.invites where expires_at > now()) as total;

-- name: DeleteExpiredInvitesForAccount :execrows
-- an expired invite still holds its slot in invites_tenant_github_idx
delete from cp.invites where github_id = @github_id and expires_at <= now();

-- name: CreateInvite :one
insert into cp.invites (tenant_id, github_id, github_login, role, created_by, expires_at)
values (cp.current_tenant_id(), @github_id, @github_login, @role, @created_by, @expires_at)
returning *;

-- name: DeleteInvite :one
delete from cp.invites where id = @id returning *;
