-- cp_tenant queries of the api metrics routes (control-plane-api section 7).
-- each query unions the branches of the three resolutions, so one query
-- serves every range; every branch but those of @resolution is cut by a
-- one-time filter. hourly and daily answers also read the hours still
-- marked in api.rollup_pending from api.metrics_minute (contract 7.2), so
-- traffic shows before the rollup runs and nothing is counted twice. no query names a tenant: row-level security on the api tables keys
-- every row on cp.current_tenant_id(), so a handler that set the wrong tenant
-- in db.WithTenant reads that tenant's rows or none, never a mix.
--
-- traffic_class (00039) splits application requests from cors preflights,
-- health routes and probes. requests, error counts, lossy and the
-- percentiles cover traffic_class = 'app' only; infrastructure_requests
-- counts the rest, so the headline never includes a load balancer's checks.
--
-- @metric_services and @span_services come from APIMetricSources (source.go):
-- each service appears in exactly one of them, so metric and span rows of a
-- service are never added together.

-- name: APIMetricSources :many
-- the services with any row in the range and which sources they have. only
-- the service filter applies: a route or method filter must not switch the
-- source of a service.
select service,
       bool_or(source = 'metric')::boolean as has_metric,
       bool_or(source = 'span')::boolean as has_span
from (
  select service, source
  from api.metrics_minute
  where @resolution::text = 'minute'
    and bucket_start >= @range_from::timestamptz and bucket_start < @range_to::timestamptz
    and (sqlc.narg(service)::text is null or service = sqlc.narg(service)::text)
  union all
  select service, source
  from api.metrics_hourly h
  where @resolution::text = 'hour'
    and h.bucket_start >= @range_from::timestamptz and h.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and (sqlc.narg(service)::text is null or service = sqlc.narg(service)::text)
  union all
  select service, source
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'hour'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and (sqlc.narg(service)::text is null or service = sqlc.narg(service)::text)
  union all
  select service, source
  from api.metrics_daily d
  where @resolution::text = 'day'
    and d.bucket_start >= @range_from::timestamptz and d.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = d.tenant_id
                    and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
    and (sqlc.narg(service)::text is null or service = sqlc.narg(service)::text)
  union all
  select service, source
  from (select distinct tenant_id, date_trunc('day', hour, 'UTC') as day
        from api.rollup_pending
        where @resolution::text = 'day' and hour >= @range_from::timestamptz and hour < @range_to::timestamptz) pd
  join api.metrics_hourly h
    on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
  where not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and (sqlc.narg(service)::text is null or service = sqlc.narg(service)::text)
  union all
  select service, source
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'day'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and (sqlc.narg(service)::text is null or service = sqlc.narg(service)::text)
) r
group by service
order by service;

