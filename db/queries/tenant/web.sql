-- cp_tenant queries of the web dashboard (control-plane-api 18). row-level
-- security limits every statement to the tenant in app.tenant_id.

-- name: WebDashSites :many
-- every site of the tenant, removed ones included: their aggregates stay in
-- the project's totals as they do in the operator views
select id, name, domain, status, quarantine_website_id, collect_utm
from cp.sites
order by created_at, id;

-- name: WebDashDaily :many
-- one row per utc day of the range: the sums over the selected sites (all
-- of the tenant's rows when site_id is null) and how many site rows it has
select day,
       sum(pageviews)::bigint as pageviews, sum(visitors)::bigint as visitors,
       sum(visits)::bigint as visits, sum(bounces)::bigint as bounces,
       sum(totaltime)::bigint as totaltime, max(read_at)::timestamptz as read_at,
       count(*)::bigint as site_rows
from agg.web_daily
where band = @band and day between @from_day::date and @to_day::date
  and (sqlc.narg(site_id)::uuid is null or site_id = sqlc.narg(site_id)::uuid)
group by day
order by day;

-- name: WebDashBandTotals :one
-- page views and visitors of the other band over the range, for the share
-- of quarantined traffic
select coalesce(sum(pageviews), 0)::bigint as pageviews, coalesce(sum(visitors), 0)::bigint as visitors
from agg.web_daily
where band = @band and day between @from_day::date and @to_day::date
  and (sqlc.narg(site_id)::uuid is null or site_id = sqlc.narg(site_id)::uuid);

-- name: WebDashDimension :many
-- a breakdown over the range: the sum of the daily values per key, largest
-- first, ties by key, with the number of distinct keys on every row
select key, sum(value)::bigint as value, count(*) over ()::bigint as total_keys
from agg.web_daily_dim
where band = @band and dimension = @dimension and day between @from_day::date and @to_day::date
  and (sqlc.narg(site_id)::uuid is null or site_id = sqlc.narg(site_id)::uuid)
group by key
order by sum(value) desc, key
limit @row_limit;
