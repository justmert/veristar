-- name: PurgeAggTenant :many
-- deletes a removed tenant's agg rows through the security definer function
-- of 00019 (cp_system has no right in agg itself) and returns the rows
-- deleted per table
select p.table_name::text as table_name, p.rows_deleted::bigint as rows_deleted
from agg.purge_tenant(@tenant_id::uuid) as p;
