#!/bin/sh
# builds every platform image from deploy/images and pushes it to the local
# converge registry (docs/deploy/local-converge.md). the nodes pull from it as
# they would from the repository host's registry.
#
#   infra/local/build-images.sh
#
# DBIP_MONTH defaults to the current utc month, which the gateway image names.
# GATEWAY_GEO_DIR, when set, is a directory written by make geo-fetch: the
# gateway build takes the month's files from it instead of downloading them
# (deploy/images/gateway.Dockerfile, the geo build context).
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
# LC_SRC builds from another checkout of the same repository, for example
# `git archive HEAD` unpacked elsewhere while the working tree is mid-change
src="${LC_SRC:-$root}"

push="127.0.0.1:${LC_REGISTRY_HOST_PORT}/sa"
tag="$LC_IMAGE_TAG"
month="${DBIP_MONTH:-$(date -u +%Y-%m)}"

build() {
	name="$1"
	shift
	echo "== $name"
	docker build -q "$@" -t "$push/$name:$tag" "$src" >/dev/null
	docker push -q "$push/$name:$tag" >/dev/null
}

cd "$src"
if [ -n "${GATEWAY_GEO_DIR:-}" ]; then
	build gateway -f deploy/images/gateway.Dockerfile --build-arg DBIP_MONTH="$month" --build-context geo="$GATEWAY_GEO_DIR"
else
	build gateway -f deploy/images/gateway.Dockerfile --build-arg DBIP_MONTH="$month"
fi
build collector -f deploy/images/collector.Dockerfile
for c in replayer metricswriter jobs platformctl prober; do
	build "$c" -f deploy/images/go-service.Dockerfile --build-arg CMD="$c"
done
# the oauthfixture tag exists only in this local image (local-converge.md)
build controlplane -f deploy/images/controlplane.Dockerfile --build-arg TAGS=oauthfixture
build caddy -f deploy/images/caddy.Dockerfile
build console -f deploy/images/console.Dockerfile --build-arg CADDY_IMAGE="$push/caddy:$tag"
build oauth-fixture -f infra/local/fixture.Dockerfile
# the version comes first in the tag, so the core role can compare it
docker build -q -f deploy/images/postgres-ha.Dockerfile -t "$push/postgres-ha:18.7-$tag" deploy/images >/dev/null
docker push -q "$push/postgres-ha:18.7-$tag" >/dev/null
echo "pushed to $push with tag $tag"
