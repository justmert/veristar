-- a site in review (control-plane-api 5.6) is approved or rejected by an
-- operator with a reason the tenant sees. cp.operator_sites gains that
-- reason and domain_previously_removed, the review rule of 5.6 evaluated
-- for each site, so operator reads never touch cp.sites directly.

-- +goose Up
alter table cp.sites add column status_reason text;

-- the view runs with its owner's rights under forced row-level security, so
-- platform_owner needs a read policy on cp.tenants for the removed-tenant check,
-- like sites_operator_view in 00003
create policy tenants_operator_view on cp.tenants for select to platform_owner using (true);

-- create or replace keeps the grants; new columns go last, as postgresql
-- requires. a site of the removed tenant itself does not count as a match.
create or replace view cp.operator_sites with (security_barrier) as
select s.id, s.tenant_id, s.name, s.domain, s.origins, s.quarantine_website_id, s.status,
       s.verify_method, s.verified_at, s.last_verify_at, s.last_verify_error,
       s.stellar_toml, s.stellar_toml_checked_at, s.created_at, s.updated_at,
       s.status_reason,
       exists (
         select 1
         from cp.sites o
         join cp.tenants t on t.id = o.tenant_id
         where t.status = 'removed' and o.tenant_id <> s.tenant_id
           and (o.domain = s.domain
                or right(s.domain, length(o.domain) + 1) = '.' || o.domain
                or right(o.domain, length(s.domain) + 1) = '.' || s.domain)
       ) as domain_previously_removed
from cp.sites s;

-- +goose Down
drop view cp.operator_sites;
create view cp.operator_sites with (security_barrier) as
select id, tenant_id, name, domain, origins, quarantine_website_id, status,
       verify_method, verified_at, last_verify_at, last_verify_error,
       stellar_toml, stellar_toml_checked_at, created_at, updated_at
from cp.sites;
grant select on cp.operator_sites to cp_system, cp_operator;
drop policy tenants_operator_view on cp.tenants;
alter table cp.sites drop column status_reason;
