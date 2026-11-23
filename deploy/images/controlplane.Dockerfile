# builds cmd/controlplane into a static binary on distroless. it differs from
# go-service.Dockerfile in also copying docs/compliance, whose terms.md is
# embedded in the binary (the version tenants accept is the text served), and
# in carrying the immutable tracker releases, which the control plane reads
# for the snippet and integrity hash it shows (TRACKER_RELEASE_DIR).
#
# TAGS is empty for every real build. the local converge alone passes
# TAGS=oauthfixture to sign in against the test-only github fixture
# (docs/deploy/local-converge.md); such an image sends sign-in to
# OAUTH_FIXTURE_URL, so it is tagged apart and never pushed for an environment.
#   docker build -f deploy/images/controlplane.Dockerfile .
# base images are pinned by tag and index digest (resolved 2026-11-12).

FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build

ARG VERSION=dev
ARG TAGS=

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd ./cmd
COPY internal ./internal
COPY docs/compliance ./docs/compliance
# the machine api embeds its openapi document
COPY docs/reference/openapi ./docs/reference/openapi

# TAGS stays empty for every environment's image, which signs in with github
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -tags "$TAGS" \
      -ldflags "-s -w -X main.version=$VERSION" \
      -o /out/service ./cmd/controlplane

FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5

COPY --from=build /out/service /service
COPY packages/tracker/releases /releases
ENV TRACKER_RELEASE_DIR=/releases
USER nonroot:nonroot
ENTRYPOINT ["/service"]
