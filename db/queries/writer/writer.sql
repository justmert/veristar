-- queries for metrics_writer. it may stamp last_seen_at on an otlp token and
-- nothing else in cp (the fence has its own package).

-- name: TouchOTLPToken :execrows
-- the role may not read last_seen_at, so the stamp is a plain
-- overwrite; the single active metricswriter writes in time order anyway
update cp.otlp_tokens
set last_seen_at = @seen_at::timestamptz
where id = @id;
