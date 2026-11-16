-- integrity grade of web traffic. the collect gateway classifies the
-- network of every accepted event (access, organisation, hosting,
-- anonymiser, unknown) from open data and reports, per site, how many events
-- each class carried and which networks the site saw today, as keyed hashes
-- under the gateways' daily secret. the jobs task edgestats stores both
-- here; the jobs task integrity grades every site and day from them, from
-- the outcome counters and from the web aggregates, and stores the inputs,
-- the grade and its reasons in agg.web_integrity_daily.
--
-- no address and no autonomous system number is stored: the evidence rows
-- hold a class and a count, the network rows a 64-bit hmac whose secret is
-- gone after its utc day, so the keys count distinct networks and can never
-- be linked to one. the integrity task deletes network rows once the day
-- can no longer be graded again.
--
-- rules of 00003 apply: forced row-level security and exactly one cp_tenant
-- policy keyed on cp.current_tenant_id() on every table with a tenant_id.

-- +goose Up
create table cp.web_evidence_daily (
  site_id   uuid not null,
  tenant_id uuid not null,
  day       date not null,
  class     text not null check (class in ('access', 'organisation', 'hosting', 'anonymiser', 'unknown')),
  count     bigint not null default 0 check (count >= 0),
  primary key (site_id, day, class)
);
create index web_evidence_daily_tenant_idx on cp.web_evidence_daily (tenant_id, day);

create table cp.web_networks_daily (
  site_id   uuid not null,
  tenant_id uuid not null,
  day       date not null,
  network   text not null check (network ~ '^[0-9a-f]{16}$'),
  primary key (site_id, day, network)
);
create index web_networks_daily_tenant_idx on cp.web_networks_daily (tenant_id, day);

-- one row per site and utc day the integrity task graded. grade is null
-- when the day had too little traffic; reasons then says so. closed is
-- false while the day can still change (today and the reread window).
create table agg.web_integrity_daily (
  site_id           uuid not null,
  tenant_id         uuid not null,
  day               date not null,
  rule_version      integer not null check (rule_version > 0),
  grade             text check (grade in ('A', 'B', 'C')),
  closed            boolean not null,
  events            bigint not null check (events >= 0),
  quarantined       bigint not null check (quarantined >= 0),
  proof_failures    bigint not null check (proof_failures >= 0),
  net_access        bigint not null check (net_access >= 0),
  net_organisation  bigint not null check (net_organisation >= 0),
  net_hosting       bigint not null check (net_hosting >= 0),
  net_anonymiser    bigint not null check (net_anonymiser >= 0),
  net_unknown       bigint not null check (net_unknown >= 0),
  networks          bigint not null check (networks >= 0),
  visitors          bigint not null check (visitors >= 0),
  pageviews         bigint not null check (pageviews >= 0),
  -- median primary visitors of up to 28 earlier days with data; null with
  -- none. the reconciler publishes it to the gateways as the site's scale
  baseline_visitors bigint check (baseline_visitors >= 0),
  baseline_days     integer not null check (baseline_days between 0 and 28),
  -- array of {code, level, value, limit} (internal/integrity)
  reasons           jsonb not null check (jsonb_typeof(reasons) = 'array'),
  computed_at       timestamptz not null default now(),
  primary key (site_id, day)
);
create index web_integrity_daily_tenant_day_idx on agg.web_integrity_daily (tenant_id, day);

alter table cp.web_evidence_daily enable row level security;
alter table cp.web_evidence_daily force row level security;
alter table cp.web_networks_daily enable row level security;
alter table cp.web_networks_daily force row level security;
alter table agg.web_integrity_daily enable row level security;
alter table agg.web_integrity_daily force row level security;

-- cp_tenant: a project reads its own grades, the same rows operators read.
-- the raw evidence and network rows get no grant; their policies exist so a
-- later grant cannot expose other tenants' rows
create policy web_evidence_daily_tenant on cp.web_evidence_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy web_networks_daily_tenant on cp.web_networks_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy web_integrity_daily_tenant on agg.web_integrity_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());

-- cp_system deletes a purged tenant's evidence and network rows
create policy web_evidence_daily_system on cp.web_evidence_daily for all to cp_system using (true) with check (true);
create policy web_networks_daily_system on cp.web_networks_daily for all to cp_system using (true) with check (true);

