#!/bin/sh
# the restart drill for the start-at-boot unit of the edge and core roles
# (roles/{edge,core}/tasks/start_unit.yml, docs/reference/drills/2026-11-13.md).
# one local debian 13 systemd node from the this stage tooling (infra/local),
# converged with the base role and the edge role's unit, runs a test-only
# compose project (start-unit/compose.yml). then:
#   1. power-off: the node container is killed (no shutdown), then started;
#      every container must run again and the unit must have succeeded
#   2. the case the unit exists for: containers docker will not start by
#      itself at boot (restart policy set to "no", as docker leaves a
#      container whose start failed while the daemon came up): nats, and the
#      client, which compose starts only once the slow service is healthy,
#      100 s after boot (longer than systemd's 90 s oneshot default). after
#      a power-off the unit must start both
#   3. control: the same with the unit disabled (systemctl disable); the container must stay
#      down, which shows that step 2 measured the unit and not docker
# everything it created is removed at the end, also on failure. raw output
# goes to .scratch/scratch/drills/start-unit/.
#
# START_UNIT_TIMEOUT=90s puts systemd's default oneshot timeout back with a
# drop-in, to show what the unit's TimeoutStartSec prevents: step 2 must then
# fail, because the unit is killed before the slow service is healthy.
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
. "$root/infra/local/lib.sh"
ANSIBLE_BIN="${ANSIBLE_VENV:-$HOME/.local/share/stellar-analytics/ansible-venv}/bin"
OUT="$root/.scratch/scratch/drills/start-unit"
NODE=sa-lc-edge-1 LOC=fsn1 PUB=172.31.99.12 PRIV=10.99.1.12
PROJECT=/opt/stellar-analytics/edge
mkdir -p "$OUT"

ts() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }
log() { echo "[$(ts)] $*" | tee -a "$OUT/drill.log" >&2; }

created_state=false
[ -d "$LC_STATE" ] || created_state=true
cleanup() {
	lc_node_rm "$NODE"
	docker network rm "$(lc_loc_net "$LOC")" "$LC_PUBLIC_NET" >/dev/null 2>&1 || true
	docker image rm "$LC_NODE_IMAGE" >/dev/null 2>&1 || true
	if $created_state; then rm -rf "$LC_STATE"; fi
	log "removed the node, its volumes, the networks and the node image"
}
trap cleanup EXIT

mkdir -p "$LC_STATE"
chmod 700 "$LC_STATE"
[ -f "$LC_STATE/id_ed25519" ] || ssh-keygen -q -t ed25519 -N '' -C sa-local-converge -f "$LC_STATE/id_ed25519"
lc_net "$LC_PUBLIC_NET" "$LC_PUBLIC_SUBNET" "$LC_PUBLIC_GATEWAY"
prefix="$(lc_loc_prefix "$LOC")"
lc_net "$(lc_loc_net "$LOC")" "$prefix.0/24" "$prefix.1"
docker build -q -f "$root/infra/local/node.Dockerfile" --build-arg ADMIN_USER="$LC_ADMIN_USER" \
	-t "$LC_NODE_IMAGE" "$root/infra/local" >/dev/null
lc_node_create "$NODE" "$LOC" "$PUB" "$PRIV"
rm -f "$LC_STATE/start-unit-known_hosts"

inv="$LC_STATE/start-unit-inventory.yml"
cat >"$inv" <<YML
all:
  vars:
    sa_environment: local
    sa_private_network_cidr: $LC_PRIVATE_SUBNET
    sa_admin_cidrs: ["$LC_PUBLIC_GATEWAY/32"]
    base_chrony_control_clock: false
    ansible_ssh_private_key_file: $LC_STATE/id_ed25519
    ansible_ssh_common_args: "-o UserKnownHostsFile=$LC_STATE/start-unit-known_hosts"
  children:
    edge:
      hosts:
        $NODE: {ansible_host: $PUB, ansible_user: $LC_ADMIN_USER, sa_tier: edge, sa_private_ip: $PRIV, sa_public_ipv4: $PUB}
YML

converge() {
	ANSIBLE_ROLES_PATH="$root/infra/ansible/roles" ANSIBLE_CONFIG="$root/infra/ansible/ansible.cfg" \
		"$ANSIBLE_BIN/ansible-playbook" -i "$inv" "$root/tests/drills/start-unit.yml" "$@" >>"$OUT/converge.log" 2>&1 \
		|| { tail -40 "$OUT/converge.log" >&2; exit 1; }
}

