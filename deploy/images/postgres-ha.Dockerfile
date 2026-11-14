# postgresql under patroni with pgbackrest for the core nodes.
#   docker build -f deploy/images/postgres-ha.Dockerfile -t veristar/postgres-ha:18.7-4.1.5 deploy/images
# the build context is unused.
#
# PG_BASE is the one place the postgresql version is chosen. it is 18.7
# (postgresql-18 18.7-1.pgdg13+2), which fixes 52 cves including
# CVE-2026-120154 (insert ... on conflict reading rows despite row-level
# security). switching is: new PG_BASE (tag and index
# digest), new PG_VERSION, postgres_version in infra/ansible/vars/pins.yml,
# rebuild.
#
# patroni and pgbackrest come from the pgdg trixie repository that the
# official image already configures. versions are pinned exactly, and the
# build fails if installing them moved postgresql itself.

ARG PG_BASE=postgres:18.7-trixie@sha256:9f4fd4aa918f52ae3d6f3b687f1de90d8f867cd4d520021545a9a50c120eae3b

FROM ${PG_BASE}

ARG PG_VERSION=18.7
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
    # zlib1g from the debian security update: the base image carries a build
    # with a fixable high finding (trivy 2026-11-13). only-upgrade cannot pull
    # in postgresql, and the version check below would catch it anyway
    apt-get install -y --no-install-recommends --only-upgrade zlib1g; \
    apt-get clean; rm -rf /var/lib/apt/lists/*; \
    # gosu serves only the official docker-entrypoint.sh, which this image
    # replaces with patroni; it is built with go 1.24.6 and carries a fixable
    # critical and ten high stdlib findings, so it is removed, not kept unused
    rm -f /usr/local/bin/gosu; \
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
LABEL org.opencontainers.image.title="veristar postgres-ha" \
      io.veristar.postgres.version="${PG_VERSION}" \
      io.veristar.patroni.version="4.1.5" \
      io.veristar.pgbackrest.version="2.60.0"

ENV PATH="/usr/lib/postgresql/${PG_MAJOR}/bin:${PATH}"

USER postgres
WORKDIR /var/lib/postgresql
# patroni runs initdb, replication and failover itself, so the image's
# docker-entrypoint.sh (which would initdb on its own) is replaced
ENTRYPOINT ["patroni"]
CMD ["/etc/patroni/patroni.yml"]
