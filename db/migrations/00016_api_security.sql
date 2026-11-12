-- rights, row-level security and partition management for the api schema
--. follows the rules of 00003: forced row-level security
-- and exactly one cp_tenant policy on every table with a tenant_id, no policy
-- for public, and a policy of its own for every other role that reads rows.
--
-- partitions are created and dropped at runtime by the jobs role through the
-- security definer functions below, because only the operator of a partitioned
-- table may add or remove its partitions. every partition gets the same
-- forced row-level security and cp_tenant policy, and no right for any
-- application role: reads and writes go through the parent, whose rights and
-- policies are the only ones that apply there.

-- +goose Up

-- cp_tenant reads its own api metrics (this stage dashboard)
grant usage on schema api to cp_tenant;

alter table api.metrics_minute enable row level security;
alter table api.metrics_minute force row level security;
alter table api.metrics_hourly enable row level security;
alter table api.metrics_hourly force row level security;
alter table api.metrics_daily enable row level security;
alter table api.metrics_daily force row level security;
alter table api.series_state enable row level security;
alter table api.series_state force row level security;
alter table api.resource_seen enable row level security;
alter table api.resource_seen force row level security;
alter table api.ingest_dedup enable row level security;
alter table api.ingest_dedup force row level security;
alter table api.route_registry enable row level security;
alter table api.route_registry force row level security;
alter table api.ingest_warnings enable row level security;
alter table api.ingest_warnings force row level security;
alter table api.rollup_pending enable row level security;
alter table api.rollup_pending force row level security;

-- cp_tenant: one policy per table, also where it has no right, so a later
-- grant cannot expose other tenants' rows
create policy metrics_minute_tenant on api.metrics_minute for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy metrics_hourly_tenant on api.metrics_hourly for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy metrics_daily_tenant on api.metrics_daily for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy series_state_tenant on api.series_state for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy resource_seen_tenant on api.resource_seen for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy ingest_dedup_tenant on api.ingest_dedup for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy route_registry_tenant on api.route_registry for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy ingest_warnings_tenant on api.ingest_warnings for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());
create policy rollup_pending_tenant on api.rollup_pending for all to cp_tenant
  using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id());

-- cp_operator: hourly and daily aggregates across tenants, never minutes
create policy metrics_hourly_operator on api.metrics_hourly for select to cp_operator using (true);
create policy metrics_daily_operator on api.metrics_daily for select to cp_operator using (true);

-- metrics_writer: every tenant's rows in the tables it writes
create policy metrics_minute_writer on api.metrics_minute for all to metrics_writer using (true) with check (true);
create policy series_state_writer on api.series_state for all to metrics_writer using (true) with check (true);
create policy resource_seen_writer on api.resource_seen for all to metrics_writer using (true) with check (true);
create policy ingest_dedup_writer on api.ingest_dedup for all to metrics_writer using (true) with check (true);
create policy route_registry_writer on api.route_registry for all to metrics_writer using (true) with check (true);
create policy ingest_warnings_writer on api.ingest_warnings for all to metrics_writer using (true) with check (true);
create policy rollup_pending_writer on api.rollup_pending for all to metrics_writer using (true) with check (true);

-- jobs: rollups, retention and the tenant purge over every api table
create policy metrics_minute_jobs on api.metrics_minute for all to jobs using (true) with check (true);
create policy metrics_hourly_jobs on api.metrics_hourly for all to jobs using (true) with check (true);
create policy metrics_daily_jobs on api.metrics_daily for all to jobs using (true) with check (true);
create policy series_state_jobs on api.series_state for all to jobs using (true) with check (true);
create policy resource_seen_jobs on api.resource_seen for all to jobs using (true) with check (true);
create policy ingest_dedup_jobs on api.ingest_dedup for all to jobs using (true) with check (true);
create policy route_registry_jobs on api.route_registry for all to jobs using (true) with check (true);
create policy ingest_warnings_jobs on api.ingest_warnings for all to jobs using (true) with check (true);
create policy rollup_pending_jobs on api.rollup_pending for all to jobs using (true) with check (true);

-- table rights. the default privileges of 00001 already gave metrics_writer
-- select, insert, update and jobs select, insert, update, delete on every
-- table above; what follows takes away what the roles must not have and adds
-- the readers.

revoke all on all tables in schema api from public;

-- the writer produces minutes only; hourly and daily rows are the rollup's
revoke all on api.metrics_hourly, api.metrics_daily from metrics_writer;

grant select on api.metrics_minute, api.metrics_hourly, api.metrics_daily, api.ingest_warnings to cp_tenant;
grant select on api.metrics_hourly, api.metrics_daily to cp_operator;

