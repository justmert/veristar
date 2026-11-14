-- the metrics writer refuses queued API_METRICS messages of a removed tenant
-- or a revoked token, so a message
-- still queued when a tenant is purged writes no row afterwards. the writer
-- reads the two status columns it needs and nothing else: column grants keep
-- every other column (names, hashes, answers) out of its reach, as before.
--
-- the check takes a share lock on the token row in the writer's fenced
-- transaction. the purge revokes the tenant's tokens before it deletes its
-- api rows, so a revocation waits for a writer transaction that already
-- passed the check, and the purge then deletes what that transaction wrote;
-- a writer transaction that starts after the revocation sees it and refuses.
-- a share lock needs an update right and an update policy, which the writer
-- already has for last_seen_at (00003).

-- +goose Up
grant select (tenant_id, status) on cp.otlp_tokens to metrics_writer;
grant select (id, status) on cp.tenants to metrics_writer;
create policy tenants_writer_status on cp.tenants for select to metrics_writer using (true);

-- +goose Down
drop policy tenants_writer_status on cp.tenants;
revoke select (id, status) on cp.tenants from metrics_writer;
revoke select (tenant_id, status) on cp.otlp_tokens from metrics_writer;
