-- the on-chain footprint of a project and its daily ledger activity.
--
-- cp.chain_ids holds the stellar accounts (g...) and soroban contracts
-- (c...) a project declares, at enrollment or later in the console, with
-- the proof of control each one carries. a trigger on cp.tenants copies the
-- g and c ids of the enrollment answers into it, so enrollment code stays
-- unchanged. cp_tenant adds and removes rows of its own tenant; only
-- cp_system writes the challenge and verification columns, the same split
-- as the verification columns of cp.sites (00028), so no tenant handler can
-- mark an id verified.
--
-- agg.chain_daily holds one row per id and utc day that the jobs task
-- chain-activity read, zeros included, so a missing row means "not read"
-- and never "no activity". agg.chain_reads says why a day is missing: the
-- last error of each id and the days its source covers.
--
-- rows are keyed on (tenant_id, strkey), not on the footprint row id, so an
-- id removed and added again finds its history. the reads join
-- cp.chain_ids, so a removed id's rows are not shown. ledger data is
-- public; nothing here is personal data of an end user.
--
-- rules of 00003 apply: forced row-level security and exactly one
-- cp_tenant policy keyed on cp.current_tenant_id() on every table with a
-- tenant_id.

-- +goose Up
create table cp.chain_ids (
  id                   uuid primary key default gen_random_uuid(),
  tenant_id            uuid not null references cp.tenants (id) on delete cascade,
  strkey               text not null check (strkey ~ '^[GC][A-Z2-7]{55}$'),
  kind                 text not null check (kind in ('account', 'contract')),
  added_by             text not null check (added_by in ('enrollment', 'console')),
  created_at           timestamptz not null default now(),
  -- the pending challenge of an account: a random nonce and when it ends
  challenge_nonce      text check (challenge_nonce ~ '^[0-9a-f]{32}$'),
  challenge_expires_at timestamptz,
  verify_method        text check (verify_method in ('sep53', 'manage_data', 'contract_admin', 'contract_deployer')),
  verified_at          timestamptz,
  -- the verified account of the same tenant that proved a contract
  verified_via         text check (verified_via ~ '^[GC][A-Z2-7]{55}$'),
  -- what the proof read: the storage key of an admin, or a deployment hash
  verify_detail        jsonb check (verify_detail is null or jsonb_typeof(verify_detail) = 'object'),
  last_verify_at       timestamptz,
  last_verify_error    text,
  unique (tenant_id, strkey),
  check ((kind = 'account') = (strkey like 'G%')),
  check ((verify_method is null) = (verified_at is null)),
  check ((challenge_nonce is null) = (challenge_expires_at is null))
);
create index chain_ids_strkey_idx on cp.chain_ids (strkey);

create table agg.chain_daily (
  tenant_id      uuid not null,
  strkey         text not null,
  day            date not null,
  -- horizon for accounts, rpc events for contracts (control-plane-api 15)
  source         text not null check (source in ('horizon', 'rpc_events')),
  operations     bigint not null check (operations >= 0),
  transactions   bigint not null check (transactions >= 0 and transactions <= operations),
  counterparties bigint not null check (counterparties >= 0),
  read_at        timestamptz not null default now(),
  primary key (tenant_id, strkey, day)
);
create index chain_daily_tenant_day_idx on agg.chain_daily (tenant_id, day);

create table agg.chain_reads (
  tenant_id         uuid not null,
  strkey            text not null,
  -- the first and last utc day with a row in agg.chain_daily
  covered_from      date,
  covered_to        date,
  last_attempt_at   timestamptz not null,
  last_success_at   timestamptz,
  last_error        text,
  last_error_at     timestamptz,
  -- accounts: the home_domain of the ledger entry at the last read
  home_domain       text,
  account_exists    boolean,
  primary key (tenant_id, strkey),
  check ((covered_from is null) = (covered_to is null)),
  check (covered_to is null or covered_to >= covered_from),
  check ((last_error is null) = (last_error_at is null))
);

alter table cp.chain_ids enable row level security;
alter table cp.chain_ids force row level security;
alter table agg.chain_daily enable row level security;
alter table agg.chain_daily force row level security;
alter table agg.chain_reads enable row level security;
alter table agg.chain_reads force row level security;

create policy chain_ids_tenant on cp.chain_ids for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy chain_daily_tenant on agg.chain_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy chain_reads_tenant on agg.chain_reads for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());

create policy chain_ids_system on cp.chain_ids for all to cp_system using (true) with check (true);
create policy chain_ids_operator on cp.chain_ids for select to cp_operator using (true);
create policy chain_ids_jobs on cp.chain_ids for select to jobs using (true);
create policy chain_daily_operator on agg.chain_daily for select to cp_operator using (true);
create policy chain_reads_operator on agg.chain_reads for select to cp_operator using (true);
create policy chain_daily_jobs on agg.chain_daily for all to jobs using (true) with check (true);
create policy chain_reads_jobs on agg.chain_reads for all to jobs using (true) with check (true);

