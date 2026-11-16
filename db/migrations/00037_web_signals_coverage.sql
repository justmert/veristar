-- page signals of tracker 1.3.0 and the coverage estimate.
--
-- page signals: the tracker reports a page that runs inside a frame and a
-- page that was hidden from load until it was left. the collect gateway
-- counts, per site, the page views of trackers that report signals, the
-- framed part of them and the never-visible notices; the jobs task
-- edgestats stores the counts in cp.web_signals_daily, and the integrity
-- task copies them into agg.web_integrity_daily as inputs of rule version 2.
--
-- coverage: a browser with global privacy control on loads sdk.js but the
-- tracker sends nothing. the gateway counts, per site, the requests for the
-- tracker file and the part that carried Sec-GPC: 1; edgestats stores both
-- in agg.web_coverage_daily. the share of the second in the first estimates
-- the visits that are not measured because of the signal.
--
-- every row is a site, a day and counts: no address, no header, no token.
-- rules of 00003 apply: forced row-level security and exactly one cp_tenant
-- policy keyed on cp.current_tenant_id() on every table with a tenant_id.

-- +goose Up
create table cp.web_signals_daily (
  site_id   uuid not null,
  tenant_id uuid not null,
  day       date not null,
  signal    text not null check (signal in ('signal_views', 'signal_framed', 'signal_never_visible')),
  count     bigint not null default 0 check (count >= 0),
  primary key (site_id, day, signal)
);
create index web_signals_daily_tenant_idx on cp.web_signals_daily (tenant_id, day);

-- defaults of 0 keep every row graded under rule version 1 exactly as it
-- was computed: version 1 does not read these columns
alter table agg.web_integrity_daily
  add column signal_views  bigint not null default 0 check (signal_views >= 0),
  add column framed        bigint not null default 0 check (framed >= 0),
  add column never_visible bigint not null default 0 check (never_visible >= 0);

-- the two counters arrive in separate edge stats windows when a node's
-- window is full, so gpc_requests may briefly lead script_requests; no check
-- ties them
create table agg.web_coverage_daily (
  site_id         uuid not null,
  tenant_id       uuid not null,
  day             date not null,
  script_requests bigint not null default 0 check (script_requests >= 0),
  gpc_requests    bigint not null default 0 check (gpc_requests >= 0),
  primary key (site_id, day)
);
create index web_coverage_daily_tenant_day_idx on agg.web_coverage_daily (tenant_id, day);

alter table cp.web_signals_daily enable row level security;
alter table cp.web_signals_daily force row level security;
alter table agg.web_coverage_daily enable row level security;
alter table agg.web_coverage_daily force row level security;

-- cp_tenant: a project reads its own coverage, the same rows operators read.
-- the raw signal rows get no grant; their policy exists so a later grant
-- cannot expose other tenants' rows
create policy web_signals_daily_tenant on cp.web_signals_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy web_coverage_daily_tenant on agg.web_coverage_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());

-- cp_system deletes a purged tenant's signal rows
create policy web_signals_daily_system on cp.web_signals_daily for all to cp_system using (true) with check (true);

-- cp_operator: coverage across tenants, read only
create policy web_coverage_daily_operator on agg.web_coverage_daily for select to cp_operator using (true);

-- jobs: edgestats writes both, integrity reads the signals
create policy web_signals_daily_jobs on cp.web_signals_daily for all to jobs using (true) with check (true);
create policy web_coverage_daily_jobs on agg.web_coverage_daily for all to jobs using (true) with check (true);

-- platform_owner runs agg.purge_tenant
create policy web_coverage_daily_purge on agg.web_coverage_daily for delete to platform_owner using (true);
create policy web_coverage_daily_purge_read on agg.web_coverage_daily for select to platform_owner using (true);

revoke all on cp.web_signals_daily, agg.web_coverage_daily from public;
grant select, insert, update, delete on cp.web_signals_daily to jobs, cp_system;
grant select, insert, update, delete on agg.web_coverage_daily to jobs;
grant select on agg.web_coverage_daily to cp_operator, cp_tenant;

-- +goose StatementBegin
-- agg.purge_tenant of 00033 with the coverage rows: they are aggregates and
-- follow removal_keep_aggregates like every other agg row.
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
  delete from agg.web_coverage_daily c where c.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'web_coverage_daily'; rows_deleted := n; return next;
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
  delete from agg.web_integrity_daily i where i.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'web_integrity_daily'; rows_deleted := n; return next;
end
$$;
-- +goose StatementEnd
drop table agg.web_coverage_daily;
alter table agg.web_integrity_daily drop column never_visible, drop column framed, drop column signal_views;
drop table cp.web_signals_daily;