-- functions. histograms are merged and read by everyone who reads metrics.
grant execute on function api.add_buckets(bigint[], bigint[]) to cp_tenant, cp_operator, metrics_writer, jobs;
grant execute on function api.sum_buckets(bigint[]) to cp_tenant, cp_operator, jobs;
grant execute on function api.histogram_quantile(double precision, bigint[]) to cp_tenant, cp_operator, jobs;

-- partitions

-- +goose StatementBegin
-- gives a new partition the rules of the parent. called only by the
-- security definer functions below, so it runs as the operator; never granted.
create function api.protect_partition(part text) returns void
language plpgsql
set search_path = pg_catalog, pg_temp
as $$
begin
  execute format('alter table api.%I enable row level security', part);
  execute format('alter table api.%I force row level security', part);
  execute format(
    'create policy tenant on api.%I for all to cp_tenant '
    'using (tenant_id = cp.current_tenant_id()) with check (tenant_id = cp.current_tenant_id())', part);
  -- the default privileges of 00001 apply to the new table as well; access
  -- goes through the parent only
  execute format('revoke all on api.%I from public, cp_system, cp_tenant, cp_operator, metrics_writer, jobs', part);
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- creates the minute partition of one utc day. returns false when it exists.
create function api.create_minute_partition(day date) returns boolean
language plpgsql security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
declare
  part text := 'metrics_minute_p' || to_char(day, 'YYYYMMDD');
begin
  if day is null then
    raise exception 'api.create_minute_partition: day is null' using errcode = 'null_value_not_allowed';
  end if;
  if to_regclass(format('api.%I', part)) is not null then
    return false;
  end if;
  execute format('create table api.%I partition of api.metrics_minute for values from (%L) to (%L)',
    part, day::timestamp at time zone 'UTC', (day + 1)::timestamp at time zone 'UTC');
  perform api.protect_partition(part);
  return true;
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- creates the hourly partition of one utc month, named by its first day.
-- returns false when it exists.
create function api.create_hourly_partition(month date) returns boolean
language plpgsql security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
declare
  part text := 'metrics_hourly_p' || to_char(month, 'YYYYMM');
begin
  if month is null or extract(day from month) <> 1 then
    raise exception 'api.create_hourly_partition: % is not the first day of a month', month
      using errcode = 'invalid_parameter_value';
  end if;
  if to_regclass(format('api.%I', part)) is not null then
    return false;
  end if;
  execute format('create table api.%I partition of api.metrics_hourly for values from (%L) to (%L)',
    part, month::timestamp at time zone 'UTC', (month + interval '1 month')::timestamp at time zone 'UTC');
  perform api.protect_partition(part);
  return true;
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- drops the minute partition of one utc day with all its rows. returns false
-- when there is none. the caller decides what has expired.
create function api.drop_minute_partition(day date) returns boolean
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  part text := 'metrics_minute_p' || to_char(day, 'YYYYMMDD');
  rel regclass := to_regclass(format('api.%I', part));
begin
  if rel is null then
    return false;
  end if;
  -- the name alone is not proof; only a partition of the minute table goes
  if not exists (select 1 from pg_inherits where inhrelid = rel and inhparent = 'api.metrics_minute'::regclass) then
    raise exception 'api.drop_minute_partition: % is not a partition of api.metrics_minute', rel
      using errcode = 'wrong_object_type';
  end if;
  execute format('drop table api.%I', part);
  return true;
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- drops the hourly partition of one utc month. returns false when there is
-- none. the design keeps hourly data at least 24 months; the retention job
-- enforces that bound on its configuration before it calls this.
create function api.drop_hourly_partition(month date) returns boolean
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  part text := 'metrics_hourly_p' || to_char(month, 'YYYYMM');
  rel regclass;
begin
  if month is null or extract(day from month) <> 1 then
    raise exception 'api.drop_hourly_partition: % is not the first day of a month', month
      using errcode = 'invalid_parameter_value';
  end if;
  rel := to_regclass(format('api.%I', part));
  if rel is null then
    return false;
  end if;
  if not exists (select 1 from pg_inherits where inhrelid = rel and inhparent = 'api.metrics_hourly'::regclass) then
    raise exception 'api.drop_hourly_partition: % is not a partition of api.metrics_hourly', rel
      using errcode = 'wrong_object_type';
  end if;
  execute format('drop table api.%I', part);
  return true;
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- the days that have a minute partition, read from the partition bounds
-- rather than the names
create function api.minute_partitions() returns setof date
language sql stable
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select (substring(pg_get_expr(c.relpartbound, c.oid) from $re$FROM \('([^']+)'\)$re$)::timestamptz
          at time zone 'UTC')::date
  from pg_inherits i
  join pg_class c on c.oid = i.inhrelid
  where i.inhparent = 'api.metrics_minute'::regclass
  order by 1
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- the first days of the months that have an hourly partition
create function api.hourly_partitions() returns setof date
language sql stable
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select (substring(pg_get_expr(c.relpartbound, c.oid) from $re$FROM \('([^']+)'\)$re$)::timestamptz
          at time zone 'UTC')::date
  from pg_inherits i
  join pg_class c on c.oid = i.inhrelid
  where i.inhparent = 'api.metrics_hourly'::regclass
  order by 1
