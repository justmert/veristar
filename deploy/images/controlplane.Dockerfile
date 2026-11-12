# builds cmd/controlplane into a static binary on distroless. it differs from
# go-service.Dockerfile only in also copying docs/compliance, whose terms.md
# is embedded in the binary (the version tenants accept is the text served).
#   docker build -f deploy/images/controlplane.Dockerfile .
# base images are pinned by tag and index digest (resolved 2026-11-12).

FROM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build

ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd ./cmd
COPY internal ./internal
COPY docs/compliance ./docs/compliance

# no oauthfixture tag: production images always sign in with github
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=$VERSION" \
      -o /out/service ./cmd/controlplane

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

COPY --from=build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
