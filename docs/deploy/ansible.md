---
title: "Ansible: roles, playbooks and images"
sidebarTitle: "Ansible"
description: "The Ansible roles, playbooks and images that configure every node."
---

Ansible configures every node of an environment: the operating system, the firewall, the
secrets, the platform services and monitoring. This page lists the pinned versions, the images,
the roles and playbooks, the firewall and the transport security of the private network, and
ends with the procedure for a converge.

## 1. Pins (checked 2026-11-12)

| Item | Version | Check |
|---|---|---|
| Ansible | 14.6.0 | resolves `ansible-core` 2.21.6; ships community.docker 5.4.0, community.sops 2.5.0, ansible.posix 2.2.2, community.general 13.5.1 |
| ansible-lint, yamllint | 26.11.0, 1.38.0 | `infra/ansible/requirements.txt` |
| Docker Engine, Compose | `5:29.9.3-1~debian.13~trixie`, `5.6.1-1~debian.13~trixie` | Docker's trixie apt repo; key fingerprint 9DC8 5822 9FC7 DD38 854A E2D8 8D81 803C 0EBF CD88 is checked |
| Patroni, pgBackRest | `4.1.5-2.pgdg13+1`, `2.60.0-1.pgdg13+1` | PGDG trixie, installed into `postgres:18.7-trixie` without moving `postgresql-18` (the build checks it) |
| SOPS, age | 3.13.3, 1.3.2 | SHA-256 against the release checksums file (SOPS) and GitHub's asset digest (age) |
| Caddy | 2.11.9 with `caddy-dns/cloudflare` v0.2.4 | `caddy.Dockerfile` compiles against 2.11.9 and `caddy validate` accepts `dns cloudflare` for every assembled node config (2026-11-13); production holds Let's Encrypt certificates for `in`, `otlp` and `app` on every node ([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md)) |

Build notes:

- Patroni's Debian package satisfies its store dependency with Consul when installed
  without recommends, which leaves only `consul` and `kubernetes` usable. The image
  installs `python3-etcd` explicitly and imports `patroni.dcs.etcd3` at build time.
- Node 26 no longer ships corepack; the console image installs pnpm 12.18.0 with npm.
- `go-service.Dockerfile` copies `db/` (platformctl embeds the migrations), and
  `controlplane.Dockerfile` copies `docs/reference/openapi` (the control plane embeds the metrics
  API contract) and the tracker releases (`TRACKER_RELEASE_DIR=/releases`).

## 2. Images

| Image | Dockerfile | Note |
|---|---|---|
| `postgres-ha` | `deploy/images/postgres-ha.Dockerfile` | `PG_BASE` is the one place the PostgreSQL version is chosen; tag is `<pg version>-<release>` |
| `caddy` | `caddy.Dockerfile` | edge nodes |
| `console` | `console.Dockerfile` | `caddy` plus the console build at `/srv/console`; core nodes |
| `gateway`, `collector`, `controlplane`, `replayer`, `metricswriter`, `jobs`, `platformctl`, `prober` | `go-service.Dockerfile`, `controlplane.Dockerfile`, `collector.Dockerfile` | |

Names: `{{ sa_image_registry }}/<name>:{{ sa_image_tag }}`. Upstream images (NATS, etcd,
HAProxy, Umami) are pinned by digest in `infra/ansible/vars/pins.yml`.

**PostgreSQL 18.7.** `postgres_version: "18.7"` in `pins.yml`, `PG_BASE`
`postgres:18.7-trixie@sha256:2ea8630df176ad090ec92136dee850397c5af8781e4bcf2e4162593691c61bbc`
(index digest, resolved 2026-11-13) and `PG_VERSION=18.7` in the Dockerfile. The image built on
2026-11-13 reports `postgres (PostgreSQL) 18.7 (Debian 18.7-1.pgdg13+2)`, `patroni 4.1.5` and
`pgBackRest 2.60.0`.

## 3. Roles

