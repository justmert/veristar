# builds the go example into a static binary on distroless.
#   docker build -t otel-go-nethttp examples/otel-go-nethttp
# base images are pinned by tag and index digest, so a rebuild is reproducible.

FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build

WORKDIR /src
# module download is its own layer so source edits do not refetch dependencies
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY *.go ./
# cgo off gives a fully static binary, which the static distroless base needs
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w" -o /out/app .

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/app /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]
