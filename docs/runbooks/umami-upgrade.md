# Umami upgrade and rollback

Umami runs as its official image, unmodified, pinned by tag and index digest
(`ghcr.io/umami-software/umami:3.5.0@sha256:b64f4040...a5dbc` in
`infra/ansible/vars/pins.yml`). A new version is always a deliberate change: Umami migrates its
own database (Prisma migrations) when it starts, and those migrations only go forward. So the
rollback is a database restore, never just the old image.

For a security fix inside the 24-hour target, follow [security-patch.md](security-patch.md); it
uses the same upgrade gate and adds triage and mitigations. This page is the general procedure
plus the rollback.

## Upgrade

1. **Gate it on the development stack** (needs `make dev-up`, Docker, Node 26 with pnpm and the
   Playwright browsers, Go 1.27.4 on `PATH`):

   ```sh
   make umami-upgrade VERSION=<x.y.z>
   ```

   `scripts/umami-upgrade.sh` resolves the index digest the registry serves for that version,
   moves the pin in `deploy/compose/dev.yml`, `infra/ansible/vars/pins.yml`,
   `tests/observability/compose.yml` and `docs/reference/umami-contract.md`, recreates the dev
   stack so Umami migrates the existing database, then runs `umami-compat`, the isolation gate,
   `console-e2e` and `webpath`, stopping at the first failure. Each step prints
   `step <name>: ok in <n> s`. A new version stays red at `umami-compat` until someone has
   reviewed its OpenAPI routes against the Caddy deny list and updated
   `tests/umami-compat/testdata/openapi-routes.txt`. Commit the diff only when every gate passed.

2. **Release.** Merge, tag `v<release>`; `release-images.yml` builds, scans and signs the
   platform images and attaches `release-images.json`. Umami itself is an upstream image and is
   pulled by its pinned digest.

3. **Take a dump of the Umami database before the rollout.** On the core node that holds the
   Patroni leader ([README.md](README.md)):

   ```sh
   cd /opt/stellar-analytics/core
   sudo sh -c 'docker compose exec -T postgres pg_dump -U postgres -Fc -d umami > /root/umami-pre-upgrade.dump'
   sudo ls -l /root/umami-pre-upgrade.dump
   sudo docker compose exec -T postgres psql -U postgres -d umami -tAc \
     "select count(*) || ' migrations, last ' || max(migration_name) from _prisma_migrations where finished_at is not null"
   ```

   Write down the time and the migration line (for 3.5.0: `27 migrations, last
   27_add_error_tracking`). The point-in-time backups also cover this moment, but a dump of one
   database is far quicker to restore.

4. **Roll out** to staging, then production:

   ```sh
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   ```

   `deploy.yml` drains and updates one node at a time and stops at the first unhealthy node.
   The new Umami migrates the database on its first start on the first core node.

5. **Verify**: [../deploy/verification.md](../deploy/verification.md) steps for the core tier,
   `UmamiHeartbeatFailing` stays quiet, a tenant dashboard opens through the console's SSO, and
   `WEB_EVENTS` drains ([queue-backlog-and-dlq.md](queue-backlog-and-dlq.md)).

## Rollback

Use it when the new version misbehaves in a way a fix-forward cannot address in time.

1. **Stop delivery into Umami** on both core nodes, so nothing more is written to the database
   you are about to replace. Accepted events wait in `WEB_EVENTS` (72 hours):

   ```sh
   cd /opt/stellar-analytics/core && sudo docker compose stop replayer umami umami-2 umami-3 umami-4
   ```

   Run it on both core nodes. The list names every Umami process: `umami` plus `umami-2` up to
   `umami-<core_umami_processes>` (4 by default; `sudo docker compose ps` shows them). Tenant web
   dashboards are down from here until step 5.

2. **Restore the dump** on the leader's node:

   ```sh
   cd /opt/stellar-analytics/core
   sudo docker compose exec -T postgres psql -U postgres -d postgres -c 'drop database umami with (force)'
   sudo docker compose exec -T postgres psql -U postgres -d postgres -c 'create database umami owner umami_app'
   sudo docker compose exec -T postgres psql -U postgres -d postgres -c 'revoke all on database umami from public'
   sudo docker compose exec -T postgres psql -U postgres -d postgres -c 'grant connect on database umami to umami_app'
   sudo sh -c 'docker compose exec -T postgres pg_restore -U postgres -d umami --no-owner --role=umami_app < /root/umami-pre-upgrade.dump'
   ```

   Then recreate the retention role's grants, which live in the Umami database:
   run `platformctl db umami-retention-role` as in [README.md](README.md).

3. **Check the migration level** matches what step 3 of the upgrade recorded:

   ```sh
   sudo docker compose exec -T postgres psql -U postgres -d umami -tAc \
     "select count(*) || ' migrations, last ' || max(migration_name) from _prisma_migrations where finished_at is not null"
   ```

4. **Deploy the previous release**, whose `pins.yml` names the previous Umami digest. In your
   working copy check out that release's tag, then:

   ```sh
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<previous release> SA_RELEASE_MANIFEST=<its release-images.json>
   ```

   The deploy starts Umami and the replayer again.

5. **Verify** as in upgrade step 5. The replayer delivers what queued since step 1.

**What a rollback loses:** events the replayer stored in Umami between the dump (upgrade step 3)
and rollback step 1. They were acknowledged and removed from `WEB_EVENTS`, so nothing re-sends
them. Keep the time between the dump and the decision short, and tell the tenants which window
is affected. The platform database (control plane, API metrics) is not touched by this rollback.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | Upgrade rehearsal 3.4.1 to 3.5.0 with `make umami-upgrade VERSION=3.5.0` on a throwaway copy pinned to 3.4.1 | every gate green; detection to green 3 h 18 min, including two webpath reruns caused by host load | [security-patch.md](security-patch.md) section 7 |
| 2026-11-13 | this stage rollback rehearsal on throwaway containers (PostgreSQL 18.6, Umami 3.4.1 digest `sha256:e69d1a79...022e8`, Umami 3.5.0 digest `sha256:b64f4040...a5dbc`), not the shared dev stack | 3.4.1 migrated an empty database (`26 migrations, last 26_add_api_key`); `pg_dump -Fc` 66,200 bytes; 3.5.0 started and migrated (`27 migrations, last 27_add_error_tracking`); database dropped and recreated, `pg_restore --no-owner --role=umami_app` gave `26 migrations, last 26_add_api_key`; 3.4.1 started on it, heartbeat ok after 12 s | this page |

Not yet exercised: the rollout and rollback through `deploy.yml` on staging (inputs I4, I5,
I6, I13, I14, I15), the dump on a Patroni leader, and the retention role step after a restore.
The this stage rehearsal used plain containers with the same images and the same `pg_dump` and
`pg_restore` commands.
