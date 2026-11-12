-- the operator audit list (control-plane-api 5.9) filters by target,
-- action (exact or prefix), actor and tenant, newest first. text_pattern_ops
-- lets the action index serve the prefix filter (like 'tenant.%') under any
-- collation, and it still serves equality.

-- +goose Up
create index audit_log_target_at_idx on cp.audit_log (target, at);
create index audit_log_action_at_idx on cp.audit_log (action text_pattern_ops, at);
create index audit_log_actor_at_idx on cp.audit_log (actor, at);
create index audit_log_tenant_at_idx on cp.audit_log ((detail ->> 'tenant_id'), at);

-- +goose Down
drop index cp.audit_log_tenant_at_idx;
drop index cp.audit_log_actor_at_idx;
drop index cp.audit_log_action_at_idx;
drop index cp.audit_log_target_at_idx;
