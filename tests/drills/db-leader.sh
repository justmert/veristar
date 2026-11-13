#!/bin/sh
# database leader loss: the core node holding the patroni leader is powered
# off under load while a writer commits a row every 20 ms and the dashboard
# is probed on both core nodes. patroni must promote the synchronous replica
# without help; every acknowledged commit and every acknowledged event must
# be there afterwards, and the edge must not return a single error.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/db-leader"
mkdir -p "$out"
ensure_state
run="db$(date -u +%H%M%S)"
victim="$(patroni_leader)"
log "leader is $victim"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration 4m -out "$out/ledger.json" >"$out/load.json" &
lp=$!
dc commits -duration 4m -out "$out/commits-acked.json" >"$out/commits.json" &
cp=$!
dc probe -state "$STATE" -duration 4m >"$out/probe.json" &
pp=$!
sleep 60
log "power off $victim"
off="$(ts)"
docker kill "$victim" >/dev/null
start=$(date +%s)
until new="$(patroni_leader)" && [ "$new" != "$victim" ]; do sleep 0.5; done
promoted=$(( $(date +%s) - start ))
log "promoted $new after ${promoted}s"
sleep 90
log "power on $victim"
docker start "$victim" >/dev/null
wait_healthy "$victim"
wait "$lp" "$cp" "$pp"
dc commits -check "$out/commits-acked.json" >"$out/commits-check.json" || true
dc lag -until-zero 600s >"$out/lag.json" || true
dc verify -ledger "$out/ledger.json" -wait 10m >"$out/verify.json" || true
docker exec "$new" docker compose --project-directory /opt/veristar/core exec -T postgres patronictl -c /etc/patroni/patroni.yml list >"$out/patroni-after.txt" 2>&1 || true
echo "{\"victim\": \"$victim\", \"off\": \"$off\", \"new_leader\": \"$new\", \"promoted_after_s\": $promoted}" | tee "$out/events.json"
cat "$out/commits.json" "$out/commits-check.json" "$out/probe.json" "$out/load.json" "$out/verify.json" "$out/patroni-after.txt"
dc commits -drop >/dev/null || true
