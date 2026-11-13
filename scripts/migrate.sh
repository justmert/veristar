#!/bin/sh
# data migration between two environments (the design,
# docs/handover/rebuild.md section 5). the two databases, umami and
# platform, move as postgresql custom-format dumps:
#
#   scripts/migrate.sh export <env file> <new directory>
#   scripts/migrate.sh import <env file> <directory from export>
#
# export reads from a server that nothing writes to any more: the source
# after ingestion was drained and every service stopped, or the standalone
# server restore.yml brings up from the source's pgbackrest repository in the
# new environment (the pgbackrest path, make migrate-export METHOD=pgbackrest).
# import replaces both databases of a bootstrapped target whose services are
# stopped, then the makefile target checks the schema and rebuilds the nats
# registries with jobs run reconcile. queue contents are never migrated.
#
# the env file names the server and an admin login: PLATFORM_DB_HOST,
# PLATFORM_DB_PORT, PLATFORM_DB_NAME, PLATFORM_DB_SSLMODE,
# PLATFORM_DB_ADMIN_USER, PLATFORM_DB_ADMIN_PASSWORD (a superuser),
# UMAMI_DB_NAME and, for sslmode verify-full against a converged
# environment,
# PLATFORM_DB_SSLROOTCERT: the environment's ca bundle. the postgresql client tools run from MIGRATE_PG_IMAGE (the
# pinned server image, so pg_dump is never older than the server) on
# MIGRATE_DOCKER_NETWORK (default host, so 127.0.0.1 is this machine).
set -eu

PG_IMAGE="${MIGRATE_PG_IMAGE:-postgres:18.7-trixie@sha256:9f4fd4aa918f52ae3d6f3b687f1de90d8f867cd4d520021545a9a50c120eae3b}"
NETWORK="${MIGRATE_DOCKER_NETWORK:-host}"

die() {
	echo "migrate: $*" >&2
	exit 1
}

usage() {
	echo "usage: scripts/migrate.sh export|import <env file> <directory>" >&2
	exit 2
}

[ $# -eq 3 ] || usage
cmd="$1"
env_file="$2"
dir="$3"
[ -f "$env_file" ] || die "$env_file does not exist"

# reads one key of the env file without sourcing it, so a value is never
# run as shell code
key() {
	v="$(sed -n "s/^$1=//p" "$env_file")"
	[ "$(printf '%s\n' "$v" | grep -c '')" -le 1 ] || die "$env_file sets $1 more than once"
	[ -n "$v" ] || die "$env_file does not set $1"
	printf '%s' "$v"
}

host="$(key PLATFORM_DB_HOST)"
port="$(key PLATFORM_DB_PORT)"
sslmode="$(key PLATFORM_DB_SSLMODE)"
admin="$(key PLATFORM_DB_ADMIN_USER)"
platform_db="$(key PLATFORM_DB_NAME)"
umami_db="$(key UMAMI_DB_NAME)"
PGPASSWORD="$(key PLATFORM_DB_ADMIN_PASSWORD)"
export PGPASSWORD
# the ca is optional in the file, but verify-full needs it and a ca with
# any other mode is refused, as the services refuse it (internal/config)
rootcert="$(sed -n 's/^PLATFORM_DB_SSLROOTCERT=//p' "$env_file")"
case "$sslmode" in
verify-ca | verify-full) [ -f "$rootcert" ] || die "sslmode $sslmode needs PLATFORM_DB_SSLROOTCERT naming the ca bundle" ;;
*) [ -z "$rootcert" ] || die "PLATFORM_DB_SSLROOTCERT is set, so PLATFORM_DB_SSLMODE must be verify-full" ;;
esac

conninfo() {
	printf 'host=%s port=%s user=%s sslmode=%s dbname=%s' "$host" "$port" "$admin" "$sslmode" "$1"
	[ -z "$rootcert" ] || printf ' sslrootcert=/tls/ca.crt'
}

