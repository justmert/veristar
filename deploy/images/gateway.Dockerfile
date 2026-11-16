# the collect gateway with what it serves baked in: the immutable tracker
# releases (packages/tracker/releases), the db-ip country lite file and the
# network data (db-ip asn lite, ipverse as-metadata categories, tor exit
# addresses) with their attribution, downloaded at image build.
#   docker build -f deploy/images/gateway.Dockerfile --build-arg DBIP_MONTH=2026-11 .
# DBIP_MONTH is required: db-ip publishes one file per month and the image
# names the one it carries, so a rebuild never picks a month silently. the
# network data is pinned by the same month (scripts/fetch-network.sh).
#
# a build can bring the month's files instead of downloading them: the
# directory that make geo-fetch writes (.scratch/geo), given as the build
# context named geo, replaces the geo stage below.
#   docker build ... --build-context geo=.scratch/geo .
# either way the files must name DBIP_MONTH and pass the gateway's own
# loaders (geo-checked) before the image takes them. ci uses this, because
# db-ip refuses some downloads from shared runner addresses.
# base images are pinned by tag and index digest (resolved 2026-11-12).

FROM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build

ARG VERSION=dev

WORKDIR /src

# module download is its own layer so source edits do not refetch dependencies
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY scripts ./scripts
COPY cmd ./cmd
COPY internal ./internal
COPY db ./db

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=$VERSION" \
      -o /out/gateway ./cmd/gateway

# the month's files, downloaded. fetch-dbip.sh and fetch-network.sh open
# them with the gateway's own readers before keeping them
FROM build AS geo-fetch
ARG DBIP_MONTH
RUN if [ -z "$DBIP_MONTH" ]; then echo "build arg DBIP_MONTH (YYYY-MM) is required" >&2; exit 1; fi; \
    sh scripts/fetch-dbip.sh /out/geo "$DBIP_MONTH" \
 && sh scripts/fetch-network.sh /out/geo "$DBIP_MONTH"

# the files at the root of this stage; --build-context geo=<dir> replaces it
FROM scratch AS geo
COPY --from=geo-fetch /out/geo/ /

# whichever way they came: the month they name, then the gateway's loaders
FROM build AS geo-checked
ARG DBIP_MONTH
COPY --from=geo / /out/geo/
RUN if [ -z "$DBIP_MONTH" ]; then echo "build arg DBIP_MONTH (YYYY-MM) is required" >&2; exit 1; fi; \
    for f in dbip-country-lite.mmdb.month network-data.month; do \
      m=$(cat "/out/geo/$f"); \
      if [ "$m" != "$DBIP_MONTH" ]; then echo "geo: $f names $m, the build asks for $DBIP_MONTH" >&2; exit 1; fi; \
    done; \
    go run ./scripts/dbipcheck /out/geo/dbip-country-lite.mmdb \
 && go run ./scripts/netdata check /out/geo

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

COPY --from=build /out/gateway /gateway
COPY --from=geo-checked /out/geo/dbip-country-lite.mmdb /out/geo/dbip-country-lite.mmdb.month /out/geo/DBIP-ATTRIBUTION.txt /geo/
# the network data sits beside the country file, where the gateway reads it
COPY --from=geo-checked /out/geo/dbip-asn-lite.mmdb /out/geo/asn-classes.csv /out/geo/tor-exits.txt \
     /out/geo/network-data.month /out/geo/NETWORK-ATTRIBUTION.txt /geo/
COPY packages/tracker/releases /releases

# paths baked into the image; the limits stay required environment values
ENV DBIP_MMDB=/geo/dbip-country-lite.mmdb \
    TRACKER_RELEASE_DIR=/releases

USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
CMD ["serve"]
