#!/bin/sh
# full nats restart mid-day: all three nats servers restart at once under a
# web load from a fixed set of visitors (each site's five user agents from
# one address). the memory buckets empty, so the day's visitor secret is new
# afterwards: the same visitors are counted again and
# their open visits split. the drill counts both, and checks that no
# acknowledged event is lost.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/nats-restart"
mkdir -p "$out"
ensure_state
run="natsr$(date -u +%H%M%S)"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration 4m -out "$out/ledger.json" >"$out/load.json" &
lp=$!
sleep 90
log "restart nats on all edge nodes"
at="$(ts)"
for n in sa-lc-edge-1 sa-lc-edge-2 sa-lc-edge-3; do
	nexec "$n" edge restart nats >/dev/null 2>&1 &
done
wait %2 %3 %4 2>/dev/null || true
sleep 5
wait "$lp"
dc lag -until-zero 600s >"$out/lag.json" || true
dc verify -ledger "$out/ledger.json" -wait 10m >"$out/verify.json" || true
leader="$(patroni_leader)"
q="select case when created_at < '$at' then 'before' else 'after' end, count(distinct session_id), count(distinct visit_id), count(*)
   from website_event where url_path like '/d/$run/%' group by 1 order by 1"
docker exec "$leader" docker compose --project-directory /opt/veristar/core exec -T postgres \
	psql -U postgres -d umami -tAX -F ' ' -c "$q" >"$out/sessions.txt"
both="select count(*) from (select session_id from website_event where url_path like '/d/$run/%' and created_at < '$at'
   intersect select session_id from website_event where url_path like '/d/$run/%' and created_at >= '$at') x"
docker exec "$leader" docker compose --project-directory /opt/veristar/core exec -T postgres \
	psql -U postgres -d umami -tAX -c "$both" >"$out/sessions-both.txt"
echo "{\"restart\": \"$at\"}" | tee "$out/events.json"
echo "period sessions visits events"; cat "$out/sessions.txt"; echo "sessions seen on both sides: $(cat "$out/sessions-both.txt")"
cat "$out/load.json" "$out/verify.json"
