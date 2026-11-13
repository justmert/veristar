#!/bin/sh
# rolling deploy under load: every image is retagged lc2 (same content, new
# reference, so compose recreates every service) and deploy.yml rolls it out
# node by node with draining. the dns stand-in checks port 80 every 30 s, so
# the drain wait is 45 s here instead of the 120 s that bunny.net's 30 s
# check plus the 60 s ttl need. pass: zero failed ingest requests.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/rolling-deploy"
mkdir -p "$out"
ensure_state
reg="127.0.0.1:$LC_REGISTRY_HOST_PORT/sa"
for img in gateway collector caddy console controlplane replayer metricswriter jobs platformctl oauth-fixture postgres-ha; do
	from="lc" to="lc2"
	[ "$img" = postgres-ha ] && from="18.6-lc" to="18.6-lc2"
	docker pull -q "$reg/$img:$from" >/dev/null
	docker tag "$reg/$img:$from" "$reg/$img:$to"
	docker push -q "$reg/$img:$to" >/dev/null
	docker image rm "$reg/$img:$from" "$reg/$img:$to" >/dev/null
done
run="roll$(date -u +%H%M%S)"
rm -f "$out/stop"
dc load -stop-file "$out/stop" -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration 30m -out "$out/ledger.json" >"$out/load.json" &
lp=$!
sleep 30
t0=$(date +%s)
(cd "$root/infra/ansible" && "$ANSIBLE_BIN/ansible-playbook" -i inventories/local/ playbooks/deploy.yml \
	-e sa_image_tag=lc2 -e deploy_drain_seconds=45) >"$out/deploy.log" 2>&1 || echo "deploy failed" >&2
echo "deploy_s=$(( $(date +%s) - t0 ))" | tee "$out/times.env"
grep -A10 'PLAY RECAP' "$out/deploy.log"
sleep 30
touch "$out/stop"
wait "$lp"
dc lag -until-zero 600s >"$out/lag.json" || true
dc verify -ledger "$out/ledger.json" -wait 10m >"$out/verify.json" || true
cat "$out/load.json" "$out/verify.json"
