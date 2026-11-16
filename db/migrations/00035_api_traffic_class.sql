-- traffic classes of api requests (internal/trafficclass, rule set 1).
-- the collector marks cors preflights, health and probe routes and requests
-- from known health checkers; the writer stores them in rows of their own,
-- with traffic_class as one more dimension of the three metric tables. every
-- row still holds one complete histogram, so rows of any set of classes
-- merge with api.sum_buckets exactly like rows of different routes do, and
-- the headline (traffic_class = 'app') and the infrastructure figure
-- (every other class) come from the same rows.
--
-- rows written before this migration keep traffic_class = 'app': nothing
-- marked them, and they stay as they were counted. api.traffic_class_rules
-- records when each rule set version took effect, so every view can say
-- from which moment infrastructure requests are counted apart.
--
-- the column joins the primary keys, so the writer's upsert and the
-- rollups keep one row per class. the row-level security policies of 00016
-- and 00017 are per row and per role, so they cover the new column as they
-- are. api.operator_hourly and api.operator_daily of 00023 return the class.

-- +goose Up
alter table api.metrics_minute add column traffic_class text not null default 'app'
  constraint metrics_minute_traffic_class_check
  check (traffic_class in ('app', 'preflight', 'health', 'probe'));
alter table api.metrics_hourly add column traffic_class text not null default 'app'
  constraint metrics_hourly_traffic_class_check
  check (traffic_class in ('app', 'preflight', 'health', 'probe'));
alter table api.metrics_daily add column traffic_class text not null default 'app'
  constraint metrics_daily_traffic_class_check
  check (traffic_class in ('app', 'preflight', 'health', 'probe'));

alter table api.metrics_minute drop constraint metrics_minute_pkey,
  add primary key (tenant_id, bucket_start, service, method, route, status_code, source, traffic_class);
alter table api.metrics_hourly drop constraint metrics_hourly_pkey,
  add primary key (tenant_id, bucket_start, service, method, route, status_code, source, traffic_class);
alter table api.metrics_daily drop constraint metrics_daily_pkey,
  add primary key (tenant_id, bucket_start, service, method, route, status_code, source, traffic_class);

-- one row per rule set version. no tenant data, read by every role that
-- shows api numbers.
create table api.traffic_class_rules (
  version      integer primary key check (version > 0),
  applied_from timestamptz not null default now()
);
insert into api.traffic_class_rules (version) values (1);

alter table api.traffic_class_rules enable row level security;
alter table api.traffic_class_rules force row level security;
create policy traffic_class_rules_read on api.traffic_class_rules for select
  to cp_tenant, cp_operator, cp_system using (true);
revoke all on api.traffic_class_rules from public;
grant select on api.traffic_class_rules to cp_tenant, cp_operator, cp_system;

drop function api.operator_daily(timestamptz, timestamptz, uuid[]);
drop function api.operator_hourly(timestamptz, timestamptz, uuid[]);

