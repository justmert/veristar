---
title: "Disaster recovery: the database cluster lost"
sidebarTitle: "Disaster recovery"
description: "Bring the platform back when the database cluster is lost."
---

Brings the platform back after the PostgreSQL cluster is gone: an operator mistake that dropped
data on every member alike, ransomware on the database nodes, or the loss of the nodes holding
the members. Ingestion does not stop meanwhile: the edge tier keeps
accepting events into JetStream (`WEB_EVENTS` 72 hours, `API_METRICS` 7 days), and they are
delivered once the core tier is back.

> **Layout.** Staging and production run three converged nodes (`<env>-node-1` to `-3`, one per
> zone). Each node runs the edge project (`/opt/veristar/edge`: gateway, collector, NATS, etcd)
> and the core project (`/opt/veristar/core`: Caddy for every site, a Patroni member, HAProxy,
> Umami, control plane, replayer, metrics writer, jobs). The NATS store on each node holds the
> queued events, so this runbook keeps a node's VM whenever its system can be trusted and
> replaces only what was lost.

**This runbook or a replica rebuild.** Check the members first, on any node that still runs:
`cd /opt/veristar/core && sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list`.
While one member still holds good data (a lost node, a damaged replica), the cluster is not lost:
replace the node or rebuild the replica as in
[database-failover.md](database-failover.md) ("A lost node", "Replica rebuild"), and Patroni
re-joins it from the leader. Follow this runbook, which restores from pgBackRest, only when no
member holds good data.

The path chains pieces that exist and are tested on their own:

1. a fresh **restore node** from OpenTofu (`restore_enabled`);
2. `restore.yml`, which restores the pgBackRest repository to a moment on that node;
3. `migrate-export METHOD=dump` from the restored server;
4. an empty, bootstrapped cluster on the three nodes, archiving to a **new repository generation**;
5. `migrate-import`, `verify-migration` against the restored server, then the services.

Data loss window: what was not yet archived when the database was lost, at most
`archive_timeout` (60 s, `roles/core/defaults/main.yml`) of committed transactions
([../reference/availability.md](../reference/availability.md)).

## Steps

1. **Stop the database and its services on every node**, if anything still runs, so nothing
   writes into a half-dead cluster. Caddy keeps running: it is in the same project and also
   serves ingestion, which the edge project keeps queueing into JetStream. On each reachable
   node:

   ```sh
   cd /opt/veristar/core
   sudo docker compose stop $(sudo docker compose config --services | grep -vx caddy)
   ```

   Expected: `docker compose ps` lists only `caddy` as running. The node's `app` endpoint
   in Traffic Manager turns degraded (their `/core` probe fails); `in` and `otlp` stay
   online.

2. **Create the restore node.** In `infra/tofu/envs/<env>/terraform.tfvars` set
   `restore_enabled = true` (and `restore_zone` if zone 3 is affected), then
   `make <env>-plan` and `make <env>-apply`. Expected: `Plan: 1 to add` for
   `<env>-restore-1` (a core-sized server on the private network at `.41`) and an inventory
   whose `restore` group holds it (`tests/infra/*.tftest.hcl` run `inventory_with_restore`
   checks this shape).

3. **Restore** to the moment before the loss (or to the end of the newest backup when the
   repository itself is the only survivor):

   ```sh
   cd infra/ansible
   ansible-playbook -i inventories/<env>/ playbooks/restore.yml \
     -e sa_image_tag=<release> -e sa_release_manifest=<release-images.json> \
     -e "restore_target='2026-11-20 07:59:00+00'"
   ```

   If someone deleted from the repository too, read it as it was before the deletion by adding
   `-e '{"restore_extra_args": ["--repo=1", "--repo-target-time=2026-11-20 07:00:00+00"]}'`
   (the bucket is versioned and object-locked, [../deploy/backups.md](../deploy/backups.md)).
   Expected: `restored to ... on <restore node private address>:5432`.

