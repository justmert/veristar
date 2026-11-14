#!/bin/sh
# private network encryption check on the local converge
# (docs/deploy/local-converge.md): after `make local-converge`, proves that
#   1. the private network carries no readable protocol: tcpdump sidecars on
#      the three location bridges capture every packet of 10.99.0.0/16 while
#      the end-to-end suite runs and for a further window (scrapes, gossip,
#      replication, health checks), and tlsaudit classifies every flow and
#      searches every payload for the environment's secrets and protocol
#      words;
#   2. a host on the private network without a certificate is refused, and
#      the operators' certificate is served (tls-refusal.py);
#   3. the end-to-end path still works (tests/localconverge, the same run
#      as 1).
# everything it creates is named sa-lc-*; results go to
# .scratch/tls-audit/<utc time>/. the endpoints it tries exist in
# both topologies (LC_TOPOLOGY=minimal has one core node, 10.99.1.22).
set -eu
root="$(cd "$(dirname "$0")/../.." && pwd)"
. "$root/infra/local/env.sh"
. "$root/infra/local/lib.sh"
GO="${GO:-go}"
out="$root/.scratch/tls-audit/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$out"
chmod 700 "$out"
sops="$HOME/.local/share/veristar/tools/sops-3.13.3"
export SOPS_AGE_KEY_FILE="$LC_STATE/age.key"
window="${TLS_AUDIT_WINDOW:-90}"

cleanup() {
	for loc in fsn1 nbg1 hel1; do docker rm -f "sa-lc-capture-$loc" >/dev/null 2>&1 || true; done
	docker rm -f sa-lc-tlsprobe >/dev/null 2>&1 || true
	rm -rf "$out/secret"
}
trap cleanup EXIT

# markers: every string value of the secrets file (passwords, keys,
# tokens), and the words of the plain protocols that cross this network
(
	umask 077
	mkdir -p "$out/secret"
	"$sops" decrypt --output-type json "$root/infra/secrets/local/secrets.enc.yaml" | python3 -c '
import json, sys
def walk(v):
    if isinstance(v, dict):
        for x in v.values(): yield from walk(x)
    elif isinstance(v, list):
        for x in v: yield from walk(x)
    elif isinstance(v, str) and len(v) >= 8:
        yield v
for s in walk(json.load(sys.stdin)): print(s)
' >"$out/secret/markers"
	printf '%s\n' "CONNECT {" "INFO {" "HPUB " "SCRAM-SHA-256" "application_name" "HTTP/1.1" "/converge/" \
		"client_encoding" "/v3/kv/" "BEGIN CERTIFICATE" >>"$out/secret/markers"
	for k in ca_bundle:ca.crt operator.cert:client.crt operator.key:client.key; do
		path="$(echo "${k%%:*}" | sed 's/\./"]["/; s/^/["/; s/$/"]/')"
		"$sops" decrypt --extract "$path" "$root/infra/secrets/local/tls.enc.yaml" >"$out/secret/${k#*:}"
	done
)
wc -l <"$out/secret/markers" | sed 's/^/markers: /'

docker build -q -f "$root/tests/localconverge/capture.Dockerfile" -t sa-lc-capture:local "$root/tests/localconverge" >/dev/null
for loc in fsn1 nbg1 hel1; do
	docker run -d --name "sa-lc-capture-$loc" --network host --cap-add NET_ADMIN --cap-add NET_RAW \
		-v "$out:/out" sa-lc-capture:local -i "sa-lc-$loc" -U -s 0 -Z root -w "/out/$loc.pcap" net 10.99.0.0/16 >/dev/null
done
sleep 5

echo "== end-to-end path under capture"
e2e=0
(cd "$root" && SA_LC_INVENTORY="$root/infra/ansible/inventories/local/hosts.json" \
	"$GO" test -tags localconverge -count=1 -v ./tests/localconverge/) >"$out/e2e.log" 2>&1 || e2e=$?
grep -E '^(--- |ok|FAIL|PASS)' "$out/e2e.log" || true
echo "e2e exit $e2e"

