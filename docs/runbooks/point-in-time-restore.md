# Point-in-time restore

Restores the whole PostgreSQL cluster (both databases, `umami` and `platform`) to a moment in
the past onto a **fresh** node, from the pgBackRest repository, without touching the running
cluster. Use it after data was destroyed by mistake or by an attacker, or to inspect old state.
Code: `infra/ansible/playbooks/restore.yml`, `infra/ansible/roles/backup`.

What the repository holds: a full backup every Sunday 01:00 UTC, a differential every other day
at 01:00 UTC, `verify` plus `check` daily at 05:30 UTC, and continuous WAL archiving
(`archive_timeout` 60 s). Retention: four full backups and the differentials of the last seven
days, so any point from the oldest full backup to the newest archived WAL can be reached.
Backups are encrypted with `pgbackrest_cipher_pass` from the SOPS file. In staging and
production the repository is Hetzner Object Storage (`backup_repo_mode: s3`, an operator input.

## Steps

1. **Pick the target time** as a timestamp with offset, for example from the audit log or the
   moment an incident started. Check what the repository can reach, on the leader's node:

   ```sh
   cd /opt/stellar-analytics/core && sudo docker compose exec postgres pgbackrest --stanza=sa info
   ```

   Expected: `status: ok`, the list of full and diff backups with their times, and the WAL
   range. The target must lie after the oldest backup's stop time.

2. **Make sure the WAL covering the target is archived.** If the target is within the last
   minute, force a segment switch on the leader:

   ```sh
   sudo docker compose exec postgres psql -U postgres -tAc 'select pg_switch_wal()'
   ```

3. **Provide a fresh node.** `restore.yml` runs on the hosts of the inventory group `restore`.
   Set `restore_enabled = true` (and `restore_location` if needed) in
   `infra/tofu/envs/staging/terraform.tfvars`, then `make staging-plan` and
   `make staging-apply`. OpenTofu creates `staging-restore-1`, a core-sized Debian 13 server on
   the private network at `.41` of its location, and the regenerated inventory lists it in
   `restore` (checked by `inventory_with_restore` in `tests/infra/staging.tftest.hcl`).

   The playbook refuses a node that already holds a restore volume.

4. **Run the restore** from the controller:

   ```sh
   cd infra/ansible && ~/.local/share/stellar-analytics/ansible-venv/bin/ansible-playbook \
     -i inventories/staging/ playbooks/restore.yml -e "restore_target='<YYYY-MM-DD HH:MM:SS+00>'"
   ```

   It applies the base and secrets roles, writes the pgBackRest settings, restores the stanza
   with `--type=time --target-action=promote`, starts a standalone PostgreSQL with archiving off,
   and waits until recovery has promoted. Expected last task:
   `restored to <target> on <private address>:5432`.

5. **Check the result** on the restore node:

   ```sh
   sudo docker exec sa-restore-postgres psql -h /var/run/postgresql -U postgres -d platform -tAc 'select max(at) from cp.audit_log'
   ```

   The newest audit row must be at or just before the target. Compare whatever was lost against
   the running cluster.

6. **Use it.** Either copy the needed rows back into the running cluster (`pg_dump` of the
   affected tables on the restore node, reviewed, then loaded on the leader), or, for a total
   loss, make the restored data the new cluster with
   [disaster-recovery.md](disaster-recovery.md), which continues from this step.

7. **Remove the restore node** when done: `restore_enabled = false`, then plan and apply.

## What is lost

Everything after the target time in PostgreSQL. Web events accepted at the edge after the
target that are still in `WEB_EVENTS` (72 hours) are not lost if the replayer is pointed at the
restored database, but events the replayer already stored after the target are. API metric
batches likewise within `API_METRICS`'s 7 days. Data erased after the target comes back with the
restore; re-run those erasure requests ([erasure-requests.md](erasure-requests.md)).

## Exercised

**Not yet exercised.** `restore.yml` and `tests/drills/pitr.sh` are written but were not run:
the this stage drills stopped when the shared host became unusable
([../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md)). What did run: on the
local converge, `stanza-create` and `check` succeeded against the posix repository on the backup
node, and the timers exist (same record, "Topology checks"). The S3 repository on Hetzner Object
Storage, including `repo1-s3-uri-style=host`, has never been tested an operator input. In this stage this
could not be executed: it needs Patroni, pgBackRest and a repository node, which is the
multi-node topology the shared host may not run. It must be executed on staging, with recovery
time and loss window recorded, before production.
