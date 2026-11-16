-- weekly active accounts of a project's on-chain footprint, by the scf
-- growth hack definition: an active account is an account that initiated
-- at least one operation in the week. the footprint is the project's
-- verified accounts and contracts; the week is the iso week in utc.
--
-- agg.chain_weeks holds one row per project and week. while the week is
-- open it carries a random hmac key, and agg.chain_week_members holds the
-- keyed hash of every account that initiated an operation involving one of
-- the project's ids, so the job counts distinct accounts across days
-- without storing an address. when the week closes the job stores the
-- count, or the reason it has none, deletes the members and drops the key,
-- so nothing about which accounts were active outlives the week.
--
-- agg.chain_daily.initiators says whether a stored day's initiating
-- accounts went into its week: 'read' when they did, 'day_over_read_cap'
-- or 'before_source_history' when they could not, and null for a day
-- outside the weeks still open, or stored before this migration (the job
-- reads such a day again while its week is open). a week counts only when
-- every verified id has seven 'read' days in it.
--
-- rules of 00003 apply: forced row-level security and exactly one
-- cp_tenant policy keyed on cp.current_tenant_id() on every table with a
-- tenant_id.

-- +goose Up
alter table agg.chain_daily add column initiators text
  check (initiators in ('read', 'day_over_read_cap', 'before_source_history'));

create table agg.chain_weeks (
  tenant_id       uuid not null,
  -- the monday of the iso week, utc
  week            date not null check (extract(isodow from week) = 1),
  hash_key        bytea check (hash_key is null or octet_length(hash_key) = 32),
  active_accounts bigint check (active_accounts >= 0),
  missing         text,
  closed_at       timestamptz,
  created_at      timestamptz not null default now(),
  primary key (tenant_id, week),
  -- an open week has a key and no result; a closed week has exactly one of
  -- a count and a reason, and no key
  check ((closed_at is null) = (hash_key is not null)),
  check (closed_at is null or ((active_accounts is null) <> (missing is null))),
  check (closed_at is not null or (active_accounts is null and missing is null))
);

create table agg.chain_week_members (
  tenant_id uuid not null,
  week      date not null,
  -- the declared id whose operation the account initiated
  strkey    text not null,
  -- hmac-sha256 of the account under the week's key
  member    bytea not null check (octet_length(member) = 32),
  primary key (tenant_id, week, strkey, member),
  foreign key (tenant_id, week) references agg.chain_weeks (tenant_id, week) on delete cascade
);

alter table agg.chain_weeks enable row level security;
alter table agg.chain_weeks force row level security;
alter table agg.chain_week_members enable row level security;
alter table agg.chain_week_members force row level security;

create policy chain_weeks_tenant on agg.chain_weeks for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy chain_week_members_tenant on agg.chain_week_members for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy chain_weeks_operator on agg.chain_weeks for select to cp_operator using (true);
create policy chain_weeks_jobs on agg.chain_weeks for all to jobs using (true) with check (true);
create policy chain_week_members_jobs on agg.chain_week_members for all to jobs using (true) with check (true);

-- the default privileges of 00001 give cp_operator select on every agg
-- table; the key column and the members are taken back from it
revoke all on agg.chain_weeks, agg.chain_week_members from public, cp_operator;
-- the tenant and operators read the counts; the key column is the jobs
-- task's alone, and the members table is never readable outside it
grant select (tenant_id, week, active_accounts, missing, closed_at, created_at) on agg.chain_weeks to cp_tenant, cp_operator;
grant select, insert, update, delete on agg.chain_weeks, agg.chain_week_members to jobs;

create policy chain_weeks_purge on agg.chain_weeks for all to platform_owner using (true) with check (true);
create policy chain_week_members_purge on agg.chain_week_members for delete to platform_owner using (true);
create policy chain_week_members_purge_read on agg.chain_week_members for select to platform_owner using (true);

-- +goose StatementBegin
-- agg.purge_chain of 00032 with the weeks: the members and the keys always
-- go, the closed counts follow keep_aggregates like the daily counts.
create or replace function agg.purge_chain(tenant uuid, keep_aggregates boolean)
returns table (table_name text, rows_deleted bigint)
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  n bigint;
begin
  if tenant is null or keep_aggregates is null then
    raise exception 'agg.purge_chain: tenant and keep_aggregates are required'
      using errcode = 'null_value_not_allowed';
  end if;
  if not exists (select 1 from cp.tenants t where t.id = tenant and t.status = 'removed') then
    raise exception 'agg.purge_chain: tenant % is not removed', tenant
      using errcode = 'object_not_in_prerequisite_state';
  end if;
  perform pg_advisory_xact_lock(hashtextextended('agg.tenant:' || tenant::text, 0));

  delete from agg.chain_reads r where r.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'chain_reads'; rows_deleted := n; return next;
  n := 0;
  if not keep_aggregates then
    delete from agg.chain_daily d where d.tenant_id = tenant;
    get diagnostics n = row_count;
  end if;
  table_name := 'chain_daily'; rows_deleted := n; return next;

  delete from agg.chain_week_members m where m.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'chain_week_members'; rows_deleted := n; return next;
  -- an open week has no count to keep, so it goes in both cases
  if keep_aggregates then
    delete from agg.chain_weeks w where w.tenant_id = tenant and w.closed_at is null;
  else
    delete from agg.chain_weeks w where w.tenant_id = tenant;
  end if;
  get diagnostics n = row_count;
  table_name := 'chain_weeks'; rows_deleted := n; return next;
end
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
create or replace function agg.purge_chain(tenant uuid, keep_aggregates boolean)
returns table (table_name text, rows_deleted bigint)
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  n bigint;
begin
  if tenant is null or keep_aggregates is null then
    raise exception 'agg.purge_chain: tenant and keep_aggregates are required'
      using errcode = 'null_value_not_allowed';
  end if;
  if not exists (select 1 from cp.tenants t where t.id = tenant and t.status = 'removed') then
    raise exception 'agg.purge_chain: tenant % is not removed', tenant
      using errcode = 'object_not_in_prerequisite_state';
  end if;
  perform pg_advisory_xact_lock(hashtextextended('agg.tenant:' || tenant::text, 0));

  delete from agg.chain_reads r where r.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'chain_reads'; rows_deleted := n; return next;
  n := 0;
  if not keep_aggregates then
    delete from agg.chain_daily d where d.tenant_id = tenant;
    get diagnostics n = row_count;
  end if;
  table_name := 'chain_daily'; rows_deleted := n; return next;
end
$$;
-- +goose StatementEnd
drop table agg.chain_week_members;
drop table agg.chain_weeks;
alter table agg.chain_daily drop column initiators;
