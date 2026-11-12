#!/bin/sh
# runs once, on an empty data directory, from /docker-entrypoint-initdb.d.
# creates the umami database with its own login role and the platform
# database. platform roles and schemas come from the platform migrations.
set -eu

: "${UMAMI_DB_PASSWORD:?UMAMI_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  --set umami_pw="$UMAMI_DB_PASSWORD" <<'SQL'
create role umami_app login password :'umami_pw';
create database umami owner umami_app;
revoke all on database umami from public;
grant connect on database umami to umami_app;
create database platform;
revoke all on database platform from public;
SQL
