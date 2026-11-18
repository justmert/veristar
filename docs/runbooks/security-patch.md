---
title: "Security patch"
description: "Take a vulnerability from signal to patched production within the 24-hour commitment."
---

The path from a vulnerability signal to a patched production. The service target is that critical
and high vulnerabilities are patched within 24 hours ([service targets](../sla.md)). The target for
the whole path, detection to production, is **8 hours**, which leaves 16 hours of margin.

## 1. Where signals come from

| Signal | Source | Lands as |
|---|---|---|
| Umami repository advisory, new Umami release | `security.yml` hourly, `scripts/supply-chain/advisories.py` | issue labelled `security-finding`, alert to `ALERT_WEBHOOK_URL` |
| Next.js, React (as built into the pinned Umami), nats-server, Caddy, etcd, collector modules | same, GitHub advisory database at the exact pinned versions | same |
| New PostgreSQL minor of the pinned major | same, postgresql.org `versions.json` | same |
| Fixable critical or high in a deployed digest | `security.yml`, Trivy on `release-images.json` and the upstream pins | same |
| Called Go vulnerability, npm advisory | `security.yml`, govulncheck and `pnpm audit` | same |
| Private report | GitHub private vulnerability reporting ([SECURITY.md](../security/SECURITY.md)) | security advisory draft |
| Dependency update, advisory on a dependency | Dependabot | pull request; for the pnpm workspace an alert only (section 4, npm packages) |

The clock starts when the first signal is filed, not when someone reads it.

Umami has published advisories without stating a fixed version. Triage therefore never waits for
upstream wording: an unreviewed advisory is a finding by itself, and the operator decides.

## 2. Triage (target: 1 hour)

1. Open the issue. Read the advisory and identify the component and its pinned version
   (`infra/ansible/vars/pins.yml`, `deploy/images/*.Dockerfile`, `go.mod`, `pnpm-lock.yaml`).
2. Decide whether the platform is exposed. No public host serves Umami
   ([ADR 0005](../adr/0005-umami-as-engine-only.md)): only the replayer, the control plane and jobs
   reach it on the private network, the control plane only through the routes of
   [umami-contract.md](../reference/umami-contract.md). A flaw that needs a request to Umami from
   the internet is mitigated already; record that and still upgrade on the normal path.
3. Severity: use the advisory's. If it has none, treat it as high until proven otherwise.
4. Write the decision in the issue: exposed or not, mitigation, upgrade target, and who does it.
5. For an Umami repository advisory, add its id with the decision to
   `docs/security/advisories-reviewed.json` in the patch pull request, so the hourly check stops
   reporting it. For an upstream image finding without a fixed release, record it as in
   section 4, upstream images other than Umami.

## 3. Immediate mitigations (target: within the first hour, when exposed)

Pick the narrowest one that removes the exposure. Each one keeps ingestion running, because the
gateways answer from NATS and do not need Umami or PostgreSQL ([architecture](../architecture.md)).

- **Block a route at Caddy.** Add the path to `deploy/caddy/app.Caddyfile` for the control plane,
  run `go test ./deploy/caddy/` and `make test-caddy-closed`, and roll out with the normal
  deploy.
- **Take the dashboards offline, keep ingestion.** On the core nodes stop every Umami process (and
  the `controlplane` if the flaw is there): `sudo docker compose stop umami umami-2`, plus
  `umami-3` and up when the inventory raises `core_umami_processes`. Events queue in the
  `WEB_EVENTS` stream (72 hour retention) and the replayer delivers them when Umami returns, with
  their original timestamps. Post a notice on the status page.
- **Rotate exposed credentials.** OTLP tokens and operator API keys can be revoked in the console;
  `UMAMI_USER_PASSWORD_KEY` rotation invalidates every Umami session at once.

## 4. Upgrade

### Umami

```
make umami-upgrade VERSION=<x.y.z>
```

`scripts/umami-upgrade.sh` resolves the index digest the registry serves for that version, moves
the pin in every file that holds it (`deploy/compose/dev.yml`, `infra/ansible/vars/pins.yml`,
`tests/observability/compose.yml`, `docs/reference/umami-contract.md`), then runs, timed, stopping at
the first failure:

1. the dev stack recreated on the new image, so Umami migrates an existing database, and
   `umami-bootstrap`;
2. `make umami-compat` (the Umami behaviour checks in Go and Playwright). For a new version its
   reviewed route list (`tests/umami-compat/testdata/openapi-routes.txt`) names the old version and
   the gate stays red until someone reviews the new OpenAPI contract against the deny list and
   updates the list. That review is part of the patch;
3. `make isolation` (the tenant isolation gate);
4. `make console-e2e` and `make webpath` (the end-to-end suites).

The log is in `.scratch/umami-upgrade/`. Commit the diff only after every gate passed.
Before rolling a new Umami out, take the dump that makes a rollback possible, and if the new
version misbehaves, roll back as in [umami-upgrade.md](umami-upgrade.md) (Umami's migrations only
go forward, so the rollback is a database restore).

### Platform images

