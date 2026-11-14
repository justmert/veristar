#!/bin/sh
# removes everything up.sh created: node containers and their docker
# storage volumes, the router, the registry and its volume, the networks, the node
# image and the generated state, secrets and inventory.
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
. "$root/infra/local/lib.sh"
printf '%s\n%s\n' "$LC_NODES" "$LC_RESTORE_NODE" | while read -r name _ _ _; do
	lc_node_rm "$name"
done
# every optional node, whatever LC_BACKUP, LC_PROBE and LC_TOPOLOGY were at up.sh
for name in sa-lc-backup-1 sa-lc-core-2 sa-lc-probe-1 sa-lc-node-1 sa-lc-node-2 sa-lc-node-3; do
	lc_node_rm "$name"
done
lc_s3_rm
docker rm -f "$LC_ROUTER" >/dev/null 2>&1 || true
docker rm -f "$LC_REGISTRY" >/dev/null 2>&1 || true
docker volume rm -f sa-lc-registry >/dev/null 2>&1 || true
docker network rm "$LC_PUBLIC_NET" >/dev/null 2>&1 || true
echo "$LC_LOCATIONS" | while read -r loc _; do
	docker network rm "$(lc_loc_net "$loc")" >/dev/null 2>&1 || true
done
# the network of the earlier single-network topology, in case it is still there
docker network rm sa-lc-private >/dev/null 2>&1 || true
docker image rm "$LC_NODE_IMAGE" >/dev/null 2>&1 || true
docker images --format '{{.Repository}}:{{.Tag}}' | grep "^127.0.0.1:$LC_REGISTRY_HOST_PORT/sa/" | xargs -r docker image rm >/dev/null 2>&1 || true
rm -rf "$LC_STATE" "$root/infra/secrets/local" "$root/infra/ansible/inventories/local/hosts.json"
echo "local converge removed"
