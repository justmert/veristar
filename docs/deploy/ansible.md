# Ansible: roles, playbooks and images

Status: first version (this stage, stream b). Verified on the local converge
(docs/deploy/local-converge.md); not yet run against staging, which is blocked on the
inputs listed in section 6.

## 1. Pins (checked 2026-11-12)

| Item | Version | Check |
|---|---|---|
| Ansible | 14.6.0 | resolves `ansible-core` 2.21.6; ships community.docker 5.4.0, community.sops 2.5.0, ansible.posix 2.2.2, community.general 13.5.1 |
| ansible-lint, yamllint | 26.11.0, 1.38.0 | `infra/ansible/requirements.txt` |
| Docker Engine, Compose | `5:29.9.3-1~debian.13~trixie`, `5.6.1-1~debian.13~trixie` | Docker's trixie apt repo; key fingerprint 9DC8 5822 9FC7 DD38 854A E2D8 8D81 803C 0EBF CD88 is checked |
| Patroni, pgBackRest | `4.1.5-2.pgdg13+1`, `2.60.0-1.pgdg13+1` | PGDG trixie, installed into `postgres:18.6-trixie` without moving `postgresql-18` (the build checks it) |
| SOPS, age | 3.13.3, 1.3.2 | SHA-256 against the release checksums file (SOPS) and GitHub's asset digest (age) |
| Caddy | 2.11.9 with `caddy-dns/bunny` v1.2.0 | the existing `caddy.Dockerfile` compiles against 2.11.9 and `caddy validate` accepts `dns bunny`; no certificate issued yet |

Findings that changed the build:

- Patroni's Debian package satisfies its store dependency with Consul when installed
  without recommends, which leaves only `consul` and `kubernetes` usable. The image
  installs `python3-etcd` explicitly and imports `patroni.dcs.etcd3` at build time.
- Node 26 no longer ships corepack; the console image installs pnpm 12.18.0 with npm.
- `go-service.Dockerfile` now copies `db/` (platformctl embeds the migrations) and
  `controlplane.Dockerfile` copies `docs/reference/openapi` (this stage embeds it) and the
  tracker releases (`TRACKER_RELEASE_DIR=/releases`).

## 2. Images

| Image | Dockerfile | Note |
|---|---|---|
| `postgres-ha` | `deploy/images/postgres-ha.Dockerfile` | `PG_BASE` is the one place the PostgreSQL version is chosen; tag is `<pg version>-<release>` |
| `caddy` | `caddy.Dockerfile` (unchanged) | edge nodes |
| `console` | `console.Dockerfile` | `caddy` plus the console build at `/srv/console`; core nodes |
| `gateway`, `collector`, `controlplane`, `replayer`, `metricswriter`, `jobs`, `platformctl` | existing | |

Names: `{{ sa_image_registry }}/<name>:{{ sa_image_tag }}`. Upstream images (NATS, etcd,
HAProxy, Umami) are pinned by digest in `infra/ansible/vars/pins.yml`.

**PostgreSQL 18.7 gap.** `postgres_version: "18.6"` and `postgres_min_version: "18.7"`
in `pins.yml`. The core role refuses any environment other than the local converge
while the version is below the minimum. When `postgres:18.7-trixie` exists: set
`PG_BASE` and `PG_VERSION` in the Dockerfile, resolve the digest, raise
`postgres_version`.

## 3. Roles

- **base**: checks the inventory contract; unattended upgrades from Debian security only,
  no automatic reboot; sshd drop-in (key-only, `AllowUsers ops`, no root, no forwarding);
  host firewall (section 4); chrony with an explicit check that the node is synchronised
  within `base_time_max_offset_seconds` (1 s, so any two nodes agree within 2 s, well
  inside the 15 s that lock fencing assumes); Docker Engine and Compose at their pins,
  held, with a `local` log driver and `live-restore`.
- **secrets**: SOPS and age on the controller, decryption on the controller, root-only
  env files on the nodes (docs/deploy/secrets.md).
