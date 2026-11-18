-- the text search of the operator audit log (control-plane-api 5.9, q).
-- the search matches a substring of a row's action, actor, target and
-- detail, case-insensitively, so a btree index cannot serve it; a trigram
-- index over the same expression can. pg_trgm is a trusted extension, so
-- platform_owner, the database owner, installs it; it goes into a schema of
-- its own that no application role can use, because only the index needs
-- its operator class. the planner uses the index for the ilike of the audit
-- query without any role holding a right on the extension's functions.
--
-- the expression must stay byte for byte the one in
-- db/queries/system/audit.sql, or the planner no longer matches the index.

-- +goose Up
create schema ext;
revoke all on schema ext from public;
create extension pg_trgm with schema ext;
create index audit_log_search_idx on cp.audit_log
  using gin ((action || ' ' || actor || ' ' || target || ' ' || detail::text) ext.gin_trgm_ops);

-- +goose Down
drop index cp.audit_log_search_idx;
drop extension pg_trgm;
drop schema ext;