Bump the dependency (`go get`, `pnpm update`, a base image digest), run `make test`, `make test-containers`, `make isolation`
and the image scan (`scripts/supply-chain/scan-image.sh <image> <dir>` on a local build).

### npm packages (Dependabot alerts on `pnpm-lock.yaml`)

Dependabot raises the alert, but its security update pull request does not complete for this
workspace, so you patch by hand. Two limits of the Dependabot updater cause this, and
`.github/dependabot.yml` has no setting for either:

- The updater runs its own Node 24. The workspace requires Node 26.13.1 with `engineStrict: true`,
  so `pnpm install --lockfile-only` stops with `ERR_PNPM_UNSUPPORTED_ENGINE` before it changes
  the lockfile.
- For a transitive package whose parent pins an older major, the updater runs only
  `pnpm update <package>`, which stays inside the parent's range, and it never writes
  `overrides`. It reports `security_update_not_possible`.

Patch it yourself:

1. List the alerts and trace each package to the workspace project that pulls it in:

   ```sh
   gh api 'repos/justmert/veristar/dependabot/alerts?state=open'
   pnpm why -r <package>
   ```

   Note whether the path ends in a shipped artifact (the console, the site, the tracker, an
   image) or only in tests, tooling or `examples/`.
2. Pick the fixed version from the registry (`npm view <package> time`); it must be older than
   `minimumReleaseAge`.
   - A direct dependency: move its entry in the `catalog` of `pnpm-workspace.yaml`.
   - A transitive dependency: prefer a parent release that no longer needs the package. Otherwise
     add an entry to `overrides` in `pnpm-workspace.yaml`, with the advisory ids in a comment, and
     check from the parent's code that the new version keeps the interface it calls.
3. Run `pnpm install`, `pnpm check`, `pnpm test` and the builds of the console, the site and the
   tracker. A changed console bundle or image is a platform release (section 5).
4. When no fixed version exists, read the advisory and decide from the code whether the
   vulnerable function runs on untrusted input. Replace or remove the package, or dismiss the
   alert with the reason and the evidence:

   ```sh
   gh api -X PATCH repos/justmert/veristar/dependabot/alerts/<number> \
     -f state=dismissed -f dismissed_reason=<not_used|inaccurate|tolerable_risk> \
     -f dismissed_comment='<where the package is used and why the vulnerable path does not run>'
   ```

### Upstream images other than Umami

Move the tag and digest in `infra/ansible/vars/pins.yml` (and `deploy/compose/dev.yml` where
present), resolved from the registry, and run the suites that start that image.

When no release fixes a finding, check whether the vulnerable code is in the image at all
(`govulncheck -mode=binary` on the extracted binary for a Go finding) and whether the platform
exposes it. Record the decision in two places in one pull request: the reasoning in
[supply-chain.md section 8.1](../security/supply-chain.md), and an entry in
`docs/security/advisories-reviewed.json` under `upstream_image_findings` with the exact image tag
and the findings as `<id> <package>`, the way the issue lists them. The hourly report then stops
filing those findings and closes the image's issue once nothing undecided is left. A new tag of
the image is triaged again, because the entry names the old one.

### PostgreSQL

Change `PG_BASE` and `PG_VERSION` in `deploy/images/postgres-ha.Dockerfile` and `postgres_version`
in `vars/pins.yml`, rebuild, then run `make test`, `make test-containers`, `make isolation` and the
recovery drills ([local drill record](../reference/drills/2026-11-13.md)) before rollout.

## 5. Release and roll out (target: 4 hours)

1. Merge the change to `main` with all checks green.
2. Tag `v<release>` and build the release: `release-images.yml`, or the workstation release
   ([supply chain](../security/supply-chain.md), section 2.1), builds, scans and signs every image
   and produces `release-images.json`.
3. Roll out to staging first when you run one: `make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release>
   SA_RELEASE_MANIFEST=<release-images.json>`. The deploy verifies every signature first and
   refuses anything unsigned or mismatched. Check the status page and the alerts.
4. Production: the same command with `production-deploy`.
5. Close the issue with the elapsed time from the first signal.

## 6. Caddy runs as non-root

The Caddy and console images run as uid 10001 (`deploy/images/caddy.Dockerfile`).
New named volumes get the right owner from the image. A node whose `caddydata` and `caddyconfig`
volumes were created by an older root image needs one change before the first deploy of such an
image, or Caddy cannot write its certificates:

```
docker run --rm -v veristar-edge_caddydata:/data -v veristar-edge_caddyconfig:/config \
  --user 0 --entrypoint chown <caddy image> -R 10001:10001 /data /config
```

(`veristar-core_*` on core nodes.)

## 6a. Host operating system (Debian security updates)

Unattended upgrades (`roles/base/tasks/upgrades.yml`) install Debian security updates every day
on every node, with `Automatic-Reboot "false"` so a node never restarts on its own while it
holds a Patroni leader or is in DNS. A kernel or libc fix therefore applies only after a reboot.

