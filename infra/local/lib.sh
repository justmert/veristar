# shared functions of the local converge scripts (docs/deploy/local-converge.md).
# sourced after env.sh by up.sh, down.sh and the drills in tests/drills.

lc_net() {
	docker network inspect "$1" >/dev/null 2>&1 || docker network create --subnet "$2" --gateway "$3" "$1" >/dev/null
}

# the docker network of one location
lc_loc_net() {
	echo "sa-lc-$1"
}

lc_loc_prefix() {
	echo "$LC_LOCATIONS" | awk -v l="$1" '$1 == l { print $2 }'
}

# creates the location networks and the router between them. the router is a
# plain container with ip forwarding and one interface per location; netem on
# each interface delays what the router sends into that location.
lc_router_up() {
	# packets between locations stay inside this machine's bridges, but each
	# one enters the bridge of the router's source location and is addressed
	# to a container on another bridge. docker drops such direct routing
	# unless the source bridge is trusted, so each location network trusts
	# the bridges of the other two (named, so they can be listed).
	echo "$LC_LOCATIONS" | while read -r loc prefix; do
		net="$(lc_loc_net "$loc")"
		others="$(echo "$LC_LOCATIONS" | awk -v l="$loc" '$1 != l { printf "%s%s", sep, "sa-lc-" $1; sep = ":" }')"
		docker network inspect "$net" >/dev/null 2>&1 || docker network create --subnet "$prefix.0/24" --gateway "$prefix.1" \
			-o "com.docker.network.bridge.name=$net" \
			-o "com.docker.network.bridge.trusted_host_interfaces=$others" "$net" >/dev/null
	done
	if ! docker inspect "$LC_ROUTER" >/dev/null 2>&1; then
		first="$(echo "$LC_LOCATIONS" | head -n1)"
		set -- $first
		docker create --name "$LC_ROUTER" --hostname "$LC_ROUTER" --privileged \
			--sysctl net.ipv4.ip_forward=1 \
			--network "name=$(lc_loc_net "$1"),ip=$2.254" \
			--entrypoint sleep "$LC_NODE_IMAGE" infinity >/dev/null
		echo "$LC_LOCATIONS" | tail -n +2 | while read -r loc prefix; do
			docker network connect --ip "$prefix.254" "$(lc_loc_net "$loc")" "$LC_ROUTER"
		done
		docker start "$LC_ROUTER" >/dev/null
	fi
	lc_netem_apply
}

# the router interface that faces a location
lc_router_if() {
	prefix="$(lc_loc_prefix "$1")"
	docker exec "$LC_ROUTER" ip -o -4 addr show | awk -v a="$prefix.254/" 'index($4, a) == 1 { print $2 }'
}

lc_netem_apply() {
	echo "$LC_LOCATIONS" | while read -r loc _; do
		dev="$(lc_router_if "$loc")"
		delay="$(eval echo "\$LC_DELAY_$loc")"
		docker exec "$LC_ROUTER" tc qdisc replace dev "$dev" root netem delay "${delay}ms" "${LC_JITTER_MS}ms"
	done
}

# lc_partition <location> cuts the location off the private network (both
# directions), lc_heal <location> restores it. the public network stays up,
# as it would when only the provider's private network between sites fails.
lc_partition() {
	docker exec "$LC_ROUTER" ip link set dev "$(lc_router_if "$1")" down
}

lc_heal() {
	docker exec "$LC_ROUTER" ip link set dev "$(lc_router_if "$1")" up
}

# lc_node_create <name> <location> <public> <private>
lc_node_create() {
	name="$1" loc="$2" pub="$3" priv="$4"
	if docker inspect "$name" >/dev/null 2>&1; then
		return 0
	fi
	prefix="$(lc_loc_prefix "$loc")"
	# the public network carries the default route, as on a cloud server;
	# without gw-priority docker may pick the private one.
	# privileged inside this unprivileged container: capabilities stay inside the
	# container's user namespace (the host clock cannot be set, for example).
	# docker and containerd storage are volumes, because overlayfs cannot
	# stack on the container's own overlay root.
	# LC_PRIVATE_ROUTE is the route a cloud private network gives each server
	# toward the other locations; node-init.sh installs it at every start.
	docker create --name "$name" --hostname "$name" --privileged --cgroupns=private \
		--network "name=$LC_PUBLIC_NET,ip=$pub,gw-priority=1" \
		--tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
		-e "LC_PRIVATE_ROUTE=$LC_PRIVATE_SUBNET via $prefix.254" \
		-v "$name-docker:/var/lib/docker" -v "$name-containerd:/var/lib/containerd" \
		-v "$LC_STATE/id_ed25519.pub:/etc/local-node/authorized_key:ro" \
		"$LC_NODE_IMAGE" >/dev/null
	docker network connect --ip "$priv" "$(lc_loc_net "$loc")" "$name"
	docker start "$name" >/dev/null
}

lc_node_rm() {
	docker rm -f "$1" >/dev/null 2>&1 || true
	docker volume rm -f "$1-docker" "$1-containerd" >/dev/null 2>&1 || true
}
