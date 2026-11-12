-- cp_system queries for the otlp token state the reconciler publishes and
-- the removal purge (control-plane-api 6).

-- name: ProvisionOTLPTokens :many
select id, hash, status from cp.otlp_tokens where tenant_id = @tenant_id order by id;

-- name: PurgeRevokeOTLPTokens :many
-- removal revokes every token for good; resume never applies to a removed tenant
update cp.otlp_tokens
set status = 'revoked', revoked_at = now()
where tenant_id = @tenant_id and status = 'active'
returning id;
