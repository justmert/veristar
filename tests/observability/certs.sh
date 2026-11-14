#!/bin/sh
# TEST FIXTURE: a private ca and one leaf per node for the observability local
# stack (tests/observability). caddy's own internal ca issues 12 hour
# certificates, which the 14 day certificate alert would always flag, so the
# stack uses these 90 day certificates instead; drill-cert.sh swaps one node
# to a short one to fire that alert on purpose.
#   certs.sh <dir>                 ca and every node
#   certs.sh <dir> <node> <days> <host>...   reissue one node's leaf
set -eu
dir="${1:?usage: certs.sh <dir> [node days host...]}"
mkdir -p "$dir"
cd "$dir"
if [ ! -f ca.key ]; then
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout ca.key -out ca.crt \
		-days 365 -subj "/CN=sa-p17 test ca" 2>/dev/null
fi
leaf() {
	node="$1" days="$2"
	shift 2
	san=""
	for h in "$@"; do san="${san:+$san,}DNS:$h"; done
	openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout "$node.key" -out "$node.csr" \
		-subj "/CN=$1" 2>/dev/null
	printf 'subjectAltName=%s\nextendedKeyUsage=serverAuth\n' "$san" >"$node.ext"
	openssl x509 -req -in "$node.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -out "$node.crt" \
		-days "$days" -extfile "$node.ext" 2>/dev/null
	rm -f "$node.csr" "$node.ext"
	chmod 0644 "$node.key" "$node.crt"
}
if [ $# -ge 4 ]; then
	node="$2" days="$3"
	shift 3
	leaf "$node" "$days" "$@"
	exit 0
fi
for n in edge-1 edge-2 edge-3; do leaf "$n" 90 in.p17.test otlp.p17.test; done
for n in core-1 core-2; do leaf "$n" 90 app.p17.test web.p17.test; done
leaf probe-1 90 status.p17.test
