-- the writer's per-tenant stream cap counted every
-- api.series_state row kept for 7 idle days, so a tenant whose processes
-- restart often (deploys, autoscaling, worker recycling) reached it with
-- streams that were long dead, and every new stream was then refused for up
-- to a week. the writer now counts only streams with a point in the last
-- hour (metricswriter.ActiveSeriesWindow). the stored rows are bounded here
-- instead: api-retention calls this every hour, and a tenant above
-- max_stored rows loses its longest idle streams, idle since before
-- idle_before, never an active one. a stream evicted while its process
-- still runs comes back as a new stream for the writer (first-point rule),
-- which costs at most one export interval of that stream.

-- +goose Up
-- +goose StatementBegin
create function api.evict_idle_series_states(idle_before timestamptz, max_stored bigint)
returns bigint
language sql
set search_path = pg_catalog, pg_temp
as $$
  with over as (
    select tenant_id, count(*) filter (where last_seen_at >= idle_before) as active
    from api.series_state
    group by tenant_id
    having count(*) > max_stored
  ), ranked as (
    select s.tenant_id, s.series_hash, s.start_unix_nano, o.active,
           row_number() over (partition by s.tenant_id order by s.last_seen_at desc) as rn
    from api.series_state s
    join over o on o.tenant_id = s.tenant_id
    where s.last_seen_at < idle_before
  ), gone as (
    delete from api.series_state s
    using ranked r
    where s.tenant_id = r.tenant_id and s.series_hash = r.series_hash
      and s.start_unix_nano = r.start_unix_nano
      and r.rn > greatest(max_stored - r.active, 0)
    returning 1
  )
  select count(*) from gone
$$;
-- +goose StatementEnd
revoke all on function api.evict_idle_series_states(timestamptz, bigint) from public;
grant execute on function api.evict_idle_series_states(timestamptz, bigint) to jobs;

-- +goose Down
drop function api.evict_idle_series_states(timestamptz, bigint);
