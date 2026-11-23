# TEST-ONLY: the github oauth fixture (tests/fixtures/githuboauth) as an image
# for the local converge, which has no github oauth app. it is
# built only by infra/local/up.sh and never pushed for an environment.
FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY tests/fixtures/githuboauth ./tests/fixtures/githuboauth
RUN CGO_ENABLED=0 go build -trimpath -o /out/fixture ./tests/fixtures/githuboauth/cmd/githuboauth-fixture

FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
COPY --from=build /out/fixture /fixture
USER nonroot:nonroot
ENTRYPOINT ["/fixture"]
