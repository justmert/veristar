#!/bin/sh
# rotate-all.yml against the local converge (make rotate-local). builds the minimal topology (3 edge, 1 core, 1 backup
# node), converges it, puts traffic and a console user through it, then:
#   1. a second age identity stands in for the new operator; the first one
#      is the original operator's, which is removed
#   2. infra/secrets/rotate-secrets.py writes new values for every generated
#      secret, encrypted to the new identity only
#   3. rotate-all.yml applies them and proves every previous credential is
#      refused and the removed identity opens neither file
#   4. the local converge end-to-end check passes on the rotated system
# then removes everything (KEEP=1 keeps it). logs and timings go to
# .scratch/rotate-local/.
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
export LC_TOPOLOGY=minimal
venv="${ANSIBLE_VENV:-$HOME/.local/share/veristar/ansible-venv}"
tools="$HOME/.local/share/veristar/tools"
out="$root/.scratch/rotate-local"
state="$root/.scratch/local-converge"
mkdir -p "$out"
: >"$out/times.txt"

step() {
	name="$1"
	shift
	t0="$(date +%s)"
	echo "== $name"
	if ! "$@" >"$out/$name.log" 2>&1; then
		echo "step $name failed; tail of $out/$name.log:" >&2
		tail -40 "$out/$name.log" >&2
		exit 1
	fi
	echo "$name $(($(date +%s) - t0))s" | tee -a "$out/times.txt"
}

cleanup() {
	[ -n "${receiver_pid:-}" ] && kill "$receiver_pid" 2>/dev/null || true
	if [ -z "${KEEP:-}" ]; then
		make -C "$root" local-converge-down >"$out/down.log" 2>&1 || true
	fi
}
trap cleanup EXIT

# alertmanager needs a receiver (docs/deploy/local-converge.md section 2)
go run "$root/tests/observability/receiver" -listen 172.31.99.1:8099 >"$out/receiver.log" 2>&1 &
receiver_pid=$!
extra='-e observability_test_webhook_base=http://172.31.99.1:8099'

# names every node container that is not running, so a node that died (or
# was removed by other work on a shared machine) is reported as such
nodes_up() {
	. "$root/infra/local/env.sh"
	printf '%s\n' "$LC_NODES" | while read -r name _ _ _; do
		[ "$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" = true ] || { echo "node $name is not running" >&2; return 1; }
	done
}

step up make -C "$root" local-converge-up
step nodes-after-up nodes_up
step images make -C "$root" local-converge-images
step nodes-after-images nodes_up
step converge make -C "$root" local-converge ANSIBLE_EXTRA="$extra"
# a console user (umami gh-1001), a site and an otlp token, so the rotation
# has user passwords to re-key
step e2e-before make -C "$root" local-converge-e2e

old_id="$state/age.key"
new_id="$state/age-new-operator.key"
"$tools/age/age-keygen" -o "$new_id" 2>/dev/null
new_recipient="$("$tools/age/age-keygen" -y "$new_id")"
step rotate-secrets env SOPS="$tools/sops-3.13.3" SOPS_AGE_KEY_FILE="$old_id" \
	"$venv/bin/python" "$root/infra/secrets/rotate-secrets.py" local --age "$new_recipient"

cd "$root/infra/ansible"
step rotate-all "$venv/bin/ansible-playbook" -i inventories/local/ playbooks/rotate-all.yml \
	-e "secrets_age_keyfile=$new_id" -e "rotate_previous_age_identity=$old_id" $extra
cd "$root"
grep -E '^ *"(ok  |FAIL)|result:' "$out/rotate-all.log" | sed 's/^ *"//; s/",\{0,1\}$//' >"$out/rotation-check.txt" || true
grep -E 'PLAY RECAP' -A8 "$out/rotate-all.log" >>"$out/rotation-check.txt" || true

# the e2e check signs in and sends traffic; it reads no secret
step e2e-after make -C "$root" local-converge-e2e
cp "$out/rotation-check.txt" "$out/rotation-check.final.txt"
echo "rotation verified; results in $out"