-- cp_operator: grades across tenants, read only
create policy web_integrity_daily_operator on agg.web_integrity_daily for select to cp_operator using (true);

-- jobs: edgestats writes evidence and networks, integrity reads them, writes
-- the grades and prunes old network rows
create policy web_evidence_daily_jobs on cp.web_evidence_daily for all to jobs using (true) with check (true);
create policy web_networks_daily_jobs on cp.web_networks_daily for all to jobs using (true) with check (true);
create policy web_integrity_daily_jobs on agg.web_integrity_daily for all to jobs using (true) with check (true);

-- platform_owner runs agg.purge_tenant and agg.site_baselines
create policy web_integrity_daily_purge on agg.web_integrity_daily for delete to platform_owner using (true);
create policy web_integrity_daily_purge_read on agg.web_integrity_daily for select to platform_owner using (true);

revoke all on cp.web_evidence_daily, cp.web_networks_daily, agg.web_integrity_daily from public;
grant select, insert, update, delete on cp.web_evidence_daily, cp.web_networks_daily to jobs, cp_system;
grant select, insert, update, delete on agg.web_integrity_daily to jobs;
grant select on agg.web_integrity_daily to cp_operator;
-- usage on agg is cp_tenant's since 00032
grant select on agg.web_integrity_daily to cp_tenant;

-- +goose StatementBegin
-- the latest baseline of every site of a tenant, for the reconciler, which
-- publishes it to the gateways as the site's scale. cp_system has no right
-- in agg; this function is its only way to the baselines.
create function agg.site_baselines(tenant uuid)
returns table (site_id uuid, baseline_visitors bigint)
language sql stable security definer
set search_path = pg_catalog, pg_temp
as $$
  select distinct on (i.site_id) i.site_id, i.baseline_visitors
  from agg.web_integrity_daily i
  where i.tenant_id = tenant and i.baseline_visitors is not null
  order by i.site_id, i.day desc
$$;
-- +goose StatementEnd

revoke all on function agg.site_baselines(uuid) from public;
grant execute on function agg.site_baselines(uuid) to cp_system;

-- +goose StatementBegin
-- agg.purge_tenant of 00019 with the grades: they are aggregates and follow
-- removal_keep_aggregates like every other agg row.
create or replace function agg.purge_tenant(tenant uuid)
returns table (table_name text, rows_deleted bigint)
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  n bigint;
begin
  if tenant is null then
    raise exception 'agg.purge_tenant: tenant is required'
      using errcode = 'null_value_not_allowed';
  end if;
  if not exists (select 1 from cp.tenants t where t.id = tenant and t.status = 'removed') then
    raise exception 'agg.purge_tenant: tenant % is not removed', tenant
      using errcode = 'object_not_in_prerequisite_state';
  end if;
  perform pg_advisory_xact_lock(hashtextextended('agg.tenant:' || tenant::text, 0));

  delete from agg.web_daily w where w.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'web_daily'; rows_deleted := n; return next;
  delete from agg.web_daily_dim d where d.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'web_daily_dim'; rows_deleted := n; return next;
  delete from agg.web_integrity_daily i where i.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'web_integrity_daily'; rows_deleted := n; return next;
end
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
create or replace function agg.purge_tenant(tenant uuid)
returns table (table_name text, rows_deleted bigint)
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  n bigint;
begin
  if tenant is null then
    raise exception 'agg.purge_tenant: tenant is required'
      using errcode = 'null_value_not_allowed';
  end if;
  if not exists (select 1 from cp.tenants t where t.id = tenant and t.status = 'removed') then
    raise exception 'agg.purge_tenant: tenant % is not removed', tenant
      using errcode = 'object_not_in_prerequisite_state';
  end if;
  perform pg_advisory_xact_lock(hashtextextended('agg.tenant:' || tenant::text, 0));

  delete from agg.web_daily w where w.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'web_daily'; rows_deleted := n; return next;
  delete from agg.web_daily_dim d where d.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'web_daily_dim'; rows_deleted := n; return next;
end
$$;
-- +goose StatementEnd
revoke execute on function agg.site_baselines(uuid) from cp_system;
drop function agg.site_baselines(uuid);
drop table agg.web_integrity_daily;
drop table cp.web_networks_daily;
drop table cp.web_evidence_daily;
