#!/bin/sh
# removes the observability local test stack: containers, volumes and the image
# tags it made. .scratch/p17 (certs, secrets, rendered configs,
# binaries) is kept unless -a is given.
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
P17="$root/.scratch/p17"
export P17
docker compose -f "$root/tests/observability/compose.yml" down --volumes --remove-orphans
docker image rm sa-p17/collector:local sa-p17/caddy:local 2>/dev/null || true
if [ "${1:-}" = "-a" ]; then rm -rf "$P17"; fi
