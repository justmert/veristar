# builds one go command from cmd/<CMD> into a static binary on distroless.
#   docker build -f deploy/images/go-service.Dockerfile --build-arg CMD=platformctl .
# base images are pinned by tag and index digest (resolved 2026-11-12).

FROM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build

ARG CMD
ARG VERSION=dev

WORKDIR /src

# module download is its own layer so source edits do not refetch dependencies
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd ./cmd
COPY internal ./internal
# platformctl embeds the migrations (package db)
COPY db ./db
# platformctl and jobs link control-plane packages that embed the terms and
# compliance texts and the machine api's openapi document (this stage)
COPY docs/compliance ./docs/compliance
COPY docs/reference/openapi ./docs/reference/openapi

# cgo off gives a fully static binary, which the static distroless base needs
RUN if [ -z "$CMD" ]; then echo "build arg CMD is required, e.g. --build-arg CMD=platformctl" >&2; exit 1; fi; \
    if [ ! -d "./cmd/$CMD" ]; then echo "no such command: cmd/$CMD" >&2; exit 1; fi; \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=$VERSION" \
      -o /out/service "./cmd/$CMD"

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

COPY --from=build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
