-- rights and row-level security for the cp schema.
--
-- rules this file follows:
-- * row-level security is enabled and forced on every table with a tenant_id
--   column, plus cp.tenants (keyed on id) and cp.users (visible to a tenant
--   only through membership). forced means it also binds platform_owner.
-- * every such table has exactly one policy for cp_tenant, keyed on
--   cp.current_tenant_id(). internal/db checks this after every migration and
--   refuses a database where a tenant table lacks it.
-- * no policy is granted to public. every role that needs rows from a
--   protected table gets its own policy, so a missing policy means no rows.
-- * select is granted next to insert or update where postgresql needs it:
--   update ... where reads columns, insert ... on conflict do update reads the
--   conflicting row.

-- +goose Up

-- +goose StatementBegin
-- the setting is absent outside db.WithTenant, and an empty string after a
-- transaction that set it locally has ended; both mean no tenant and match no row
create function cp.current_tenant_id() returns uuid
language sql stable parallel safe
as $$ select nullif(current_setting('app.tenant_id', true), '')::uuid $$;
-- +goose StatementEnd
grant execute on function cp.current_tenant_id() to cp_tenant;

-- +goose StatementBegin
create function cp.audit_log_append_only() returns trigger
language plpgsql
as $$
begin
  raise exception 'cp.audit_log is append-only, % refused', tg_op
    using errcode = 'insufficient_privilege';
end
$$;
-- +goose StatementEnd

-- grants already keep application roles out; the triggers also stop the operator
-- and superusers from rewriting history by accident
create trigger audit_log_no_change before update or delete on cp.audit_log
  for each row execute function cp.audit_log_append_only();
create trigger audit_log_no_truncate before truncate on cp.audit_log
  for each statement execute function cp.audit_log_append_only();

-- +goose StatementBegin
-- a fencing token only works if it never goes backwards. the worker's own
-- update carries "where revision <= $new", and this trigger makes any other
-- writer obey the same rule.
create function cp.leader_fence_monotonic() returns trigger
language plpgsql
as $$
begin
  if new.name <> old.name then
    raise exception 'cp.leader_fence: name of fence % cannot change', old.name
      using errcode = 'check_violation';
  end if;
  if new.revision < old.revision then
    raise exception 'cp.leader_fence: revision % for % is older than stored revision %',
      new.revision, old.name, old.revision
      using errcode = 'check_violation';
  end if;
  return new;
end
$$;
-- +goose StatementEnd

create trigger leader_fence_monotonic before update on cp.leader_fence
  for each row execute function cp.leader_fence_monotonic();

-- operator view of sites without the verification secret. it runs with the
-- owner's rights (not security_invoker), so cp_operator needs no right on
-- cp.sites itself; security_barrier keeps operator-supplied predicates from
-- running before the view's own.
create view cp.operator_sites with (security_barrier) as
select id, tenant_id, name, domain, origins, quarantine_website_id, status,
       verify_method, verified_at, last_verify_at, last_verify_error,
       stellar_toml, stellar_toml_checked_at, created_at, updated_at
from cp.sites;

-- row-level security

alter table cp.users enable row level security;
alter table cp.users force row level security;
alter table cp.tenants enable row level security;
alter table cp.tenants force row level security;
alter table cp.tenant_members enable row level security;
alter table cp.tenant_members force row level security;
alter table cp.invites enable row level security;
alter table cp.invites force row level security;
alter table cp.sites enable row level security;
alter table cp.sites force row level security;
alter table cp.otlp_tokens enable row level security;
alter table cp.otlp_tokens force row level security;
alter table cp.erasure_requests enable row level security;
alter table cp.erasure_requests force row level security;
alter table cp.web_ingest_hourly enable row level security;
alter table cp.web_ingest_hourly force row level security;

-- cp_tenant: one policy per table
create policy tenants_tenant on cp.tenants for all to cp_tenant
  using (id = cp.current_tenant_id()) with check (id = cp.current_tenant_id());
create policy tenant_members_tenant on cp.tenant_members for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy invites_tenant on cp.invites for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy sites_tenant on cp.sites for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy otlp_tokens_tenant on cp.otlp_tokens for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy erasure_requests_tenant on cp.erasure_requests for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy web_ingest_hourly_tenant on cp.web_ingest_hourly for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
-- a tenant sees the users who are its members, for member lists and invites
create policy users_tenant on cp.users for select to cp_tenant
  using (exists (
    select 1 from cp.tenant_members m
    where m.user_id = users.id and m.tenant_id = cp.current_tenant_id()));

-- cp_system: every row
create policy users_system on cp.users for all to cp_system using (true) with check (true);
create policy tenants_system on cp.tenants for all to cp_system using (true) with check (true);
create policy tenant_members_system on cp.tenant_members for all to cp_system using (true) with check (true);
create policy invites_system on cp.invites for all to cp_system using (true) with check (true);
create policy sites_system on cp.sites for all to cp_system using (true) with check (true);
create policy otlp_tokens_system on cp.otlp_tokens for all to cp_system using (true) with check (true);
create policy erasure_requests_system on cp.erasure_requests for all to cp_system using (true) with check (true);
create policy web_ingest_hourly_system on cp.web_ingest_hourly for all to cp_system using (true) with check (true);

