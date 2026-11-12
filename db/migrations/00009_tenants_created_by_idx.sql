-- enrollment allows at most 10 new tenants per user per day
-- (control-plane-api 4.2.1); the check counts by creator and time.

-- +goose Up
create index tenants_created_by_idx on cp.tenants (created_by, created_at);

-- +goose Down
drop index cp.tenants_created_by_idx;
