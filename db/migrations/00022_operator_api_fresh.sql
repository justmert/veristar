-- operator reads of api metrics (global views and the machine api) with the freshness rule of control-plane-api 7.2: an hour still
-- marked in api.rollup_pending has stale hourly and daily rows until the next
-- api-rollup run, so those hours are computed from api.metrics_minute.
--
-- cp_operator may not read api.metrics_minute (operators see only hourly
-- and daily aggregates). these two security definer functions are
-- its only way to them: they return rows at hour or day grain, the same
-- shape and the same numbers the rollup will write, never a minute row.
-- they read through platform_owner's select policies of 00017. rows of one hour
-- come either from api.metrics_hourly or from minutes, never both: the
-- rollup removes a mark in the transaction that replaces that hour's rows,
-- and each call reads marks and rows in one snapshot.

-- +goose Up

-- +goose StatementBegin
-- hour-grain rows of [from_at, to_end), the range aligned to hours
create function api.operator_hourly(from_at timestamptz, to_end timestamptz)
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, counts bigint[], count bigint, lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select h.tenant_id, h.bucket_start, h.service, h.method, h.route, h.status_code, h.source,
         h.counts, h.count, h.lossy
  from api.metrics_hourly h
  where h.bucket_start >= from_at and h.bucket_start < to_end
    and not exists (select 1 from api.rollup_pending p
                    where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
  union all
  select m.tenant_id, p.hour, m.service, m.method, m.route, m.status_code, m.source,
         api.sum_buckets(m.counts), sum(m.count)::bigint, bool_or(m.lossy)
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where p.hour >= from_at and p.hour < to_end
  group by m.tenant_id, p.hour, m.service, m.method, m.route, m.status_code, m.source
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- day-grain rows of [from_at, to_end), the range aligned to utc days. a day
-- with a pending hour is rebuilt from its unmarked hourly rows and its
-- marked hours' minutes; intervals are in hours so no zone can shift a day
create function api.operator_daily(from_at timestamptz, to_end timestamptz)
returns table (tenant_id uuid, bucket_start timestamptz, service text, method text, route text,
               status_code smallint, source text, counts bigint[], count bigint, lossy boolean)
language sql stable security definer
set search_path = pg_catalog, pg_temp
set timezone = 'UTC'
as $$
  select d.tenant_id, d.bucket_start, d.service, d.method, d.route, d.status_code, d.source,
         d.counts, d.count, d.lossy
  from api.metrics_daily d
  where d.bucket_start >= from_at and d.bucket_start < to_end
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
          where p.hour >= from_at and p.hour < to_end) pd
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
    where p.hour >= from_at and p.hour < to_end
  ) x
  group by x.tenant_id, x.day, x.service, x.method, x.route, x.status_code, x.source
$$;
-- +goose StatementEnd

revoke all on function api.operator_hourly(timestamptz, timestamptz), api.operator_daily(timestamptz, timestamptz) from public;
grant execute on function api.operator_hourly(timestamptz, timestamptz), api.operator_daily(timestamptz, timestamptz) to cp_operator;

-- +goose Down
drop function api.operator_daily(timestamptz, timestamptz);
drop function api.operator_hourly(timestamptz, timestamptz);
