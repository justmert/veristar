# Disaster recovery: both database nodes or the database lost

Brings the platform back after both core nodes, or the PostgreSQL cluster on them, are gone:
a destroyed location pair, an operator mistake that dropped data on the leader and its
synchronous replica alike, or ransomware on both nodes. Finding O-4 of
the review notes. Ingestion does not stop meanwhile: the edge tier keeps
accepting events into JetStream (`WEB_EVENTS` 72 hours, `API_METRICS` 7 days), and they are
delivered once the core tier is back.

The path chains pieces that exist and are tested on their own:

1. a fresh **restore node** from OpenTofu (`restore_enabled`);
2. `restore.yml`, which restores the pgBackRest repository to a moment on that node;
3. `migrate-export METHOD=dump` from the restored server;
4. new core nodes with an empty, bootstrapped cluster on a **new repository generation**;
5. `migrate-import`, `verify-migration` against the restored server, then the services.

Data loss window: what was not yet archived when the database was lost, at most
`archive_timeout` (60 s, `roles/core/defaults/main.yml`) of committed transactions
([../reference/availability.md](../reference/availability.md)).

## Steps

1. **Stop what still runs on the core nodes**, if anything does, so nothing writes into a
   half-dead cluster: on each reachable core node
   `cd /opt/stellar-analytics/core && sudo docker compose stop`.

2. **Create the restore node.** In `infra/tofu/envs/<env>/terraform.tfvars` set
   `restore_enabled = true` (and `restore_location` if `fsn1` is affected), then
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

5. **Replace the core nodes**:
   `tofu ... plan -replace=module.node["<env>-core-1"].hcloud_server.this` (and core-2), as in
   [edge-node-failure.md](edge-node-failure.md) step 1, then `make <env>-apply`.

6. **Remove the old cluster from etcd**, so the new nodes bootstrap a new cluster instead of
   waiting to join a leader that no longer exists. On any edge node:

   ```sh
   cd /opt/stellar-analytics/edge
   sudo docker compose exec etcd etcdctl --endpoints=https://127.0.0.1:2379 \
     --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key \
     del --prefix /stellar-analytics/sa-<env>/
   ```

7. **Raise the repository generation.** The new cluster has a new system id, which the old
   stanza refuses. Add `backup_repo_generation: <old + 1>` (it starts at 1) to the
   environment's `group_vars/all.yml`; the new cluster then archives to
   `/<env>-g<n>` in the same bucket, and the old generation stays readable and locked until its
   retention ends. Restores from the old generation pass `-e backup_repo_generation=<old>`.

8. **Converge**: `make <env>-deploy SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<...>`.
   `site.yml` bootstraps an empty cluster, creates the stanza of the new generation, switches
   WAL archiving on and starts the services.

9. **Import and verify** with the services on the core nodes stopped
   (`sudo docker compose stop umami controlplane replayer metricswriter jobs` on both):

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

Not yet exercised: the whole chain on staging (blocked on an operator input, and steps 3 to 10 on the
local converge. The local point-in-time drill (`tests/drills/pitr.sh`) covers step 3 only and has
not been run either (the design).
