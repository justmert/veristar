-- the public demo (control-plane-api section 21). an operator marks a
-- project as a demo with platformctl tenant demo; anonymous visitors of
-- /api/demo then read that project, and the operator views of the demo
-- show demo projects only.
--
-- cp.tenants.demo is the flag. cp_tenant cannot change it (its column
-- grants stay name, description and the terms), and no console route
-- writes it: only cp_system does, through platformctl, with an audit row.
--
-- the operator views of a demo request do not run as cp_operator, whose
-- policies see every tenant. the control plane switches the transaction to
-- cp_demo (set local role), a role without login that cp_operator may set
-- but does not inherit. cp_demo holds select on exactly the relations the
-- demo's operator routes read, and each tenant-keyed one has a single
-- cp_demo policy that keeps demo projects only. a relation cp_demo has no
-- policy on returns no rows, and one it has no right on refuses the
-- statement, so a view of the demo that reads something new fails closed
-- instead of showing another tenant.
--
-- two objects run with their owner's rights rather than the caller's and
-- so cannot rely on cp_demo's policies: cp.operator_sites filters on
-- current_user, which a view leaves as the caller, and the api functions
-- api.operator_hourly and api.operator_daily keep only demo tenants of the
-- list they are given while the transaction carries app.demo = 'on', which
-- the control plane sets beside the role. their callers already pass a
-- list read from cp.tenants under cp_demo, so this is the second check.

-- +goose Up
alter table cp.tenants add column demo boolean not null default false;
create index tenants_demo_idx on cp.tenants (id) where demo;

-- +goose StatementBegin
do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'cp_demo') then
    create role cp_demo nologin nosuperuser nocreatedb nocreaterole noinherit noreplication nobypassrls;
  end if;
end
$$;
-- +goose StatementEnd
-- set lets cp_operator switch to the role inside a transaction; inherit
-- false keeps cp_demo's rights and policies away from cp_operator itself
grant cp_demo to cp_operator with inherit false, set true;

grant usage on schema cp, api, agg to cp_demo;

-- the projects of the demo: flagged and not removed
create policy tenants_demo on cp.tenants for select to cp_demo using (demo and status <> 'removed');
grant select on cp.tenants to cp_demo;