# runs a postgresql client tool from the pinned image. the password travels
# in the environment, never on a command line. $dir is mounted at /work.
pgtool() {
	docker run --rm -i --network "$NETWORK" -e PGPASSWORD --user "$(id -u):$(id -g)" \
		${rootcert:+-v "$(cd "$(dirname "$rootcert")" && pwd)/$(basename "$rootcert"):/tls/ca.crt:ro"} \
		-v "$(cd "$dir" && pwd):/work" -w /work "$PG_IMAGE" "$@"
}

# its own variable name: sh has no local, and callers loop over $db
psqlq() {
	_psql_db="$1"
	shift
	pgtool psql -X -v ON_ERROR_STOP=1 -tA -d "$(conninfo "$_psql_db")" "$@"
}

# a dump of a database something still writes to would not match the source
# afterwards, and verify-migration would report it as lost data
require_idle() {
	busy="$(psqlq postgres -c "select coalesce(string_agg(usename || '@' || datname || ' (' || application_name || ')', ', '), '')
		from pg_stat_activity where datname in ('$platform_db', '$umami_db') and pid <> pg_backend_pid()")"
	[ -z "$busy" ] || die "connections to the databases are open: $busy. stop every service (docs/handover/rebuild.md section 5) and retry"
}

server_major() {
	psqlq postgres -c "show server_version_num" | cut -c1-2
}

# platform goose version and umami's finished prisma migrations, one line each
versions() {
	psqlq "$platform_db" -c "select coalesce(max(version_id), 0) from goose_db_version where is_applied"
	psqlq "$umami_db" -c "select coalesce(string_agg(migration_name, ',' order by migration_name), '') from _prisma_migrations where finished_at is not null and rolled_back_at is null"
}

# every role that owns an object, holds a privilege or a policy, or has a
# setting in the database. the restore recreates owners and grants by name,
# so each must exist on the target before anything is dropped there.
roles_sql="select coalesce(string_agg(distinct rolname, ',' order by rolname), '') from pg_roles where rolname !~ '^pg_' and oid in (
	select relowner from pg_class union select nspowner from pg_namespace
	union select proowner from pg_proc union select typowner from pg_type
	union select (aclexplode(relacl)).grantee from pg_class where relacl is not null
	union select (aclexplode(attacl)).grantee from pg_attribute where attacl is not null
	union select (aclexplode(nspacl)).grantee from pg_namespace where nspacl is not null
	union select (aclexplode(proacl)).grantee from pg_proc where proacl is not null
	union select (aclexplode(typacl)).grantee from pg_type where typacl is not null
	union select defaclrole from pg_default_acl
	union select (aclexplode(defaclacl)).grantee from pg_default_acl
	union select unnest(polroles) from pg_policy
	union select datdba from pg_database where datname = current_database()
	union select (aclexplode(datacl)).grantee from pg_database where datname = current_database() and datacl is not null
	union select setrole from pg_db_role_setting s join pg_database d on d.oid = s.setdatabase where d.datname = current_database())"

case "$cmd" in
export)
	[ ! -e "$dir" ] || die "$dir exists; export writes a new directory"
	umask 077
	mkdir -p "$dir"
	major="$(server_major)"
	[ "$major" = 18 ] || die "server major version is $major, this release migrates postgresql 18"
	require_idle
	set -- $(versions | tr '\n' ' ')
	goose="$1"
	prisma="${2:-}"
	[ -n "$prisma" ] || die "umami database $umami_db has no finished migrations"
	roles="$(psqlq "$platform_db" -c "$roles_sql"),$(psqlq "$umami_db" -c "$roles_sql")"
	roles="$(printf '%s' "$roles" | tr ',' '\n' | sort -u | paste -sd, -)"
	start="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	for db in "$platform_db" "$umami_db"; do
		pgtool pg_dump --format=custom --compress=zstd:3 --file="/work/$db.dump" -d "$(conninfo "$db")"
		# a dump pg_restore cannot list is not a dump
		pgtool pg_restore --list "/work/$db.dump" >/dev/null
	done
	# a write during the dumps would make the copy inconsistent
	require_idle
	[ "$(versions | tr '\n' ' ')" = "$goose $prisma " ] || die "schema changed while exporting"
	(cd "$dir" && sha256sum "$platform_db.dump" "$umami_db.dump" >SHA256SUMS)
	cat >"$dir/manifest.env" <<EOF
