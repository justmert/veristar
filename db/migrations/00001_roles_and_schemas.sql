-- roles, database access and schemas for the platform database.
--
-- platformctl db migrate runs every migration as platform_owner (set role on
-- the admin session), so every object created here and later is owned by
-- platform_owner. platform_owner holds createrole only so that this file can
-- create the application roles. passwords never appear in migrations; they
-- are set by platformctl db roles.

-- +goose Up
-- +goose StatementBegin
do $$
declare
  r text;
begin
  foreach r in array array['cp_system', 'cp_tenant', 'cp_operator', 'metrics_writer', 'jobs'] loop
    if not exists (select 1 from pg_roles where rolname = r) then
      -- nologin until platformctl db roles sets a password. nobypassrls is
      -- the point: row-level security must hold for every application role.
      execute format(
        'create role %I nologin nosuperuser nocreatedb nocreaterole noinherit noreplication nobypassrls',
        r);
    end if;
  end loop;
end
$$;
-- +goose StatementEnd

-- +goose StatementBegin
do $$
begin
  -- the database name differs between dev, tests and production
  execute format('revoke all on database %I from public', current_database());
  execute format(
    'grant connect on database %I to cp_system, cp_tenant, cp_operator, metrics_writer, jobs',
    current_database());
end
$$;
-- +goose StatementEnd

create schema cp;
create schema api;
create schema agg;

revoke all on schema cp, api, agg from public;

grant usage on schema cp to cp_system, cp_tenant, cp_operator, metrics_writer, jobs;
grant usage on schema api to cp_operator, metrics_writer, jobs;
grant usage on schema agg to cp_operator, jobs;

-- functions are executable by public unless revoked; every grant of execute
-- is explicit instead. this must be the role-wide default: a per-schema
-- default can only add rights, never remove the built-in public grant.
alter default privileges revoke execute on functions from public;

-- api and agg tables arrive in later phases. these defaults give each role
-- its section 4.7 rights on them without a grant per table. cp_operator gets
-- nothing in api by default, because minute-level tables must stay hidden
-- from it; this stage grants it the hourly and daily tables one by one.
-- select is included with insert and update because postgresql requires it
-- for the where clause of an update and for insert ... on conflict do update.
alter default privileges in schema agg grant select on tables to cp_operator;
alter default privileges in schema agg grant select, insert, update, delete on tables to jobs;
alter default privileges in schema api grant select, insert, update on tables to metrics_writer;
alter default privileges in schema api grant select, insert, update, delete on tables to jobs;

-- +goose Down
alter default privileges grant execute on functions to public;
drop schema agg cascade;
drop schema api cascade;
drop schema cp cascade;

-- roles are cluster-wide and may hold rights in another database of the same
-- cluster, so rolling back this database only takes away its own access
-- +goose StatementBegin
do $$
begin
  execute format(
    'revoke connect on database %I from cp_system, cp_tenant, cp_operator, metrics_writer, jobs',
    current_database());
end
$$;
-- +goose StatementEnd
