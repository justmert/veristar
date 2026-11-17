-- api-rollup parks a pending hour it cannot recompute because the database
-- refuses the result (a data error such as a count sum past bigint): the
-- mark stays in api.rollup_pending with the error, so the hour keeps
-- counting as pending everywhere (readers take it from its minute rows,
-- the consistency check leaves it alone, retention keeps its minutes), and
-- every other hour of every tenant is rolled up as usual. an operator
-- clears parked_at once the cause is dealt with (docs/runbooks/alerts.md).

-- +goose Up
alter table api.rollup_pending
  add column parked_at timestamptz,
  add column parked_error text check (parked_error is null or length(parked_error) <= 1000),
  add constraint rollup_pending_parked_check check ((parked_at is null) = (parked_error is null));

-- +goose Down
alter table api.rollup_pending
  drop constraint rollup_pending_parked_check,
  drop column parked_error,
  drop column parked_at;