-- +goose StatementBegin
-- hour-grain rows of [from_at, to_end), the range aligned to hours
create function api.operator_hourly(from_at timestamptz, to_end timestamptz, tenants uuid[])
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, traffic_class text, counts bigint[], count bigint,
               lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select h.tenant_id, h.bucket_start, h.service, h.method, h.route, h.status_code, h.source,
         h.traffic_class, h.counts, h.count, h.lossy
  from api.metrics_hourly h
  where h.tenant_id = any(tenants)
    and h.bucket_start >= from_at and h.bucket_start < to_end
    and not exists (select 1 from api.rollup_pending p
                    where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
  union all
  select m.tenant_id, p.hour, m.service, m.method, m.route, m.status_code, m.source, m.traffic_class,
         api.sum_buckets(m.counts), sum(m.count)::bigint, bool_or(m.lossy)
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where p.tenant_id = any(tenants) and p.hour >= from_at and p.hour < to_end
  group by m.tenant_id, p.hour, m.service, m.method, m.route, m.status_code, m.source, m.traffic_class
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- day-grain rows of [from_at, to_end), the range aligned to utc days. a day
-- with a pending hour is rebuilt from its unmarked hourly rows and its
-- marked hours' minutes; intervals are in hours so no zone can shift a day
create function api.operator_daily(from_at timestamptz, to_end timestamptz, tenants uuid[])
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, traffic_class text, counts bigint[], count bigint,
               lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select d.tenant_id, d.bucket_start, d.service, d.method, d.route, d.status_code, d.source,
         d.traffic_class, d.counts, d.count, d.lossy
  from api.metrics_daily d
  where d.tenant_id = any(tenants)
    and d.bucket_start >= from_at and d.bucket_start < to_end
    and not exists (select 1 from api.rollup_pending p
                    where p.tenant_id = d.tenant_id
                      and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
  union all
  select x.tenant_id, x.day, x.service, x.method, x.route, x.status_code, x.source, x.traffic_class,
         api.sum_buckets(x.counts), sum(x.count)::bigint, bool_or(x.lossy)
  from (
    select h.tenant_id, pd.day, h.service, h.method, h.route, h.status_code, h.source,
           h.traffic_class, h.counts, h.count, h.lossy
    from (select distinct p.tenant_id, date_trunc('day', p.hour, 'UTC') as day
          from api.rollup_pending p
          where p.tenant_id = any(tenants) and p.hour >= from_at and p.hour < to_end) pd
    join api.metrics_hourly h
      on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
    where not exists (select 1 from api.rollup_pending p
                      where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    union all
    select m.tenant_id, date_trunc('day', p.hour, 'UTC'), m.service, m.method, m.route, m.status_code,
           m.source, m.traffic_class, m.counts, m.count, m.lossy
    from api.rollup_pending p
    join api.metrics_minute m
      on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
    where p.tenant_id = any(tenants) and p.hour >= from_at and p.hour < to_end
  ) x
  group by x.tenant_id, x.day, x.service, x.method, x.route, x.status_code, x.source, x.traffic_class
$$;
-- +goose StatementEnd

revoke all on function api.operator_hourly(timestamptz, timestamptz, uuid[]), api.operator_daily(timestamptz, timestamptz, uuid[]) from public;
grant execute on function api.operator_hourly(timestamptz, timestamptz, uuid[]), api.operator_daily(timestamptz, timestamptz, uuid[]) to cp_operator;

-- +goose Down
drop function api.operator_daily(timestamptz, timestamptz, uuid[]);
drop function api.operator_hourly(timestamptz, timestamptz, uuid[]);

-- +goose StatementBegin
create function api.operator_hourly(from_at timestamptz, to_end timestamptz, tenants uuid[])
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, counts bigint[], count bigint, lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select h.tenant_id, h.bucket_start, h.service, h.method, h.route, h.status_code, h.source,
         h.counts, h.count, h.lossy
  from api.metrics_hourly h
  where h.tenant_id = any(tenants)
    and h.bucket_start >= from_at and h.bucket_start < to_end
    and not exists (select 1 from api.rollup_pending p
                    where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
  union all
  select m.tenant_id, p.hour, m.service, m.method, m.route, m.status_code, m.source,
         api.sum_buckets(m.counts), sum(m.count)::bigint, bool_or(m.lossy)
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where p.tenant_id = any(tenants) and p.hour >= from_at and p.hour < to_end
  group by m.tenant_id, p.hour, m.service, m.method, m.route, m.status_code, m.source
$$;
-- +goose StatementEnd

-- +goose StatementBegin
create function api.operator_daily(from_at timestamptz, to_end timestamptz, tenants uuid[])
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, counts bigint[], count bigint, lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select d.tenant_id, d.bucket_start, d.service, d.method, d.route, d.status_code, d.source,
         d.counts, d.count, d.lossy
  from api.metrics_daily d
  where d.tenant_id = any(tenants)
    and d.bucket_start >= from_at and d.bucket_start < to_end
    and not exists (select 1 from api.rollup_pending p
                    where p.tenant_id = d.tenant_id
                      and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
  union all
  select x.tenant_id, x.day, x.service, x.method, x.route, x.status_code, x.source,
         api.sum_buckets(x.counts), sum(x.count)::bigint, bool_or(x.lossy)
  from (
    select h.tenant_id, pd.day, h.service, h.method, h.route, h.status_code, h.source,
           h.counts, h.count, h.lossy
    from (select distinct p.tenant_id, date_trunc('day', p.hour, 'UTC') as day
          from api.rollup_pending p
          where p.tenant_id = any(tenants) and p.hour >= from_at and p.hour < to_end) pd
    join api.metrics_hourly h
      on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
    where not exists (select 1 from api.rollup_pending p
                      where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    union all
    select m.tenant_id, date_trunc('day', p.hour, 'UTC'), m.service, m.method, m.route, m.status_code,
           m.source, m.counts, m.count, m.lossy
    from api.rollup_pending p
    join api.metrics_minute m
      on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
    where p.tenant_id = any(tenants) and p.hour >= from_at and p.hour < to_end
  ) x
  group by x.tenant_id, x.day, x.service, x.method, x.route, x.status_code, x.source
$$;
-- +goose StatementEnd

revoke all on function api.operator_hourly(timestamptz, timestamptz, uuid[]), api.operator_daily(timestamptz, timestamptz, uuid[]) from public;
grant execute on function api.operator_hourly(timestamptz, timestamptz, uuid[]), api.operator_daily(timestamptz, timestamptz, uuid[]) to cp_operator;

drop table api.traffic_class_rules;

-- rows of infrastructure classes merge into the app rows of their key
-- before the class leaves the key, so no request is lost on the way down
-- +goose StatementBegin
do $$
declare
  t text;
begin
  foreach t in array array['metrics_minute', 'metrics_hourly', 'metrics_daily'] loop
    execute format('alter table api.%I no force row level security', t);
    execute format($f$
      with moved as (
        delete from api.%1$I where traffic_class <> 'app'
        returning tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy)
      insert into api.%1$I (tenant_id, bucket_start, service, method, route, status_code, source,
                            traffic_class, counts, sum, count, lossy)
      select tenant_id, bucket_start, service, method, route, status_code, source, 'app',
             api.sum_buckets(counts), sum(sum), sum(count)::bigint, bool_or(lossy)
      from moved
      group by tenant_id, bucket_start, service, method, route, status_code, source
      on conflict (tenant_id, bucket_start, service, method, route, status_code, source, traffic_class) do update
        set counts = api.add_buckets(api.%1$I.counts, excluded.counts),
            sum = api.%1$I.sum + excluded.sum,
            count = api.%1$I.count + excluded.count,
            lossy = api.%1$I.lossy or excluded.lossy$f$, t);
    execute format('alter table api.%I force row level security', t);
  end loop;
end
$$;
-- +goose StatementEnd

alter table api.metrics_minute drop constraint metrics_minute_pkey,
  add primary key (tenant_id, bucket_start, service, method, route, status_code, source);
alter table api.metrics_hourly drop constraint metrics_hourly_pkey,
  add primary key (tenant_id, bucket_start, service, method, route, status_code, source);
alter table api.metrics_daily drop constraint metrics_daily_pkey,
  add primary key (tenant_id, bucket_start, service, method, route, status_code, source);

alter table api.metrics_minute drop column traffic_class;
alter table api.metrics_hourly drop column traffic_class;
alter table api.metrics_daily drop column traffic_class;
