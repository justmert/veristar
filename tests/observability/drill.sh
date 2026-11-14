#!/bin/sh
# alert fire-drills on the local TEST STACK (up.sh first). each
# drill causes one condition for real, waits until the alert reaches the
# test receiver (the stand-in for discord), undoes the cause and
# waits for the resolved message.
#   tests/observability/drill.sh <drill>...
# drills: pipeline nats-quorum nats-server etcd umami edge-node certificate
#         dlq cardinality wal
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
P17="$root/.scratch/p17"
export P17
compose() { docker compose -f "$root/tests/observability/compose.yml" "$@" >/dev/null 2>&1; }
tool() { docker run --rm --network sa-p17 --user 65534:65534 -v "$P17:/p17:ro" \
	--add-host in.p17.test:172.30.17.21 --add-host otlp.p17.test:172.30.17.21 --add-host app.p17.test:172.30.17.31 \
	debian:trixie /p17/bin/drill "$@"; }
probe() { sed -n "s/^$1=//p" "$P17/env/probe-tenant.out"; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
log() { echo "$(date -u +%H:%M:%S) $*"; }

# wait_msg <FIRING|RESOLVED> <alertname> <since> <timeout seconds> [source]
wait_msg() {
	end=$(($(date +%s) + $4))
	while [ "$(date +%s)" -lt "$end" ]; do
		if curl -s 127.0.0.1:18099/messages | python3 -c '
import json, sys
status, name, since, source = sys.argv[1:5]
for m in json.load(sys.stdin):
    if m["at"] < since or m["source"] != source:
        continue
    b = m["body"]
    title = b["embeds"][0]["title"] if b.get("embeds") else ""
    desc = b["embeds"][0].get("description", "") if b.get("embeds") else ""
    text = title + " " + desc
    if source == "alertmanager" and title.startswith(status + " " + name + " "):
        sys.exit(0)
    if source == "gatus" and name in text and (("resolved" in text.lower()) == (status == "RESOLVED")):
        sys.exit(0)
sys.exit(1)' "$1" "$2" "$3" "${5:-alertmanager}"; then
			log "received $1 $2 (${5:-alertmanager})"
			return 0
		fi
		sleep 5
	done
	log "TIMEOUT waiting for $1 $2 (${5:-alertmanager})"
	return 1
}

check() { curl -s 127.0.0.1:19300/metrics | grep "^prober_check_success{check=\"$1\"}" | awk '{print $2}'; }

drill_pipeline() {
	# stops every consumer: replayer, metrics writer and jobs. measures how
	# long the freshness checks take to fail while ingestion and the plain
	# http checks stay up, then fills both queues past the lag thresholds
	t0=$(now)
	s0=$(date +%s)
	compose stop replayer-1 replayer-2 metricswriter-1 metricswriter-2 jobs-1 jobs-2
	log "stopped replayer, metrics writer and jobs"
	web="" api=""
	while [ -z "$web" ] || [ -z "$api" ]; do
		sleep 5
		[ -z "$web" ] && [ "$(check slo_freshness-web)" = 0 ] && web=$(($(date +%s) - s0)) && log "freshness web failed after ${web}s"
		[ -z "$api" ] && [ "$(check slo_freshness-api)" = 0 ] && api=$(($(date +%s) - s0)) && log "freshness api failed after ${api}s"
		[ $(($(date +%s) - s0)) -gt 300 ] && { log "freshness did not fail within 300 s"; return 1; }
	done
	log "while stopped: ingestion=$(check slo_ingestion) console=$(check probe_console) web host=$(check probe_web-host) machine api=$(check probe_machine-api) in host gatus=$(curl -s '127.0.0.1:18080/api/v1/endpoints/hosts_in-host/statuses?pageSize=1' | python3 -c 'import json,sys; print(json.load(sys.stdin)["results"][-1]["success"])')"
	tool events -ca /p17/certs/ca.crt -site "$(probe site_id)" -origin https://probe.p17.test -n 5100 &
	tool otlp -ca /p17/certs/ca.crt -token "$(probe otlp_token)" -n 5100
	wait
	wait_msg FIRING ProbeFreshnessFailing "$t0" 300
	wait_msg FIRING ReplayerNotConsuming "$t0" 300
	wait_msg FIRING MetricsWriterNotConsuming "$t0" 300
	wait_msg FIRING ServiceDown "$t0" 300
	wait_msg FIRING QueueOldestMessageTooOld "$t0" 900
	wait_msg FIRING ReplayerLagHigh "$t0" 900
	wait_msg FIRING MetricsWriterLagHigh "$t0" 900
	wait_msg FIRING WebAggregationStale "$t0" 1500
	wait_msg FIRING UmamiPasswordRotationNotRun "$t0" 600
	wait_msg FIRING "freshness web" "$t0" 300 gatus
	t1=$(now)
	compose start replayer-1 replayer-2 metricswriter-1 metricswriter-2 jobs-1 jobs-2
	log "started them again"
	for a in ProbeFreshnessFailing ReplayerNotConsuming MetricsWriterNotConsuming ServiceDown QueueOldestMessageTooOld ReplayerLagHigh MetricsWriterLagHigh WebAggregationStale UmamiPasswordRotationNotRun; do
		wait_msg RESOLVED "$a" "$t1" 900
	done
	wait_msg RESOLVED "freshness web" "$t1" 600 gatus
}

drill_nats_quorum() {
	t0=$(now)
	compose stop nats-2 nats-3
	log "stopped nats on edge-2 and edge-3"
	wait_msg FIRING NATSQuorumLost "$t0" 300
	wait_msg FIRING ProbeIngestionFailing "$t0" 300
	wait_msg FIRING IngestWebErrorRatioHigh "$t0" 600
	wait_msg FIRING IngestOTLPErrorRatioHigh "$t0" 600
	wait_msg FIRING "ingestion" "$t0" 300 gatus
	t1=$(now)
	compose start nats-2 nats-3
	log "started them again"
	for a in NATSQuorumLost ProbeIngestionFailing IngestWebErrorRatioHigh IngestOTLPErrorRatioHigh; do
		wait_msg RESOLVED "$a" "$t1" 900
	done
	wait_msg RESOLVED "ingestion" "$t1" 600 gatus
}

drill_nats_server() {
	t0=$(now)
	compose stop nats-3
	wait_msg FIRING NATSServerDown "$t0" 300
	t1=$(now)
	compose start nats-3
	wait_msg RESOLVED NATSServerDown "$t1" 600
}

drill_etcd() {
	t0=$(now)
	compose stop etcd-2 etcd-3
	wait_msg FIRING EtcdMembersDown "$t0" 300
	wait_msg FIRING EtcdNoLeader "$t0" 300
	t1=$(now)
	compose start etcd-2 etcd-3
	wait_msg RESOLVED EtcdMembersDown "$t1" 600
	wait_msg RESOLVED EtcdNoLeader "$t1" 600
}

drill_umami() {
	t0=$(now)
	compose stop umami
	wait_msg FIRING UmamiHeartbeatFailing "$t0" 400
	wait_msg FIRING ProbeDashboardFailing "$t0" 400
	wait_msg FIRING "umami host" "$t0" 300 gatus
	t1=$(now)
	compose start umami
	wait_msg RESOLVED UmamiHeartbeatFailing "$t1" 600
	wait_msg RESOLVED ProbeDashboardFailing "$t1" 600
	wait_msg RESOLVED "umami host" "$t1" 600 gatus
}

drill_edge_node() {
	# the outside view: the node fails by address, the hostname stays up
	t0=$(now)
	compose stop gateway-2
	wait_msg FIRING EdgeNodeNotReady "$t0" 400
	wait_msg FIRING ProbeNodeCheckFailing "$t0" 400
	wait_msg FIRING "edge-2 in.p17.test" "$t0" 300 gatus
	log "while edge-2 is down: in host gatus=$(curl -s '127.0.0.1:18080/api/v1/endpoints/hosts_in-host/statuses?pageSize=1' | python3 -c 'import json,sys; print(json.load(sys.stdin)["results"][-1]["success"])') ingestion=$(check slo_ingestion)"
	t1=$(now)
	compose start gateway-2
	wait_msg RESOLVED EdgeNodeNotReady "$t1" 600
	wait_msg RESOLVED ProbeNodeCheckFailing "$t1" 600
	wait_msg RESOLVED "edge-2 in.p17.test" "$t1" 600 gatus
}

drill_certificate() {
	# edge-2 serves a certificate valid for 7 days; the dns provider would
	# still list the node, the tls check by address does not
	t0=$(now)
	cp "$P17/certs/edge-2.crt" "$P17/certs/edge-2.crt.good"
	cp "$P17/certs/edge-2.key" "$P17/certs/edge-2.key.good"
	sh "$root/tests/observability/certs.sh" "$P17/certs" edge-2 7 in.p17.test otlp.p17.test
	compose restart caddy-edge-2
	wait_msg FIRING CertificateExpiringSoon "$t0" 300
	wait_msg FIRING "edge-2 in.p17.test" "$t0" 300 gatus
	t1=$(now)
	mv "$P17/certs/edge-2.crt.good" "$P17/certs/edge-2.crt"
	mv "$P17/certs/edge-2.key.good" "$P17/certs/edge-2.key"
	compose restart caddy-edge-2
	wait_msg RESOLVED CertificateExpiringSoon "$t1" 600
	wait_msg RESOLVED "edge-2 in.p17.test" "$t1" 600 gatus
}

drill_dlq() {
	t0=$(now)
	tool dlq -nats nats://172.30.17.21:4222 -action publish
	wait_msg FIRING DLQNotEmpty "$t0" 300
	t1=$(now)
	tool dlq -nats nats://172.30.17.21:4222 -action purge
	wait_msg RESOLVED DLQNotEmpty "$t1" 600
}

drill_cardinality() {
	# the probe tenant gets 310 routes in one request; the cap is 300 per
	# tenant and day, so the rest go to the overflow route. it resolves when
	# the 15 minute window has passed.
	t0=$(now)
	tool routes -ca /p17/certs/ca.crt -token "$(probe otlp_token)" -n 310
	wait_msg FIRING CardinalityCapHit "$t0" 300
	wait_msg RESOLVED CardinalityCapHit "$t0" 1500
}

drill_wal() {
	t0=$(now)
	docker exec sa-p17-postgres-1 psql -U postgres -qc "alter system set archive_command = '/bin/false'" -c "select pg_reload_conf()" -c "select pg_switch_wal()" >/dev/null
	wait_msg FIRING WALArchivingFailing "$t0" 400
	docker exec sa-p17-postgres-1 psql -U postgres -qc "alter system set archive_command = '/bin/true'" -c "select pg_reload_conf()" >/dev/null
	wait_msg RESOLVED WALArchivingFailing "$t0" 1800
}

for d in "$@"; do
	log "== drill $d"
	"drill_$(echo "$d" | tr - _)"
done
