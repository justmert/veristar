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
	# the probe (location none) has the public network only
	route=""
	if [ "$loc" != none ]; then
		route="LC_PRIVATE_ROUTE=$LC_PRIVATE_SUBNET via $(lc_loc_prefix "$loc").254"
	fi
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
		-e "${route:-LC_PRIVATE_ROUTE=}" \
		-v "$name-docker:/var/lib/docker" -v "$name-containerd:/var/lib/containerd" \
		-v "$LC_STATE/id_ed25519.pub:/etc/local-node/authorized_key:ro" \
		"$LC_NODE_IMAGE" >/dev/null
	if [ "$loc" != none ]; then
		docker network connect --ip "$priv" "$(lc_loc_net "$loc")" "$name"
	fi
	docker start "$name" >/dev/null
}

# lc_s3_up: the object storage stand-in of LC_BACKUP=s3 (env.sh). a private
# ca signs a certificate for the endpoint name and every bucket under it
# (host-style addressing, as on hetzner); the s3 identity's keys are the
# ones up.sh puts into the sops file as object_storage_*. the container sits
# in its location's network and, like the nodes, reaches the other locations
# through the router.
lc_s3_up() {
	dir="$LC_STATE/s3"
	mkdir -p "$dir"
	if [ ! -f "$dir/ca.crt" ]; then
		(
			umask 077
			cd "$dir" || exit 1
			openssl req -x509 -newkey ed25519 -nodes -days 3650 -subj "/CN=sa-lc-s3-ca" -keyout ca.key -out ca.crt 2>/dev/null
			openssl req -newkey ed25519 -nodes -subj "/CN=$LC_S3_DOMAIN" -keyout s3.key -out s3.csr 2>/dev/null
			# pgbackrest checks the certificate against repo1-storage-host, the
			# address it connects to, so the address is in it too
			printf 'subjectAltName=DNS:%s,DNS:*.%s,IP:%s\nextendedKeyUsage=serverAuth\n' "$LC_S3_DOMAIN" "$LC_S3_DOMAIN" "$LC_S3_IP" >s3.ext
			openssl x509 -req -in s3.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 825 -extfile s3.ext -out s3.crt 2>/dev/null
			rm -f s3.csr s3.ext
			printf '%s\n%s\n' "$(openssl rand -hex 10)" "$(openssl rand -hex 20)" >keys
			set -- $(cat keys)
			printf '{"identities":[{"name":"backup","credentials":[{"accessKey":"%s","secretKey":"%s"}],"actions":["Admin","Read","Write","List","Tagging"]}]}\n' "$1" "$2" >s3.json
		)
	fi
	if ! docker inspect "$LC_S3" >/dev/null 2>&1; then
		# the stand-in's files are read inside the container as root; the
		# copies keep the state directory itself private
		docker volume create sa-lc-s3-conf >/dev/null
		docker run --rm -v sa-lc-s3-conf:/c -v "$dir:/src:ro" --entrypoint sh "$LC_S3_IMAGE" \
			-c 'cp /src/s3.crt /src/s3.key /src/s3.json /c/ && chmod 0400 /c/s3.key /c/s3.json' >/dev/null
		prefix="$(lc_loc_prefix "$LC_S3_LOCATION")"
		docker run -d --name "$LC_S3" --hostname "$LC_S3" --restart unless-stopped \
			--cap-add NET_ADMIN \
			--network "name=$(lc_loc_net "$LC_S3_LOCATION"),ip=$LC_S3_IP" \
			-v sa-lc-s3-conf:/conf:ro -v sa-lc-s3-data:/data \
			--entrypoint sh "$LC_S3_IMAGE" -ec "
				ip route replace $LC_PRIVATE_SUBNET via $prefix.254
				exec weed mini -dir=/data -master.telemetry=false -admin.ui=false \
					-s3.config=/conf/s3.json -s3.cert.file=/conf/s3.crt -s3.key.file=/conf/s3.key \
					-s3.port.https=443 -s3.domainName=$LC_S3_DOMAIN" >/dev/null
	fi
}

lc_s3_rm() {
	docker rm -f "$LC_S3" >/dev/null 2>&1 || true
	docker volume rm -f sa-lc-s3-conf sa-lc-s3-data >/dev/null 2>&1 || true
}

lc_node_rm() {
	docker rm -f "$1" >/dev/null 2>&1 || true
	docker volume rm -f "$1-docker" "$1-containerd" >/dev/null 2>&1 || true
}
