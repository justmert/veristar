# Backups: bucket, stanza and WAL archiving

How the pgBackRest repository of staging and production comes into existence and stays
protected. `site.yml` does every step below; nothing is done by hand. Code:
`infra/ansible/roles/storage`, `infra/ansible/roles/core/tasks/archive.yml`,
`infra/ansible/roles/backup`. Restores are in
[../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md) and
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md).

## 1. Order in `site.yml`

| Play | What it does | Why here |
|---|---|---|
| Backup bucket (`roles/storage`, on the controller) | creates `backup_bucket` with object lock, enables versioning, sets the default retention, reads all three back | before any database node can archive to it |
| Core infrastructure | Patroni starts PostgreSQL with `archive_mode = on` and an **empty** `archive_command` | PostgreSQL keeps every WAL segment and archives nothing, instead of failing each segment against a repository without a stanza |
| WAL archiving (`core/tasks/archive.yml`) | the Patroni leader runs `pgbackrest stanza-create`; every database node gets the marker `/etc/stellar-analytics/pgbackrest/stanza-ready` and the real `archive_command`; Patroni reloads; `pgbackrest check` proves a fresh segment reaches the bucket | the stanza exists before archiving is switched on; the kept WAL is archived at once |
| Core services | the backup timers (`sa-pgbackrest-{full,diff,verify}`) on both core nodes; only the leader runs them | |

Every step is idempotent: a second converge reports no change (`stanza-create` on an existing,
matching stanza says so and changes nothing).

## 2. The bucket

`roles/storage/files/backup-bucket.sh` talks to the S3 API with curl (SigV4), like
`make state-bootstrap`, so the controller needs no S3 client.

- **Object lock** is enabled when the bucket is created (`x-amz-bucket-object-lock-enabled`).
  It cannot be added to an existing bucket. A bucket that exists without it stops the run with
  that message: create a new bucket name in `terraform.tfvars` (`backup_bucket`) rather than
  converging without protection.
- **Versioning** is enabled (Hetzner turns it on for locked buckets anyway; the script sets and
  reads it back).
- **Default retention:** `COMPLIANCE` for `storage_lock_days` (35) days. pgBackRest keeps four
  weekly full backups and seven days of differentials, so the oldest file a kept backup needs is
  at most about 34 days old. Compliance mode cannot be shortened or bypassed by any key,
  including the one that wrote the object. Governance mode can be bypassed by a key with the
  bypass right, and Hetzner keys are not scoped, so it would protect against mistakes only.
  The role refuses a lock shorter than `(backup_retention_full + 1) * 7` days.

Hetzner support, checked on 2026-11-13 against Hetzner's own documentation:

- [Object Lock: Retention](https://docs.hetzner.com/storage/object-storage/howto-protect-objects/protect-object-lock-retention/):
  object lock must be enabled at bucket creation; `put-object-lock-configuration` with a
  `DefaultRetention` of `GOVERNANCE` or `COMPLIANCE` and `Days`; compliance mode cannot be ended
  early; a new default retention "takes a few minutes" to apply (the script then stops and asks
  for a rerun instead of passing).
- [Buckets & objects FAQ](https://docs.hetzner.com/storage/object-storage/faq/buckets-objects/):
  locked buckets always have versioning; a delete without a version id only adds a delete
  marker; permanent deletion is blocked by compliance retention.
- [List of supported actions](https://docs.hetzner.com/storage/object-storage/supported-actions/):
  `CreateBucket` accepts `x-amz-bucket-object-lock-enabled`; conditional PUT and DELETE are not
  supported on versioned buckets, which matters only for the OpenTofu state bucket
  ([opentofu.md](opentofu.md) section 3.4), not here: pgBackRest does not use conditional writes.
- Not documented by Hetzner: keys scoped to one bucket or to read-only. Every key of the project
  can write and delete in every bucket. The lock is therefore the only protection against a
  stolen core-node key, and the key in the SOPS file stays the only key the nodes hold.
- Hetzner's documentation also says a contract termination deletes the data even before the
  retention ends. The lock protects against keys, not against the account owner (see the
  billing section of [prerequisites.md](prerequisites.md)).

What it costs: every version, including those pgBackRest expired, stays billed until its lock
ends, so the stored volume is about five weeks of backups and WAL rather than the four-week
retention alone.

## 3. Reading the repository as it was before a deletion

`pgbackrest expire`, or anyone with the key, can only add delete markers while the lock holds.
pgBackRest 2.60 reads a versioned repository as it was at a moment with `--repo-target-time`
(`pgbackrest help restore repo-target-time` in the `postgres-ha` image):

```sh
cd /opt/stellar-analytics/core
sudo docker compose exec postgres pgbackrest --stanza=sa --repo=1 \
  --repo-target-time="2026-11-20 08:00:00+00" info
```

`restore.yml` passes the same option through `restore_extra_args` (see
[../runbooks/disaster-recovery.md](../runbooks/disaster-recovery.md) step 3).

## 4. What was proven where

| What | Where | Result |
|---|---|---|
| `backup-bucket.sh`: bucket created with object lock, default retention `COMPLIANCE` 35 days, versioning read back; second run reports no change; after a plain delete a locked version is still listed and deleting that version answers 403 | the script against a standalone SeaweedFS 4.52 with TLS (2026-11-13, by hand) | as stated |
| `site.yml` in `s3` mode: storage role creates the bucket (`changed: created bucket sa-lc-backup with object lock`, `default retention none 0d -> COMPLIANCE 35d`), the leader creates the stanza before `archive_command` is set, `pgbackrest check` passes, `pg_stat_archiver` counts archived and no failed segments, archived WAL is in the bucket | `make ops-local`, steps `converge` (247 s, `failed=0`), `bucket` and `archiving`, 2026-11-13 | passed |
| scheduled full backup, delete refusal with the node's key, `--repo-target-time` read after delete markers, second converge with no change | `make ops-local`, later steps | not completed: the run was stopped (host load, then the topology was removed from outside the run) |
| The same against Hetzner Object Storage | never run | blocked on an operator input |

The stand-in runs the same role, script, `pgbackrest.conf` template (`repo1-type=s3`,
host-style addressing, the bucket and path) and the same commands. It differs in two settings
only, both refused outside the local converge: `repo1-storage-host` (its address, because the
`.test` name resolves nowhere) and `repo1-storage-ca-file` (its private CA). It is not Hetzner:
whether Hetzner requires `Content-MD5` or another checksum on writes to a locked bucket is not
documented, so the first staging converge proves it with `pgbackrest check`, which fails the run
if writes are refused.
