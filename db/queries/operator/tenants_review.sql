-- operator review reads (control-plane-api 5.3 and 5.4), as cp_operator:
-- cp.tenants, cp.operator_sites (no verify_token), cp.web_ingest_hourly.
-- internal tenants (the monitoring probe) are never shown.

-- name: OperatorTenantPage :many
-- one query for every sort and order: k is the sort key as text that sorts
-- like the column (iso timestamps with microseconds, or lower(name)), and
-- the keyset runs over (k, id) in the requested direction.
with t as (
  select t.id, t.slug, t.name, t.status, t.status_reason,
         coalesce(t.enrollment_answers ->> 'website', '')::text as website,
         t.created_at, t.status_changed_at,
         (case @sort::text
            when 'name' then lower(t.name)
            when 'status_changed_at' then to_char(t.status_changed_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US')
            else to_char(t.created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US')
          end)::text as k,
         (select count(*) from cp.operator_sites s
           where s.tenant_id = t.id and s.status <> 'removed')::bigint as site_count,
         (select count(*) from cp.operator_sites s
           where s.tenant_id = t.id and s.status = 'pending' and t.status = 'active')::bigint as sites_in_review,
         (select count(*) from cp.operator_sites s
           where s.tenant_id = t.id and s.status <> 'removed'
             and s.verified_at is not null and s.last_verify_error is null)::bigint as sites_verified,
         (select count(*) from cp.operator_sites s
           where s.tenant_id = t.id and s.status <> 'removed'
             and s.stellar_toml ->> 'status' = 'found')::bigint as sites_with_stellar_toml
  from cp.tenants t
  where not t.is_internal
    and (cardinality(@statuses::text[]) = 0 or t.status = any(@statuses::text[]))
    and (sqlc.narg(q)::text is null
         or strpos(lower(t.name), lower(sqlc.narg(q)::text)) > 0
         or strpos(lower(t.slug), lower(sqlc.narg(q)::text)) > 0
         or strpos(lower(coalesce(t.enrollment_answers ->> 'website', '')), lower(sqlc.narg(q)::text)) > 0
         or exists (select 1 from cp.operator_sites s
                    where s.tenant_id = t.id and strpos(lower(s.domain), lower(sqlc.narg(q)::text)) > 0))
)
select id, slug, name, status, status_reason, website, created_at, status_changed_at, k,
       site_count, sites_in_review, sites_verified, sites_with_stellar_toml
from t
where (not @review::boolean or status = 'pending' or sites_in_review > 0)
  and (sqlc.narg(after_k)::text is null
       or (@descending::boolean and (k, id) < (sqlc.narg(after_k)::text, sqlc.narg(after_id)::uuid))
       or (not @descending::boolean and (k, id) > (sqlc.narg(after_k)::text, sqlc.narg(after_id)::uuid)))
order by case when @descending::boolean then k end desc,
         case when @descending::boolean then id end desc,
         case when not @descending::boolean then k end asc,
         case when not @descending::boolean then id end asc
limit @row_limit;

-- name: OperatorTenant :one
select * from cp.tenants where id = @id and not is_internal;

-- name: OperatorTenantSites :many
-- OperatorSite of contract 5.2 for every site of one tenant
select v.id, v.tenant_id, v.name, v.domain, v.origins, v.status, v.status_reason,
       (v.status = 'pending' and t.status = 'active')::boolean as in_review,
       v.domain_previously_removed,
       v.verify_method, v.verified_at, v.last_verify_at, v.last_verify_error,
       v.stellar_toml, v.stellar_toml_checked_at, v.created_at, v.updated_at
from cp.operator_sites v
join cp.tenants t on t.id = v.tenant_id
where v.tenant_id = @tenant_id
order by v.created_at, v.id;

-- name: OperatorTenantIngest :many
-- non-zero outcome sums over [from_at, to_at) for all sites of a tenant
select outcome, sum(count)::bigint as total
from cp.web_ingest_hourly
where tenant_id = @tenant_id and hour >= @from_at and hour < @to_at
group by outcome
having sum(count) > 0
order by outcome;
