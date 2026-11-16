-- signed usage reports (control-plane-api section 20). a member of a
-- project generates a report for a period; the control plane serialises it
-- in its canonical json form, signs those bytes with the platform's report
-- key (sep-53) and stores both here. the row is immutable: body,
-- signature, signer and every column that describes them can never change,
-- for any role. a member may only revoke the report's link (revoked_at and
-- revoked_by, once), and the public report route counts views.
--
-- id is the unguessable part of the shareable link
-- https://app.<BASE_DOMAIN>/r/<id>: 128 random bits in lowercase base32.
-- the public route reads one row by that id under cp_system and nothing
-- else.
--
-- a report holds aggregates only: project totals and daily sums, the
-- integrity grade and its reasons, the cohort and percentiles. no visitor,
-- address, user agent or member is in it.
--
-- rules of 00003 apply: forced row-level security and exactly one
-- cp_tenant policy keyed on cp.current_tenant_id() on every table with a
-- tenant_id.

-- +goose Up
create table cp.usage_reports (
  id             text primary key check (id ~ '^[a-z2-7]{26}$'),
  tenant_id      uuid not null references cp.tenants (id) on delete cascade,
  period_from    date not null,
  period_to      date not null,
  -- the exact canonical json bytes that were signed, as utf-8 text
  body           text not null check (body <> ''),
  -- the sep-53 signature of body, base64, and the g address that made it
  signature      text not null check (signature ~ '^[A-Za-z0-9+/]{86}==$'),
  signer         text not null check (signer ~ '^G[A-Z2-7]{55}$'),
  created_by     uuid references cp.users (id) on delete set null,
  created_at     timestamptz not null default now(),
  revoked_at     timestamptz,
  revoked_by     uuid references cp.users (id) on delete set null,
  view_count     bigint not null default 0 check (view_count >= 0),
  last_viewed_at timestamptz,
  check (period_to >= period_from),
  check (revoked_by is null or revoked_at is not null)
);
create index usage_reports_tenant_idx on cp.usage_reports (tenant_id, created_at desc);

alter table cp.usage_reports enable row level security;
alter table cp.usage_reports force row level security;

create policy usage_reports_tenant on cp.usage_reports for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy usage_reports_system on cp.usage_reports for all to cp_system using (true) with check (true);
create policy usage_reports_operator on cp.usage_reports for select to cp_operator using (true);

revoke all on cp.usage_reports from public;
-- the public route reads a report and counts its views under cp_system
grant select, update (view_count, last_viewed_at) on cp.usage_reports to cp_system;
grant select on cp.usage_reports to cp_operator;
grant select on cp.usage_reports to cp_tenant;
grant insert (id, tenant_id, period_from, period_to, body, signature, signer, created_by)
  on cp.usage_reports to cp_tenant;
grant update (revoked_at, revoked_by) on cp.usage_reports to cp_tenant;

-- +goose StatementBegin
-- the report itself never changes once stored: only a revocation (once,
-- by the session user under cp_tenant) and the view counter (cp_system)
-- may be written. an insert under cp_tenant names the session user as its
-- author and starts unrevoked and unviewed.
create function cp.usage_report_guard() returns trigger
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  if tg_op = 'INSERT' then
    -- nested, so cp.app_user_id() runs only as cp_tenant, the one role
    -- allowed to execute it
    if current_user = 'cp_tenant' then
      if new.created_by is distinct from cp.app_user_id() or new.revoked_at is not null
         or new.view_count <> 0 or new.last_viewed_at is not null then
        raise exception 'cp_tenant may only store a new report by the session user'
          using errcode = 'insufficient_privilege';
      end if;
    end if;
    return new;
  end if;
  if new.id is distinct from old.id or new.tenant_id is distinct from old.tenant_id
     or new.period_from is distinct from old.period_from or new.period_to is distinct from old.period_to
     or new.body is distinct from old.body or new.signature is distinct from old.signature
     or new.signer is distinct from old.signer or new.created_at is distinct from old.created_at
     or (new.created_by is distinct from old.created_by and new.created_by is not null) then
    raise exception 'a usage report is immutable'
      using errcode = 'insufficient_privilege';
  end if;
  if new.revoked_at is distinct from old.revoked_at or new.revoked_by is distinct from old.revoked_by then
    if old.revoked_at is not null and new.revoked_at is distinct from old.revoked_at then
      raise exception 'a revoked report link stays revoked'
        using errcode = 'insufficient_privilege';
    end if;
    if current_user = 'cp_tenant' then
      if new.revoked_by is distinct from cp.app_user_id() then
        raise exception 'cp_tenant revocations must name the session user'
          using errcode = 'insufficient_privilege';
      end if;
    end if;
  end if;
  return new;
end
$$;
-- +goose StatementEnd
revoke all on function cp.usage_report_guard() from public;
create trigger usage_reports_guard before insert or update on cp.usage_reports
  for each row execute function cp.usage_report_guard();

-- the removal purge deletes a removed tenant's reports through the
-- function below; it runs with platform_owner's rights
create policy usage_reports_purge on cp.usage_reports for delete to platform_owner using (true);
create policy usage_reports_purge_read on cp.usage_reports for select to platform_owner using (true);

-- +goose StatementBegin
-- deletes a removed tenant's usage reports and returns how many went. a
-- shared link of a removed tenant already answers 404; the purge removes the
-- stored reports as well. cp_system has no delete right on the table, so a
-- handler can never delete a report of a tenant that is not removed.
create function cp.purge_usage_reports(tenant uuid)
returns bigint
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  n bigint;
begin
  if tenant is null then
    raise exception 'cp.purge_usage_reports: tenant is required'
      using errcode = 'null_value_not_allowed';
  end if;
  if not exists (select 1 from cp.tenants t where t.id = tenant and t.status = 'removed') then
    raise exception 'cp.purge_usage_reports: tenant % is not removed', tenant
      using errcode = 'object_not_in_prerequisite_state';
  end if;
  delete from cp.usage_reports r where r.tenant_id = tenant;
  get diagnostics n = row_count;
  return n;
end
$$;
-- +goose StatementEnd
revoke all on function cp.purge_usage_reports(uuid) from public;
grant execute on function cp.purge_usage_reports(uuid) to cp_system;

-- +goose Down
revoke execute on function cp.purge_usage_reports(uuid) from cp_system;
drop function cp.purge_usage_reports(uuid);
drop trigger usage_reports_guard on cp.usage_reports;
drop function cp.usage_report_guard();
drop table cp.usage_reports;
