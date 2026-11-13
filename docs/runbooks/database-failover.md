# Database failover and replica rebuild

PostgreSQL runs under Patroni on both core nodes: one leader, one synchronous replica, the
leader key in etcd on the three edge nodes. HAProxy on each core node sends port 5000 to whichever
member answers `/primary` on Patroni's REST port 8008. Settings (`core_patroni_dcs` in
`infra/ansible/roles/core/defaults/main.yml`): `ttl` 30 s, `loop_wait` 10 s,
`synchronous_mode` on, `synchronous_mode_strict` off, `maximum_lag_on_failover` 1 MiB,
`failsafe_mode` on. The cluster is named `sa-<env>` (`sa-staging`, `sa-production`).

**etcd down is not a database outage.** With `failsafe_mode`, a leader that loses etcd quorum
(`EtcdNoLeader`, two edge nodes down, the edge tier cut off from the core tier) stays primary as
long as it reaches every member listed in Patroni's `/failsafe` through the REST API; no
failover can happen meanwhile. Restore etcd first ([edge-node-failure.md](edge-node-failure.md)
step 4); the database keeps serving.

Alerts that lead here: `PatroniNoLeader`, `ReplicationLagHigh`, `EtcdNoLeader`,
`EtcdMembersDown`, `WALArchivingFailing` ([alerts.md](alerts.md)). Conventions are in
[README.md](README.md).

Ingestion does not depend on the database: gateways and collectors keep answering and the queues
hold the work ([queue-backlog-and-dlq.md](queue-backlog-and-dlq.md)). Dashboards, the control
plane, the writer and jobs stop while no leader exists. The configured window is at most about
40 s (TTL plus one loop); it has not been measured.

## Automatic failover: check it happened

1. On either core node:

   ```sh
   cd /opt/veristar/core && sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list
   ```

   Expected after a leader loss: the former replica shows `Leader` and `running`, the lost
   member is missing or `stopped`. Timeline (`TL`) is one higher than before.

2. Check HAProxy routes to it: on each core node,

   ```sh
   sudo docker compose exec postgres psql -h haproxy -p 5000 -U postgres -d postgres -tAc 'select pg_is_in_recovery()'
   ```

   Expected: `f`. The command asks for the superuser password; it is in
   `/etc/veristar/env/platformctl.env` as `PLATFORM_DB_ADMIN_PASSWORD`. Without
   automatic failover (no `Leader` for more than a minute), check etcd first: Patroni cannot
   take the leader key without two etcd members ([edge-node-failure.md](edge-node-failure.md)).

3. Check the services reconnected: `sudo docker compose ps` on both core nodes, the
   `ServiceDown` and `UmamiHeartbeatFailing` alerts resolve, and the console loads.

While only one member runs, the leader commits alone (synchronous mode is not strict). A second
failure in that time loses what only the leader had, back to the last archived WAL
(`archive_timeout` 60 s). Bring the other member back promptly.

## Planned switchover

For maintenance on the leader's node. `deploy.yml` does the same before it recreates a leader's
postgres container (`infra/ansible/playbooks/tasks/switchover.yml`).

```sh
cd /opt/veristar/core
sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml switchover \
  --leader <current leader name> --candidate <replica name> --force
```

Names are the inventory names (`staging-core-1`, `staging-core-2`). The candidate must be the
`Sync Standby` in `streaming` state. Expected: the list shows the candidate as `Leader` within
a few seconds and no committed transaction is lost.

## Replica rebuild

The replica is stopped, far behind, or its data is damaged (`ReplicationLagHigh` that does not
recover, `start failed` in the list).

1. Start it first: `sudo docker compose up -d postgres` on its node, then `patronictl list`.
   Patroni uses `pg_rewind` (`use_pg_rewind: true`), so a former leader usually rejoins as a
   replica by itself.

2. If it does not, have Patroni rebuild it from the leader (this deletes the replica's data
   directory):

   ```sh
   sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml reinit sa-staging <replica name> --force
   ```

   Expected: the member goes through `creating replica` to `streaming`, and `Lag in MB`
   reaches 0. On production the cluster is `sa-production`.

3. Confirm the replica is synchronous again: the list shows `Sync Standby`.

## A lost core node

Replace the server with OpenTofu as in [edge-node-failure.md](edge-node-failure.md) ("A node
that is gone for good", with `module.node["staging-core-<n>"]`), then
`make staging-deploy SA_IMAGE_TAG=<release tag> SA_RELEASE_MANIFEST=<release-images.json>`. Patroni on the new node finds the cluster in
etcd and creates a replica from the leader.

## Exercised

**Not yet exercised.** The this stage database-leader drill (`tests/drills/db-leader.sh`) is
written but was not run: the shared host stopped being usable before it
([../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md)). The switchover task
of `deploy.yml` has not run either, because the rolling-deploy drill did not run. What has run:
the local converge with two core nodes showed core 2 `Leader` and core 1 `Sync Standby`,
`streaming`, lag 0 (same record, "Topology checks"). In this stage the steps could not be executed:
the development stack has a single PostgreSQL without Patroni, and the multi-node topology may
not run on the shared host. This runbook must be executed on staging (inputs I5, I13, I14, I15)
before it is relied on.
