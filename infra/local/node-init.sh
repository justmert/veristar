#!/bin/sh
# does what cloud-init does on a fresh server before handing over to systemd:
# host keys, the admin's authorized key (mounted at /etc/local-node/authorized_key
# by up.sh), the hostname and the private route, then exec /sbin/init.
set -eu
ssh-keygen -A >/dev/null
user="$(getent group sudo | cut -d: -f4 | cut -d, -f1)"
install -m 0600 -o "$user" -g "$user" /etc/local-node/authorized_key "/home/$user/.ssh/authorized_keys"
# the route toward the other locations of the private network, which a cloud
# private network hands each server (lib.sh lc_node_create)
if [ -n "${LC_PRIVATE_ROUTE:-}" ]; then
	# shellcheck disable=SC2086 # the value is "<cidr> via <address>"
	ip route replace $LC_PRIVATE_ROUTE
fi
exec /sbin/init
