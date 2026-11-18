-- how each soroban contract of a project is used, per function and utc
-- day (control-plane-api 15).
--
-- agg.chain_function_daily holds, for a contract day the chain-activity
-- task read, one row per function and kind of caller: 'account' when the
-- operation's host function invokes the contract, 'contract' when another
-- contract calls it during an invocation. calls are split into successful
-- and failed ones; failed is null for the calls of accounts, because every
-- transaction the task reads succeeded (a failed transaction keeps no
-- contract events, so the event source does not find it). accounts is the
-- number of distinct accounts that initiated the operations the calls
-- belong to, counted in memory while the day is read: no address and no
-- hash of one is stored. at most 64 functions per contract day keep their
-- own rows; the others add up under '{other}', which no soroban symbol can
-- spell.
--
-- agg.chain_daily.functions says how much of a contract day's calls was
-- read: 'read' (both kinds of caller), 'no_call_tree' (the source sent a
-- transaction without the diagnostic events that hold its call tree, so
-- only the calls of accounts are stored), 'day_over_read_cap' (none), or
-- 'before_source_history' (the day was stored before this migration and
-- is outside the source's window now). null for an account day, and for a
-- contract day the task has not read again yet.
--
-- the rows reference their day, so a day deleted goes with its functions.
-- ledger data is public; nothing here identifies an end user.
--
-- rules of 00003 apply: forced row-level security and exactly one
-- cp_tenant policy keyed on cp.current_tenant_id() on every table with a
-- tenant_id.

-- +goose Up
alter table agg.chain_daily add column functions text
  check (functions in ('read', 'no_call_tree', 'day_over_read_cap', 'before_source_history'));

create table agg.chain_function_daily (
  tenant_id     uuid not null,
  strkey        text not null check (strkey like 'C%'),
  day           date not null,
  function_name text not null check (function_name ~ '^[A-Za-z0-9_]{1,32}$' or function_name = '{other}'),
  caller        text not null check (caller in ('account', 'contract')),
  successful    bigint not null check (successful >= 0),
  failed        bigint check (failed >= 0),
  accounts      bigint not null check (accounts >= 0),
  primary key (tenant_id, strkey, day, caller, function_name),
  foreign key (tenant_id, strkey, day) references agg.chain_daily (tenant_id, strkey, day) on delete cascade,
  check ((caller = 'contract') = (failed is not null)),
  check (successful + coalesce(failed, 0) > 0),
  check (accounts <= successful + coalesce(failed, 0))
);
create index chain_function_daily_tenant_day_idx on agg.chain_function_daily (tenant_id, day);

alter table agg.chain_function_daily enable row level security;
alter table agg.chain_function_daily force row level security;

create policy chain_function_daily_tenant on agg.chain_function_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy chain_function_daily_operator on agg.chain_function_daily for select to cp_operator using (true);
create policy chain_function_daily_jobs on agg.chain_function_daily for all to jobs using (true) with check (true);
create policy chain_function_daily_purge on agg.chain_function_daily for delete to platform_owner using (true);
create policy chain_function_daily_purge_read on agg.chain_function_daily for select to platform_owner using (true);

revoke all on agg.chain_function_daily from public;
grant select on agg.chain_function_daily to cp_tenant, cp_operator;
grant select, insert, update, delete on agg.chain_function_daily to jobs;

-- +goose StatementBegin
-- agg.purge_chain of 00038 with the function rows: they are daily counts
-- and follow keep_aggregates like agg.chain_daily, whose rows they
-- reference.
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
    delete from agg.chain_function_daily f where f.tenant_id = tenant;
    get diagnostics n = row_count;
  end if;
  table_name := 'chain_function_daily'; rows_deleted := n; return next;
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
drop table agg.chain_function_daily;
alter table agg.chain_daily drop column functions;