- **Signal.** Debian's unattended-upgrades kernel hook creates `/var/run/reboot-required`.
  A timer on every edge and core node (`sa-reboot-required.timer`, `roles/observability`)
  turns it into `node_reboot_required`, and the `HostRebootRequired` alert fires after 12 hours
  ([alerts.md](alerts.md#hostrebootrequired)), which leaves half of the 24-hour target for the
  reboot. For a critical kernel CVE, reboot without waiting for the alert.
- **Install the fix on every node first.** Each node's daily upgrade runs at its own time of
  day and installs only what its package lists held then, so one node can miss a kernel the
  others already have. On every node:

  ```sh
  sudo apt-get update -qq && sudo unattended-upgrade --dry-run -v | grep 'Packages that will be upgraded'
  sudo unattended-upgrade
  ```

  Expected: the dry run lists the fixed package (for a kernel, `linux-image-cloud-amd64`), or
  nothing when the node has it already, and `/var/run/reboot-required` exists on each node
  that installed it.
- **Reboot, one node at a time:**

  ```sh
  cd infra/ansible
  ansible-playbook -i inventories/<env>/ playbooks/reboot.yml
  ```

  It reboots only the nodes holding `/var/run/reboot-required` (`-e reboot_all=true` reboots
  every node): edge nodes one by one, drained from DNS first as in `deploy.yml`; then core
  nodes one by one, drained and with the Patroni leader handed to the synchronous replica before
  the reboot; then the probe. Each node must be healthy again (gateway readiness behind Caddy,
  Patroni `/health`, the control plane's composite check) before its drain flag is removed and
  the next node starts. Any failure stops the run with the other nodes untouched.
  To reboot one node, add `--limit <node>` and `-e reboot_all=true` when the node holds no
  `/var/run/reboot-required`. Its leader handover needs no other host in the limit.
- **Check:** `node_reboot_required` is 0 on every node in Prometheus, and
  `uname -r` on each node shows the new kernel.

### Exercised

| Date | What ran | Result |
|---|---|---|
| 2026-11-14 | Production: `reboot.yml` on all three converged nodes, release `v0.2.1`, under 4 page views a second for 40 minutes | `failed=0` on every node in 14 min 47 s; three planned switchovers, each within 7 s; each node out of rotation 4 min 12 s to 5 min 16 s and back 1 min 23 s to 2 min 27 s after its shutdown; 9,305 of 9,352 events acknowledged and **0 acknowledged events lost**, 46 refused with 503 by drained nodes still holding the client's keep-alive connection at their shutdown. Node 3 missed the new kernel (fixed above with the install step), and the prober needed a restart ([drill record](../reference/drills/2026-11-13-azure.md), "Rolling reboot under event load") |
| 2026-11-14 | Production: `reboot.yml --limit production-node-2 -e reboot_all=true`, release `v0.2.2` | `failed=0` in 5 min 20 s; node 2 shut down at 23:22:19, booted at 23:23:26, and the run ended at 23:24:53 with the drain flag removed; red on the status page were node 2's four by-address checks from 23:22:56 to 23:24:47 and one `hosts_tracker-file` result at 23:23:12 (Gatus's kept-alive connection). The prober closed its connections to node 2 on its own and its checks stayed green, with no restart ([drill record](../reference/drills/2026-11-13-azure.md), "Release v0.2.2 rollout and prober recovery") |

## 7. Rehearsal record

See section 8 of [supply-chain.md](../security/supply-chain.md) for scan results.

**2026-11-13, Umami 3.4.1 to 3.5.0, on a local development stack.** No Umami release newer than 3.5.0 existed, so the
rehearsal ran on a throwaway copy of the repository pinned to 3.4.1 (index digest
`sha256:e69d1a79...022e8`, the last release before 3.5.0), with a dev stack whose database 3.4.1 had
migrated.

| Time (UTC) | Step | Result |
|---|---|---|
| 01:42:32 | detection: `advisories.py` reports `umami-release-3.5.0` | T0 |
| 01:42:38 | `make umami-upgrade VERSION=3.5.0`: digest `sha256:b64f4040...a5dbc` resolved, pin moved in 4 files | |
| 01:42:54 | dev stack on 3.5.0 (database migrated from 3.4.1), 16 s; bootstrap 1 s | ok |
| 01:43:34 | umami-compat, 39 s | ok |
| 01:45:48 | isolation gate, 134 s | ok |
| 01:50:33 | console-e2e, 285 s | ok |
| 03:01:48 | webpath run 1, 71 min | red: Chromium renderer crash on one page of the second visit; the other 17 subtests passed |
| 04:00:11 | webpath run 2, 58 min | red: 5 of 80 custom-event browser runs missed, during host load averages of 300 to 1,100; the other 17 subtests passed |
| 05:00:11 | webpath run 3, 58 min, host quiet | ok, all subtests |

Detection to every gate green: **3 h 18 min**, inside the 8 hour target, including two webpath
reruns. Webpath alone takes about an hour because one check waits out a 31 minute visit gap.

The rollout step of this path, a rolling deploy of a signed release, ran on production on
2026-11-14 with release `v0.2.1`: `failed=0` on all three nodes, with the planned Patroni
switchovers ([drill record](../reference/drills/2026-11-13-azure.md), "Release v0.2.1 rollout and
gateway capacity").
