#!/bin/sh
# the whole core tier down. both core nodes are powered off under load and
# powered on again; ingestion must answer without a single error while they
# are down, and afterwards the backlog must drain with exact counts.
#
# duration: the plan asks for two hours. this drill runs CORE_OUTAGE_MINUTES
# (default 12) at ten times the expected peak, which queues the same number
# of events as two hours at the expected peak (500/s x 720 s = 50/s x 7200 s
# = 360,000 web events, and 1,160/s x 720 s = 116/s x 7200 s = 835,200 api
# exports). the reasons this is equivalent, and what it does not cover, are
# in docs/reference/drills/2026-11-13.md; the script prints the time
# settings the argument rests on (stream max age, consumer settings).
. "$(dirname "$0")/lib.sh"
out="$DRILLS/core-outage"
mkdir -p "$out"
ensure_state
mins="${CORE_OUTAGE_MINUTES:-12}"
run="core$(date -u +%H%M%S)"
tcurl -fsS "https://$(priv_of sa-lc-edge-1):8222/jsz?accounts=true&streams=true&consumers=true&config=true" >"$out/jsz-before.json"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration "$((mins + 2))m" -out "$out/ledger.json" >"$out/load.json" &
lp=$!
sleep 60
log "power off both core nodes for $mins min"
off="$(ts)"
docker kill sa-lc-core-1 sa-lc-core-2 >/dev/null
sleep $((mins * 60))
log "power on both core nodes"
on="$(ts)"
docker start sa-lc-core-1 sa-lc-core-2 >/dev/null
wait "$lp"
loadend="$(ts)"
wait_healthy sa-lc-core-1
wait_healthy sa-lc-core-2
up="$(ts)"
dc lag -until-zero 3600s >"$out/lag.json" || true
drained="$(ts)"
dc verify -ledger "$out/ledger.json" -wait 30m >"$out/verify.json" || true
echo "{\"minutes\": $mins, \"off\": \"$off\", \"on\": \"$on\", \"load_end\": \"$loadend\", \"core_healthy\": \"$up\", \"queues_empty\": \"$drained\"}" | tee "$out/events.json"
cat "$out/load.json" "$out/lag.json" "$out/verify.json"