**Converged nodes (tier `node`).** Production runs three nodes that are each in the `edge` and the
`core` group ([../reference/availability.md](../reference/availability.md)).
Both roles run on such a node with their own Compose projects; the edge project then has no
Caddy, and the core project's Caddy (the console image) serves `in.`, `otlp.` and `app.`
from one config (`assemble.sh node`), joins the edge project's network to reach the gateway
and the collector, and answers port 80 with the edge health at `/` and the core health at
`/core`. Every node runs a Patroni member (three members), an etcd member and a NATS server, and
the observability project on it carries both tiers' exporters and relay targets. Roles keep
accepting single-tier `edge` and `core` nodes, which the local converge's default topology uses;
`LC_TOPOLOGY=node` runs the production layout locally ([local-converge.md](local-converge.md)).

- **base**: checks the inventory contract; unattended upgrades from Debian security only,
  no automatic reboot; sshd drop-in (key-only, `AllowUsers ops`, no root, no forwarding);
  host firewall (section 4); chrony with an explicit check that the node is synchronised
  within `base_time_max_offset_seconds` (1 s, so any two nodes agree within 2 s, well
  inside the 15 s that lock fencing assumes); Docker Engine and Compose at their pins,
  held, with a `local` log driver and `live-restore`.
- **secrets**: SOPS and age on the controller, decryption on the controller, root-only
  env files on the nodes ([secrets.md](secrets.md)).
- **edge**: Compose project `/opt/veristar/edge` with Caddy, gateway, collector,
  NATS and etcd. NATS: one cluster `sa-<env>` with authenticated routes over the private
  network and the authorization block rendered by `platformctl nats auth-config` from
  `internal/natsx` (so the deployed permissions are the tested ones). etcd: one member
  per edge node, peers and clients on the private network.
- **core**: Compose project `/opt/veristar/core` with postgres-ha (Patroni, one
  member per core-group node, three on the converged nodes of production; DCS etcd3 on the edge
  nodes), HAProxy routing port 5000
  to the member whose Patroni REST answers `/primary` with 200, Umami, control plane, replayer, metricswriter, jobs and Caddy with the console.
- **storage** (on the controller): reads the Azure backup storage account back through the
  az CLI (ZRS, no account keys, versioning, immutability, lifecycle, container) and stops when
  a protection is missing ([backups.md](backups.md)). In the local converge's `s3` mode it
  creates the stand-in's bucket instead.
- **base** on the probe: the same role without a private network (no private interface,
  no private firewall rules), so the probe gets the firewall, SSH hardening, time sync and
  Docker at the pins.
- Caddy configs are assembled by `deploy/caddy/assemble.sh` from the tested per-host
  Caddyfiles: TLS by `SA_TLS` (`cloudflare` for DNS-01, `internal` only locally), automatic
  HTTP redirects off, and port 80 answering the node's composite health.

## 4. Firewall and exposure