echo "== $window s more of background traffic"
sleep "$window"
for loc in fsn1 nbg1 hel1; do docker stop "sa-lc-capture-$loc" >/dev/null; done
ls -l "$out"/*.pcap

echo "== capture analysis"
audit=0
(cd "$root" && "$GO" run ./tests/localconverge/tlsaudit -markers "$out/secret/markers" "$out"/fsn1.pcap "$out"/nbg1.pcap "$out"/hel1.pcap) \
	>"$out/audit.json" || audit=$?
cat "$out/audit.json"
echo "audit exit $audit"

echo "== refusals from a host on the private network without a certificate"
cat >"$out/targets.json" <<'JSON'
[
 {"name": "etcd client", "host": "10.99.1.12", "port": 2379, "kind": "http", "path": "/health", "modes": ["plain", "no cert", "operator"]},
 {"name": "etcd peer", "host": "10.99.2.13", "port": 2380, "kind": "raw", "modes": ["plain", "no cert"]},
 {"name": "nats route", "host": "10.99.1.12", "port": 6222, "kind": "raw", "modes": ["plain", "no cert"]},
 {"name": "nats client", "host": "10.99.3.14", "port": 4222, "kind": "nats", "client_cert_required": false, "modes": ["plain", "no cert"]},
 {"name": "nats monitoring (relay)", "host": "10.99.1.12", "port": 8222, "kind": "http", "path": "/varz", "modes": ["plain", "no cert", "operator"]},
 {"name": "metrics relay", "host": "10.99.2.13", "port": 9301, "kind": "http", "path": "/metrics", "modes": ["plain", "no cert", "operator"]},
 {"name": "node_exporter", "host": "10.99.1.22", "port": 9100, "kind": "http", "path": "/metrics", "modes": ["plain", "no cert", "operator"]},
 {"name": "patroni rest", "host": "10.99.1.22", "port": 8008, "kind": "http", "path": "/health", "modes": ["plain", "no cert", "operator"]},
 {"name": "postgresql", "host": "10.99.1.22", "port": 5432, "kind": "postgres", "modes": ["plain"]},
 {"name": "postgres_exporter", "host": "10.99.1.22", "port": 9187, "kind": "http", "path": "/metrics", "modes": ["plain", "no cert", "operator"]},
 {"name": "alertmanager", "host": "10.99.1.22", "port": 9093, "kind": "http", "path": "/-/healthy", "modes": ["plain", "no cert", "operator"]},
 {"name": "alertmanager gossip", "host": "10.99.1.22", "port": 9094, "kind": "raw", "modes": ["plain", "no cert"]},
 {"name": "pgbackrest (repository)", "host": "10.99.3.31", "port": 8432, "kind": "raw", "client_cert_required": false, "modes": ["plain", "no cert"]},
 {"name": "pgbackrest (database node)", "host": "10.99.1.22", "port": 8432, "kind": "raw", "client_cert_required": false, "modes": ["plain", "no cert"]}
]
JSON
refusal=0
docker run --rm --name sa-lc-tlsprobe --cap-add NET_ADMIN --network "name=sa-lc-fsn1,ip=10.99.1.250" \
	-v "$out/secret:/s:ro" -v "$out/targets.json:/targets.json:ro" -v "$root/tests/localconverge/tls-refusal.py:/check.py:ro" \
	--entrypoint sh "$LC_NODE_IMAGE" -ec \
	"ip route replace $LC_PRIVATE_SUBNET via 10.99.1.254; python3 /check.py /s/ca.crt /s/client.crt /s/client.key /targets.json" \
	>"$out/refusal.jsonl" || refusal=$?
cat "$out/refusal.jsonl"
echo "refusal exit $refusal"

# pgbackrest's tls server completes a handshake without a client
# certificate (its greeting answers server-ping) and authorizes every
# command by the certificate's common name (tls-server-auth). a valid
# certificate of the internal ca with a name not in that list, the
# operators', must be refused, and its client refuses to run without one.
echo "== pgbackrest authorization"
pgb=0
docker run --rm --name sa-lc-tlsprobe --cap-add NET_ADMIN --network "name=sa-lc-hel1,ip=10.99.3.250" \
	-v "$out/secret:/s:ro" --user 0 --entrypoint sh "127.0.0.1:$LC_REGISTRY_HOST_PORT/sa/postgres-ha:18.7-$LC_IMAGE_TAG" -c "
		ip route replace $LC_PRIVATE_SUBNET via 10.99.3.254
		mkdir /tmp/t && cp /s/ca.crt /s/client.crt /s/client.key /tmp/t/ && chown -R postgres /tmp/t && chmod 600 /tmp/t/*
		r='--stanza=sa --repo1-host=10.99.3.31 --repo1-host-type=tls --repo1-host-port=8432 --repo1-host-ca-file=/tmp/t/ca.crt --log-level-console=warn'
		su postgres -s /bin/sh -c \"pgbackrest \$r --repo1-host-cert-file=/tmp/t/client.crt --repo1-host-key-file=/tmp/t/client.key info\"
		su postgres -s /bin/sh -c \"pgbackrest \$r info\"" >"$out/pgbackrest.txt" 2>&1 || true
cat "$out/pgbackrest.txt"
grep -q 'access denied' "$out/pgbackrest.txt" && grep -q 'requires option: repo1-host-cert-file' "$out/pgbackrest.txt" || pgb=1
echo "pgbackrest exit $pgb"

[ "$e2e" = 0 ] && [ "$audit" = 0 ] && [ "$refusal" = 0 ] && [ "$pgb" = 0 ]
