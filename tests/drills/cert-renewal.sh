#!/bin/sh
# certificate renewal forced on every node. the local converge issues from
# caddy's local ca (sa_tls internal); acme dns-01 through cloudflare needs
# the real zone, so this proves caddy re-issues and serves a
# new certificate on every node, not that let's encrypt does.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/cert-renewal"
mkdir -p "$out"
serial() { echo | openssl s_client -connect "$1:443" -servername "$2" 2>/dev/null | openssl x509 -noout -serial -startdate 2>/dev/null | tr '\n' ' '; }
: >"$out/result.txt"
for n in sa-lc-edge-1 sa-lc-edge-2 sa-lc-edge-3 sa-lc-core-1 sa-lc-core-2; do
	case "$n" in *edge*) names="in.sa-local.test otlp.sa-local.test" p=edge ;; *) names="app.sa-local.test" p=core ;; esac
	ip="$(pub_of "$n")"
	before=""
	for h in $names; do before="$before $h=[$(serial "$ip" "$h")]"; done
	# drop the stored certificates and restart caddy: it finds none and
	# issues new ones
	nexec "$n" "$p" exec -T caddy sh -c 'rm -rf /data/caddy/certificates/*' >/dev/null
	nexec "$n" "$p" restart caddy >/dev/null 2>&1
	sleep 3
	after=""
	ok=1
	for h in $names; do
		for _ in $(seq 1 30); do s="$(serial "$ip" "$h")"; [ -n "$s" ] && break; sleep 1; done
		after="$after $h=[$s]"
		case "$before" in *"$s"*) ok=0 ;; esac
		[ -n "$s" ] || ok=0
	done
	echo "$n renewed=$ok before:$before after:$after" | tee -a "$out/result.txt"
done