Table `inet sa_host`, loaded by its own unit `sa-firewall.service` (Debian's
`nftables.service` flushes the whole ruleset on stop, which would remove Docker's rules).
Input: loopback, established, the private network from its CIDR, ICMP, TCP 80 and 443
and UDP 443 on the public interface, TCP 22 from `admin_cidrs`, and this node's
containers reaching ports it publishes on itself. Forward (before Docker's chains):
from the public interface only established traffic and DNAT to 80 and 443. Private
services also bind only the private address, so they never listen publicly at all.

### Client addresses over IPv6

Caddy is the only container that sees the visitor's address, and it hands that address to
the gateway (`X-Edge-Client-Ip`: visitor token, country, per-prefix limits) and to the
control plane (`X-Sa-Client-Addr`: sign-in limits). Docker forwards a published IPv6 port
into a container with no IPv6 address through `docker-proxy`, so Caddy then sees the
bridge gateway, one IPv4 address, as the peer of every IPv6 visitor (with a plain layout on
Docker 29.3.1, an IPv6 client is seen as `172.31.0.1`).
Both compose files therefore attach Caddy, and only Caddy, to a second network `public`
that is IPv6-only (`enable_ipv4: false`, a unique local /64 from `edge_public6_subnet`
and `core_public6_subnet`). Docker picks the gateway endpoint per address family, so IPv6
is DNATed straight to Caddy's `public` address and keeps the client address, while IPv4
still arrives through the default network. The other services keep IPv4-only names, so
nothing else changes (pg_hba, NATS and HAProxy addresses). This needs Docker 28 or newer
and `ip6tables` (set in `daemon.json` by the base role). The forward chain of `sa_host`
is an `inet` table, so its DNAT rule already admits IPv6. `TestCaddySeesIPv6ClientAddress`
in `deploy/caddy` reproduces both layouts.

## 5. Playbooks

- `site.yml`: `verify-release.yml` (image signatures, [../security/supply-chain.md](../security/supply-chain.md) section 3), base and secrets
  everywhere, the backup bucket (storage role), NATS and etcd (a rebuilt edge node rejoins
  etcd, `roles/edge/tasks/etcd.yml`), postgres-ha and HAProxy, WAL archiving
  (`roles/core/tasks/archive.yml`), `bootstrap.yml`, all edge and core services, the
  observability role, then a probe node if the inventory has one. A second run reports no change.
- `verify-release.yml`: imported first by `site.yml`, `deploy.yml`, `rotate-all.yml` and
  `restore.yml`; verifies the release manifest and pins every node to the verified digests.
- `reboot.yml`: rolling host reboot for kernel and libc fixes
  ([../runbooks/security-patch.md](../runbooks/security-patch.md) section 6a).
- `bootstrap.yml` (core[0]): checks `sa_operator_github_ids`, waits for a writable leader, creates the Umami
  role and database and the platform database, `platformctl db migrate`, `db roles`,
  starts Umami (which migrates itself), `umami bootstrap`, `db umami-retention-role`,
  `nats apply`. Each step reports a change only when it made one.
- `deploy.yml`: stops the jobs daemon on every core node, so none of the running release writes
  after the migrations; `bootstrap.yml` (migrations and the NATS topology), core nodes one at a
  time (each starts jobs again with the new image), edge nodes one at a time, then the observability role on every node including the
  probe. The core tier goes first so that the jobs process already reads every counter the new
  gateways send ([upgrade.md](upgrade.md)). Each node must answer 308 on port 80 (the composite health) before the next
  starts; any failure stops the run.

Composite health, served on port 80 by Caddy and behind it on the services' ops ports:

- edge: gateway `/edgez` = gateway readiness, collector health check 200, local NATS
  `/healthz` 200 (fails when its streams are not current, for example without quorum).
  Without `EDGE_HEALTH_COLLECTOR_URL` and `EDGE_HEALTH_NATS_URL` it answers 503.
- core: control plane `/corez` = readiness (database pools, NATS), the database behind
  HAProxy is not in recovery, Umami heartbeat.

Healthy answers 308 to https, unhealthy 503, whatever the Host header; Traffic Manager
counts 200 to 299 and 308 as healthy (`infra/tofu/modules/failover`).

On Azure the public address is NAT-mapped onto the VM's one interface, so Caddy publishes 80
and 443 on the private address (`sa_public_bind_ipv4` in the environment's
`group_vars/all.yml`), and the host firewall's forward chain lets the private network reach
the ports published there, since it arrives on the same interface.

## 6. Transport security on the private network

`playbooks/internal-ca.yml` (`make internal-ca ENV=<env>`, before the first converge and
after any inventory change) keeps one CA per environment in `infra/secrets/<env>/ca.enc.yaml`,
read on the controller only, and one key and certificate per node in `tls.enc.yaml`, which the
`secrets` role installs under `/etc/veristar/tls/<container user>/` (each directory
`0500`, the key `0400`, owned by the user of the container that reads it). Rotation:
[../runbooks/secret-rotation.md](../runbooks/secret-rotation.md), "Internal CA and node
certificates".

| Link | Transport | Client authentication |
|---|---|---|
| Services to NATS (gateway, collector, core services, platformctl) | TLS 1.3 from the first byte (`tls.handshake_first`, `nats.TLSHandshakeFirst`), server certificate checked against the dialled name or address | NATS user and password (unchanged) |
| NATS routes | TLS 1.3, `verify: true` | node certificate, then the route password |
| etcd clients (Patroni, Ansible) and peers | `https`, `--client-cert-auth`, `--peer-client-cert-auth` | node certificate |
| Services and Umami to PostgreSQL, through HAProxy | TLS end to end, `sslmode=verify-full` against `haproxy`; `pg_hba` has `hostssl` lines only | SCRAM password (unchanged) |
| Replication, `pg_rewind`, `postgres_exporter` to PostgreSQL | `sslmode=verify-full` against the leader's private address | SCRAM password |
| HAProxy checks, Prometheus, Patroni members, Ansible to the Patroni REST API | `https`, `verify_client: required` | node certificate |
| pgBackRest TLS server (posix-host mode) | TLS, `tls-server-auth` by common name | node certificate |
| Prometheus to node_exporter, postgres_exporter, Alertmanager, etcd, Patroni and the metrics relay | `https`, exporter-toolkit `RequireAndVerifyClientCert`, HAProxy `verify required` | node certificate |
| Alertmanager gossip | `--cluster.tls-config` (experimental in 0.34.2): TCP with mutual TLS, no UDP | node certificate |
| NATS monitoring | plain HTTP on the node's loopback; to the private network only through the relay's TLS listener on port 8222 | node or operators' certificate |
| Caddy to its upstreams | stays inside each node's Compose network; no cross-node hop exists | n/a |

Services take the files from configuration (`NATS_TLS_CA`, `NATS_TLS_CERT`, `NATS_TLS_KEY`,
`PLATFORM_DB_SSLROOTCERT`, `PLATFORM_DB_SSLCERT`, `PLATFORM_DB_SSLKEY`, `internal/config/tls.go`)
and refuse to start with a CA and `PLATFORM_DB_SSLMODE` other than `verify-full`; with a CA the
NATS client refuses a server without TLS. There is no fallback to plain text anywhere.

Each container that reads a certificate carries the label `sa.tls` (hash of the node certificate
and the CA bundle), so a reissue recreates exactly those containers at the next converge.

**New clusters only.** The roles do not migrate a running etcd from `http` to `https` peer URLs
(etcd keeps the advertised peer URLs of an existing member), so every environment is converged
with TLS from the start.

## 7. Procedure, step by step

Staging is shown. Needs [prerequisites.md](prerequisites.md), the SOPS files of
[secrets.md](secrets.md) section 5, the inventory from [opentofu.md](opentofu.md#procedure),
`cosign` on `PATH` and your age identity in `~/.config/sops/age/keys.txt` (or
`SOPS_AGE_KEY_FILE`). Steps 1 to 3 are the first converge; [bootstrap.md](bootstrap.md) walks
through it with the checks around it.

1. **Environment variables that are not secret** go in
   `infra/ansible/inventories/staging/group_vars/all.yml`, beside the generated `hosts.json`:

   ```yaml
   # images of the release workflow of your repository
   sa_image_registry: ghcr.io/<owner>/veristar
   # numeric github ids of the scf operators, comma separated
   sa_operator_github_ids: "<operator github ids>"
   ```

2. **Converge** at a release tag (`v<release>`, whose images `release-images.yml` built and
   signed):

   ```sh
   make staging-deploy SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   ```

   This runs `site.yml`: base and secrets on every node, NATS and etcd, postgres-ha and HAProxy,
   the backup settings, `bootstrap.yml`, the edge and core services, then the observability role.
   Expected: a `PLAY RECAP` with `failed=0` and `unreachable=0` for every node. The guards
   print `no inventory: run make staging-apply first` and
   `set SA_IMAGE_TAG to the release to deploy` when their inputs are missing.

   `site.yml` first verifies every platform image's signature against the release manifest
   (`playbooks/verify-release.yml`, so `SA_RELEASE_MANIFEST=<release-images.json>` is required)
   and the nodes pull the verified digests, exactly as the rolling deploy does
   ([../security/supply-chain.md](../security/supply-chain.md) section 3).

3. **Run it again.** Expected: `failed=0` on every node. "Start the edge services" reports a
   change on every run, because the one-shot `volume-owners` container runs at every
   `compose up`; the production record lists the other changes a second run can show
   ([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md)).

4. **Later rollouts** use the rolling deploy with signature verification
   ([upgrade.md](upgrade.md)):

   ```sh
   make staging-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   ```

   Without the manifest it stops and asks for `SA_RELEASE_MANIFEST`.

5. **Lint** after changing a role: `make ansible-lint`. Expected: the production profile passes
   with 0 failures.