# waits for ssh, then for systemd to finish booting, as after a power-on
wait_boot() {
	i=0
	until docker exec "$NODE" systemctl is-system-running --wait >/dev/null 2>&1 \
		|| [ "$(docker exec "$NODE" systemctl is-system-running 2>/dev/null)" = degraded ]; do
		i=$((i + 1))
		[ "$i" -lt 120 ] || { log "node did not finish booting"; exit 1; }
		sleep 1
	done
}

states() {
	docker exec "$NODE" docker compose --project-directory "$PROJECT" ps -a --format '{{.Service}}={{.State}}' | sort | tr '\n' ' '
}

power_cycle() {
	log "power-off: kill $NODE"
	docker kill "$NODE" >/dev/null
	sleep 2
	docker start "$NODE" >/dev/null
	log "power-on: started $NODE"
	wait_boot
	log "interfaces: $(docker exec "$NODE" ip -o -4 addr show | awk '$2 != "lo" { printf "%s=%s ", $2, $4 }')"
	# the unit runs once after docker; give docker's own restarts and health
	# checks time to settle before reading the result
	sleep 20
}

# waits until the unit's run of this boot has ended
wait_unit() {
	i=0
	while [ "$(docker exec "$NODE" systemctl show sa-edge-start.service -p ActiveState --value)" = activating ]; do
		i=$((i + 1))
		[ "$i" -lt 900 ] || { log "unit still running after 15 minutes"; exit 1; }
		sleep 1
	done
}

unit_report() {
	docker exec "$NODE" systemctl show sa-edge-start.service -p UnitFileState -p Result -p ExecMainStatus \
		-p ExecMainStartTimestamp | tr '\n' ' '
	echo
	docker exec "$NODE" systemctl show docker.service -p ActiveEnterTimestamp
}

log "converge: base role and the edge start unit on $NODE"
converge
if [ -n "${START_UNIT_TIMEOUT:-}" ]; then
	log "drop-in: TimeoutStartSec=$START_UNIT_TIMEOUT"
	docker exec "$NODE" sh -c "mkdir -p /etc/systemd/system/sa-edge-start.service.d && printf '[Service]\nTimeoutStartSec=%s\n' '$START_UNIT_TIMEOUT' >/etc/systemd/system/sa-edge-start.service.d/timeout.conf && systemctl daemon-reload"
fi
log "before: $(states)"
log "interfaces before: $(docker exec "$NODE" ip -o -4 addr show | awk '$2 != "lo" { printf "%s=%s ", $2, $4 }')"

log "step 1: power-off and power-on"
power_cycle
wait_unit
s1="$(states)"
log "after step 1: $s1"
unit_report | tee -a "$OUT/drill.log"
docker exec "$NODE" journalctl -b -u sa-edge-start.service --no-pager >"$OUT/step1-journal.txt" 2>&1 || true

log "step 2: nats and client with restart policy no, then power-off"
docker exec "$NODE" sh -c "docker update --restart=no \$(docker compose --project-directory $PROJECT ps -q nats client)" >/dev/null
power_cycle
wait_unit
s2="$(states)"
log "after step 2: $s2"
unit_report | tee -a "$OUT/drill.log"
docker exec "$NODE" journalctl -b -u sa-edge-start.service --no-pager >"$OUT/step2-journal.txt" 2>&1 || true

log "step 3 (control): unit disabled, nats with restart policy no, power-off"
# disabled over docker exec rather than a converge: after a power-on the
# node's interfaces can come back in another order (see iface below), and
# the base role's firewall, which names the interfaces, then drops ssh
docker exec "$NODE" systemctl disable sa-edge-start.service 2>>"$OUT/drill.log"
docker exec "$NODE" sh -c "docker update --restart=no \$(docker compose --project-directory $PROJECT ps -q nats)" >/dev/null
power_cycle
s3="$(states)"
log "after step 3: $s3"
unit_report | tee -a "$OUT/drill.log"

fail=0
case "$s1" in *exited*|*created*|*dead*) log "FAIL step 1: not every container runs"; fail=1 ;; esac
case "$s2" in *client=running*nats=running*) ;; *) log "FAIL step 2: the unit did not start nats and client"; fail=1 ;; esac
case "$s3" in *nats=exited*) ;; *) log "FAIL step 3: nats came back without the unit, so step 2 proves nothing"; fail=1 ;; esac
[ "$fail" = 0 ] && log "PASS: every container came back after a power-off; the unit started the one docker left down"
exit "$fail"