- **edge**: Compose project `/opt/stellar-analytics/edge` with Caddy, gateway, collector,
  NATS and etcd. NATS: one cluster `sa-<env>` with authenticated routes over the private
  network and the authorization block rendered by `platformctl nats auth-config` from
  `internal/natsx` (so the deployed permissions are the tested ones). etcd: one member
  per edge node, peers and clients on the private network.
- **core**: Compose project `/opt/stellar-analytics/core` with postgres-ha (Patroni, on
  `core[0]` only in this phase, DCS etcd3 on the edge nodes), HAProxy routing port 5000
  to the member whose Patroni REST answers `/primary` with 200, Umami (the design
  environment), control plane, replayer, metricswriter, jobs and Caddy with the console.
- **storage** (on the controller): the backup bucket through the S3 API with object lock,
  versioning and a 35-day compliance retention ([backups.md](backups.md)).
- **base** on the probe: the same role without a private network (no private interface,
  no private firewall rules), so the probe gets the firewall, SSH hardening, time sync and
  Docker at the pins.
- Caddy configs are assembled by `deploy/caddy/assemble.sh` from the tested per-host
  Caddyfiles: TLS by `SA_TLS` (`bunny` for DNS-01, `internal` only locally), automatic
  HTTP redirects off, and port 80 answering the node's composite health.

## 4. Firewall and exposure

