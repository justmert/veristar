# TEST-ONLY: the github oauth fixture (tests/fixtures/githuboauth) as an image
# for the local converge, which has no github oauth app an operator input. it is
# built only by infra/local/up.sh and never pushed for an environment.
FROM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY tests/fixtures/githuboauth ./tests/fixtures/githuboauth
RUN CGO_ENABLED=0 go build -trimpath -o /out/fixture ./tests/fixtures/githuboauth/cmd/githuboauth-fixture

FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /out/fixture /fixture
USER nonroot:nonroot
ENTRYPOINT ["/fixture"]
