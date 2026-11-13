#!/bin/sh
# edge node loss under load: edge 2 is powered off (its container killed,
# nothing shut down cleanly) 60 s into the load and powered on 120 s later.
# the client keeps the dead address in its answer until the dns stand-in's
# 30 s port-80 check drops it; bunny.net's removal time cannot be measured
# here an operator input.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/edge-loss"
mkdir -p "$out"
ensure_state
run="edge$(date -u +%H%M%S)"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration 5m -out "$out/ledger.json" >"$out/load.json" &
lp=$!
sleep 60
log "power off sa-lc-edge-2"
echo "{\"off\": \"$(ts)\"" >"$out/events.json"
docker kill sa-lc-edge-2 >/dev/null
sleep 120
log "power on sa-lc-edge-2"
docker start sa-lc-edge-2 >/dev/null
echo ", \"on\": \"$(ts)\"" >>"$out/events.json"
wait_healthy sa-lc-edge-2
echo ", \"healthy\": \"$(ts)\"}" >>"$out/events.json"
wait "$lp"
dc lag -until-zero 600s >"$out/lag.json" || true
dc verify -ledger "$out/ledger.json" -wait 10m >"$out/verify.json" || true
cat "$out/events.json" "$out/load.json" "$out/verify.json"
