#!/bin/sh
# operations checks on the minimal local converge (make ops-local). builds 3 edge nodes, 1 core node, the s3
# object storage stand-in (LC_BACKUP=s3) and the probe node (LC_PROBE=1),
# then proves:
#   -    site.yml's storage role creates the backup bucket with versioning
#        and a compliance-mode default retention; a second run changes
#        nothing; the database node's own s3 key cannot delete a locked
#        object version
#   -    in object storage mode the patroni leader creates the stanza before
#        archive_command is set, wal reaches the bucket (pgbackrest check,
#        pg_stat_archiver), the scheduled full backup runs and is listed
#   -    a second converge reports changed=0 on every node
#   -    with etcd stopped on all three edge nodes the leader stays primary
#        and writable (patroni failsafe mode)
#   -    edge-3 destroyed and rebuilt with empty disks rejoins etcd: three
#        started members, edge-3 recorded as existing
#   -    site.yml converges the probe node (public network only): caddy,
#        gatus and the prober run, the prober's pushes arrive
#   -    alertmanager's watchdog push reaches the probe's gatus
# then removes everything (KEEP=1 keeps it). logs and timings go to
# .scratch/ops-local/. refuses to start at a load average of 20 or
# more, because the host is shared.
set -eu

root="$(cd "$(dirname "$0")/../.." && pwd)"
export LC_TOPOLOGY=minimal LC_BACKUP=s3 LC_PROBE=1
venv="${ANSIBLE_VENV:-$HOME/.local/share/veristar/ansible-venv}"
tools="$HOME/.local/share/veristar/tools"
out="$root/.scratch/ops-local"
mkdir -p "$out"
: >"$out/times.txt"
. "$root/infra/local/env.sh"
. "$root/infra/local/lib.sh"

load="$(cut -d' ' -f1 /proc/loadavg)"
if [ "${load%%.*}" -ge 20 ]; then
	echo "load average $load is 20 or more; not starting the topology on a shared host" >&2
	exit 1
fi

# before every step: the host is shared, so wait up to ten minutes for the
# one-minute load to fall below 20, then give up (the trap tears down)
calm() {
	i=0
	while load="$(cut -d' ' -f1 /proc/loadavg)" && [ "${load%%.*}" -ge 20 ]; do
		i=$((i + 1))
		if [ "$i" -gt 60 ]; then
			echo "load average still $load after ten minutes; stopping" >&2
			return 1
		fi
		sleep 10
	done
}

