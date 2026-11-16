-- the tenant web dashboard (docs/reference/control-plane-api.md section 18)
-- reads a project's breakdowns from the same daily aggregates the operator
-- views read, so a member and an operator never see different numbers for
-- the same days. 00019 gave agg.web_daily_dim forced row-level security and
-- a cp_tenant policy keyed on cp.current_tenant_id(); this grant is the one
-- that policy was written for. cp_tenant reads only, as it reads
-- agg.web_daily since 00032; usage on agg came with 00032 too.

-- +goose Up
grant select on agg.web_daily_dim to cp_tenant;

-- +goose Down
revoke select on agg.web_daily_dim from cp_tenant;
