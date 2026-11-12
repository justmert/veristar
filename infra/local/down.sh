#!/bin/sh
# removes everything up.sh created: node containers and their docker
# storage volumes, the registry and its volume, both networks, the node
# image and the generated state, secrets and inventory.
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
echo "$LC_NODES" | while read -r name _ _; do
	docker rm -f "$name" >/dev/null 2>&1 || true
	docker volume rm -f "$name-docker" "$name-containerd" >/dev/null 2>&1 || true
done
docker rm -f "$LC_REGISTRY" >/dev/null 2>&1 || true
docker volume rm -f sa-lc-registry >/dev/null 2>&1 || true
docker network rm "$LC_PUBLIC_NET" "$LC_PRIVATE_NET" >/dev/null 2>&1 || true
docker image rm "$LC_NODE_IMAGE" >/dev/null 2>&1 || true
docker images --format '{{.Repository}}:{{.Tag}}' | grep "^127.0.0.1:$LC_REGISTRY_HOST_PORT/sa/" | xargs -r docker image rm >/dev/null 2>&1 || true
rm -rf "$LC_STATE" "$root/infra/secrets/local" "$root/infra/ansible/inventories/local/hosts.json"
echo "local converge removed"
