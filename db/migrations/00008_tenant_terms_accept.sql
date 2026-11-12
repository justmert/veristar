-- accepting a new terms version (control-plane-api 4.2.6) is an owner action
-- on the operator's own tenant, so it runs as cp_tenant under row-level security
-- like every other tenant change, instead of as cp_system with no row filter.

-- +goose Up
grant update (accepted_terms_version) on cp.tenants to cp_tenant;

-- +goose Down
revoke update (accepted_terms_version) on cp.tenants from cp_tenant;
