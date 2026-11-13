---
title: "Upgrading an environment"
sidebarTitle: "Upgrade"
description: "Release a new version and roll it out one node at a time, with signatures checked first."
---

A new version of the platform is a release tag; rolling it out is `deploy.yml`, one node at a
time, with every image's signature checked first. Upstream images (Umami, NATS, etcd, HAProxy,
observability) change only when their pin in `infra/ansible/vars/pins.yml` changes, which is a
code change released the same way.

**Status:** the rolling deploy ran on the local converge with a new image tag (this stage:
edge 1, 2, 3, then core 1, each gated on its port-80 answer; [local-converge.md](local-converge.md)
section 3) and its signature check ran in `make supplychain-it` with a test-only key. The this stage
rolling-deploy-under-load drill was not run. Nothing has run against staging.

## Steps

1. **Prepare the change.** Merge to `main` with the required checks green. For changes that touch
   a specific component, also follow its runbook first:
   Umami ([../runbooks/umami-upgrade.md](../runbooks/umami-upgrade.md)), collector
   ([../runbooks/collector-upgrade.md](../runbooks/collector-upgrade.md)), tracker
   ([../runbooks/tracker-release.md](../runbooks/tracker-release.md)), DB-IP
   ([../runbooks/dbip-update.md](../runbooks/dbip-update.md)), PostgreSQL
   ([../runbooks/security-patch.md](../runbooks/security-patch.md) section 4).

2. **Tag the release** `v<release>`. `release-images.yml` builds, scans (Trivy fails on fixable
   critical or high), signs keyless and attests every image, and attaches `release-images.json`
   to the GitHub release. Download that file.

3. **Check out the release** in your working copy (the playbooks and `pins.yml` are read from
   it), then roll out to staging:

   ```sh
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   ```

   Order (`infra/ansible/playbooks/deploy.yml`): verify every platform image's signature on the
   controller and pin the nodes to the verified digests; then each edge node in turn: drain
   (port 80 answers 503), wait 120 s for DNS, update, wait for health, undrain; run `bootstrap.yml` (migrations, role
   passwords, Umami bootstrap, NATS topology of the release); then each core node in turn, handing the Patroni leader to the
   synchronous replica before a node's postgres image changes. Any failure stops the run with the
   remaining nodes untouched.

   Expected: `PLAY RECAP` with `failed=0`. Ingestion should see no failed requests; that has not
   been measured (this stage drill not run).

4. **Verify** with [verification.md](verification.md), then watch the alerts for an hour.

5. **Production:** the same command with `production-deploy`.

## Database migrations

`deploy.yml` imports `bootstrap.yml` between the edge and the core nodes (the play named "Apply
the nats topology of the new release"), so `platformctl db migrate`, `db roles`, Umami's own
migration on start, and `nats apply` all run with the verified release images before any core
node is updated. Migrations are additive by convention; read a release's new files in
`db/migrations/` before rolling it out, because the rollback below does not undo them. Expected
log line from the migration step: `platform database migrated` with `rls_coverage ok`.

## Rollback

Deploy the previous release tag the same way (step 3 with the previous tag and its manifest).
This undoes image changes only. It does not undo a database migration (platform or Umami): for
Umami see the rollback in [../runbooks/umami-upgrade.md](../runbooks/umami-upgrade.md); for the
platform database, restore from backup ([../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md)).
