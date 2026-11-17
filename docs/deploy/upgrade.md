---
title: "Upgrading an environment"
sidebarTitle: "Upgrade"
description: "Release a new version and roll it out one node at a time, with signatures checked first."
---

A new version of the platform is a release tag. You roll it out with `deploy.yml`, one node at a
time, with every image's signature checked first. Upstream images (Umami, NATS, etcd, HAProxy,
observability) change only when their pin in `infra/ansible/vars/pins.yml` changes, which is a
code change released the same way.

Production rolls out releases this way: release `v0.3.0` reached all three nodes with
`failed=0` in the core-first order below and three planned Patroni switchovers
([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Release
v0.3.0 rollout").

## Steps

1. **Prepare the change.** Merge to `main` with the checks green. For changes that touch
   a specific component, also follow its runbook first:
   Umami ([../runbooks/umami-upgrade.md](../runbooks/umami-upgrade.md)), collector
   ([../runbooks/collector-upgrade.md](../runbooks/collector-upgrade.md)), tracker
   ([../runbooks/tracker-release.md](../runbooks/tracker-release.md)), DB-IP
   ([../runbooks/dbip-update.md](../runbooks/dbip-update.md)), PostgreSQL
   ([../runbooks/security-patch.md](../runbooks/security-patch.md) section 4).

2. **Release the images** of tag `v<release>` in one of the two signing modes
   ([../security/supply-chain.md](../security/supply-chain.md) section 3):

   - **Keyless** (`release-images.yml`, repository variable `SIGNING_MODE=keyless`): pushing the
     tag builds and scans every image (Trivy fails on fixable critical or high findings), signs
     and attests it with the workflow's GitHub identity, and attaches `release-images.json` to the
     GitHub release. Releases `v0.2.3` and `v0.3.0` are keyless.
   - **Key** (a workstation release, for example while the repository's GitHub Actions are off):
     `scripts/supply-chain/release-workstation.sh v<release> <work dir>` from a clean checkout of
     the tag runs the same build and gate, signs with the release key from the production SOPS
     file and attaches `release-images.json`
     ([../security/supply-chain.md](../security/supply-chain.md) section 2.1). Releases `v0.2.0`
     to `v0.2.2` are key-mode workstation releases.

   Download `release-images.json` of the release. `infra/ansible/vars/signing.yml` sets the mode
   the deploy verifies, `keyless`. For a key-mode release, add
   `ANSIBLE_EXTRA='-e sa_signature_mode=key'` to every `make <env>-deploy` below; without it the
   signature check (`verify-release.yml`) refuses the release before any node is touched.

3. **Check out the release** in your working copy (the playbooks and `pins.yml` are read from
   it), then roll out to staging:

   ```sh
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   # a key-mode release:
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json> \
     ANSIBLE_EXTRA='-e sa_signature_mode=key'
   ```

   Order (`infra/ansible/playbooks/deploy.yml`): verify every platform image's signature on the
   controller and pin the nodes to the verified digests; run `bootstrap.yml` (migrations, role
   passwords, Umami bootstrap, NATS topology of the release); then each core node in turn, the
   node holding the Patroni leader last: drain (port 80 answers 503), wait 150 s for DNS, hand
   the Patroni leader to the synchronous replica before the node's postgres image changes,
   update, wait for health, undrain; then each edge node
   in turn the same way with a 120 s wait; then the observability role on every node. Any failure
   stops the run with the remaining nodes untouched.

   The core tier goes before the edge tier because the jobs process and the control plane of a
   release read everything its gateways send, while an older jobs daemon refuses an `EDGE_STATS`
   outcome it does not know as malformed. Release `0.3.0` is the first that needs this order: its
   gateways send the `evidence_net_<class>` and `network_<key>` counters of the integrity grade.
   Its jobs and control plane also read the counters of a `0.2.x` gateway, and a `0.2.x` gateway
   reads the new registry entries (it skips the new `scale` field), so the platform keeps
   counting while the tiers differ. A release whose topology needs a newer NATS server says so in
   its upgrade notes; you converge it with `site.yml` instead.

   Expected: `PLAY RECAP` with `failed=0`, and one Patroni switchover when the release changes the
   postgres image: the leader's node goes last, so its synchronous replica already runs the new
   image when it takes over. A rolling deploy of all three production nodes takes
   about 30 min. Release `v0.3.0`: migrations applied 2 min 47 s after the start, the edge tier
   began after 17 min 28 s, once every core node was done, and the run ended after 34 min 1 s.

4. **Verify** with [verification.md](verification.md), then watch the alerts for an hour.

5. **Production:** the same command with `production-deploy`.

## Database migrations

`deploy.yml` imports `bootstrap.yml` right after the signature check (the play named "Apply the
migrations and the nats topology of the new release"), so `platformctl db migrate`, `db roles`,
Umami's own migration on start, and `nats apply` all run with the verified release images before
any node is updated. Migrations are additive by convention; read a release's new files in
`db/migrations/` before rolling it out, because the rollback below does not undo them. Expected
log line from the migration step: `platform database migrated` with `rls_coverage ok`.

## Rollback

Deploy the previous release tag the same way (step 3 with the previous tag and its manifest).
Rolling back from `0.3.0` to a `0.2.x` release, the older jobs refuse every `EDGE_STATS` window of
a `0.3.0` gateway that still runs, and move it with all its counters to the dead-letter stream,
until the edge tier is rolled back too. This undoes image changes only. It does not undo a database migration (platform or Umami): for
Umami see the rollback in [../runbooks/umami-upgrade.md](../runbooks/umami-upgrade.md); for the
platform database, restore from backup ([../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md)).

### Rollback across an OpenTofu change

Some releases come with an OpenTofu change that the release's upgrade notes tell you to apply
after the deploy, such as the removal of the `web.<BASE_DOMAIN>` host after `0.3.0`.
`make <env>-apply` changes the cloud resources and rewrites the inventory
`infra/ansible/inventories/<env>/hosts.json` from the new state, and the templates of the earlier
release read hosts from that inventory. The `0.3.0` templates read the `web` hostname, which the
new inventory no longer has, so a deploy of `0.3.0` stops on the missing name once the apply ran.

The apply is therefore the point after which a rollback needs one more step:

1. Before the apply, keep the inventory the earlier release reads:
   `cp infra/ansible/inventories/<env>/hosts.json infra/ansible/inventories/<env>/hosts.json.pre-<release>`.
   Apply only after the new release has run through its soak (step 4 and a day without a
   rollback).
2. To roll back after the apply, check out the earlier release's tag and run
   `make <env>-plan` and `make <env>-apply` from that checkout. Its configuration recreates the
   removed resources (for `0.3.0`: the Traffic Manager profile `web`, its endpoints and the
   Cloudflare CNAME) and writes the inventory the earlier templates read. Compare it with the
   kept copy, then deploy the earlier release as above.
