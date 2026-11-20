---
title: "etcd compaction and defragmentation"
sidebarTitle: "etcd maintenance"
description: "Compact and defragment the etcd cluster by hand, and clear a NOSPACE alarm."
icon: "broom"
---

etcd holds Patroni's leader key and the cluster state. It keeps every revision of every key until
it is compacted, and Patroni writes its leader key on every loop, so the history grows by
thousands of revisions an hour. Compaction drops old revisions but leaves the database file the
same size; a defragmentation rewrites the file and returns the free pages to the disk. The
database file counts against the quota (`--quota-backend-bytes`, 2 GiB). At the quota etcd
raises the `NOSPACE` alarm and refuses every write until the alarm is cleared.

Two mechanisms keep the file small without you:

- **Compaction**: every member runs with `--auto-compaction-mode=periodic` and
  `--auto-compaction-retention=1h` (`infra/ansible/roles/edge/templates/compose.yml.j2`), so etcd
  compacts every hour and keeps one hour of history. The log shows
  `completed auto periodic compaction` each time.
- **Defragmentation**: `sa-etcd-defrag.timer` on each node defragments that node's member once
  a week, on Tuesday from 04:00 UTC, each member 20 minutes after the previous one. It first
  checks that every member is healthy and does nothing otherwise (the unit fails, and
  [SystemdUnitFailed](alerts.md#systemdunitfailed) reports it).

Use this runbook when:

- [EtcdDatabaseSpaceHigh](alerts.md#etcddatabasespacehigh) or
  [EtcdNoSpaceAlarm](alerts.md#etcdnospacealarm) fires.
- A cluster that ran without compaction gets the settings above for the first time. Its file
  keeps its size until it is defragmented, so run the steps below once after that deploy.

A member blocks its own reads and writes while it defragments, for well under a second at a few
megabytes. Defragment one member at a time, and only while all three are healthy, so two members
always keep the quorum.

## Steps

1. **Open a shell on one node** and define the etcd client of its member. Every later command
   uses it:

   ```sh
   ssh ops@<node>
   cd /opt/veristar/edge
   e() { sudo docker compose exec -T etcd etcdctl --endpoints=https://127.0.0.1:2379 \
     --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key "$@"; }
   ```

2. **Read the state of the cluster:**

   ```sh
   e endpoint status --cluster -w table
   e endpoint health --cluster
   e alarm list
   ```

   Expected: three rows with `QUOTA` `2.1 GB`, three lines `is healthy`. `DB SIZE` and
   `IN USE` show how much the defragmentation can return. `alarm list` prints nothing, or
   `memberID:<id> alarm:NOSPACE` when the cluster refuses writes. If a member is not healthy,
   bring it back first ([edge-node-failure.md](edge-node-failure.md)).

3. **Compact to the current revision.** Compaction is cluster wide, so run it once:

   ```sh
   rev=$(e endpoint status -w json | grep -o '"revision":[0-9]*' | cut -d: -f2)
   e compaction --physical "$rev"
   ```

   Expected: `compacted revision <rev>`. Patroni reads only current keys, so dropping the
   history does not affect it.

4. **Defragment the member of this node**, then check the cluster again:

   ```sh
   e defrag
   e endpoint health --cluster
   ```

   Expected: `Finished defragmenting etcd member[https://127.0.0.1:2379]`, then three lines
   `is healthy`.

5. **Repeat step 4 on each other node**, one node at a time: open a shell there as in step 1 and
   run step 4. Start the next node only after the previous one reported three healthy members.

6. **Clear the alarm** if step 2 listed one. etcd keeps the alarm after the space is free until
   it is disarmed:

   ```sh
   e alarm disarm
   e alarm list
   ```

   Expected: `alarm disarm` prints the alarm it cleared, and `alarm list` then prints nothing.

7. **Check** with `e endpoint status --cluster -w table`: `DB SIZE` is a few megabytes on every
   member, and `IS LEADER` is `true` on one of them. Patroni has a leader again
   (`sudo docker compose --project-directory /opt/veristar/core exec -T postgres patronictl -c
   /etc/patroni/patroni.yml list`). EtcdDatabaseSpaceHigh and EtcdNoSpaceAlarm resolve at the
   next scrape.

## Exercised

| Date | What ran | Result |
|---|---|---|
| 2026-11-17 | Steps 2 to 7 against a local three-member etcd 3.6.16 cluster with mutual TLS, after 300 writes of a 20 kB key | `DB SIZE` 6.3 MB before, 41 kB after on every member; every member healthy after each defragmentation |
| 2026-11-17 | `NOSPACE` on a local single etcd member with a 4 MiB quota, on etcd 3.6.16 and on 3.7.3 | writes refused with `database space exceeded` and `etcd_debugging_server_alarms{alarm_type="NOSPACE"}` 1 on both versions; after steps 3, 4 and 6 `alarm list` printed nothing and writes succeeded again |
| 2026-11-17 | `sa-etcd-defrag` against the same local cluster, all members healthy and then one member stopped | healthy: the member was defragmented; one stopped: `Error: unhealthy cluster`, exit 1, no defragmentation |
