-- operator reads of tenant lifecycle state, as cp_operator.

-- name: OperatorRemovalKeepAggregates :one
select value from cp.settings where key = 'removal_keep_aggregates';

-- name: OperatorTenantRemoval :one
-- the removal object of contract 5.4
select tenant_id, requested_at, requested_by, reason, keep_aggregates, steps, completed_at
from cp.tenant_removals
where tenant_id = @tenant_id;