-- tenant-keyed relations the demo's operator routes read, one policy each
create policy web_ingest_hourly_demo on cp.web_ingest_hourly for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy tenant_removals_demo on cp.tenant_removals for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy tenant_cohorts_demo on cp.tenant_cohorts for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy chain_ids_demo on cp.chain_ids for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy usage_reports_demo on cp.usage_reports for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy metrics_hourly_demo on api.metrics_hourly for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy metrics_daily_demo on api.metrics_daily for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy web_daily_demo on agg.web_daily for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy web_daily_dim_demo on agg.web_daily_dim for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy web_integrity_daily_demo on agg.web_integrity_daily for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy web_coverage_daily_demo on agg.web_coverage_daily for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy chain_daily_demo on agg.chain_daily for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy chain_reads_demo on agg.chain_reads for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy chain_weeks_demo on agg.chain_weeks for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));
create policy chain_function_daily_demo on agg.chain_function_daily for select to cp_demo
  using (tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed'));

grant select on cp.web_ingest_hourly, cp.tenant_removals, cp.tenant_cohorts, cp.chain_ids, cp.usage_reports,
  api.metrics_hourly, api.metrics_daily,
  agg.web_daily, agg.web_daily_dim, agg.web_integrity_daily, agg.web_coverage_daily,
  agg.chain_daily, agg.chain_reads, agg.chain_function_daily
  to cp_demo;
-- as for cp_operator (00038): the counts without the key column
grant select (tenant_id, week, active_accounts, missing, closed_at, created_at) on agg.chain_weeks to cp_demo;

-- relations without tenant rows: the operators' threshold rules hold a
-- metric and limits only (their authors stay in columns no demo route
-- returns), and the traffic class rule set is platform-wide
create policy threshold_rules_demo on cp.threshold_rules for select to cp_demo using (true);
create policy traffic_class_rules_demo on api.traffic_class_rules for select to cp_demo using (true);
grant select on cp.threshold_rules, api.traffic_class_rules to cp_demo;

-- the bucket arithmetic the operator views call
grant execute on function api.sum_buckets(bigint[]), api.histogram_quantile(double precision, bigint[]),
  api.sum_buckets_read(bigint[]), api.add_buckets(bigint[], bigint[]),
  api.add_buckets_numeric(numeric[], bigint[]), api.merge_buckets_numeric(numeric[], numeric[]),
  api.fit_buckets(numeric[])
  to cp_demo;

-- create or replace keeps the columns and the grants. under cp_demo the
-- view lists demo sites only, and the previously removed check, which
-- looks at other tenants' domains, answers false
create or replace view cp.operator_sites with (security_barrier) as
select s.id, s.tenant_id, s.name, s.domain, s.origins, s.quarantine_website_id, s.status,
       s.verify_method, s.verified_at, s.last_verify_at, s.last_verify_error,
       s.stellar_toml, s.stellar_toml_checked_at, s.created_at, s.updated_at,
       s.status_reason,
       exists (
         select 1
         from cp.sites o
         join cp.tenants t on t.id = o.tenant_id
         where current_user <> 'cp_demo'
           and t.status = 'removed' and o.tenant_id <> s.tenant_id
           and (o.domain = s.domain
                or right(s.domain, length(o.domain) + 1) = '.' || o.domain
                or right(o.domain, length(s.domain) + 1) = '.' || s.domain)
       ) as domain_previously_removed
from cp.sites s
where current_user <> 'cp_demo'
   or s.tenant_id in (select t.id from cp.tenants t where t.demo and t.status <> 'removed');
grant select on cp.operator_sites to cp_demo;

-- +goose StatementBegin
-- the tenants of ids that a demo request may read: all of them outside a
-- demo transaction, the demo projects among them inside one
create function cp.demo_tenants(ids uuid[]) returns uuid[]
language sql stable security definer
set search_path = pg_catalog, pg_temp
as $$
  select case when coalesce(current_setting('app.demo', true), '') = 'on'
              then array(select t.id from cp.tenants t
                         where t.id = any(ids) and t.demo and t.status <> 'removed')
              else ids end
$$;
-- +goose StatementEnd
revoke all on function cp.demo_tenants(uuid[]) from public;

-- the functions of 00040 keep their bodies under a new name; the old names
-- become wrappers that pass the list through cp.demo_tenants first
alter function api.operator_hourly(timestamptz, timestamptz, uuid[]) rename to operator_hourly_listed;
alter function api.operator_daily(timestamptz, timestamptz, uuid[]) rename to operator_daily_listed;
revoke all on function api.operator_hourly_listed(timestamptz, timestamptz, uuid[]),
  api.operator_daily_listed(timestamptz, timestamptz, uuid[]) from public, cp_operator;

-- +goose StatementBegin
create function api.operator_hourly(from_at timestamptz, to_end timestamptz, tenants uuid[])
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, traffic_class text, counts bigint[], count bigint,
               lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select * from api.operator_hourly_listed(from_at, to_end, cp.demo_tenants(tenants))
$$;
-- +goose StatementEnd

-- +goose StatementBegin
create function api.operator_daily(from_at timestamptz, to_end timestamptz, tenants uuid[])
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, traffic_class text, counts bigint[], count bigint,
               lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select * from api.operator_daily_listed(from_at, to_end, cp.demo_tenants(tenants))
$$;
-- +goose StatementEnd

revoke all on function api.operator_hourly(timestamptz, timestamptz, uuid[]),
  api.operator_daily(timestamptz, timestamptz, uuid[]) from public;
grant execute on function api.operator_hourly(timestamptz, timestamptz, uuid[]),
  api.operator_daily(timestamptz, timestamptz, uuid[]) to cp_operator, cp_demo;

-- +goose Down
drop function api.operator_daily(timestamptz, timestamptz, uuid[]);
drop function api.operator_hourly(timestamptz, timestamptz, uuid[]);
alter function api.operator_hourly_listed(timestamptz, timestamptz, uuid[]) rename to operator_hourly;
alter function api.operator_daily_listed(timestamptz, timestamptz, uuid[]) rename to operator_daily;
grant execute on function api.operator_hourly(timestamptz, timestamptz, uuid[]),
  api.operator_daily(timestamptz, timestamptz, uuid[]) to cp_operator;
drop function cp.demo_tenants(uuid[]);

create or replace view cp.operator_sites with (security_barrier) as
select s.id, s.tenant_id, s.name, s.domain, s.origins, s.quarantine_website_id, s.status,
       s.verify_method, s.verified_at, s.last_verify_at, s.last_verify_error,
       s.stellar_toml, s.stellar_toml_checked_at, s.created_at, s.updated_at,
       s.status_reason,
       exists (
         select 1
         from cp.sites o
         join cp.tenants t on t.id = o.tenant_id
         where t.status = 'removed' and o.tenant_id <> s.tenant_id
           and (o.domain = s.domain
                or right(s.domain, length(o.domain) + 1) = '.' || o.domain
                or right(o.domain, length(s.domain) + 1) = '.' || s.domain)
       ) as domain_previously_removed
from cp.sites s;

drop policy traffic_class_rules_demo on api.traffic_class_rules;
drop policy threshold_rules_demo on cp.threshold_rules;
drop policy chain_function_daily_demo on agg.chain_function_daily;
drop policy chain_weeks_demo on agg.chain_weeks;
drop policy chain_reads_demo on agg.chain_reads;
drop policy chain_daily_demo on agg.chain_daily;
drop policy web_coverage_daily_demo on agg.web_coverage_daily;
drop policy web_integrity_daily_demo on agg.web_integrity_daily;
drop policy web_daily_dim_demo on agg.web_daily_dim;
drop policy web_daily_demo on agg.web_daily;
drop policy metrics_daily_demo on api.metrics_daily;
drop policy metrics_hourly_demo on api.metrics_hourly;
drop policy usage_reports_demo on cp.usage_reports;
drop policy chain_ids_demo on cp.chain_ids;
drop policy tenant_cohorts_demo on cp.tenant_cohorts;
drop policy tenant_removals_demo on cp.tenant_removals;
drop policy web_ingest_hourly_demo on cp.web_ingest_hourly;
drop policy tenants_demo on cp.tenants;

revoke all on all tables in schema cp, api, agg from cp_demo;
revoke all on all functions in schema api from cp_demo;
revoke usage on schema cp, api, agg from cp_demo;
-- the role and cp_operator's membership are cluster-wide, like the roles of
-- 00001; another database of the cluster may still use them
drop index cp.tenants_demo_idx;
alter table cp.tenants drop column demo;
