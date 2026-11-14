---
title: "Backups: storage account, stanza and WAL archiving"
sidebarTitle: "Backups"
description: "How the pgBackRest repository is created and protected, from storage to WAL archiving."
---

This page describes how the pgBackRest repository of an environment comes into existence and
stays protected. OpenTofu creates the storage (`infra/tofu/modules/backup-storage`); `site.yml` does
every step after that; nothing is done by hand. Code: `infra/ansible/roles/storage`,
`infra/ansible/roles/core/tasks/archive.yml`, `infra/ansible/roles/backup`. Restores are in
[../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md) and
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md).

## 1. The storage account

One storage account per environment (`backup_storage_account` in `terraform.tfvars`) in the
environment's region, Azure Denmark East for production (`veristarprodbackups`), with container
`pgbackrest`. Each environment writes under its own path
(`/<environment>`, or `/<environment>-g<n>` after a disaster recovery).

| Setting | Value | Why |
|---|---|---|
| Redundancy | ZRS: three copies in three availability zones | the repository outlives the loss of a zone |
| Access | Entra ID only: account keys and shared access signatures are off | no storage key exists that could leak |
| Who writes | the managed identity `<env>-node` of the three nodes and the restore node, role Storage Blob Data Contributor on the container | pgBackRest signs in with `repo1-azure-key-type=auto` |
| Network | default deny; the environment's three subnets (service endpoint) and the IPv4 ranges of `admin_cidrs` | |
| Blob versioning | on | a delete or overwrite keeps the previous version |
| Immutability | version-level, time-based: every version is immutable for 7 days after it was written; `Unlocked` until the operator locks it (`backup_lock_state`) | not even a subscription owner can delete a protected version early once the policy is `Locked` |
| Lifecycle | previous versions are deleted 35 days after they became previous | covers the four-week retention plus a week |
| Container soft delete | 35 days | |

How the protection works against a stolen node identity: Storage Blob Data Contributor can
delete the current version of a blob, which with versioning only turns it into a previous
version, but it cannot delete a previous version (that needs the `deleteBlobVersion` data
action, which only Storage Blob Data Owner has; Azure "blob versioning" documentation). So for
35 days every backup file stays readable whatever is done with the node's identity, and for the
first 7 days not even the subscription can remove it once the policy is locked.

Why the immutability period is 7 days: Azure refuses to delete a blob while its retention runs,
and pgBackRest deletes files itself when it expires old backups. The youngest file it expires
with the retention of `roles/backup` is a differential backup with seven newer ones, which is 8
days old (seven differentials, plus one Sunday full backup per six of them). A period of 7 days
keeps every expire working; the storage role refuses a period that is not below that age
(`storage_azure_lock_days < backup_retention_diff + backup_retention_diff // 6`).

One case needs an operator: a backup that was interrupted and cannot be resumed is removed by
the next backup run. If that run comes before the interrupted backup's files are 7 days old,
Azure refuses the delete and the run fails; after 26 hours without a new backup the alert
`BackupTooOld` pages. WAL archiving and
the earlier backups are unaffected. The fix is to wait until the files reach 7 days and let the
next scheduled run remove them, or, if a fresh full backup is needed sooner, raise
`backup_repo_generation` so the new backups go to a new path (as in
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md) step 2).

**Locking.** `backup_lock_state = "Locked"` makes the policy permanent: the period can then only
grow, and the storage account cannot be deleted while it holds a protected version. Lock it in
production after the first week of verified backups (`pgbackrest verify` passes every day), with
`make production-plan` and `make production-apply`.

What it costs: every version, including those pgBackRest expired, is billed until the lifecycle
policy deletes it, so the stored volume is about five weeks of backups and WAL rather than the
four-week retention alone (Blob Storage ZRS hot, 0.023 USD per GB a month,
[prerequisites.md](prerequisites.md)).

## 2. Order in `site.yml`

| Play | What it does | Why here |
|---|---|---|
| Backup storage (`roles/storage`, on the controller) | reads the storage account back through the az CLI: ZRS, no account keys, network default deny, versioning, immutability of 7 days, the lifecycle rule, the container | stops before any database node archives when a protection is missing |
| Core infrastructure | Patroni starts PostgreSQL with `archive_mode = on` and an **empty** `archive_command` | PostgreSQL keeps every WAL segment and archives nothing, instead of failing each segment against a repository without a stanza |
| WAL archiving (`core/tasks/archive.yml`) | the Patroni leader runs `pgbackrest stanza-create`; every database node gets the marker `/etc/veristar/pgbackrest/stanza-ready` and the real `archive_command`; Patroni reloads; `pgbackrest check` proves a fresh segment reaches the container | the stanza exists before archiving is switched on; the kept WAL is archived at once |
| Core services | the backup timers (`sa-pgbackrest-{full,diff,verify}`) on every node with a database member (all three nodes in staging and production); only the leader runs them | |

Every step is idempotent: a second converge reports no change (`stanza-create` on an existing,
matching stanza says so and changes nothing).

## 3. Reading the repository as it was before a deletion

pgBackRest 2.60 reads a repository on versioned Azure storage as it was at a moment with
`--repo-target-time` (pgBackRest release notes; `pgbackrest help restore repo-target-time` in
the `postgres-ha` image):

```sh
cd /opt/veristar/core
sudo docker compose exec postgres pgbackrest --stanza=sa --repo=1 \
  --repo-target-time="2026-11-20 08:00:00+00" info
```

`restore.yml` passes the same option through `restore_extra_args` (see
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md) step 3).

## 4. pgBackRest on Azure

Checked against the pgBackRest documentation and release notes on 2026-11-13: `repo-type=azure`
with `repo-azure-account`, `repo-azure-container` and `repo-azure-key-type`, whose values are
`shared`, `sas` and `auto`. `auto` ("automatically authorize using Azure managed identities")
was added in the release of 2026-01-19 and is in the pinned 2.60.0. Later releases also added
batch deletes on Azure (2026-07-20) and fixed retries of batch sub-requests (2026-10-18). The
user guide warns against a hierarchical namespace, which the storage account does not have.

pgBackRest runs inside the `postgres` container and fetches its token from the Azure instance
metadata service (169.254.169.254), which Docker's bridge network reaches through the host.

## 5. Evidence

On production, `pgbackrest check` archived a WAL segment to the backup storage account, a full
backup took 17 s, and `pgbackrest info` reports `status: ok`; a point-in-time restore onto the
restore node promoted at its target
([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md)). The
converge itself proves the identity can write: `pgbackrest check` in `archive.yml` fails the run
if it cannot, and the verify timer runs every day. `make ops-local` runs the stanza, archiving,
backup, delete refusal and `--repo-target-time` steps on the local converge, against an S3 object
storage stand-in (`LC_BACKUP=s3`, [local-converge.md](local-converge.md) section 3).
