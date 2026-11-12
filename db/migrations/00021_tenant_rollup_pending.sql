-- the tenant api metrics routes (control-plane-api 7.2) read
-- api.rollup_pending: an hour still marked there has hourly and daily rows
-- that the next api-rollup run will replace, so hourly and daily answers
-- compute such hours from api.metrics_minute instead and show traffic
-- before the rollup runs. the cp_tenant policy of 00016 already limits the
-- table to the tenant in app.tenant_id; this adds only the read right.

-- +goose Up
grant select on api.rollup_pending to cp_tenant;

-- +goose Down
revoke select on api.rollup_pending from cp_tenant;
