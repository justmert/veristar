#!/bin/sh
# nats member loss: the nats server on edge 3 is stopped under load for
# 120 s, then started; the drill then waits until every stream replica on it
# is current again and reports how long that took.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/nats-member"
mkdir -p "$out"
ensure_state
run="nats$(date -u +%H%M%S)"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration 4m -out "$out/ledger.json" >"$out/load.json" &
lp=$!
sleep 45
log "stop nats on sa-lc-edge-3"
off="$(ts)"
nexec sa-lc-edge-3 edge stop nats >/dev/null 2>&1
sleep 120
log "start nats on sa-lc-edge-3"
on="$(ts)"
nexec sa-lc-edge-3 edge start nats >/dev/null 2>&1
start=$(date +%s)
# caught up: the server on edge 3 reports jetstream healthy, which needs
# every stream and consumer it holds a replica of to be current
until curl -fsS -m 3 -o /dev/null "http://$(priv_of sa-lc-edge-3):8222/healthz"; do sleep 1; done
caught=$(( $(date +%s) - start ))
wait "$lp"
curl -fsS "http://$(priv_of sa-lc-edge-3):8222/jsz?streams=true" >"$out/jsz-edge3.json"
dc lag -until-zero 600s >"$out/lag.json" || true
dc verify -ledger "$out/ledger.json" -wait 10m >"$out/verify.json" || true
echo "{\"stopped\": \"$off\", \"started\": \"$on\", \"caught_up_after_start_s\": $caught}" | tee "$out/events.json"
cat "$out/load.json" "$out/verify.json"
