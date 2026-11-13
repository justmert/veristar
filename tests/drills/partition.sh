#!/bin/sh
# location partition: fsn1 (edge 1 and core 1) is cut off the private
# network for 180 s under load, the public network stays up. edge 1's nats
# and etcd are then a minority of one; the two other locations must keep
# quorum for both, with zero acknowledged events lost.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/partition"
mkdir -p "$out"
ensure_state
run="part$(date -u +%H%M%S)"
loc="${PARTITION_LOCATION:-fsn1}"
before="$(patroni_leader)"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration 5m -out "$out/ledger.json" >"$out/load.json" &
lp=$!
dc probe -state "$STATE" -duration 5m >"$out/probe.json" &
pp=$!
sleep 60
log "partition $loc"
off="$(ts)"
lc_partition "$loc"
sleep 20
# quorum seen from the majority side
tcurl -fsS "https://$(priv_of sa-lc-edge-2):8222/jsz" >"$out/jsz-majority.json" || true
etcdctl_in sa-lc-edge-2 --endpoints=https://10.99.2.13:2379,https://10.99.3.14:2379 endpoint status -w json >"$out/etcd-majority.json" 2>&1 || true
etcdctl_in sa-lc-edge-1 --endpoints=https://10.99.1.12:2379 endpoint health >"$out/etcd-minority.txt" 2>&1 || true
curl -s -o /dev/null -w '%{http_code}\n' "http://$(pub_of sa-lc-edge-1)/" >"$out/edge1-port80.txt" || true
sleep 160
log "heal $loc"
on="$(ts)"
lc_heal "$loc"
wait_healthy sa-lc-edge-1
wait_healthy sa-lc-core-1
wait "$lp" "$pp"
after="$(patroni_leader)"
dc lag -until-zero 600s >"$out/lag.json" || true
dc verify -ledger "$out/ledger.json" -wait 10m >"$out/verify.json" || true
echo "{\"location\": \"$loc\", \"cut\": \"$off\", \"healed\": \"$on\", \"leader_before\": \"$before\", \"leader_after\": \"$after\"}" | tee "$out/events.json"
cat "$out/etcd-majority.json" "$out/etcd-minority.txt" "$out/edge1-port80.txt" "$out/probe.json" "$out/load.json" "$out/verify.json"
