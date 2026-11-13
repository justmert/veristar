#!/bin/sh
# frees disk on a github-hosted ubuntu runner by removing toolchains the
# suites never use (android sdk, .net, haskell, codeql, swift, boost). the
# local converge and rotate suites build every platform image, which does not
# fit in the space the runner image leaves free.
#
#   scripts/ci/free-disk.sh
set -eu

df -h / | tail -1
for d in /usr/local/lib/android /usr/share/dotnet /opt/ghc /usr/local/.ghcup \
	/opt/hostedtoolcache/CodeQL /usr/share/swift /usr/local/share/boost \
	/usr/local/share/powershell /usr/local/julia* /opt/microsoft; do
	if [ -e "$d" ]; then
		sudo rm -rf "$d"
	fi
done
df -h / | tail -1
