#!/bin/sh
# replaces the runner image's docker engine with the version production
# runs (docker_ce_version in infra/ansible/vars/pins.yml, built for ubuntu
# 24.04 instead of debian 13). the container suites test how docker
# forwards published ports and keeps client addresses, which changed between
# engine releases: ubuntu-24.04 runners ship 28.0.4, production pins 29.x.
#
#   scripts/ci/install-docker.sh
#
# github-hosted ubuntu 24.04 runners only (passwordless sudo, apt).
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
pins="$root/infra/ansible/vars/pins.yml"
# 5:29.9.3-1~debian.13~trixie -> 29.9.3, the upstream release
upstream="$(sed -n 's/^docker_ce_version: "[0-9]*:\([0-9.]*\)-.*"$/\1/p' "$pins")"
fpr="$(sed -n 's/^docker_apt_key_fingerprint: \([0-9A-F]*\)$/\1/p' "$pins")"
[ -n "$upstream" ] && [ -n "$fpr" ] || { echo "docker pins not found in $pins" >&2; exit 1; }
# shellcheck source=/dev/null
. /etc/os-release
[ "$ID" = ubuntu ] || { echo "ubuntu runner expected, got $ID" >&2; exit 1; }
version="5:${upstream}-1~ubuntu.${VERSION_ID}~${VERSION_CODENAME}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
curl -fsSL --retry 5 -o "$work/docker.asc" https://download.docker.com/linux/ubuntu/gpg
# the key must be the one production trusts
gpg --show-keys --with-colons "$work/docker.asc" | grep -q "^fpr:::::::::$fpr:$" || {
	echo "docker apt key is not $fpr" >&2
	exit 1
}
sudo install -D -m 0644 "$work/docker.asc" /etc/apt/keyrings/docker-pinned.asc
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker-pinned.asc] https://download.docker.com/linux/ubuntu $VERSION_CODENAME stable" |
	sudo tee /etc/apt/sources.list.d/docker-pinned.list >/dev/null
sudo apt-get update -qq -o Dir::Etc::sourcelist=sources.list.d/docker-pinned.list \
	-o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --allow-downgrades \
	"docker-ce=$version" "docker-ce-cli=$version"
sudo systemctl restart docker
got="$(docker version --format '{{.Server.Version}}')"
[ "$got" = "$upstream" ] || { echo "docker engine is $got, want $upstream" >&2; exit 1; }
echo "docker engine $got"
