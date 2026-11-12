-- the operator review queue lists tenants by status, newest first
-- (control-plane-api 5.3), and pending tenants are a small share of all of
-- them, so the status filter needs its own index.

-- +goose Up
create index tenants_status_created_idx on cp.tenants (status, created_at);

-- +goose Down
drop index cp.tenants_status_created_idx;
