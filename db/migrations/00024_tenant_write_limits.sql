-- cp_tenant held update on every column of cp.sites and
-- cp.otlp_tokens and could append audit rows with any actor. row-level
-- security keeps those writes inside the caller's tenant, but one handler
-- bug could still let a tenant re-activate a site an operator put in
-- review, reset a site's verify token, re-activate a revoked token or
-- write audit rows that look like an operator's. this narrows cp_tenant to
-- what its handlers write:
--
-- - cp.sites: update of name, origins and updated_at (site edit), status
--   (delete only, enforced below), and the verification and stellar.toml
--   columns, which the tenant's own verify route writes under cp_tenant
--   (contract 5.8 and 5.12). id, tenant_id, domain, quarantine_website_id,
--   verify_token and created_at are no longer writable.
-- - cp.otlp_tokens: update of status and revoked_at only (revoke).
-- - a trigger allows cp_tenant to move a site only to 'removed' and a token
--   only from 'active' to 'revoked', so neither can be re-activated.
-- - a trigger requires cp_tenant audit rows to name a console user as the
--   actor ('user:<uuid>'), the only actor tenant handlers write. operator
--   and system actors come from cp_system alone.
--
-- the triggers test current_user, which is the login role of the pool: the
-- application roles never switch role, and cp_system keeps every
-- transition the reconciler and the operator actions need.

-- +goose Up
revoke update on cp.sites, cp.otlp_tokens from cp_tenant;
grant update (name, origins, updated_at, status, verify_method, verified_at, last_verify_at,
              last_verify_error, stellar_toml, stellar_toml_checked_at) on cp.sites to cp_tenant;
grant update (status, revoked_at) on cp.otlp_tokens to cp_tenant;

-- +goose StatementBegin
create function cp.tenant_site_status_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if current_user = 'cp_tenant' and new.status is distinct from old.status and new.status <> 'removed' then
    raise exception 'cp_tenant may only change a site''s status to removed'
      using errcode = 'insufficient_privilege';
  end if;
  return new;
end
$$;
-- +goose StatementEnd
create trigger sites_tenant_status_guard before update of status on cp.sites
  for each row execute function cp.tenant_site_status_guard();

-- +goose StatementBegin
create function cp.tenant_token_status_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if current_user = 'cp_tenant'
     and (new.status is distinct from old.status or new.revoked_at is distinct from old.revoked_at)
     and not (old.status = 'active' and new.status = 'revoked' and new.revoked_at is not null) then
    raise exception 'cp_tenant may only revoke an active token'
      using errcode = 'insufficient_privilege';
  end if;
  return new;
end
$$;
-- +goose StatementEnd
create trigger otlp_tokens_tenant_status_guard before update of status, revoked_at on cp.otlp_tokens
  for each row execute function cp.tenant_token_status_guard();

-- +goose StatementBegin
create function cp.tenant_audit_actor_guard() returns trigger
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
create trigger audit_log_tenant_actor_guard before insert on cp.audit_log
  for each row execute function cp.tenant_audit_actor_guard();

-- trigger functions only; no role calls them directly
revoke all on function cp.tenant_site_status_guard(), cp.tenant_token_status_guard(),
  cp.tenant_audit_actor_guard() from public;

-- +goose Down
drop trigger audit_log_tenant_actor_guard on cp.audit_log;
drop function cp.tenant_audit_actor_guard();
drop trigger otlp_tokens_tenant_status_guard on cp.otlp_tokens;
drop function cp.tenant_token_status_guard();
drop trigger sites_tenant_status_guard on cp.sites;
drop function cp.tenant_site_status_guard();
revoke update on cp.sites, cp.otlp_tokens from cp_tenant;
grant update on cp.sites, cp.otlp_tokens to cp_tenant;
