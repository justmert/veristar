---
title: "Database failover and replica rebuild"
sidebarTitle: "Database failover"
description: "Patroni failover between the database members and rebuilding a replica."
---

PostgreSQL runs under Patroni with three members, one on each converged node of staging and
production (one per availability zone): one leader, one synchronous standby and one
asynchronous replica, the leader key in etcd, which also has a member on each node. HAProxy on
each node sends port 5000 to whichever
member answers `/primary` on Patroni's REST port 8008. Settings (`core_patroni_dcs` in
`infra/ansible/roles/core/defaults/main.yml`): `ttl` 30 s, `loop_wait` 10 s,
`synchronous_mode` on, `synchronous_mode_strict` off, `maximum_lag_on_failover` 1 MiB,
`failsafe_mode` on. The cluster is named `sa-<env>` (`sa-staging`, `sa-production`).

**etcd down is not a database outage.** With `failsafe_mode`, a leader that loses etcd quorum
(`EtcdNoLeader`, two nodes down or cut off) stays primary as
long as it reaches every member listed in Patroni's `/failsafe` through the REST API; no
failover can happen meanwhile. Restore etcd first ([edge-node-failure.md](edge-node-failure.md)
step 4); the database keeps serving.

Alerts that lead here: `PatroniNoLeader`, `ReplicationLagHigh`, `EtcdNoLeader`,
`EtcdMembersDown`, `WALArchivingFailing` ([alerts.md](alerts.md)). Conventions are in
[overview.md](overview.md).

Ingestion does not depend on the database: gateways and collectors keep answering and the queues
hold the work ([queue-backlog-and-dlq.md](queue-backlog-and-dlq.md)). Dashboards, the control
plane, the writer and jobs stop while no leader exists. The configured window is at most about
40 s (TTL plus one loop); on production, writes resumed 30.3 s after the leader was powered off
([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Database
leader loss").

## Automatic failover: check it happened

1. On any node:

   ```sh
   cd /opt/veristar/core && sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list
   ```

   Expected after a leader loss: the former replica shows `Leader` and `running`, the lost
   member is missing or `stopped`. Timeline (`TL`) is one higher than before.

2. Check HAProxy routes to it: on each node,

   ```sh
   sudo docker compose exec postgres psql -h haproxy -p 5000 -U postgres -d postgres -tAc 'select pg_is_in_recovery()'
   ```

   Expected: `f`. The command asks for the superuser password; it is in
   `/etc/veristar/env/platformctl.env` as `PLATFORM_DB_ADMIN_PASSWORD`. Without
   automatic failover (no `Leader` for more than a minute), check etcd first: Patroni cannot
   take the leader key without two etcd members ([edge-node-failure.md](edge-node-failure.md)).

3. Check the services reconnected: `sudo docker compose ps` on every node, the
   `ServiceDown` and `UmamiHeartbeatFailing` alerts resolve, and the console loads.

With one member down the leader still has a synchronous standby. While only the leader runs, it
commits alone (synchronous mode is not strict); a further failure in that time loses what only
the leader had, back to the last archived WAL (`archive_timeout` 60 s). Bring the lost members
back promptly.

## Planned switchover

For maintenance on the leader's node. `deploy.yml` does the same before it recreates a leader's
postgres container (`infra/ansible/playbooks/tasks/switchover.yml`).

```sh
cd /opt/veristar/core
sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml switchover \
  --leader <current leader name> --candidate <replica name> --force
```

Names are the inventory names (`production-node-1` to `-3`). The candidate must be the
`Sync Standby` in `streaming` state. Expected: the list shows the candidate as `Leader` within
a few seconds and no committed transaction is lost.

## Replica rebuild

The replica is stopped, far behind, or its data is damaged (`ReplicationLagHigh` that does not
recover, `start failed` in the list).

1. Start it first: `sudo docker compose up -d postgres` on its node, then `patronictl list`.
   Patroni uses `pg_rewind` (`use_pg_rewind: true`), so a former leader usually rejoins as a
   replica by itself.

2. If it does not, have Patroni rebuild it from the leader (this deletes the replica's data
   directory). A replica that fell behind the leader's WAL retention fetches the missing
   segments from the pgBackRest repository (`restore_command` in `patroni.yml`), so it usually
   catches up without this step:

   ```sh
   sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml reinit sa-staging <replica name> --force
   ```

   Expected: the member goes through `creating replica` to `streaming`, and `Lag in MB`
   reaches 0. On production the cluster is `sa-production`.

3. Confirm the replica is synchronous again: the list shows `Sync Standby`.

## A lost node

Replace the server with OpenTofu as in [edge-node-failure.md](edge-node-failure.md) ("A node
that is gone for good", with `module.node["<env>-node-<n>"]`), then
`make staging-deploy SA_IMAGE_TAG=<release tag> SA_RELEASE_MANIFEST=<release-images.json>`. Patroni on the new node finds the cluster in
etcd and creates a replica from the leader.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | Production: automatic failover, the leader `production-node-2` powered off | `production-node-3` became leader on timeline 2 without action; writes through HAProxy resumed 30.3 s after the power-off; the newest committed row from before it was present; the old leader streamed again as a replica with lag 0 64 s after its start | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Database leader loss" |
| 2026-11-14 | Production: replica rebuild of `production-node-3` through the Patroni REST API (`POST /reinitialize`) after it stopped in recovery | the member rebuilt from the leader and the node served again | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Rollout incident: web.veristar.app 502 on node 3" |
| 2026-11-14 | Production: planned switchovers of the rolling `deploy.yml`, release `v0.2.1` | both ran (leader node 2 to node 3, then node 3 to node 1), `failed=0` on every node | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Release v0.2.1 rollout and gateway capacity" |
