-- web aggregates for the operator view (schema agg). the jobs
-- task aggregate-web reads umami's own /stats and /metrics routes for each
-- active site, band and utc day and replaces that day's rows here, so the
-- numbers operators see are the numbers tenants see in umami.
--
-- rules of 00003 apply: forced row-level security and exactly one cp_tenant
-- policy keyed on cp.current_tenant_id() on every table with a tenant_id.
-- cp_tenant gets no right in agg (its own numbers are in umami); the policy
-- exists so that a later grant cannot expose other tenants' rows.
--
-- the default privileges of 00001 give cp_operator select and jobs select,
-- insert, update and delete on every agg table.

-- +goose Up
create table agg.web_daily (
  site_id    uuid not null,
  tenant_id  uuid not null,
  day        date not null,
  -- primary is the site's own umami website, quarantine its second website
  -- (docs/reference/umami-contract.md item 14)
  band       text not null check (band in ('primary', 'quarantine')),
  pageviews  bigint not null check (pageviews >= 0),
  visitors   bigint not null check (visitors >= 0),
  visits     bigint not null check (visits >= 0),
  bounces    bigint not null check (bounces >= 0),
  -- seconds summed over visits, as umami's /stats returns it
  totaltime  bigint not null check (totaltime >= 0),
  -- when umami was last read for this day; a row exists for every day the
  -- job read, zeros included, so a missing row means "not aggregated yet"
  read_at    timestamptz not null default now(),
  primary key (site_id, day, band)
);
create index web_daily_tenant_day_idx on agg.web_daily (tenant_id, day);

-- one ranked dimension of /metrics for a site, band and day. value is
-- umami's y: distinct sessions with a pageview carrying that key. no foreign
-- keys, so aggregates can outlive a removed site (removal_keep_aggregates).
create table agg.web_daily_dim (
  site_id   uuid not null,
  tenant_id uuid not null,
  day       date not null,
  band      text not null check (band in ('primary', 'quarantine')),
  dimension text not null check (dimension in ('referrer', 'country', 'device', 'browser', 'os')),
  key       text not null check (key <> ''),
  value     bigint not null check (value > 0),
  primary key (site_id, day, band, dimension, key)
);
create index web_daily_dim_tenant_day_idx on agg.web_daily_dim (tenant_id, day, dimension);

alter table agg.web_daily enable row level security;
alter table agg.web_daily force row level security;
alter table agg.web_daily_dim enable row level security;
alter table agg.web_daily_dim force row level security;

create policy web_daily_tenant on agg.web_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy web_daily_dim_tenant on agg.web_daily_dim for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());

-- cp_operator: aggregates across tenants, read only
create policy web_daily_operator on agg.web_daily for select to cp_operator using (true);
create policy web_daily_dim_operator on agg.web_daily_dim for select to cp_operator using (true);

-- jobs: the aggregation task replaces rows of every tenant
create policy web_daily_jobs on agg.web_daily for all to jobs using (true) with check (true);
create policy web_daily_dim_jobs on agg.web_daily_dim for all to jobs using (true) with check (true);

revoke all on agg.web_daily, agg.web_daily_dim from public;
-- the defaults of 00001 already granted these; stated again so the file
-- reads as the whole set of rights
grant select on agg.web_daily, agg.web_daily_dim to cp_operator;
grant select, insert, update, delete on agg.web_daily, agg.web_daily_dim to jobs;

-- the task skips removed tenants, so rows of a tenant purged a moment ago
-- are not written again. it reads the two status columns it needs, as the
-- metrics writer does (00018).
grant select (id, status) on cp.tenants to jobs;
create policy tenants_jobs_status on cp.tenants for select to jobs using (true);

-- the purge runs as the function's owner under forced row-level security,
-- so platform_owner gets delete and select policies on the two tables it purges
create policy web_daily_purge on agg.web_daily for delete to platform_owner using (true);
create policy web_daily_dim_purge on agg.web_daily_dim for delete to platform_owner using (true);
create policy web_daily_purge_read on agg.web_daily for select to platform_owner using (true);
create policy web_daily_dim_purge_read on agg.web_daily_dim for select to platform_owner using (true);

-- +goose StatementBegin
-- deletes a removed tenant's agg rows and returns how many went per table.
-- cp_system has no right in agg; this function is its only way in, and it
-- refuses a tenant that is not removed. it takes the tenant's advisory lock
-- exclusively, while aggregate-web holds it shared for every write of that
-- tenant, so a write that started before the purge finishes first and is
-- deleted, and one that starts after it sees the removed status and stops.
-- keep_aggregates is decided by the caller (lifecycle.AggPurger), because
-- every agg row is an aggregate.
create function agg.purge_tenant(tenant uuid)
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

revoke all on function agg.purge_tenant(uuid) from public;
grant usage on schema agg to cp_system;
grant execute on function agg.purge_tenant(uuid) to cp_system;

-- +goose Down
revoke execute on function agg.purge_tenant(uuid) from cp_system;
revoke usage on schema agg from cp_system;
drop function agg.purge_tenant(uuid);
drop policy tenants_jobs_status on cp.tenants;
revoke select (id, status) on cp.tenants from jobs;
drop table agg.web_daily_dim;
drop table agg.web_daily;
