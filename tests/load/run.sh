#!/bin/sh
# the load profile at ten times expected peak (profiles.env) against the
# local converge: web and api load for LOAD_DURATION (default 5m), then the
# ledger check, the drain time and the queue lag.
#   tests/load/run.sh
. "$(dirname "$0")/../drills/lib.sh"
out="$DRILLS/load"
mkdir -p "$out"
ensure_state
run="load$(date -u +%H%M%S)"
log "load $run: web $LOAD_WEB_RATE/s api $LOAD_API_RATE/s for ${LOAD_DURATION:-5m}"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" \
	-duration "${LOAD_DURATION:-5m}" -out "$out/ledger.json" >"$out/load.json"
log "load done, waiting for the backlog"
dc lag -until-zero "${LOAD_DRAIN_SECONDS}s" >"$out/lag.json" || true
dc verify -ledger "$out/ledger.json" -wait 10m >"$out/verify.json" || true
cat "$out/load.json" "$out/lag.json" "$out/verify.json"
