-- cp_system queries of the domain checks: the verify rate limit of the
-- control plane and the jobs task verify-domains (control-plane-api 5.8, 5.12)

-- name: RecentUserVerifies :one
-- console checks of one site since a time, read from the audit rows the
-- route writes, so the limit holds across control plane instances
select count(*)::bigint as checks, coalesce(min(at), now())::timestamptz as oldest
from cp.audit_log
where target = @target and action = 'site.verify' and actor like 'user:%' and at > @since;

-- name: ListSitesDueForCheck :many
-- non-removed sites of non-removed tenants whose verification or
-- stellar.toml check is older than the cutoff, oldest first
select s.id, s.tenant_id, s.domain, s.verify_token, s.verify_method, s.verified_at,
       s.last_verify_at, s.last_verify_error
from cp.sites s
join cp.tenants t on t.id = s.tenant_id
where s.status <> 'removed' and t.status <> 'removed'
  and (s.last_verify_at is null or s.last_verify_at < @cutoff
       or s.stellar_toml_checked_at is null or s.stellar_toml_checked_at < @cutoff)
order by least(coalesce(s.last_verify_at, '-infinity'), coalesce(s.stellar_toml_checked_at, '-infinity')), s.id
limit @row_limit;

-- name: SystemSetSiteVerifyResult :one
update cp.sites
set verify_method = case when @ok::boolean then sqlc.narg(method)::text else verify_method end,
    verified_at = case when @ok::boolean then now() else verified_at end,
    last_verify_at = now(),
    last_verify_error = sqlc.narg(error)::text
where id = @id and status <> 'removed'
returning id, tenant_id, verify_method, verified_at, last_verify_at, last_verify_error;

-- name: SystemLockSiteStellarToml :one
select stellar_toml from cp.sites where id = @id and status <> 'removed' for update;

-- name: SystemSetSiteStellarToml :exec
update cp.sites set stellar_toml = @doc, stellar_toml_checked_at = now()
where id = @id and status <> 'removed';
