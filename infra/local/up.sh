#!/bin/sh
# creates the local converge topology (docs/deploy/local-converge.md): a
# registry, a public network, one private network per location joined by a
# router that adds the inter-location latency, and six debian 13 systemd
# containers standing in for 3 edge, 2 core and 1 backup server, each with
# its own docker engine storage. then generates the
# throwaway ssh key, age identity, sops-encrypted secrets and the inventory.
# everything generated lives under .scratch/local-converge.
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
. "$root/infra/local/lib.sh"
mkdir -p "$LC_STATE"
chmod 700 "$LC_STATE"

lc_net "$LC_PUBLIC_NET" "$LC_PUBLIC_SUBNET" "$LC_PUBLIC_GATEWAY"

if ! docker inspect "$LC_REGISTRY" >/dev/null 2>&1; then
	docker run -d --name "$LC_REGISTRY" --restart unless-stopped \
		--network "$LC_PUBLIC_NET" --ip "$LC_REGISTRY_IP" \
		-p "127.0.0.1:$LC_REGISTRY_HOST_PORT:5000" \
		-v sa-lc-registry:/var/lib/registry "$LC_REGISTRY_IMAGE" >/dev/null
fi

[ -f "$LC_STATE/id_ed25519" ] || ssh-keygen -q -t ed25519 -N '' -C sa-local-converge -f "$LC_STATE/id_ed25519"

docker build -q -f "$root/infra/local/node.Dockerfile" --build-arg ADMIN_USER="$LC_ADMIN_USER" \
	-t "$LC_NODE_IMAGE" "$root/infra/local" >/dev/null

lc_router_up
if [ "$LC_BACKUP" = s3 ]; then
	lc_s3_up
fi
echo "$LC_NODES" | while read -r name loc pub priv; do
	lc_node_create "$name" "$loc" "$pub" "$priv"
done

# age identity and secrets for the local environment only
tools="$HOME/.local/share/veristar/tools"
if [ ! -f "$LC_STATE/age.key" ]; then
	"$tools/age/age-keygen" -o "$LC_STATE/age.key" 2>/dev/null
fi
recipient="$("$tools/age/age-keygen" -y "$LC_STATE/age.key")"
secrets="$root/infra/secrets/local/secrets.enc.yaml"
if [ ! -f "$secrets" ]; then
	mkdir -p "$(dirname "$secrets")"
	# in s3 mode the stand-in's keys go into the sops file
	if [ "$LC_BACKUP" = s3 ]; then
		SA_OBJECT_STORAGE_ACCESS_KEY="$(sed -n 1p "$LC_STATE/s3/keys")"
		SA_OBJECT_STORAGE_SECRET_KEY="$(sed -n 2p "$LC_STATE/s3/keys")"
		export SA_OBJECT_STORAGE_ACCESS_KEY SA_OBJECT_STORAGE_SECRET_KEY
	fi
	"${ANSIBLE_VENV:-$HOME/.local/share/veristar/ansible-venv}/bin/python" "$root/infra/secrets/new-secrets.py" local >"$LC_STATE/secrets.plain.yaml"
	"$tools/sops-3.13.3" encrypt --age "$recipient" --input-type yaml --output-type yaml \
		"$LC_STATE/secrets.plain.yaml" >"$secrets"
	rm -f "$LC_STATE/secrets.plain.yaml"
fi

# the inventory in the shape of docs/deploy/opentofu.md section 1
inv="$root/infra/ansible/inventories/local/hosts.json"
"${ANSIBLE_VENV:-$HOME/.local/share/veristar/ansible-venv}/bin/python" - "$inv" <<PY
import json, sys
nodes = """$LC_NODES
$LC_RESTORE_NODE""".strip().splitlines()
# backup holds the pgbackrest repository; restore is the fresh node of the
# point-in-time restore drill, which exists only while that drill runs
hosts = {"edge": {}, "core": {}, "backup": {}, "restore": {}, "probe": {}}
# a converged node (LC_TOPOLOGY=node) is in both the edge and the core group,
# as opentofu's inventory lists it (infra/tofu/modules/inventory)
groups_of = {"node": ["edge", "core"]}
for line in nodes:
    name, loc, pub, priv = line.split()
    tier, idx = name.split("-")[-2], int(name.split("-")[-1])
    # the probe has no location network and no private address, like a
    # probe host outside the platform's network
    for group in groups_of.get(tier, [tier]):
      hosts[group][name] = {
        "ansible_host": pub, "ansible_user": "$LC_ADMIN_USER",
        "sa_tier": tier, "sa_index": idx, "sa_provider": "local",
        "sa_location": None if loc == "none" else loc,
        "sa_public_ipv4": pub, "sa_public_ipv6": None,
        "sa_private_ip": None if priv == "none" else priv,
    }
domain = "sa-local.test"
s3 = "$LC_BACKUP" == "s3"
state = "$LC_STATE"
inv = {"all": {
    "vars": {
        "sa_environment": "local",
        "sa_base_domain": domain,
        "sa_hostnames": {k: f"{k}.{domain}" for k in ["in", "otlp", "app", "status"]},
        "sa_private_network_cidr": "$LC_PRIVATE_SUBNET",
        # the controller reaches the nodes from the public network's gateway
        "sa_admin_cidrs": ["$LC_PUBLIC_GATEWAY/32"],
        # s3 mode: the stand-in (https endpoint, region, bucket,
        # host-style addressing); storage_host and
        # ca_file are the two local-only keys (roles/backup, roles/storage)
        "sa_object_storage": {
            "endpoint": "https://$LC_S3_DOMAIN", "region": "$LC_S3_LOCATION",
            "backup_bucket": "$LC_S3_BUCKET", "storage_host": "$LC_S3_IP",
            "ca_file": state + "/s3/ca.crt",
        } if s3 else None,
        "backup_repo_mode": "s3" if s3 else "posix-host",
    },
    "children": {t: {"hosts": h} for t, h in hosts.items()},
}}
with open(sys.argv[1], "w") as f:
    json.dump(inv, f, indent=2)
    f.write("\n")
PY
echo "local converge topology up; inventory at $inv"
