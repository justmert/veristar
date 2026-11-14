#!/bin/sh
# removes the observability local test stack: containers, volumes and the image
# tags it made. .scratch/obs (certs, secrets, rendered configs,
# binaries) is kept unless -a is given.
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
OBS="$root/.scratch/obs"
export OBS
docker compose -f "$root/tests/observability/compose.yml" down --volumes --remove-orphans
docker image rm sa-obs/collector:local sa-obs/caddy:local 2>/dev/null || true
if [ "${1:-}" = "-a" ]; then rm -rf "$OBS"; fi
