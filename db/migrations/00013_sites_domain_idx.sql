-- a new site's domain is compared with the domains of removed tenants' sites
-- (control-plane-api 5.6), and the operator search matches site domains
-- (5.3), so lookups by domain must not scan every site.

-- +goose Up
create index sites_domain_idx on cp.sites (domain);

-- +goose Down
drop index cp.sites_domain_idx;
