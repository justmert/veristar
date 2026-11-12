-- queries for cp_operator (db.WithOperator): aggregates and non-secret
-- columns across tenants.

-- name: ListTenants :many
select id, slug, name, status, is_internal, created_at, status_changed_at
from cp.tenants
order by created_at, id;

-- name: ListOperatorSites :many
select * from cp.operator_sites order by created_at, id;

-- name: RecordAPIKeyUse :exec
-- one row per key and hour; rejected counts requests refused after the key matched
insert into cp.api_key_usage_hourly (key_id, hour, requests, rejected)
values (@key_id, date_trunc('hour', @at::timestamptz), 1, case when @rejected::boolean then 1 else 0 end)
on conflict (key_id, hour) do update
  set requests = cp.api_key_usage_hourly.requests + 1,
      rejected = cp.api_key_usage_hourly.rejected + excluded.rejected;

-- name: TouchOperatorAPIKey :execrows
update cp.operator_api_keys set last_used_at = @at::timestamptz where id = @id;
