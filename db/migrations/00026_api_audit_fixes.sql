-- fixes from the review of the api metrics path
--.
--
-- A-2: the rollup must never recompute an hour while a writer transaction
-- that writes minute rows into it is still open; such a transaction's rows
-- are invisible to the rollup, and the mark it finds already present gives
-- no row lock, so its requests were lost from the hourly and daily rows for
-- good. the writer now takes a shared transaction-level advisory lock on
-- every (tenant, hour) before it writes the minutes, and the rollup takes
-- the exclusive lock with try before it recomputes a claimed hour, putting
-- the mark back when a writer holds it. both sides derive the lock key from
-- this one function, so they can never disagree on it. a hash collision
-- between two hours only makes the rollup skip an hour until its next run.
--
-- A-5: a cumulative stream whose first point became a baseline remembers
-- that, so its second point can tell from the export cadence whether the
-- process had only just started, and then counts in full instead of losing
-- the first export interval.
--
-- A-12: a histogram whose source buckets are much wider than the canonical
-- ones puts requests into a canonical bucket far above their real latency.
-- such requests are counted under their own warning kind.

-- +goose Up
create function api.rollup_lock_key(tenant uuid, hour timestamptz) returns bigint
language sql immutable strict parallel safe
set search_path = pg_catalog
as $$
  select hashtextextended('api.rollup:' || tenant::text || ':' || extract(epoch from hour)::bigint::text, 0)
$$;
grant execute on function api.rollup_lock_key(uuid, timestamptz) to metrics_writer, jobs;

alter table api.series_state add column baseline boolean not null default false;

alter table api.ingest_warnings drop constraint ingest_warnings_kind_check;
alter table api.ingest_warnings add constraint ingest_warnings_kind_check
  check (kind in ('cardinality_overflow', 'lossy_histogram', 'secret_in_path', 'span_derived', 'coarse_buckets'));

-- +goose Down
delete from api.ingest_warnings where kind = 'coarse_buckets';
alter table api.ingest_warnings drop constraint ingest_warnings_kind_check;
alter table api.ingest_warnings add constraint ingest_warnings_kind_check
  check (kind in ('cardinality_overflow', 'lossy_histogram', 'secret_in_path', 'span_derived'));

alter table api.series_state drop column baseline;

revoke execute on function api.rollup_lock_key(uuid, timestamptz) from metrics_writer, jobs;
drop function api.rollup_lock_key(uuid, timestamptz);
