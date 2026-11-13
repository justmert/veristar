-- cp_tenant queries of the site routes (control-plane-api 4.3)

-- name: InsertSite :one
insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
values (@id, cp.current_tenant_id(), @name, @domain, @origins, @quarantine_website_id, @status, @verify_token)
returning *;

-- name: ListSitesPage :many
select * from cp.sites
where (@include_removed::boolean or status <> 'removed')
  and (sqlc.narg(after_created_at)::timestamptz is null
       or (created_at, id) > (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
order by created_at, id
limit @row_limit;

-- name: GetSite :one
select * from cp.sites where id = @id;

-- name: CountLiveSites :one
select count(*) from cp.sites where status <> 'removed';

-- name: UpdateSite :one
update cp.sites
set name = coalesce(sqlc.narg(name)::text, name),
    origins = coalesce(sqlc.narg(origins)::text[], origins),
    collect_utm = coalesce(sqlc.narg(collect_utm)::boolean, collect_utm),
    updated_at = now()
where id = @id
returning *;

-- name: RemoveSite :one
update cp.sites set status = 'removed', updated_at = now()
where id = @id
returning *;