step() {
	name="$1"
	shift
	calm
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
go run "$root/tests/observability/receiver" -listen 10.98.99.1:8099 >"$out/receiver.log" 2>&1 &
receiver_pid=$!
webhook='-e observability_test_webhook_base=http://10.98.99.1:8099'

# nested docker: the node containers run their own engine
on() {
	node="$1"
	shift
	docker exec "$node" "$@"
}
core() { on sa-lc-core-1 docker compose --project-directory /opt/veristar/core "$@"; }
edge() {
	node="$1"
	shift
	on "$node" docker compose --project-directory /opt/veristar/edge "$@"
}

converge() {
	(cd "$root/infra/ansible" && "$venv/bin/ansible-playbook" -i inventories/local/ playbooks/site.yml $webhook "$@")
}

# every play recap line reports changed=0
no_changes() {
	log="$1"
	grep -E '^[a-z0-9-]+ +: ok=' "$log" >"$out/recap.txt"
	[ -s "$out/recap.txt" ] || { echo "no recap in $log" >&2; return 1; }
	if grep -v ' changed=0 ' "$out/recap.txt"; then
		echo "the second converge changed something (above)" >&2
		return 1
	fi
}

bucket() {
	# shellcheck disable=SC2046 # the file holds the key and the secret, one per line
	set -- $(cat "$LC_STATE/s3/keys") "$@"
	k="$1" s="$2"
	shift 2
	curl -sS --cacert "$LC_STATE/s3/ca.crt" \
		--connect-to "$LC_S3_BUCKET.$LC_S3_DOMAIN:443:$LC_S3_IP:443" \
		--aws-sigv4 "aws:amz:$LC_S3_LOCATION:s3" -u "$k:$s" "$@"
}
bucket_url="https://$LC_S3_BUCKET.$LC_S3_DOMAIN"

check_bucket() {
	bucket "$bucket_url/?versioning" | grep -q '<Status>Enabled</Status>'
	bucket "$bucket_url/?object-lock" | tee /dev/stderr | grep -q '<Mode>COMPLIANCE</Mode><Days>35</Days>'
}

check_archiving() {
	core exec -T postgres pgbackrest --stanza=sa check
	core exec -T postgres psql -h /var/run/postgresql -U postgres -tAX -c \
		"select archived_count, failed_count, current_setting('archive_command') from pg_stat_archiver" | tee "$out/archiver.txt"
	awk -F'|' '$1 > 0 && $2 == 0 && $3 ~ /^pgbackrest / { ok = 1 } END { exit !ok }' "$out/archiver.txt"
	# the archive lives in the bucket, under the environment's path
	bucket "$bucket_url/?list-type=2&prefix=local/archive/sa/" | grep -q '<Key>local/archive/sa/18-1/'
}

check_backup() {
	on sa-lc-core-1 systemctl start sa-pgbackrest-full.service
	core exec -T postgres pgbackrest --stanza=sa --output=json info >"$out/info.json"
	python3 -c 'import json,sys; s=json.load(open(sys.argv[1]))[0]; assert s["status"]["code"]==0, s["status"]; b=[x for x in s["backup"] if x["type"]=="full"]; assert b, "no full backup"; print("full backups:", [x["label"] for x in b])' "$out/info.json"
}

# the database node's key is the bucket's only key here: a plain delete
# only adds a marker, deleting the locked version itself is refused
check_lock() {
	key="$(bucket "$bucket_url/?list-type=2&prefix=local/backup/sa/" | sed -n -E 's/.*<Key>(local\/backup\/sa\/backup.info)<\/Key>.*/\1/p' | head -n1)"
	[ -n "$key" ] || { echo "no backup.info in the bucket" >&2; return 1; }
	vid="$(bucket "$bucket_url/?versions&prefix=$key" | sed -n -E 's/.*<Version><Key>[^<]*<\/Key><VersionId>([^<]*)<.*/\1/p' | head -n1)"
	code="$(bucket -o /dev/null -w '%{http_code}' -X DELETE "$bucket_url/$key?versionId=$vid")"
	echo "delete of $key version $vid answered $code"
	[ "$code" = 403 ]
}

check_failsafe() {
	for n in sa-lc-edge-1 sa-lc-edge-2 sa-lc-edge-3; do edge "$n" stop etcd; done
	# patroni's ttl is 30 s: without failsafe the leader demotes within it
	sleep 75
	# patroni's rest api is https with client certificates
	on sa-lc-core-1 curl -fsS -o /dev/null -w 'primary %{http_code}\n' --cacert /etc/veristar/tls/root/ca.crt --cert /etc/veristar/tls/root/node.crt --key /etc/veristar/tls/root/node.key https://10.99.1.22:8008/primary
	core exec -T postgres psql -h /var/run/postgresql -U postgres -tAX -c \
		"create table if not exists failsafe_probe(t timestamptz); insert into failsafe_probe values (now()); select pg_is_in_recovery()" | tee "$out/failsafe.txt"
	grep -qx f "$out/failsafe.txt"
	for n in sa-lc-edge-1 sa-lc-edge-2 sa-lc-edge-3; do edge "$n" start etcd; done
	core exec -T postgres psql -h /var/run/postgresql -U postgres -tAX -c "drop table failsafe_probe"
}

# what someone holding the node's key can do to the repository: delete
# markers on the backup and archive info files. pgbackrest then cannot read
# the stanza, but --repo-target-time reads the repository as it was, and
# removing the markers (allowed, they are not locked) restores it
check_target_time() {
	t="$(date -u '+%Y-%m-%d %H:%M:%S+00')"
	sleep 2
	for k in local/backup/sa/backup.info local/backup/sa/backup.info.copy; do
		bucket -fsS -o /dev/null -X DELETE "$bucket_url/$k"
	done
	if core exec -T postgres pgbackrest --stanza=sa --output=json info >"$out/info-deleted.json" 2>&1 &&
		python3 -c 'import json,sys; sys.exit(json.load(open(sys.argv[1]))[0]["status"]["code"] == 0)' "$out/info-deleted.json"; then
		echo "info still reads the stanza after the delete" >&2
		return 1
	fi
	core exec -T postgres pgbackrest --stanza=sa --repo=1 "--repo-target-time=$t" --output=json info >"$out/info-target-time.json"
	python3 -c 'import json,sys; s=json.load(open(sys.argv[1]))[0]; assert s["status"]["code"]==0, s["status"]; assert [b for b in s["backup"] if b["type"]=="full"]; print("read at", sys.argv[2], ":", [b["label"] for b in s["backup"]])' "$out/info-target-time.json" "$t"
	for k in local/backup/sa/backup.info local/backup/sa/backup.info.copy; do
		vid="$(bucket "$bucket_url/?versions&prefix=$k" | python3 -c 'import re,sys; x=sys.stdin.read(); m=[v for kk,v in re.findall(r"<DeleteMarker><Key>([^<]*)</Key><VersionId>([^<]*)<", x) if kk==sys.argv[1]]; print(m[0] if m else "")' "$k")"
		[ -n "$vid" ] || { echo "no delete marker for $k" >&2; return 1; }
		bucket -fsS -o /dev/null -X DELETE "$bucket_url/$k?versionId=$vid"
	done
	core exec -T postgres pgbackrest --stanza=sa --output=json info >"$out/info-restored.json"
	python3 -c 'import json,sys; s=json.load(open(sys.argv[1]))[0]; assert s["status"]["code"]==0, s["status"]' "$out/info-restored.json"
}

rebuild_edge3() {
	# shellcheck disable=SC2046 # name location public private
	set -- $(printf '%s\n' "$LC_NODES" | grep '^sa-lc-edge-3 ')
	lc_node_rm "$1"
	lc_node_create "$1" "$2" "$3" "$4"
	ssh-keygen -R "$3" -f "$LC_STATE/known_hosts" >/dev/null 2>&1 || true
}

check_etcd() {
	edge sa-lc-edge-1 exec -T etcd etcdctl --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key --endpoints=https://127.0.0.1:2379 member list -w table | tee "$out/members.txt"
	[ "$(grep -c ', started, ' "$out/members.txt")" = 3 ] || [ "$(grep -c '| started |' "$out/members.txt")" = 3 ]
	edge sa-lc-edge-1 exec -T etcd etcdctl --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key --endpoints=https://10.99.1.12:2379,https://10.99.2.13:2379,https://10.99.3.14:2379 endpoint health
	[ "$(on sa-lc-edge-3 cat /etc/veristar/edge/etcd-initial-state)" = existing ]
}

# the probe tenant needs an operator who signed in; the end-to-end suite
# signs in as the fixture's operator 1001
probe_secrets() {
	(cd "$root" && SA_LC_INVENTORY="$root/infra/ansible/inventories/local/hosts.json" go test -tags localconverge -count=1 ./tests/localconverge/)
	on sa-lc-core-1 docker run --rm --network veristar-core_default \
		--env-file /etc/veristar/env/platformctl.env \
		-v /etc/veristar/tls/ca:/etc/sa-tls:ro \
		-e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
		-e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
		"$LC_REGISTRY_IP:5000/sa/platformctl:$LC_IMAGE_TAG" \
		probe-tenant create -operator-github-id 1001 -domain probe.sa-local.test -origin https://probe.sa-local.test >"$LC_STATE/probe-tenant.out"
	f="$root/infra/secrets/local/secrets.enc.yaml"
	for pair in site_id:probe_site_id otlp_token:probe_otlp_token machine_api_key:probe_machine_api_key; do
		v="$(sed -n "s/^${pair%%:*}=//p" "$LC_STATE/probe-tenant.out")"
		SOPS_AGE_KEY_FILE="$LC_STATE/age.key" "$tools/sops-3.13.3" set "$f" "[\"${pair#*:}\"]" "\"$v\""
	done
	# a new jobs leader runs the reconcile that provisions the probe's site
	core restart jobs
}

# the local caddies issue from their own local cas; the probe trusts all of
# them, and prometheus and alertmanager trust the probe's
ca_bundle() {
	: >"$LC_STATE/probe-ca.crt"
	for n in sa-lc-edge-1 sa-lc-edge-2 sa-lc-edge-3; do
		on "$n" docker compose --project-directory /opt/veristar/edge exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >>"$LC_STATE/probe-ca.crt"
	done
	core exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >>"$LC_STATE/probe-ca.crt"
	if docker exec sa-lc-probe-1 test -f /opt/veristar/observability/compose.yml; then
		on sa-lc-probe-1 docker compose --project-directory /opt/veristar/observability exec -T caddy cat /data/caddy/pki/authorities/local/root.crt >>"$LC_STATE/probe-ca.crt"
	fi
	grep -c 'BEGIN CERTIFICATE' "$LC_STATE/probe-ca.crt"
}

probe_vars() {
	cat >"$out/probe-vars.json" <<EOF
{"observability_probe_tls": "internal",
 "observability_probe_ca_file": "$LC_STATE/probe-ca.crt",
 "observability_probe_extra_hosts": {"in.sa-local.test": "10.98.99.12", "otlp.sa-local.test": "10.98.99.12",
   "app.sa-local.test": "10.98.99.22", "status.sa-local.test": "10.98.99.51"}}
EOF
}

gatus_status() {
	on sa-lc-probe-1 docker compose --project-directory /opt/veristar/observability exec -T caddy \
		wget -q -O - http://gatus:8080/api/v1/endpoints/statuses >"$out/gatus.json"
	python3 - "$out/gatus.json" <<'PY'
import json, sys
st = {e["key"]: e for e in json.load(open(sys.argv[1]))}
def last(k):
    r = st.get(k, {}).get("results") or []
    return r[-1]["success"] if r else None
need = ["monitoring_watchdog", "slo_ingestion", "slo_dashboard", "hosts_console"]
for k in need:
    print(k, last(k))
bad = [k for k in need if last(k) is not True]
sys.exit(1 if bad else 0)
PY
}

wait_gatus() {
	i=0
	until gatus_status; do
		i=$((i + 1))
		[ "$i" -lt 40 ] || return 1
		sleep 15
	done
}

step up make -C "$root" local-converge-up
step images make -C "$root" local-converge-images
step converge converge --limit 'all:!probe'
step bucket check_bucket
step archiving check_archiving
step backup check_backup
step lock check_lock
step target-time check_target_time
step converge-again converge --limit 'all:!probe'
step no-changes no_changes "$out/converge-again.log"
step failsafe check_failsafe
step rebuild-edge3 rebuild_edge3
step converge-rebuilt converge --limit 'all:!probe'
step etcd check_etcd
step archiving-after-rebuild check_archiving
step probe-secrets probe_secrets
step probe-vars probe_vars
step ca-bundle ca_bundle
step converge-probe converge -e "@$out/probe-vars.json"
step ca-bundle-probe ca_bundle
step converge-probe-ca converge -e "@$out/probe-vars.json"
step gatus wait_gatus
step converge-probe-again converge -e "@$out/probe-vars.json"
step no-changes-probe no_changes "$out/converge-probe-again.log"
echo "ops-local passed"
