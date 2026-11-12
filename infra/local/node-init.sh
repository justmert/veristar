#!/bin/sh
# does what cloud-init does on a fresh server before handing over to systemd:
# host keys, the admin's authorized key (mounted at /etc/local-node/authorized_key
# by up.sh) and the hostname, then exec /sbin/init.
set -eu
ssh-keygen -A >/dev/null
user="$(getent group sudo | cut -d: -f4 | cut -d, -f1)"
install -m 0600 -o "$user" -g "$user" /etc/local-node/authorized_key "/home/$user/.ssh/authorized_keys"
exec /sbin/init
