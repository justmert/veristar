-- utm collection as a per-site setting, off by default (rfp conformance
-- audit, gap 4). the gateway drops utm_* and ref from page urls unless the
-- site's owner turns this on; the reconciler publishes it in the site's
-- registry_sites entry. off by default because no metric the service
-- promises needs campaign parameters and cnil's audience measurement tool
-- recommends not collecting them (docs/compliance/privacy-strategy.md
-- section 9). existing sites start off: until now the gateway kept these
-- parameters for every site, and keeping them stays a decision per site.
--
-- cp_tenant may write the column, the site edit route's only new write
-- (column grants of 00024).

-- +goose Up
alter table cp.sites add column collect_utm boolean not null default false;
grant update (collect_utm) on cp.sites to cp_tenant;

-- +goose Down
revoke update (collect_utm) on cp.sites from cp_tenant;
alter table cp.sites drop column collect_utm;
