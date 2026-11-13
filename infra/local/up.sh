#!/bin/sh
# creates the local converge topology (docs/deploy/local-converge.md): a
# registry, two networks standing in for the public and the private network,
# and four debian 13 systemd containers standing in for 3 edge and 1 core
# server, each with its own docker engine storage. then generates the
# throwaway ssh key, age identity, sops-encrypted secrets and the inventory.
# everything generated lives under .scratch/scratch/local-converge.
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
mkdir -p "$LC_STATE"
chmod 700 "$LC_STATE"

net() {
	docker network inspect "$1" >/dev/null 2>&1 || docker network create --subnet "$2" --gateway "$3" "$1" >/dev/null
}
net "$LC_PUBLIC_NET" "$LC_PUBLIC_SUBNET" "$LC_PUBLIC_GATEWAY"
net "$LC_PRIVATE_NET" "$LC_PRIVATE_SUBNET" 10.99.0.1

if ! docker inspect "$LC_REGISTRY" >/dev/null 2>&1; then
	docker run -d --name "$LC_REGISTRY" --restart unless-stopped \
		--network "$LC_PUBLIC_NET" --ip "$LC_REGISTRY_IP" \
		-p "127.0.0.1:$LC_REGISTRY_HOST_PORT:5000" \
		-v sa-lc-registry:/var/lib/registry "$LC_REGISTRY_IMAGE" >/dev/null
fi

[ -f "$LC_STATE/id_ed25519" ] || ssh-keygen -q -t ed25519 -N '' -C sa-local-converge -f "$LC_STATE/id_ed25519"

docker build -q -f "$root/infra/local/node.Dockerfile" --build-arg ADMIN_USER="$LC_ADMIN_USER" \
	-t "$LC_NODE_IMAGE" "$root/infra/local" >/dev/null

echo "$LC_NODES" | while read -r name pub priv; do
	if docker inspect "$name" >/dev/null 2>&1; then
		continue
	fi
	# the public network carries the default route, as on a cloud server;
	# without gw-priority docker may pick the private one.
	# privileged inside this unprivileged container: capabilities stay inside the
	# container's user namespace (the host clock cannot be set, for example).
	# docker and containerd storage are volumes, because overlayfs cannot
	# stack on the container's own overlay root.
	docker create --name "$name" --hostname "$name" --privileged --cgroupns=private \
		--network "name=$LC_PUBLIC_NET,ip=$pub,gw-priority=1" \
		--tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
		-v "$name-docker:/var/lib/docker" -v "$name-containerd:/var/lib/containerd" \
		-v "$LC_STATE/id_ed25519.pub:/etc/local-node/authorized_key:ro" \
		"$LC_NODE_IMAGE" >/dev/null
	docker network connect --ip "$priv" "$LC_PRIVATE_NET" "$name"
	docker start "$name" >/dev/null
done

# age identity and secrets for the local environment only
tools="$HOME/.local/share/stellar-analytics/tools"
if [ ! -f "$LC_STATE/age.key" ]; then
	"$tools/age/age-keygen" -o "$LC_STATE/age.key" 2>/dev/null
fi
recipient="$("$tools/age/age-keygen" -y "$LC_STATE/age.key")"
secrets="$root/infra/secrets/local/secrets.enc.yaml"
if [ ! -f "$secrets" ]; then
	mkdir -p "$(dirname "$secrets")"
	"${ANSIBLE_VENV:-$HOME/.local/share/stellar-analytics/ansible-venv}/bin/python" "$root/infra/secrets/new-secrets.py" local >"$LC_STATE/secrets.plain.yaml"
	"$tools/sops-3.13.3" encrypt --age "$recipient" --input-type yaml --output-type yaml \
		"$LC_STATE/secrets.plain.yaml" >"$secrets"
	rm -f "$LC_STATE/secrets.plain.yaml"
fi

# the inventory in the shape of docs/deploy/opentofu.md section 1
inv="$root/infra/ansible/inventories/local/hosts.json"
"${ANSIBLE_VENV:-$HOME/.local/share/stellar-analytics/ansible-venv}/bin/python" - "$inv" <<PY
import json, sys
nodes = """$LC_NODES""".strip().splitlines()
hosts = {"edge": {}, "core": {}, "probe": {}}
for line in nodes:
    name, pub, priv = line.split()
    tier, idx = name.split("-")[-2], int(name.split("-")[-1])
    loc = ["fsn1", "nbg1", "hel1"][idx - 1]
    hosts[tier][name] = {
        "ansible_host": pub, "ansible_user": "$LC_ADMIN_USER",
        "sa_tier": tier, "sa_index": idx, "sa_provider": "local", "sa_location": loc,
        "sa_public_ipv4": pub, "sa_public_ipv6": None, "sa_private_ip": priv,
    }
domain = "sa-local.test"
inv = {"all": {
    "vars": {
        "sa_environment": "local",
        "sa_base_domain": domain,
        "sa_hostnames": {k: f"{k}.{domain}" for k in ["in", "otlp", "app", "web", "status"]},
        "sa_private_network_cidr": "$LC_PRIVATE_SUBNET",
        # the controller reaches the nodes from the public network's gateway
        "sa_admin_cidrs": ["$LC_PUBLIC_GATEWAY/32"],
        "sa_object_storage": None,
    },
    "children": {t: {"hosts": h} for t, h in hosts.items()},
}}
with open(sys.argv[1], "w") as f:
    json.dump(inv, f, indent=2)
    f.write("\n")
PY
echo "local converge topology up; inventory at $inv"
