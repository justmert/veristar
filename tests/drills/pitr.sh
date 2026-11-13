#!/bin/sh
# point-in-time restore: a full backup, load, the checksum snapshot at time
# T (writes paused by table locks for the instant of the snapshot), more
# load, then restore.yml restores to T on a fresh node and the checksums are
# compared. reports the restore time and the archive lag, which is the
# data-loss window if both database nodes were lost at once.
. "$(dirname "$0")/lib.sh"
out="$DRILLS/pitr"
mkdir -p "$out"
ensure_state
bk() { docker exec sa-lc-backup-1 docker compose --project-directory /opt/stellar-analytics/backup exec -T pgbackrest pgbackrest --stanza=sa "$@"; }
log "full backup"
t0=$(date +%s)
bk --type=full backup >"$out/backup-full.log" 2>&1
echo "full_backup_s=$(( $(date +%s) - t0 ))" >"$out/times.env"
run="pitr$(date -u +%H%M%S)"
dc load -state "$STATE" -run "$run" -web "$LOAD_WEB_RATE" -api "$LOAD_API_RATE" -duration 4m -out "$out/ledger.json" >"$out/load.json" &
lp=$!
# the archive lag under load: age of the last archived wal, every 5 s
leader="$(patroni_leader)"
(for _ in $(seq 1 45); do
	docker exec "$leader" docker compose --project-directory /opt/stellar-analytics/core exec -T postgres \
		psql -U postgres -tAX -c "select extract(epoch from now() - last_archived_time), failed_count from pg_stat_archiver" 2>/dev/null
	sleep 5
done) >"$out/archive-lag.txt" &
ap=$!
sleep 90
log "diff backup under load"
t0=$(date +%s)
bk --type=diff backup >"$out/backup-diff.log" 2>&1
echo "diff_backup_s=$(( $(date +%s) - t0 ))" >>"$out/times.env"
dc snapshot -out "$out/snapshot.json" >/dev/null
target="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["t"])' "$out/snapshot.json")"
log "snapshot at $target"
wait "$lp" "$ap"
# the wal holding T must be in the repository before it can be replayed
docker exec "$leader" docker compose --project-directory /opt/stellar-analytics/core exec -T postgres \
	psql -U postgres -tAX -c "select pg_switch_wal()" >/dev/null
sleep 10
bk info >"$out/info.txt" 2>&1
bk verify >"$out/verify-repo.txt" 2>&1 || true
set -- $LC_RESTORE_NODE
log "fresh node $1"
lc_node_create "$1" "$2" "$3" "$4"
sleep 5
ssh-keyscan -T 5 "$3" 2>/dev/null >>"$LC_STATE/known_hosts" || true
t0=$(date +%s)
(cd "$root/infra/ansible" && "$ANSIBLE_BIN/ansible-playbook" -i inventories/local/ playbooks/restore.yml -e "restore_target='$target'") >"$out/restore.log" 2>&1
rc=$?
echo "restore_playbook_s=$(( $(date +%s) - t0 )) restore_rc=$rc" >>"$out/times.env"
grep -E 'TASK \[|ok=|failed=' "$out/restore.log" | tail -3
dc checksum -addr "$4" -snapshot "$out/snapshot.json" >"$out/checksum.json" || true
cat "$out/times.env" "$out/checksum.json"
echo "archive lag samples (age of last archived wal in s, failures):"; sort -n "$out/archive-lag.txt" | tail -3