4. **Export** from the restored, standalone server (no services write to it) with an env file
   naming the restore node as in [../handover/rebuild.md](../handover/rebuild.md) section 4.1
   (`PLATFORM_DB_HOST=<restore node private address>`, port 5432, the superuser login from
   the SOPS file):

   ```sh
   make migrate-export METHOD=dump MIGRATE_ENV=<restore env file> MIGRATE_DIR=<new directory>
   ```

5. **Prepare the nodes for the new cluster.** Decide for each node, then do all three:

   - **The VM runs and its system is trusted** (an operator mistake, or damage confined to the
     database): keep the VM, so its NATS member and its queued events stay, and delete only the
     database volumes. On that node:

     ```sh
     cd /opt/veristar/core
     sudo docker compose rm -sf postgres
     sudo docker volume rm veristar-core_pgdata veristar-core_pgrun
     ```

     Expected: `docker volume ls` no longer lists either volume. The converge of step 8 creates
     them empty.

   - **The VM is lost or not trusted** (ransomware, a deleted or unbootable server): replace
     it. Its public IPv4 address is a separate resource that survives the VM, so the Traffic
     Manager endpoints keep their targets and need no change. Plan one `-replace` per node
     (production shown; the node name is the inventory name):

     ```sh
     sops exec-env infra/secrets/production/tofu.enc.env \
       'tofu -chdir=infra/tofu/envs/production plan -input=false -out=tfplan \
         -replace=module.node["production-node-2"].azurerm_linux_virtual_machine.this'
     ```

     Expected: `1 to add, 1 to destroy` for each replaced VM and nothing else apart from the
     delete locks. Production VMs carry a delete lock; handle `protect` as in
     [edge-node-failure.md](edge-node-failure.md) ("A node that is gone for good", step 1).
     Then `make production-apply`, which rewrites `infra/ansible/inventories/production/hosts.json`.
     The new VM has a new SSH host key, which the controller refuses for a known address
     (`ansible.cfg`), so remove the old key: `ssh-keygen -R <node public address>`. Run
     `make internal-ca ENV=production`: the node keeps its name and private address, so its
     certificate in `tls.enc.yaml` stays valid and nothing is issued.

   **One or two nodes replaced, at least one kept.** Replace one node at a time where you can:
   the kept nodes hold the NATS quorum and the queued events, and the new node takes its copy
   of every stream from them once step 8 starts its NATS server, because a JetStream peer is
   identified by its server name. With two nodes replaced, the kept node holds the only copy
   of the queued events: never replace it in the same run. etcd keeps its quorum on the kept
   nodes for step 6, and the edge role adds each new node's member again in step 8
   (`roles/edge/tasks/etcd.yml`, [edge-node-failure.md](edge-node-failure.md) step 4).

   **All three nodes replaced.** Put all three `-replace` arguments in one plan
   (`3 to add, 3 to destroy`). The NATS stores go with the VMs, so the events queued and not
   yet replayed are part of the data loss, and ingestion answers on no node until step 8. Skip
   step 6: the new etcd cluster starts empty. In step 8 `site.yml` starts NATS on all three
   nodes, the routes connect over the private network (`roles/edge/templates/nats.conf.j2`),
   the servers elect a JetStream meta leader, and `bootstrap.yml` creates every stream, bucket
   and consumer (`platformctl nats apply`).

   **Traffic Manager.** A replaced node fails its health checks until step 8 has converged it,
   so its `in`, `otlp` and `app` endpoints show degraded and are left out of the answers
   without any change. To keep a node out on purpose until you have checked it, disable its three
   endpoints, `in-<n>`, `otlp-<n>` and `app-<n>` (profile names
   `veristar-app-production-<label>`):

   ```sh
   az network traffic-manager endpoint update -g veristar-production \
     --profile-name veristar-app-production-in --type externalEndpoints -n in-2 \
     --endpoint-status Disabled
   ```

   and enable them again with `--endpoint-status Enabled` once the node is healthy. OpenTofu
   declares every endpoint enabled, so `make production-plan` shows a change while one is
   disabled. Staging uses the same names with `staging`.

