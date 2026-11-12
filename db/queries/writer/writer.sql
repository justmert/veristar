-- queries for metrics_writer. it may stamp last_seen_at on an otlp token and
-- nothing else in cp (the fence has its own package).

-- name: TouchOTLPToken :execrows
-- the role may not read last_seen_at, so the stamp is a plain
-- overwrite; the single active metricswriter writes in time order anyway
update cp.otlp_tokens
set last_seen_at = @seen_at::timestamptz
where id = @id;

-- name: IngestStatus :one
-- the token and tenant status of an API_METRICS message, checked in the
-- writer's fenced transaction. the share lock on the token row makes a
-- concurrent revocation wait for this transaction (00018). no row means the
-- token does not belong to the tenant, or does not exist.
select o.status as token_status, t.status as tenant_status
from cp.otlp_tokens o
join cp.tenants t on t.id = o.tenant_id
where o.id = @token_id and o.tenant_id = @tenant_id
for share of o;
