---
title: "Point-in-time restore"
description: "Restore the PostgreSQL cluster to a past moment on a fresh node, without touching the running cluster."
icon: "clock-rotate-left"
---

Restores the whole PostgreSQL cluster (both databases, `umami` and `platform`) to a moment in
the past onto a **fresh** node, from the pgBackRest repository, without touching the running
cluster. Use it after data was destroyed by mistake or by an attacker, or to inspect old state.
Code: `infra/ansible/playbooks/restore.yml`, `infra/ansible/roles/backup`.

What the repository holds: a full backup every Sunday 01:00 UTC, a differential Monday to
Saturday at 01:00 UTC, `verify` plus `check` daily at 05:30 UTC, and continuous WAL archiving
(`archive_timeout` 60 s). Retention: four full backups and the differentials of the last seven
days, so any point from the oldest full backup to the newest archived WAL can be reached.
Backups are encrypted with `pgbackrest_cipher_pass` from the SOPS file. In staging and
production the repository is Azure Blob Storage (`backup_repo_mode: azure`,
[../deploy/backups.md](../deploy/backups.md)).

## Steps

1. **Pick the target time** as a timestamp with offset, for example from the audit log or the
   moment an incident started. Check what the repository can reach, on the leader's node:

   ```sh
   cd /opt/veristar/core && sudo docker compose exec postgres pgbackrest --stanza=sa info
   ```

   Expected: `status: ok`, the list of full and diff backups with their times, and the WAL
   range. The target must lie after the oldest backup's stop time.

2. **Make sure the WAL covering the target is archived.** If the target is within the last
   minute, force a segment switch on the leader:

   ```sh
   sudo docker compose exec postgres psql -U postgres -tAc 'select pg_switch_wal()'
   ```

3. **Provide a fresh node.** `restore.yml` runs on the hosts of the inventory group `restore`.
   Set `restore_enabled = true` (and `restore_zone`, `1` to `3`, if needed) in
   `infra/tofu/envs/<env>/terraform.tfvars`, then `make <env>-plan` and `make <env>-apply`.
   OpenTofu creates `<env>-restore-1`, a core-sized Debian 13 server on the private network at
   `.41` of its zone, and the regenerated inventory lists it in `restore` (checked by
   `inventory_with_restore` in `tests/infra/*.tftest.hcl`). Then issue its internal TLS
   certificate with `make internal-ca ENV=<env>`.

   The playbook refuses a node that already holds a restore volume.

4. **Run the restore** from the controller:

   ```sh
   cd infra/ansible && ~/.local/share/veristar/ansible-venv/bin/ansible-playbook \
     -i inventories/<env>/ playbooks/restore.yml \
     -e sa_image_tag=<release> -e sa_release_manifest=<release-images.json> \
     -e "restore_target='<YYYY-MM-DD HH:MM:SS+00>'"
   ```

   It verifies the release, applies the base and secrets roles, writes the pgBackRest settings, restores the stanza
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

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | Production: steps 1 to 5 and 7. `production-restore-1` in zone 3, target time one minute before a forced WAL switch on the leader | `pgbackrest info` `status: ok`; the restore promoted, the play took 2 min 43 s; `pg_is_in_recovery()` false, `platform` and `umami` present, newest `cp.audit_log` row the same as on the running cluster and before the target; the node removed with `5 destroyed` | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Restore" |
