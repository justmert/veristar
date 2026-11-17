-- aggregate-web: replaces one site's rows for a utc day and
-- band with what umami's /stats and /metrics returned. run in the fenced
-- jobs transaction (scheduler.Leader.Tx).

-- name: LockAggTenant :exec
-- shared with every other aggregate-web write of the tenant, exclusive in
-- agg.purge_tenant: a purge waits for writes in flight, and a write that
-- comes after the purge sees the removed status
-- tenant_key is the tenant id as text, the form agg.purge_tenant hashes
select pg_advisory_xact_lock_shared(hashtextextended('agg.tenant:' || @tenant_key::text, 0));

-- name: AggTenantStatus :one
select status from cp.tenants where id = @id;

-- name: UpsertWebDaily :exec
insert into agg.web_daily (site_id, tenant_id, day, band, pageviews, visitors, visits, bounces, totaltime, read_at)
values (@site_id, @tenant_id, @day, @band, @pageviews, @visitors, @visits, @bounces, @totaltime, now())
on conflict (site_id, day, band) do update
  set tenant_id = excluded.tenant_id,
      pageviews = excluded.pageviews,
      visitors  = excluded.visitors,
      visits    = excluded.visits,
      bounces   = excluded.bounces,
      totaltime = excluded.totaltime,
      read_at   = excluded.read_at;

-- name: DeleteWebDailyDims :execrows
delete from agg.web_daily_dim where site_id = @site_id and day = @day and band = @band;

-- name: InsertWebDailyDims :execrows
-- the three arrays are parallel: one row per index. set-returning functions
-- in one select list advance together, so row i takes element i of each.
insert into agg.web_daily_dim (site_id, tenant_id, day, band, dimension, key, value)
select @site_id::uuid, @tenant_id::uuid, @day::date, @band::text,
       unnest(@dimensions::text[]), unnest(@keys::text[]), unnest(@dim_values::bigint[]);

-- name: DeleteSiteAggregates :one
-- a site reset (erasure) leaves no web data; its aggregates go with it,
-- the integrity grades and coverage estimates included. the edge counters
-- in cp go in DeleteSiteEdgeCounters, as cp_system
with d as (delete from agg.web_daily w where w.site_id = @site_id::uuid returning 1),
     dd as (delete from agg.web_daily_dim m where m.site_id = @site_id::uuid returning 1),
     i as (delete from agg.web_integrity_daily g where g.site_id = @site_id::uuid returning 1),
     c as (delete from agg.web_coverage_daily v where v.site_id = @site_id::uuid returning 1)
select (select count(*) from d)::bigint as daily, (select count(*) from dd)::bigint as dims,
       (select count(*) from i)::bigint as integrity, (select count(*) from c)::bigint as coverage;