-- name: APIMetricSummary :one
-- percentiles of summed bucket arrays, never averages of percentiles
select coalesce(sum(count) filter (where traffic_class = 'app'), 0)::bigint as requests,
       coalesce(sum(count) filter (where traffic_class = 'app' and status_code between 400 and 499), 0)::bigint as errors_4xx,
       coalesce(sum(count) filter (where traffic_class = 'app' and status_code between 500 and 599), 0)::bigint as errors_5xx,
       coalesce(sum(count) filter (where traffic_class <> 'app'), 0)::bigint as infrastructure_requests,
       coalesce(bool_or(lossy) filter (where traffic_class = 'app'), false)::boolean as lossy,
       case when sum(count) filter (where traffic_class = 'app') > 0
         then api.histogram_quantile(0.5, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p50,
       case when sum(count) filter (where traffic_class = 'app') > 0
         then api.histogram_quantile(0.95, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p95,
       case when sum(count) filter (where traffic_class = 'app') > 0
         then api.histogram_quantile(0.99, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p99
from (
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_minute
  where @resolution::text = 'minute'
    and bucket_start >= @range_from::timestamptz and bucket_start < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_hourly h
  where @resolution::text = 'hour'
    and h.bucket_start >= @range_from::timestamptz and h.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, p.hour as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'hour'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_daily d
  where @resolution::text = 'day'
    and d.bucket_start >= @range_from::timestamptz and d.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = d.tenant_id
                    and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, pd.day as bucket_start
  from (select distinct tenant_id, date_trunc('day', hour, 'UTC') as day
        from api.rollup_pending
        where @resolution::text = 'day' and hour >= @range_from::timestamptz and hour < @range_to::timestamptz) pd
  join api.metrics_hourly h
    on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
  where not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, date_trunc('day', p.hour, 'UTC') as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'day'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
) r;

-- name: APIMetricTimeseries :many
-- one row per time bucket with data; the handler adds the empty buckets
select bucket_start,
       coalesce(sum(count) filter (where traffic_class = 'app'), 0)::bigint as requests,
       coalesce(sum(count) filter (where traffic_class = 'app' and status_code between 400 and 499), 0)::bigint as errors_4xx,
       coalesce(sum(count) filter (where traffic_class = 'app' and status_code between 500 and 599), 0)::bigint as errors_5xx,
       coalesce(sum(count) filter (where traffic_class <> 'app'), 0)::bigint as infrastructure_requests,
       coalesce(bool_or(lossy) filter (where traffic_class = 'app'), false)::boolean as lossy,
       case when sum(count) filter (where traffic_class = 'app') > 0
         then api.histogram_quantile(0.5, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p50,
       case when sum(count) filter (where traffic_class = 'app') > 0
         then api.histogram_quantile(0.95, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p95,
       case when sum(count) filter (where traffic_class = 'app') > 0
         then api.histogram_quantile(0.99, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p99
from (
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_minute
  where @resolution::text = 'minute'
    and bucket_start >= @range_from::timestamptz and bucket_start < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_hourly h
  where @resolution::text = 'hour'
    and h.bucket_start >= @range_from::timestamptz and h.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, p.hour as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'hour'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_daily d
  where @resolution::text = 'day'
    and d.bucket_start >= @range_from::timestamptz and d.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = d.tenant_id
                    and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, pd.day as bucket_start
  from (select distinct tenant_id, date_trunc('day', hour, 'UTC') as day
        from api.rollup_pending
        where @resolution::text = 'day' and hour >= @range_from::timestamptz and hour < @range_to::timestamptz) pd
  join api.metrics_hourly h
    on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
  where not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, date_trunc('day', p.hour, 'UTC') as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'day'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
) r
group by bucket_start
order by bucket_start;

-- name: APIMetricRoutes :many
-- one row per service, method and route, sorted by @sort_key and paged by
-- offset. 'infinity' (the open bucket) sorts above every number, as the
-- contract says; nulls (no requests) sort last in both orders.
with g as (
  select service, method, route,
         min(source)::text as source,
         coalesce(sum(count) filter (where traffic_class = 'app'), 0)::bigint as requests,
         coalesce(sum(count) filter (where traffic_class = 'app' and status_code between 400 and 499), 0)::bigint as errors_4xx,
         coalesce(sum(count) filter (where traffic_class = 'app' and status_code between 500 and 599), 0)::bigint as errors_5xx,
         coalesce(sum(count) filter (where traffic_class <> 'app'), 0)::bigint as infrastructure_requests,
         coalesce(bool_or(lossy) filter (where traffic_class = 'app'), false)::boolean as lossy,
         case when sum(count) filter (where traffic_class = 'app') > 0
           then api.histogram_quantile(0.5, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p50,
         case when sum(count) filter (where traffic_class = 'app') > 0
           then api.histogram_quantile(0.95, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p95,
         case when sum(count) filter (where traffic_class = 'app') > 0
           then api.histogram_quantile(0.99, api.sum_buckets(counts) filter (where traffic_class = 'app')) end as p99
  from (
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_minute
  where @resolution::text = 'minute'
    and bucket_start >= @range_from::timestamptz and bucket_start < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_hourly h
  where @resolution::text = 'hour'
    and h.bucket_start >= @range_from::timestamptz and h.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, p.hour as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'hour'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_daily d
  where @resolution::text = 'day'
    and d.bucket_start >= @range_from::timestamptz and d.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = d.tenant_id
                    and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, pd.day as bucket_start
  from (select distinct tenant_id, date_trunc('day', hour, 'UTC') as day
        from api.rollup_pending
        where @resolution::text = 'day' and hour >= @range_from::timestamptz and hour < @range_to::timestamptz) pd
  join api.metrics_hourly h
    on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
  where not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, date_trunc('day', p.hour, 'UTC') as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'day'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  ) r
  group by service, method, route
), p as (
  select g.*,
         (errors_4xx::float8 / nullif(requests, 0))::float8 as rate_4xx,
         (errors_5xx::float8 / nullif(requests, 0))::float8 as rate_5xx
  from g
)
select service, method, route, source, requests, errors_4xx, errors_5xx, infrastructure_requests,
       lossy, p50, p95, p99
from p
order by
  case when @sort_key::text = 'requests' and not @descending::boolean then requests end asc nulls last,
  case when @sort_key::text = 'requests' and @descending::boolean then requests end desc nulls last,
  case when @sort_key::text = 'errors_4xx' and not @descending::boolean then errors_4xx end asc nulls last,
  case when @sort_key::text = 'errors_4xx' and @descending::boolean then errors_4xx end desc nulls last,
  case when @sort_key::text = 'errors_5xx' and not @descending::boolean then errors_5xx end asc nulls last,
  case when @sort_key::text = 'errors_5xx' and @descending::boolean then errors_5xx end desc nulls last,
  case when @sort_key::text = 'rate_4xx' and not @descending::boolean then rate_4xx end asc nulls last,
  case when @sort_key::text = 'rate_4xx' and @descending::boolean then rate_4xx end desc nulls last,
  case when @sort_key::text = 'rate_5xx' and not @descending::boolean then rate_5xx end asc nulls last,
  case when @sort_key::text = 'rate_5xx' and @descending::boolean then rate_5xx end desc nulls last,
  case when @sort_key::text = 'p50' and not @descending::boolean then p50 end asc nulls last,
  case when @sort_key::text = 'p50' and @descending::boolean then p50 end desc nulls last,
  case when @sort_key::text = 'p95' and not @descending::boolean then p95 end asc nulls last,
  case when @sort_key::text = 'p95' and @descending::boolean then p95 end desc nulls last,
  case when @sort_key::text = 'p99' and not @descending::boolean then p99 end asc nulls last,
  case when @sort_key::text = 'p99' and @descending::boolean then p99 end desc nulls last,
  case when @sort_key::text = 'route' and @descending::boolean then service end desc,
  case when @sort_key::text = 'route' and @descending::boolean then method end desc,
  case when @sort_key::text = 'route' and @descending::boolean then route end desc,
  service, method, route
limit @row_limit offset @row_offset;

-- name: APIMetricRouteCount :one
-- the number of route rows in the whole list, for the total of a page
select count(*)::bigint
from (
  select distinct service, method, route
  from (
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_minute
  where @resolution::text = 'minute'
    and bucket_start >= @range_from::timestamptz and bucket_start < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_hourly h
  where @resolution::text = 'hour'
    and h.bucket_start >= @range_from::timestamptz and h.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, p.hour as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'hour'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_daily d
  where @resolution::text = 'day'
    and d.bucket_start >= @range_from::timestamptz and d.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = d.tenant_id
                    and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, pd.day as bucket_start
  from (select distinct tenant_id, date_trunc('day', hour, 'UTC') as day
        from api.rollup_pending
        where @resolution::text = 'day' and hour >= @range_from::timestamptz and hour < @range_to::timestamptz) pd
  join api.metrics_hourly h
    on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
  where not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, date_trunc('day', p.hour, 'UTC') as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'day'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  ) r
) d;

-- name: APIMetricStatusCodes :many
-- application requests per code, and the infrastructure requests the
-- handler reports beside them
select status_code,
       coalesce(sum(count) filter (where traffic_class = 'app'), 0)::bigint as requests,
       coalesce(sum(count) filter (where traffic_class <> 'app'), 0)::bigint as infrastructure_requests
from (
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_minute
  where @resolution::text = 'minute'
    and bucket_start >= @range_from::timestamptz and bucket_start < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_hourly h
  where @resolution::text = 'hour'
    and h.bucket_start >= @range_from::timestamptz and h.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, p.hour as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'hour'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_daily d
  where @resolution::text = 'day'
    and d.bucket_start >= @range_from::timestamptz and d.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = d.tenant_id
                    and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, pd.day as bucket_start
  from (select distinct tenant_id, date_trunc('day', hour, 'UTC') as day
        from api.rollup_pending
        where @resolution::text = 'day' and hour >= @range_from::timestamptz and hour < @range_to::timestamptz) pd
  join api.metrics_hourly h
    on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
  where not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, date_trunc('day', p.hour, 'UTC') as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'day'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
    and (sqlc.narg(route)::text is null or route = sqlc.narg(route)::text)
    and (sqlc.narg(method)::text is null or method = sqlc.narg(method)::text)
) r
group by status_code
having sum(count) > 0
order by status_code;

-- name: APIMetricServiceTotals :many
-- application and infrastructure requests and lossy per service over its
-- selected source
select service,
       coalesce(sum(count) filter (where traffic_class = 'app'), 0)::bigint as requests,
       coalesce(sum(count) filter (where traffic_class <> 'app'), 0)::bigint as infrastructure_requests,
       coalesce(bool_or(lossy) filter (where traffic_class = 'app'), false)::boolean as lossy
from (
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_minute
  where @resolution::text = 'minute'
    and bucket_start >= @range_from::timestamptz and bucket_start < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_hourly h
  where @resolution::text = 'hour'
    and h.bucket_start >= @range_from::timestamptz and h.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, p.hour as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'hour'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, bucket_start
  from api.metrics_daily d
  where @resolution::text = 'day'
    and d.bucket_start >= @range_from::timestamptz and d.bucket_start < @range_to::timestamptz
    and not exists (select 1 from api.rollup_pending p where p.tenant_id = d.tenant_id
                    and p.hour >= d.bucket_start and p.hour < d.bucket_start + interval '24 hours')
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, pd.day as bucket_start
  from (select distinct tenant_id, date_trunc('day', hour, 'UTC') as day
        from api.rollup_pending
        where @resolution::text = 'day' and hour >= @range_from::timestamptz and hour < @range_to::timestamptz) pd
  join api.metrics_hourly h
    on h.tenant_id = pd.tenant_id and h.bucket_start >= pd.day and h.bucket_start < pd.day + interval '24 hours'
  where not exists (select 1 from api.rollup_pending p where p.tenant_id = h.tenant_id and p.hour = h.bucket_start)
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
  union all
  select service, method, route, status_code, source, traffic_class, counts, count, lossy, date_trunc('day', p.hour, 'UTC') as bucket_start
  from api.rollup_pending p
  join api.metrics_minute m
    on m.tenant_id = p.tenant_id and m.bucket_start >= p.hour and m.bucket_start < p.hour + interval '1 hour'
  where @resolution::text = 'day'
    and p.hour >= @range_from::timestamptz and p.hour < @range_to::timestamptz
    and ((source = 'metric' and service = any(@metric_services::text[]))
         or (source = 'span' and service = any(@span_services::text[])))
) r
group by service
order by service;

-- name: TrafficClassRules :many
-- when each traffic class rule set took effect; rows before the first one
-- hold every request as application traffic
select version, applied_from from api.traffic_class_rules order by version;

-- name: APIMetricWarnings :many
-- ingest warnings of the utc days from @first_day to @last_day, both included
select day, kind, count
from api.ingest_warnings
where day >= @first_day::date and day <= @last_day::date
order by day, kind;
