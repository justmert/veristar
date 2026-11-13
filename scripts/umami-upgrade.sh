#!/bin/sh
# make umami-upgrade VERSION=<x.y.z> (the design,
# docs/runbooks/security-patch.md). moves the umami pin to the index digest
# the registry serves for that version today, then runs every gate that
# depends on umami, in order, and stops at the first red one:
#
#   1. the dev stack recreated on the new image (umami migrates the database
#      it already holds, as production would) and umami-bootstrap
#   2. tests/umami-compat: the this stage behaviour checks, api and ui. its
#      reviewed route list names the umami version, so a new version stays red
#      until a person has reviewed the new openapi contract
#   3. the isolation gate (make isolation)
#   4. the end-to-end suites: console-e2e and webpath
#
# each step and the total are timed and written to
# .scratch/scratch/umami-upgrade/<utc time>.log. the files are edited in
# place; review the diff and commit it only when every gate passed.
#
# SKIP_E2E is not offered: the patch path is only as fast as its slowest gate,
# and that is what the runbook's time budget is measured against.
set -eu

if [ -z "${VERSION:-}" ]; then
	echo "usage: make umami-upgrade VERSION=<x.y.z>" >&2
	exit 2
fi
case "$VERSION" in
*[!0-9.]* | .* | *. | *..*)
	echo "VERSION must look like 3.5.1" >&2
	exit 2
	;;
esac

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
mkdir -p .scratch/scratch/umami-upgrade
log=".scratch/scratch/umami-upgrade/$(date -u +%Y%m%dT%H%M%SZ)-$VERSION.log"
start=$(date +%s)

say() { echo "$(date -u +%H:%M:%S) $*" | tee -a "$log"; }

step() {
	name="$1"
	shift
	s=$(date +%s)
	say "step $name: start"
	if "$@" >>"$log" 2>&1; then
		say "step $name: ok in $(($(date +%s) - s)) s"
	else
		say "step $name: FAILED after $(($(date +%s) - s)) s, total $(($(date +%s) - start)) s (log $log)"
		exit 1
	fi
}

# the index digest of the tag, from the registry, never typed by hand
token="$(curl -fsS "https://ghcr.io/token?scope=repository:umami-software/umami:pull" |
	python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')"
digest="$(curl -fsSI -H "Authorization: Bearer $token" \
	-H "Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json" \
	"https://ghcr.io/v2/umami-software/umami/manifests/$VERSION" |
	tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: //p')"
case "$digest" in
sha256:????????????????????????????????????????????????????????????????) ;;
*)
	echo "no index digest for umami $VERSION (got '$digest')" >&2
	exit 1
	;;
esac
ref="ghcr.io/umami-software/umami:$VERSION@$digest"
say "umami $VERSION resolves to $digest"

# every place the pin lives; the pattern matches any earlier version and digest
files="deploy/compose/dev.yml infra/ansible/vars/pins.yml tests/observability/compose.yml docs/reference/umami-contract.md"
for f in $files; do
	if ! grep -q 'ghcr\.io/umami-software/umami:[0-9.]*@sha256:[0-9a-f]\{64\}' "$f"; then
		echo "$f has no umami pin to move" >&2
		exit 1
	fi
	sed -i "s#ghcr\.io/umami-software/umami:[0-9.]*@sha256:[0-9a-f]\{64\}#$ref#g" "$f"
	say "pinned in $f"
done

step dev-stack make dev-up
step umami-bootstrap make umami-bootstrap
step umami-compat make umami-compat
step isolation make isolation
step console-e2e make console-e2e
step webpath make webpath

say "umami $VERSION passed every gate in $(($(date +%s) - start)) s"
