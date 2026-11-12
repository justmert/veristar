-- name: PurgeAPITenant :many
-- deletes a removed tenant's api rows through the security definer function
-- of 00017 (cp_system has no right in api itself) and returns the rows
-- deleted per table. hourly, daily and warning rows stay when keep is true.
select p.table_name::text as table_name, p.rows_deleted::bigint as rows_deleted
from api.purge_tenant(@tenant_id::uuid, @keep_aggregates::boolean) as p;
