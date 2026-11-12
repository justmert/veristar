-- tenant-scoped handlers write their audit row in the same transaction as the
-- change (control-plane-api 3.3), and they run as cp_tenant. insert only: the
-- table stays append-only through the triggers of 00003, and without select a
-- tenant still cannot read anyone's audit history.

-- +goose Up
grant insert on cp.audit_log to cp_tenant;

-- +goose Down
revoke insert on cp.audit_log from cp_tenant;
