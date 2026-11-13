# shared helpers of the this stage drills (docs/reference/drills/2026-11-13.md).
# every drill runs against the local converge (docs/deploy/local-converge.md)
# and writes its raw results under .scratch/scratch/drills/<drill>/.
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
. "$root/infra/local/lib.sh"
. "$root/tests/load/profiles.env"
INV="$root/infra/ansible/inventories/local/hosts.json"
DRILLS="$root/.scratch/scratch/drills"
STATE="$DRILLS/state.json"
GO="${GO:-go}"
ANSIBLE_BIN="${ANSIBLE_VENV:-$HOME/.local/share/veristar/ansible-venv}/bin"
mkdir -p "$DRILLS"

# the drill tool, built once per run of a script
DRILLCTL="$DRILLS/drillctl"
(cd "$root" && "$GO" build -o "$DRILLCTL" ./tests/drills/drillctl)

# the postgresql superuser password, decrypted from the local sops file for
# the drill tool's own connections; it stays in this process's environment
SA_DRILL_PG_PASSWORD="$(SOPS_AGE_KEY_FILE="$LC_STATE/age.key" \
	"$HOME/.local/share/veristar/tools/sops-3.13.3" decrypt --extract '["postgres_superuser_password"]' \
	"$root/infra/secrets/local/secrets.enc.yaml")"
export SA_DRILL_PG_PASSWORD

# the operators' client certificate and the ca bundle of the internal ca
# (infra/ansible/playbooks/internal-ca.yml): every private endpoint the
# drills read (patroni, nats monitoring through the relay, postgresql) is
# tls with client certificates since. kept 0600 under the
# drills' scratch directory for the length of the run.
SA_DRILL_TLS_DIR="$DRILLS/tls"
(
	umask 077
	mkdir -p "$SA_DRILL_TLS_DIR"
	for k in ca_bundle:ca.crt operator.cert:client.crt operator.key:client.key; do
		path="$(echo "${k%%:*}" | sed 's/\./"]["/; s/^/["/; s/$/"]/')"
		SOPS_AGE_KEY_FILE="$LC_STATE/age.key" "$HOME/.local/share/veristar/tools/sops-3.13.3" decrypt \
			--extract "$path" "$root/infra/secrets/local/tls.enc.yaml" >"$SA_DRILL_TLS_DIR/${k#*:}"
	done
)
export SA_DRILL_TLS_DIR

# curl with the operators' client certificate, for the private endpoints
tcurl() {
	curl --cacert "$SA_DRILL_TLS_DIR/ca.crt" --cert "$SA_DRILL_TLS_DIR/client.crt" --key "$SA_DRILL_TLS_DIR/client.key" "$@"
}

# etcdctl inside an edge node's etcd container, with that node's certificate
etcdctl_in() {
	node="$1"
	shift
	docker exec "$node" docker compose --project-directory /opt/veristar/edge exec -T etcd \
		etcdctl --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key "$@"
}


# drillctl's flag sets take -inv after the command name
dc() {
	cmd="$1"
	shift
	"$DRILLCTL" "$cmd" -inv "$INV" "$@"
}

ts() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }
log() { echo "[$(ts)] $*" >&2; }

# compose inside a node: nexec <node> <project> <args...>
nexec() {
	node="$1" project="$2"
	shift 2
	docker exec "$node" docker compose --project-directory "/opt/veristar/$project" "$@"
}

patroni_leader() {
	for n in sa-lc-core-1 sa-lc-core-2; do
		if tcurl -fsS -o /dev/null "https://$(priv_of "$n"):8008/primary" 2>/dev/null; then
			echo "$n"
			return 0
		fi
	done
	return 1
}

priv_of() {
	printf '%s\n%s\n' "$LC_NODES" "$LC_RESTORE_NODE" | awk -v n="$1" '$1 == n { print $4 }'
}

pub_of() {
	printf '%s\n%s\n' "$LC_NODES" "$LC_RESTORE_NODE" | awk -v n="$1" '$1 == n { print $3 }'
}

# waits until port 80 of a node answers 308 (its composite health)
wait_healthy() {
	for _ in $(seq 1 300); do
		[ "$(curl -s -o /dev/null -w '%{http_code}' -m 3 "http://$(pub_of "$1")/")" = 308 ] && return 0
		sleep 2
	done
	echo "$1 never became healthy" >&2
	return 1
}

ensure_state() {
	[ -f "$STATE" ] || dc setup -out "$STATE" >/dev/null
}
