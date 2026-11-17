# the scf-otelcol collector: the ocb manifest in
# collector/builder-config.yaml compiled with strict version checking, and
# collector/otelcol.yaml baked in. every environment-specific value stays a
# required environment variable (see the header of otelcol.yaml).
#   docker build -f deploy/images/collector.Dockerfile .
# ocb is copied out of the pinned builder image rather than run in it: that
# image ships go 1.27.3 with GOTOOLCHAIN=local, and the root module needs
# 1.27.4. base images are pinned by tag and index digest (resolved 2026-11-12).

FROM otel/opentelemetry-collector-builder:0.165.0@sha256:2c15b2e66f09577f897a0106a13a36c3040e4329396a04a8814c05513891fd7a AS ocb

FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build

COPY --from=ocb /usr/local/bin/ocb /usr/local/bin/ocb

WORKDIR /src

# module download is its own layer so source edits do not refetch dependencies
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# the custom components and the internal packages they import; the
# manifest replaces the root module with this directory. ocb runs go mod
# tidy on the generated module, which also resolves the imports of those
# packages' tests, so db (natsx tests) and tests/fixtures (component tests)
# must be present even though the binary does not contain them.
COPY collector ./collector
COPY internal ./internal
COPY db ./db
COPY tests/fixtures ./tests/fixtures

# ocb creates only the last directory of dist.output_path
RUN mkdir -p dist && ocb --config collector/builder-config.yaml --skip-strict-versioning=false && \
    ./dist/scf-otelcol/scf-otelcol components >/dev/null

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

COPY --from=build /src/dist/scf-otelcol/scf-otelcol /scf-otelcol
COPY collector/otelcol.yaml /etc/scf-otelcol/otelcol.yaml

USER nonroot:nonroot
ENTRYPOINT ["/scf-otelcol"]
CMD ["--config", "/etc/scf-otelcol/otelcol.yaml"]