$$;
-- +goose StatementEnd

grant execute on function api.create_minute_partition(date), api.create_hourly_partition(date),
  api.drop_minute_partition(date), api.drop_hourly_partition(date),
  api.minute_partitions(), api.hourly_partitions()
  to jobs;

-- initial partitions, so the writer can store data before the first run of
-- the partition job: minutes from 7 days back (the API_METRICS max age, for a
-- backlog) to 7 days ahead, hours from the month 31 days back (the oldest
-- minute data a rollup can read) to 3 months ahead. the partition job keeps
-- the same window from then on.
select api.create_minute_partition(d::date)
from generate_series((now() at time zone 'UTC')::date - 7, (now() at time zone 'UTC')::date + 7, interval '1 day') d;
select api.create_hourly_partition(m::date)
from generate_series(date_trunc('month', (now() at time zone 'UTC')::date - 31),
                     date_trunc('month', (now() at time zone 'UTC')::date) + interval '3 months',
                     interval '1 month') m;

-- +goose Down
drop function api.hourly_partitions();
drop function api.minute_partitions();
drop function api.drop_hourly_partition(date);
drop function api.drop_minute_partition(date);
drop function api.create_hourly_partition(date);
drop function api.create_minute_partition(date);
drop function api.protect_partition(text);

revoke all on all tables in schema api from cp_tenant, cp_operator;
revoke execute on function api.add_buckets(bigint[], bigint[]) from cp_tenant, cp_operator, metrics_writer, jobs;
revoke execute on function api.sum_buckets(bigint[]) from cp_tenant, cp_operator, jobs;
revoke execute on function api.histogram_quantile(double precision, bigint[]) from cp_tenant, cp_operator, jobs;
grant select, insert, update on api.metrics_hourly, api.metrics_daily to metrics_writer;

drop policy rollup_pending_jobs on api.rollup_pending;
drop policy ingest_warnings_jobs on api.ingest_warnings;
drop policy route_registry_jobs on api.route_registry;
drop policy ingest_dedup_jobs on api.ingest_dedup;
drop policy resource_seen_jobs on api.resource_seen;
drop policy series_state_jobs on api.series_state;
drop policy metrics_daily_jobs on api.metrics_daily;
drop policy metrics_hourly_jobs on api.metrics_hourly;
drop policy metrics_minute_jobs on api.metrics_minute;
drop policy rollup_pending_writer on api.rollup_pending;
drop policy ingest_warnings_writer on api.ingest_warnings;
drop policy route_registry_writer on api.route_registry;
drop policy ingest_dedup_writer on api.ingest_dedup;
drop policy resource_seen_writer on api.resource_seen;
drop policy series_state_writer on api.series_state;
drop policy metrics_minute_writer on api.metrics_minute;
drop policy metrics_daily_operator on api.metrics_daily;
drop policy metrics_hourly_operator on api.metrics_hourly;
drop policy rollup_pending_tenant on api.rollup_pending;
drop policy ingest_warnings_tenant on api.ingest_warnings;
drop policy route_registry_tenant on api.route_registry;
drop policy ingest_dedup_tenant on api.ingest_dedup;
drop policy resource_seen_tenant on api.resource_seen;
drop policy series_state_tenant on api.series_state;
drop policy metrics_daily_tenant on api.metrics_daily;
drop policy metrics_hourly_tenant on api.metrics_hourly;
drop policy metrics_minute_tenant on api.metrics_minute;

alter table api.rollup_pending no force row level security;
alter table api.rollup_pending disable row level security;
alter table api.ingest_warnings no force row level security;
alter table api.ingest_warnings disable row level security;
alter table api.route_registry no force row level security;
alter table api.route_registry disable row level security;
alter table api.ingest_dedup no force row level security;
alter table api.ingest_dedup disable row level security;
alter table api.resource_seen no force row level security;
alter table api.resource_seen disable row level security;
alter table api.series_state no force row level security;
alter table api.series_state disable row level security;
alter table api.metrics_daily no force row level security;
alter table api.metrics_daily disable row level security;
alter table api.metrics_hourly no force row level security;
alter table api.metrics_hourly disable row level security;
alter table api.metrics_minute no force row level security;
alter table api.metrics_minute disable row level security;

revoke usage on schema api from cp_tenant;
