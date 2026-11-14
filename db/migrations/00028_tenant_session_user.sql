-- a second step after 00024. 00024 narrowed cp_tenant, but left
-- three gaps a handler bug could still use:
--
-- - verify_method, verified_at, last_verify_at, last_verify_error,
--   stellar_toml and stellar_toml_checked_at stayed writable, so a bug in
--   any tenant handler could mark a domain verified. the verify route and
--   the stellar.toml fetch after site creation now write them under
--   cp_system, next to the network check that decides them (the jobs task
--   verify-domains already did); cp_tenant loses the columns.
-- - the audit actor check accepted any 'user:<uuid>'. tenant transactions
--   now carry the session user in app.user_id (tenants.Scope.InTenant sets
--   it, like app.tenant_id), and a cp_tenant audit row must name exactly
--   that user.
-- - cp.tenant_members kept insert, update and delete on every column for
--   any member. only the role column stays updatable, and a trigger lets
--   cp_tenant add a member or change a role only when the session user is
--   an owner of the tenant, and remove a member only when the session user
--   is an owner or removes themself (contract 4.4). invites are accepted
--   under cp_system at sign-in, so no member row is written otherwise.
--
-- the triggers test current_user, the login role of the pool, as 00024's do.

-- +goose Up
revoke update (verify_method, verified_at, last_verify_at, last_verify_error, stellar_toml,
               stellar_toml_checked_at) on cp.sites from cp_tenant;

revoke update on cp.tenant_members from cp_tenant;
grant update (role) on cp.tenant_members to cp_tenant;

-- the session user of a tenant transaction, null when none is set
-- +goose StatementBegin
create function cp.app_user_id() returns uuid
language sql stable
set search_path = pg_catalog, pg_temp
as $$
  select nullif(current_setting('app.user_id', true), '')::uuid
$$;
-- +goose StatementEnd
grant execute on function cp.app_user_id() to cp_tenant;

-- +goose StatementBegin
create or replace function cp.tenant_audit_actor_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
declare
  uid uuid;
begin
  if current_user <> 'cp_tenant' then
    return new;
  end if;
  -- read only for cp_tenant, the one role granted the function
  uid := cp.app_user_id();
  if uid is null or new.actor is distinct from 'user:' || uid::text then
    raise exception 'cp_tenant audit rows must name the session user as actor'
      using errcode = 'insufficient_privilege';
  end if;
  return new;
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
create function cp.tenant_member_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
declare
  uid uuid;
  tid uuid;
  is_owner boolean;
begin
  if current_user <> 'cp_tenant' then
    return coalesce(new, old);
  end if;
  uid := cp.app_user_id();
  if tg_op = 'DELETE' then
    tid := old.tenant_id;
  else
    tid := new.tenant_id;
  end if;
  -- the table as it was before this statement: an owner who demotes or
  -- removes themself is still an owner here
  select exists (select 1 from cp.tenant_members m
                 where m.tenant_id = tid and m.user_id = uid and m.role = 'owner')
    into is_owner;
  if uid is null or not (is_owner or (tg_op = 'DELETE' and old.user_id = uid)) then
    raise exception 'cp_tenant may change members only as an owner, or remove itself'
      using errcode = 'insufficient_privilege';
  end if;
  return coalesce(new, old);
end
$$;
-- +goose StatementEnd
create trigger tenant_members_tenant_guard before insert or update or delete on cp.tenant_members
  for each row execute function cp.tenant_member_guard();

revoke all on function cp.tenant_member_guard() from public;

-- +goose Down
drop trigger tenant_members_tenant_guard on cp.tenant_members;
drop function cp.tenant_member_guard();
-- +goose StatementBegin
create or replace function cp.tenant_audit_actor_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if current_user = 'cp_tenant'
     and new.actor !~ '^user:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' then
    raise exception 'cp_tenant audit rows must name a console user as actor'
      using errcode = 'insufficient_privilege';
  end if;
  return new;
end
$$;
-- +goose StatementEnd
revoke execute on function cp.app_user_id() from cp_tenant;
drop function cp.app_user_id();
revoke update (role) on cp.tenant_members from cp_tenant;
grant update on cp.tenant_members to cp_tenant;
grant update (verify_method, verified_at, last_verify_at, last_verify_error, stellar_toml,
              stellar_toml_checked_at) on cp.sites to cp_tenant;
