-- cp_tenant queries of the domain verification routes (control-plane-api 5.8)
-- and the stellar.toml fetch after site creation (5.12)

-- name: SetSiteVerifyResult :one
-- a success records the method and time; a failure keeps the last success
-- and only records when and why this check failed
update cp.sites
set verify_method = case when @ok::boolean then sqlc.narg(method)::text else verify_method end,
    verified_at = case when @ok::boolean then now() else verified_at end,
    last_verify_at = now(),
    last_verify_error = sqlc.narg(error)::text
where id = @id and status <> 'removed'
returning *;

-- name: LockSiteStellarToml :one
select stellar_toml from cp.sites where id = @id and status <> 'removed' for update;

-- name: SetSiteStellarToml :exec
update cp.sites set stellar_toml = @doc, stellar_toml_checked_at = now()
where id = @id and status <> 'removed';
