--: an invite stored only a github login and was
-- resolved for whoever signed in next with that login. github logins can be
-- renamed and the freed name taken by another person, so an owner invite
-- could hand a tenant to a stranger. from here on an invite holds the
-- numeric github account id the control plane resolved from the login when
-- the operator sent it (GET /users/{login}); sign-in resolves invites by that
-- id only. github_login stays, for display.
--
-- open invites made before this migration carry no id and cannot be bound
-- to an account after the fact, so they are revoked here, each with an
-- invite.revoke audit row (actor system:migration), and the operator sends
-- them again. the release notes and contract 4.2.5 say so.

-- +goose Up
alter table cp.invites add column github_id bigint check (github_id > 0);

-- the migration runs as platform_owner, which forced row-level security
-- would hide every invite from; the force is lifted for these statements
-- only and restored before the migration commits
alter table cp.invites no force row level security;

insert into cp.audit_log (actor, action, target, detail)
select 'system:migration', 'invite.revoke', 'invite:' || i.id::text,
       jsonb_build_object('tenant_id', i.tenant_id, 'github_login', i.github_login, 'role', i.role,
                          'reason', 'invites are bound to a github account id since migration 00025')
from cp.invites i;
delete from cp.invites;
alter table cp.invites force row level security;

alter table cp.invites alter column github_id set not null;
drop index cp.invites_tenant_login_idx;
drop index cp.invites_login_idx;
-- one open invite per account and tenant
create unique index invites_tenant_github_idx on cp.invites (tenant_id, github_id);
create index invites_github_idx on cp.invites (github_id);

-- +goose Down
drop index cp.invites_github_idx;
drop index cp.invites_tenant_github_idx;
create unique index invites_tenant_login_idx on cp.invites (tenant_id, lower(github_login));
create index invites_login_idx on cp.invites (lower(github_login));
alter table cp.invites drop column github_id;
