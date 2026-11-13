#!/bin/sh
# clock agreement. lock fencing assumes server clocks agree within the 15 s
# lock ttl. the nodes of the local converge share this machine's kernel
# clock and may not set it (chronyd runs with -x), and go programs read the
# clock through the vdso, so no node clock can be skewed here: the drill
# measures the real offsets, and checks that the detector fires by giving it
# a bound below the measured spread. a real skewed node is not tested.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/clock"
mkdir -p "$out"
dc clock -max-skew 1s >"$out/clock-1s.json"; echo "rc=$?" >>"$out/clock-1s.json"
dc clock -max-skew 1ns >"$out/clock-1ns.json" 2>&1 || echo "rc=$? (alarm expected)" >>"$out/clock-1ns.json"
for n in sa-lc-edge-1 sa-lc-edge-2 sa-lc-edge-3 sa-lc-core-1 sa-lc-core-2 sa-lc-backup-1; do
	echo "$n $(docker exec "$n" chronyc -c tracking | cut -d, -f5,6)"
done | tee "$out/chrony.txt"
cat "$out/clock-1s.json" "$out/clock-1ns.json"
