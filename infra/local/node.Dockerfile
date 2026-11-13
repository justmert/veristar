# a local stand-in for one cloud server (docs/deploy/local-converge.md): plain
# debian 13 with systemd as pid 1 and openssh, which is what the hetzner
# debian-13 image gives after cloud-init. everything the roles configure
# (docker engine, firewall, chrony, ssh hardening, unattended upgrades) is
# deliberately absent, so a converge installs it from nothing.
#   docker build -f infra/local/node.Dockerfile -t veristar/local-node:trixie infra/local
# base image pinned by tag and index digest (resolved 2026-11-12).

FROM debian:trixie@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6

ENV container=docker DEBIAN_FRONTEND=noninteractive

# python3 is what ansible needs on a target; sudo and openssh match cloud-init
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv dbus openssh-server sudo python3 ca-certificates iproute2 \
 && apt-get clean && rm -rf /var/lib/apt/lists/* \
 # units that cannot work in a container would only add failed states
 && systemctl mask systemd-firstboot.service systemd-udevd.service systemd-modules-load.service getty@tty1.service console-getty.service sys-kernel-config.mount sys-kernel-debug.mount sys-kernel-tracing.mount \
 && rm -f /etc/ssh/ssh_host_*

ARG ADMIN_USER
RUN test -n "$ADMIN_USER" \
 && useradd --create-home --shell /bin/bash --groups sudo "$ADMIN_USER" \
 && echo "$ADMIN_USER ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/90-cloud-init-users \
 && chmod 0440 /etc/sudoers.d/90-cloud-init-users \
 && install -d -m 0700 -o "$ADMIN_USER" -g "$ADMIN_USER" "/home/$ADMIN_USER/.ssh"

COPY node-init.sh /usr/local/sbin/node-init.sh

STOPSIGNAL SIGRTMIN+3
ENTRYPOINT ["/usr/local/sbin/node-init.sh"]
