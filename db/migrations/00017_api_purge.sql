-- tenant purge for the api schema (control-plane-api 5.5 and 5.13).
-- the purge runs in the reconciler's cp_system transaction, in the control
-- plane and in the jobs daemon, so the deletes commit with the step record.
-- cp_system has no right in api (00016); instead it may call one security
-- definer function that deletes the rows of a tenant that is already
-- removed, and nothing else.
--
-- every api table forces row-level security, which binds the table owner too, so
-- the function's owner gets a delete-only policy on each table it purges.
-- no application role is platform_owner, so these policies open nothing to
-- them.

-- +goose Up
create policy metrics_minute_purge on api.metrics_minute for delete to platform_owner using (true);
create policy metrics_hourly_purge on api.metrics_hourly for delete to platform_owner using (true);
create policy metrics_daily_purge on api.metrics_daily for delete to platform_owner using (true);
create policy series_state_purge on api.series_state for delete to platform_owner using (true);
create policy resource_seen_purge on api.resource_seen for delete to platform_owner using (true);
create policy route_registry_purge on api.route_registry for delete to platform_owner using (true);
create policy ingest_warnings_purge on api.ingest_warnings for delete to platform_owner using (true);
create policy rollup_pending_purge on api.rollup_pending for delete to platform_owner using (true);
-- a delete with a where clause reads the rows it matches, so platform_owner needs
-- select visibility as well; it is limited to the same tables
create policy metrics_minute_purge_read on api.metrics_minute for select to platform_owner using (true);
create policy metrics_hourly_purge_read on api.metrics_hourly for select to platform_owner using (true);
create policy metrics_daily_purge_read on api.metrics_daily for select to platform_owner using (true);
create policy series_state_purge_read on api.series_state for select to platform_owner using (true);
create policy resource_seen_purge_read on api.resource_seen for select to platform_owner using (true);
create policy route_registry_purge_read on api.route_registry for select to platform_owner using (true);
create policy ingest_warnings_purge_read on api.ingest_warnings for select to platform_owner using (true);
create policy rollup_pending_purge_read on api.rollup_pending for select to platform_owner using (true);
-- the function checks the tenant's status; platform_owner sees removed tenants
-- only, which is all that check needs
create policy tenants_purge_read on cp.tenants for select to platform_owner using (status = 'removed');

-- +goose StatementBegin
-- deletes a removed tenant's api rows and returns how many went per table.
-- minute rows and the writer's per-tenant state always go; hourly rows,
-- daily rows and the daily warning counters are aggregates and stay when
-- keep_aggregates is true (contract 5.13). api.ingest_dedup is kept on
-- purpose: its rows hold only message ids, expire after 8 days, and are what
-- stops a message still queued on API_METRICS from being applied twice.
create function api.purge_tenant(tenant uuid, keep_aggregates boolean)
returns table (table_name text, rows_deleted bigint)
language plpgsql security definer
set search_path = pg_catalog, pg_temp
as $$
declare
  n bigint;
begin
  if tenant is null or keep_aggregates is null then
    raise exception 'api.purge_tenant: tenant and keep_aggregates are required'
      using errcode = 'null_value_not_allowed';
  end if;
  -- the function deletes data, so it refuses any tenant that is not removed;
  -- a caller with the cp_system role cannot use it to wipe a live tenant
  if not exists (select 1 from cp.tenants t where t.id = tenant and t.status = 'removed') then
    raise exception 'api.purge_tenant: tenant % is not removed', tenant
      using errcode = 'object_not_in_prerequisite_state';
  end if;

  delete from api.metrics_minute m where m.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'metrics_minute'; rows_deleted := n; return next;
  delete from api.series_state s where s.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'series_state'; rows_deleted := n; return next;
  delete from api.resource_seen r where r.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'resource_seen'; rows_deleted := n; return next;
  delete from api.route_registry r where r.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'route_registry'; rows_deleted := n; return next;
  delete from api.rollup_pending p where p.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'rollup_pending'; rows_deleted := n; return next;

  if keep_aggregates then
    return;
  end if;
  delete from api.metrics_hourly h where h.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'metrics_hourly'; rows_deleted := n; return next;
  delete from api.metrics_daily d where d.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'metrics_daily'; rows_deleted := n; return next;
  delete from api.ingest_warnings w where w.tenant_id = tenant;
  get diagnostics n = row_count;
  table_name := 'ingest_warnings'; rows_deleted := n; return next;
end
$$;
-- +goose StatementEnd

revoke all on function api.purge_tenant(uuid, boolean) from public;
grant usage on schema api to cp_system;
grant execute on function api.purge_tenant(uuid, boolean) to cp_system;

-- +goose Down
revoke execute on function api.purge_tenant(uuid, boolean) from cp_system;
revoke usage on schema api from cp_system;
drop function api.purge_tenant(uuid, boolean);
drop policy tenants_purge_read on cp.tenants;
drop policy rollup_pending_purge_read on api.rollup_pending;
drop policy ingest_warnings_purge_read on api.ingest_warnings;
drop policy route_registry_purge_read on api.route_registry;
drop policy resource_seen_purge_read on api.resource_seen;
drop policy series_state_purge_read on api.series_state;
drop policy metrics_daily_purge_read on api.metrics_daily;
drop policy metrics_hourly_purge_read on api.metrics_hourly;
drop policy metrics_minute_purge_read on api.metrics_minute;
drop policy rollup_pending_purge on api.rollup_pending;
drop policy ingest_warnings_purge on api.ingest_warnings;
drop policy route_registry_purge on api.route_registry;
drop policy resource_seen_purge on api.resource_seen;
drop policy series_state_purge on api.series_state;
drop policy metrics_daily_purge on api.metrics_daily;
drop policy metrics_hourly_purge on api.metrics_hourly;
drop policy metrics_minute_purge on api.metrics_minute;
