# builds the actix example with the pinned rust toolchain onto distroless cc.
#   docker build -t otel-rust-actix examples/otel-rust-actix
# base images pinned by tag and index digest.

FROM rust:1.99.1-slim-trixie@sha256:f6c4a35d21dbb3c89d7a8d056c193e94fc09037f2b58dd858b09c39a47dd02d7 AS build

WORKDIR /src
COPY rust-toolchain.toml Cargo.toml Cargo.lock ./
COPY src ./src
# --locked builds exactly the lockfile versions
RUN cargo build --release --locked && cp target/release/otel-rust-actix /app

# cc carries glibc, libgcc and ca certificates, which rustls verifies against
FROM gcr.io/distroless/cc-debian13:nonroot@sha256:cc66d222f0a7ec996b78a99fe76a969bc80b539a5016ad866c14d1949bc6d0ab
COPY --from=build /app /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]