revoke all on cp.chain_ids, agg.chain_daily, agg.chain_reads from public;
-- a tenant adds and removes its own ids; the challenge and verification
-- columns are cp_system's
grant select, delete on cp.chain_ids to cp_tenant;
grant insert (tenant_id, strkey, kind, added_by) on cp.chain_ids to cp_tenant;
grant select, insert, update, delete on cp.chain_ids to cp_system;
grant select on cp.chain_ids to cp_operator, jobs;
-- the tenant reads its own activity and the read state of its ids
grant select on agg.chain_daily, agg.chain_reads to cp_tenant;
grant select on agg.chain_daily, agg.chain_reads to cp_operator;
grant select, insert, update, delete on agg.chain_daily, agg.chain_reads to jobs;
-- the tenant overview export reads its own web aggregates; the cp_tenant
-- policy of 00019 already keys every row on the tenant
grant select on agg.web_daily to cp_tenant;
grant usage on schema agg to cp_tenant;

-- +goose StatementBegin
-- copies the g and c ids of the enrollment answers into cp.chain_ids. on
-- an update only the ids that the new answers add are copied, so an id the
-- tenant removed in the console stays removed until the answers name it
-- again. m (muxed) ids name a sub-account of a g account and are left to
-- the console, where the tenant adds the base account.
create function cp.chain_ids_from_enrollment() returns trigger
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  old_ids jsonb := '[]'::jsonb;
begin
  if tg_op = 'UPDATE' then
    old_ids := coalesce(old.enrollment_answers -> 'stellar_ids', '[]'::jsonb);
  end if;
  if jsonb_typeof(coalesce(new.enrollment_answers -> 'stellar_ids', '[]'::jsonb)) <> 'array' then
    return new;
  end if;
  insert into cp.chain_ids (tenant_id, strkey, kind, added_by)
  select new.id, s, case when s like 'G%' then 'account' else 'contract' end, 'enrollment'
  from jsonb_array_elements_text(coalesce(new.enrollment_answers -> 'stellar_ids', '[]'::jsonb)) as e(s)
  where s ~ '^[GC][A-Z2-7]{55}$'
    and not (jsonb_typeof(old_ids) = 'array' and old_ids ? s)
  on conflict (tenant_id, strkey) do nothing;
  return new;
end
$$;
-- +goose StatementEnd
revoke all on function cp.chain_ids_from_enrollment() from public;
create trigger tenants_chain_ids_from_enrollment
  after insert or update of enrollment_answers on cp.tenants
  for each row execute function cp.chain_ids_from_enrollment();
-- the trigger runs as the function's owner under forced row-level security
create policy chain_ids_enrollment on cp.chain_ids for insert to platform_owner with check (true);
create policy chain_ids_enrollment_read on cp.chain_ids for select to platform_owner using (true);

-- the projects enrolled before this migration
insert into cp.chain_ids (tenant_id, strkey, kind, added_by)
select t.id, s, case when s like 'G%' then 'account' else 'contract' end, 'enrollment'
from cp.tenants t,
     jsonb_array_elements_text(case when jsonb_typeof(t.enrollment_answers -> 'stellar_ids') = 'array'
                                    then t.enrollment_answers -> 'stellar_ids' else '[]'::jsonb end) as e(s)
where s ~ '^[GC][A-Z2-7]{55}$'
on conflict (tenant_id, strkey) do nothing;

create policy chain_daily_purge on agg.chain_daily for delete to platform_owner using (true);
create policy chain_reads_purge on agg.chain_reads for delete to platform_owner using (true);
create policy chain_daily_purge_read on agg.chain_daily for select to platform_owner using (true);
create policy chain_reads_purge_read on agg.chain_reads for select to platform_owner using (true);

-- +goose StatementBegin
-- deletes a removed tenant's chain rows and returns how many went per
-- table. the read state always goes; the daily counts go unless
-- keep_aggregates. it refuses a tenant that is not removed and takes the
-- tenant's advisory lock of 00019 exclusively, while chain-activity holds
-- it shared for every write of that tenant. cp.chain_ids goes with the
-- tenant row (on delete cascade) or stays as the record of what was
-- declared.
create function agg.purge_chain(tenant uuid, keep_aggregates boolean)
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
revoke all on function agg.purge_chain(uuid, boolean) from public;
grant execute on function agg.purge_chain(uuid, boolean) to cp_system;

-- +goose Down
revoke execute on function agg.purge_chain(uuid, boolean) from cp_system;
drop function agg.purge_chain(uuid, boolean);
drop trigger tenants_chain_ids_from_enrollment on cp.tenants;
drop function cp.chain_ids_from_enrollment();
revoke usage on schema agg from cp_tenant;
revoke select on agg.web_daily from cp_tenant;
drop table agg.chain_reads;
drop table agg.chain_daily;
drop table cp.chain_ids;
