# postgresql under patroni with pgbackrest (the design, this stage).
#   docker build -f deploy/images/postgres-ha.Dockerfile -t stellar-analytics/postgres-ha:18.6-4.1.5 deploy/images
# the build context is unused.
#
# PG_BASE is the one place the postgresql version is chosen. it is 18.6
# because 18.7, which fixes 52 cves including CVE-2026-120154 (insert ... on
# conflict reading rows despite row-level security), had neither an image nor
# a pgdg trixie package on 2026-11-12. the design requires 18.7 before any
# non-dev environment, and the ansible core role refuses to deploy an image
# whose version is below infra/ansible group_vars postgres_min_version
# outside the local converge. switching is: new PG_BASE (tag and digest),
# new PG_VERSION, rebuild, re-resolve.
#
# patroni and pgbackrest come from the pgdg trixie repository that the
# official image already configures. versions are pinned exactly, and the
# build fails if installing them moved postgresql itself.

ARG PG_BASE=postgres:18.6-trixie@sha256:e3d24f6906d0760bf4da0dae872415391ededefad23fd471c47dc53b0ddf3c42

FROM ${PG_BASE}

ARG PG_VERSION=18.6
ARG PATRONI_VERSION=4.1.5-2.pgdg13+1
ARG PGBACKREST_VERSION=2.60.0-1.pgdg13+1

# the official image pins postgresql-18 through apt preferences, so
# apt-get install of the two tools cannot upgrade the server; the check
# below proves it
RUN set -eu; \
    before="$(dpkg-query -W -f '${Version}' "postgresql-${PG_MAJOR}")"; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      "patroni=${PATRONI_VERSION}" "pgbackrest=${PGBACKREST_VERSION}" python3-psycopg2 curl \
      # patroni's etcd3 store needs python-etcd; without it apt satisfies
      # patroni's store dependency with consul and etcd3 is unavailable
      python3-etcd; \
    apt-get clean; rm -rf /var/lib/apt/lists/*; \
    after="$(dpkg-query -W -f '${Version}' "postgresql-${PG_MAJOR}")"; \
    test "$before" = "$after" || { echo "postgresql moved from $before to $after" >&2; exit 1; }; \
    server="$(postgres --version | awk '{print $3}')"; \
    test "$server" = "$PG_VERSION" || { echo "postgres is $server, expected $PG_VERSION" >&2; exit 1; }; \
    patroni --version | grep -qx "patroni 4.1.5"; \
    python3 -c "import patroni.dcs.etcd3"; \
    pgbackrest version | grep -qx "pgBackRest 2.60.0"; \
    install -d -o postgres -g postgres -m 0700 /var/lib/postgresql/patroni /var/spool/pgbackrest /var/log/pgbackrest; \
    echo "${PG_VERSION}" > /etc/postgres-ha-version

# the version a deploy checks against the minimum, readable without a shell
LABEL org.opencontainers.image.title="stellar-analytics postgres-ha" \
      io.stellar-analytics.postgres.version="${PG_VERSION}" \
      io.stellar-analytics.patroni.version="4.1.5" \
      io.stellar-analytics.pgbackrest.version="2.60.0"

ENV PATH="/usr/lib/postgresql/${PG_MAJOR}/bin:${PATH}"

USER postgres
WORKDIR /var/lib/postgresql
# patroni runs initdb, replication and failover itself, so the image's
# docker-entrypoint.sh (which would initdb on its own) is replaced
ENTRYPOINT ["patroni"]
CMD ["/etc/patroni/patroni.yml"]