6. **Remove the old cluster from etcd**, so the nodes bootstrap a new cluster instead of
   waiting to join a leader that no longer exists. Skip this step when all three nodes were
   replaced. On any kept node:

   ```sh
   cd /opt/veristar/edge
   sudo docker compose exec etcd etcdctl --endpoints=https://127.0.0.1:2379 \
     --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key \
     del --prefix /veristar/sa-<env>/
   ```

7. **Raise the repository generation.** The new cluster has a new system id, which the old
   stanza refuses. Add `backup_repo_generation: <old + 1>` (it starts at 1) to the
   environment's `group_vars/all.yml`; the new cluster then archives to
   `/<env>-g<n>` in the same bucket, and the old generation stays readable and locked until its
   retention ends. Restores from the old generation pass `-e backup_repo_generation=<old>`.

8. **Converge**: `make <env>-deploy SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<...>`.
   `site.yml` runs in this order: baseline and secrets on every node, the edge infrastructure
   (NATS and etcd; a replaced node's etcd member joins again), the core infrastructure (Patroni
   bootstraps an empty cluster on one node and the other two join it as replicas, HAProxy), the
   stanza of the new generation and WAL archiving, `bootstrap.yml` (databases, roles, the NATS
   topology), then the edge and core services and the monitoring. Check on each node:

   ```sh
   curl -s 'http://127.0.0.1:8222/jsz?streams=true' | head -c 600; echo
   cd /opt/veristar/core && sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list
   cd /opt/veristar/edge && sudo docker compose exec etcd etcdctl --endpoints=https://127.0.0.1:2379 \
     --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key \
     member list -w table
   ```

   Expected: a `meta_cluster` with a `leader`, `cluster_size` 3 and the other two peers
   `current`; one `Leader`, one
   `Sync Standby` and one `Replica`, all `streaming`; three etcd members `started`. Each node's
   port 80 answers `308`, and Traffic Manager adds its endpoints back.

9. **Import and verify** with the services on every node stopped
   (`sudo docker compose stop umami controlplane replayer metricswriter jobs` on each):

   ```sh
   make migrate-import MIGRATE_ENV=<new leader env file> MIGRATE_DIR=<directory of step 4>
   make verify-migration SOURCE_ENV=<restore env file> TARGET_ENV=<new leader env file>
   ```

   Expected: `verify-migration` exits 0 (row counts of every table, per-tenant daily web and API
   aggregates, Umami `/stats` for a sample of sites and days). Then start the services again
   (`make <env>-deploy ...`), and the replayer and metrics writer drain what JetStream held.

10. **Take a full backup** of the new generation at once:
    `sudo systemctl start sa-pgbackrest-full.service` on the leader, then
    `pgbackrest --stanza=sa info` shows it.

11. **Remove the restore node**: `restore_enabled = false`, plan and apply.

Record the recovery time (from step 2 to step 9) and the data loss window
(the restore target against the last archived WAL) in `docs/reference/drills/<date>.md`.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | the restore node in the OpenTofu inventory (step 2) against mocked providers | `make tofu-check`: `inventory_with_restore` passes for staging and production | `tests/infra/*.tftest.hcl` |
| 2026-11-13 | steps 2, 3 and 11 on production: `production-restore-1` created, `restore.yml` restored to a target time and promoted, then the node removed | `pg_is_in_recovery()` false, both databases present, newest `cp.audit_log` row equal to the running cluster's; 5 resources destroyed afterwards | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Restore" |

The export, import and verification of steps 4 and 9 are the migration tools of the handover,
run between two separate local environments ([../handover/rebuild.md](../handover/rebuild.md)).
