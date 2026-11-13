#!/bin/sh
# prints the go packages of the module whose default-build tests start
# containers, or the ones whose tests do not:
#
#   scripts/go-test-packages.sh containers|unit
#
# a package counts as a container package when testcontainers-go is anywhere
# in the dependency graph of its tests (directly or through a helper such as
# internal/db/dbtest or tests/fixtures/natsctr). the split is derived from the
# import graph instead of a build tag on each file, so a new container test
# lands in make test-containers without anyone remembering a tag, and make
# test stays container-free. the cost: the plain unit tests inside a
# container package run only under make test-containers.
#
# suites that drive docker through its cli (webpath, privacy, handover and
# the others) sit behind their own build tags and are in neither list.
set -eu

mode="${1:-}"
case "$mode" in
containers | unit) ;;
*)
	echo "usage: $0 containers|unit" >&2
	exit 2
	;;
esac

go="${GO:-go}"
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

pkgs="$("$go" list ./...)"
out=""
for p in $pkgs; do
	deps="$("$go" list -test -deps "$p")"
	if printf '%s\n' "$deps" | grep -qx 'github.com/testcontainers/testcontainers-go'; then
		[ "$mode" = containers ] && out="$out $p"
	else
		[ "$mode" = unit ] && out="$out $p"
	fi
done

# an empty list would make go test run the current directory instead, which
# reads as a pass; refuse it
if [ -z "$out" ]; then
	echo "no $mode packages found" >&2
	exit 1
fi
# one package per line; the list holds import paths, never globs
# shellcheck disable=SC2086
printf '%s\n' $out
