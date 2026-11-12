# the collect gateway with what it serves baked in: the immutable tracker
# releases (packages/tracker/releases) and the db-ip country lite file with
# its cc by 4.0 attribution, downloaded at image build.
#   docker build -f deploy/images/gateway.Dockerfile --build-arg DBIP_MONTH=2026-11 .
# DBIP_MONTH is required: db-ip publishes one file per month and the image
# names the one it carries, so a rebuild never picks a month silently.
# base images are pinned by tag and index digest (resolved 2026-11-12).

FROM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build

ARG VERSION=dev
ARG DBIP_MONTH

WORKDIR /src

# module download is its own layer so source edits do not refetch dependencies
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY scripts ./scripts
COPY cmd ./cmd
COPY internal ./internal
COPY db ./db

# fetch-dbip.sh opens the file with the gateway's own reader before keeping it
RUN if [ -z "$DBIP_MONTH" ]; then echo "build arg DBIP_MONTH (YYYY-MM) is required" >&2; exit 1; fi; \
    sh scripts/fetch-dbip.sh /out/geo "$DBIP_MONTH"

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=$VERSION" \
      -o /out/gateway ./cmd/gateway

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

COPY --from=build /out/gateway /gateway
COPY --from=build /out/geo/dbip-country-lite.mmdb /out/geo/dbip-country-lite.mmdb.month /out/geo/DBIP-ATTRIBUTION.txt /geo/
COPY packages/tracker/releases /releases

# paths baked into the image; the limits stay required environment values
ENV DBIP_MMDB=/geo/dbip-country-lite.mmdb \
    TRACKER_RELEASE_DIR=/releases

USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
CMD ["serve"]
