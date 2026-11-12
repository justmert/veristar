-- api retention queries for the jobs role (the design), beside api.sql.

-- name: RollupPendingInDay :one
-- whether any hour of the utc day starting at day_start still waits for the
-- rollup. its minute partition must not be dropped before the rollup has
-- read it, or the hour's rollup would have nothing to recompute from.
select exists (
  select 1 from api.rollup_pending
  where hour >= sqlc.arg(day_start)::timestamptz and hour < sqlc.arg(day_start)::timestamptz + interval '1 day'
)::boolean as pending;