# written by scripts/migrate.sh export; import refuses a target that differs
MIGRATE_STARTED=$start
MIGRATE_FINISHED=$(date -u +%Y-%m-%dT%H:%M:%SZ)
MIGRATE_SOURCE=$host:$port
MIGRATE_SERVER_MAJOR=$major
MIGRATE_PLATFORM_DB=$platform_db
MIGRATE_UMAMI_DB=$umami_db
MIGRATE_PLATFORM_GOOSE_VERSION=$goose
MIGRATE_UMAMI_PRISMA_MIGRATIONS=$prisma
MIGRATE_ROLES=$roles
EOF
	echo "exported $platform_db and $umami_db from $host:$port to $dir"
	cat "$dir/SHA256SUMS"
	;;
import)
	[ -f "$dir/manifest.env" ] || die "$dir/manifest.env is missing"
	(cd "$dir" && sha256sum --quiet --check SHA256SUMS) || die "a dump in $dir does not match SHA256SUMS"
	m() { sed -n "s/^$1=//p" "$dir/manifest.env"; }
	[ "$(m MIGRATE_PLATFORM_DB)" = "$platform_db" ] || die "the dump holds database $(m MIGRATE_PLATFORM_DB), the target names $platform_db"
	[ "$(m MIGRATE_UMAMI_DB)" = "$umami_db" ] || die "the dump holds database $(m MIGRATE_UMAMI_DB), the target names $umami_db"
	major="$(server_major)"
	[ "$major" = "$(m MIGRATE_SERVER_MAJOR)" ] || die "target server major $major, source $(m MIGRATE_SERVER_MAJOR)"
	# the target was bootstrapped by the same release (site.yml), so its
	# schema versions equal the source's; a different release must be
	# upgraded on one side first, because restoring would change its schema
	set -- $(versions | tr '\n' ' ')
	[ "$1" = "$(m MIGRATE_PLATFORM_GOOSE_VERSION)" ] || die "target platform goose version $1, source $(m MIGRATE_PLATFORM_GOOSE_VERSION)"
	[ "${2:-}" = "$(m MIGRATE_UMAMI_PRISMA_MIGRATIONS)" ] || die "target umami migrations differ from the source's"
	have="$(psqlq postgres -c "select string_agg(rolname, ',') from pg_roles")"
	for r in $(m MIGRATE_ROLES | tr ',' ' '); do
		case ",$have," in
		*",$r,"*) ;;
		*) die "role $r owns or holds rights on source objects but does not exist on the target; converge the target (site.yml) first" ;;
		esac
	done
	require_idle
	for db in "$platform_db" "$umami_db"; do
		psqlq postgres -c "drop database if exists \"$db\""
		# --create restores the database itself with its owner, its
		# privileges and its settings, exactly as the source had them
		pgtool pg_restore --create --exit-on-error --no-password -d "$(conninfo postgres)" "/work/$db.dump"
		psqlq "$db" -c "analyze" >/dev/null
	done
	[ "$(versions | tr '\n' ' ')" = "$(m MIGRATE_PLATFORM_GOOSE_VERSION) $(m MIGRATE_UMAMI_PRISMA_MIGRATIONS) " ] ||
		die "restored schema versions differ from the manifest"
	echo "imported $platform_db and $umami_db into $host:$port from $dir (exported $(m MIGRATE_FINISHED) from $(m MIGRATE_SOURCE))"
	;;
*)
	usage
	;;
esac
