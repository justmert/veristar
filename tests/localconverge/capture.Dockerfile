# the capture sidecar of tests/localconverge/tls-audit.sh: tcpdump on the
# local converge's location bridges. same pinned base as the nodes
# (infra/local/node.Dockerfile).
FROM debian:trixie@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6
RUN apt-get update \
 && apt-get install -y --no-install-recommends tcpdump \
 && apt-get clean && rm -rf /var/lib/apt/lists/*
ENTRYPOINT ["tcpdump"]