Table `inet sa_host`, loaded by its own unit `sa-firewall.service` (Debian's
`nftables.service` flushes the whole ruleset on stop, which would remove Docker's rules).
Input: loopback, established, the private network from its CIDR, ICMP, TCP 80 and 443
and UDP 443 on the public interface, TCP 22 from the admin CIDRs, and this node's
containers reaching ports it publishes on itself. Forward (before Docker's chains):
from the public interface only established traffic and DNAT to 80 and 443. Private
services also bind only the private address, so they never listen publicly at all.

## 5. Playbooks

- `site.yml`: `verify-release.yml` (image signatures, section 8), base and secrets
  everywhere, the backup bucket (storage role), NATS and etcd (a rebuilt edge node rejoins
  etcd, `roles/edge/tasks/etcd.yml`), postgres-ha and HAProxy, WAL archiving
  (`roles/core/tasks/archive.yml`), `bootstrap.yml`, all edge and core services, the
  observability role, then the probe node. A second run reports no change.
- `verify-release.yml`: imported first by `site.yml`, `deploy.yml`, `rotate-all.yml` and
  `restore.yml`; verifies the release manifest and pins every node to the verified digests.
- `reboot.yml`: rolling host reboot for kernel and libc fixes
  ([../runbooks/security-patch.md](../runbooks/security-patch.md) section 6a).
- `bootstrap.yml` (core[0]): checks I3, waits for a writable leader, creates the Umami
  role and database and the platform database, `platformctl db migrate`, `db roles`,
  starts Umami (which migrates itself), `umami bootstrap`, `db umami-retention-role`,
  `nats apply`. Each step reports a change only when it made one.
- `deploy.yml`: edge nodes one at a time, the NATS topology, core nodes one at a
  time, then the observability role on every node including the probe. Each node must answer 308 on port 80 (the composite health) before the next
  starts; any failure stops the run.

Composite health, served on port 80 by Caddy and behind it on the services' ops ports:

- edge: gateway `/edgez` = gateway readiness, collector health check 200, local NATS
  `/healthz` 200 (fails when its streams are not current, for example without quorum).
  Without `EDGE_HEALTH_COLLECTOR_URL` and `EDGE_HEALTH_NATS_URL` it answers 503.
- core: control plane `/corez` = readiness (database pools, NATS), the database behind
  HAProxy is not in recovery, Umami heartbeat.

Healthy answers 308 to https, unhealthy 503, whatever the Host header; the bunny.net
monitor counts non-5xx as healthy.

## 6. Blocked on inputs

| What | Input |
|---|---|
| Any staging or production converge (no servers, no inventory) | I5 (via the OpenTofu stream) |
| Hostnames and certificates, `sa_tls: bunny` issuing by DNS-01 | I4, I6 |
| Control plane sign-in (`github_client_id`, `github_client_secret`) | I2 |
| `sa_operator_github_ids` | I3 |
| SSH rule of the firewall (`sa_admin_cidrs`) | I13 |
| `.sops.yaml` rules and every `secrets.enc.yaml` | I14 |
| PostgreSQL 18.7 image and packages | upstream release, the design |

## 7. Not in this stream

- `jobs retain-web` (this stage) must run once a day; no timer is deployed for it yet.
- The second core node, Patroni replication, pgBackRest repositories and the backup
  bucket (`storage` role) are this stage work.
- etcd and NATS run without TLS on the private network, and etcd without client
  authentication; both rely on the private network and the host firewall. this stage
  revisits this together with the HA drills.
- PostgreSQL connections from the services use `sslmode=disable` on the node's Compose
  network and the private network.

## 8. Procedure, step by step

Staging is shown. Needs [prerequisites.md](prerequisites.md), the SOPS files of
[secrets.md](secrets.md) section 5, the inventory from [opentofu.md](opentofu.md) section 6,
`cosign` on `PATH` and your age identity in `~/.config/sops/age/keys.txt` (or
`SOPS_AGE_KEY_FILE`). Steps 1 to 3 are the first converge; [bootstrap.md](bootstrap.md) walks
through it with the checks around it.

1. **Environment variables that are not secret** go in
   `infra/ansible/inventories/staging/group_vars/all.yml`, beside the generated `hosts.json`:

   ```yaml
   # images of the release workflow for repository <an operator input>
   sa_image_registry: ghcr.io/justmert/stellar-analytics
   # acme dns-01 through bunny.net an operator input; "internal" is refused outside the local converge
   sa_tls: bunny
   # numeric github ids of the scf operators an operator input, comma separated
   sa_operator_github_ids: "<an operator input>"
   ```

2. **Converge** at a release tag (`v<release>`, whose images `release-images.yml` built and
   signed):

   ```sh
   make staging-deploy SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   ```

   This runs `site.yml`: base and secrets on every node, NATS and etcd, postgres-ha and HAProxy,
   the backup settings, `bootstrap.yml`, the edge and core services, then the observability role.
   Expected: a `PLAY RECAP` with `failed=0` and `unreachable=0` for every node. For comparison,
   the local converge's first run reported `ok=91 changed=40 failed=0` on the core node and
   `changed=32` on each edge node ([local-converge.md](local-converge.md) section 5). The guards
   print `no inventory: run make staging-apply first` and
   `set SA_IMAGE_TAG to the release to deploy` when their inputs are missing.

   `site.yml` first verifies every platform image's signature against the release manifest
   (`playbooks/verify-release.yml`, so `SA_RELEASE_MANIFEST=<release-images.json>` is required)
   and the nodes pull the verified digests, exactly as the rolling deploy does
   ([../security/supply-chain.md](../security/supply-chain.md) section 3).

3. **Run it again.** Expected: `changed=0` on every node (the local converge's second run:
   `ok=82 changed=0 failed=0` on the core node).

4. **Later rollouts** use the rolling deploy with signature verification
   ([upgrade.md](upgrade.md)):

   ```sh
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   ```

   Without the manifest it stops with
   `ROLLING=1 needs SA_RELEASE_MANIFEST=<release-images.json of the release> (this stage signature check)`.

5. **Lint** after changing a role: `make ansible-lint` (passed on 2026-11-13 with the production
   profile, 0 failures).

Steps 2 and 3 ran only against the local converge (`make local-converge`, same playbook,
inventory `inventories/local/`); step 4's signature check ran in `make supplychain-it` with a
test-only key. Nothing has run against staging: inputs I4, I5, I6, I13, I14, I15, and the
PostgreSQL 18.7 gap in section 2.