-- cp_operator: aggregate views across tenants
create policy tenants_operator on cp.tenants for select to cp_operator using (true);
create policy web_ingest_hourly_operator on cp.web_ingest_hourly for select to cp_operator using (true);

-- platform_owner reads sites only so that cp.operator_sites, which runs with
-- the operator's rights under forced row-level security, returns rows
create policy sites_operator_view on cp.sites for select to platform_owner using (true);

-- metrics_writer: finds a token by id and stamps last_seen_at, nothing more
create policy otlp_tokens_writer_read on cp.otlp_tokens for select to metrics_writer using (true);
create policy otlp_tokens_writer_touch on cp.otlp_tokens for update to metrics_writer
  using (true) with check (true);

-- jobs: folds EDGE_STATS counters into hourly rows
create policy web_ingest_hourly_jobs on cp.web_ingest_hourly for all to jobs using (true) with check (true);

-- table rights

revoke all on all tables in schema cp from public;

-- cp_system: all cp tables without a row filter; the audit log is append-only
grant select, insert, update, delete on
  cp.users, cp.sessions, cp.tenants, cp.tenant_members, cp.invites, cp.sites,
  cp.otlp_tokens, cp.operator_api_keys, cp.api_key_usage_hourly, cp.settings,
  cp.erasure_requests, cp.web_ingest_hourly, cp.stats_dedup, cp.leader_fence
  to cp_system;
grant select, insert on cp.audit_log to cp_system;
grant select on cp.operator_sites to cp_system;

-- cp_tenant: tenant-scoped tables under row-level security
grant select (id, github_id, github_login) on cp.users to cp_tenant;
grant select on cp.tenants to cp_tenant;
grant update (name, description, updated_at) on cp.tenants to cp_tenant;
grant select, insert, update, delete on cp.tenant_members, cp.invites, cp.sites, cp.otlp_tokens to cp_tenant;
grant select, insert on cp.erasure_requests to cp_tenant;
grant select on cp.web_ingest_hourly to cp_tenant;

-- cp_operator: no sessions, members, invites, otlp tokens or site secrets
grant select on cp.tenants, cp.operator_sites, cp.web_ingest_hourly, cp.settings, cp.operator_api_keys
  to cp_operator;
grant update (last_used_at) on cp.operator_api_keys to cp_operator;
grant select, insert, update on cp.api_key_usage_hourly to cp_operator;

-- metrics_writer: the token id to match on, last_seen_at to write, the fence
grant select (id) on cp.otlp_tokens to metrics_writer;
grant update (last_seen_at) on cp.otlp_tokens to metrics_writer;
grant select, update on cp.leader_fence to metrics_writer;

-- jobs: edge counters, their dedup ids and the fence
grant select, insert, update on cp.web_ingest_hourly, cp.stats_dedup to jobs;
grant select, update on cp.leader_fence to jobs;

-- +goose Down
revoke all on all tables in schema cp from cp_system, cp_tenant, cp_operator, metrics_writer, jobs;
drop view cp.operator_sites;
drop trigger leader_fence_monotonic on cp.leader_fence;
drop trigger audit_log_no_truncate on cp.audit_log;
drop trigger audit_log_no_change on cp.audit_log;
drop function cp.leader_fence_monotonic();
drop function cp.audit_log_append_only();

drop policy web_ingest_hourly_jobs on cp.web_ingest_hourly;
drop policy otlp_tokens_writer_touch on cp.otlp_tokens;
drop policy otlp_tokens_writer_read on cp.otlp_tokens;
drop policy sites_operator_view on cp.sites;
drop policy web_ingest_hourly_operator on cp.web_ingest_hourly;
drop policy tenants_operator on cp.tenants;
drop policy web_ingest_hourly_system on cp.web_ingest_hourly;
drop policy erasure_requests_system on cp.erasure_requests;
drop policy otlp_tokens_system on cp.otlp_tokens;
drop policy sites_system on cp.sites;
drop policy invites_system on cp.invites;
drop policy tenant_members_system on cp.tenant_members;
drop policy tenants_system on cp.tenants;
drop policy users_system on cp.users;
drop policy users_tenant on cp.users;
drop policy web_ingest_hourly_tenant on cp.web_ingest_hourly;
drop policy erasure_requests_tenant on cp.erasure_requests;
drop policy otlp_tokens_tenant on cp.otlp_tokens;
drop policy sites_tenant on cp.sites;
drop policy invites_tenant on cp.invites;
drop policy tenant_members_tenant on cp.tenant_members;
drop policy tenants_tenant on cp.tenants;

alter table cp.users disable row level security;
alter table cp.users no force row level security;
alter table cp.tenants disable row level security;
alter table cp.tenants no force row level security;
alter table cp.tenant_members disable row level security;
alter table cp.tenant_members no force row level security;
alter table cp.invites disable row level security;
alter table cp.invites no force row level security;
alter table cp.sites disable row level security;
alter table cp.sites no force row level security;
alter table cp.otlp_tokens disable row level security;
alter table cp.otlp_tokens no force row level security;
alter table cp.erasure_requests disable row level security;
alter table cp.erasure_requests no force row level security;
alter table cp.web_ingest_hourly disable row level security;
alter table cp.web_ingest_hourly no force row level security;

drop function cp.current_tenant_id();
