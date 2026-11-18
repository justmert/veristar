# builds one go command from cmd/<CMD> into a static binary on distroless.
#   docker build -f deploy/images/go-service.Dockerfile --build-arg CMD=platformctl .
# base images are pinned by tag and index digest (resolved 2026-11-12).

FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build

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
# compliance texts and the machine api's openapi document
COPY docs/compliance ./docs/compliance
COPY docs/reference/openapi ./docs/reference/openapi

# cgo off gives a fully static binary, which the static distroless base needs
RUN if [ -z "$CMD" ]; then echo "build arg CMD is required, e.g. --build-arg CMD=platformctl" >&2; exit 1; fi; \
    if [ ! -d "./cmd/$CMD" ]; then echo "no such command: cmd/$CMD" >&2; exit 1; fi; \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=$VERSION" \
      -o /out/service "./cmd/$CMD"

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

COPY --from=build /out/service /service
# platformctl tenant create prints the script tag of the newest tracker
# release from the same baked copy the control plane image carries
COPY packages/tracker/releases /releases
ENV TRACKER_RELEASE_DIR=/releases
USER nonroot:nonroot
ENTRYPOINT ["/service"]
