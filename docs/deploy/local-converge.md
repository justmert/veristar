---
title: "Local converge: the Ansible roles against real Debian 13 systems"
sidebarTitle: "Local converge"
description: "Run the same Ansible roles against Debian 13 containers on one machine."
---

The local converge runs the production Ansible playbooks against Debian 13 systemd containers on
one machine. Use it to test a change to a role, a playbook or an image before it reaches a cloud
environment: it needs no cloud account, no domain and no real credentials.

## 1. Requirements

A Linux host with Docker Engine and Compose ([prerequisites.md](prerequisites.md), "Controller
tools") and the Ansible venv from `make ansible-venv`. No virtualization is needed: the nodes
are containers, so the local converge also runs on a host without `/dev/kvm` or nested
virtualization.

## 2. Running it

```sh
make ansible-venv                      # uv venv with requirements.txt, then sops and age
make local-converge-up                 # networks, registry, nodes, secrets, inventory, internal CA
make local-converge-images             # build and push every platform image
make local-converge                    # site.yml; run it twice to check idempotency
make local-converge-e2e                # web and API paths through the converged stack
make local-converge-down               # removes containers, volumes, networks, images, state
```

`make local-converge-up` also issues the internal CA and node certificates
(`make internal-ca ENV=local`, [ansible.md](ansible.md) section 6).

`site.yml` ends with the `observability` play on every edge and core node. To see every message
Alertmanager would push, run the test receiver on the controller and pass its address:

```sh
go run ./tests/observability/receiver -listen 172.31.99.1:8099 &
make local-converge ANSIBLE_EXTRA='-e observability_test_webhook_base=http://172.31.99.1:8099'
```

The play accepts `observability_test_webhook_base` in the local environment only.

Everything generated stays under `.scratch/local-converge/` (SSH key, known hosts, age identity,
logs), `infra/secrets/local/` and `infra/ansible/inventories/local/hosts.json`, all gitignored.
The local secrets are random values from `infra/secrets/new-secrets.py`, encrypted with SOPS to
a throwaway age identity, and `local-converge-down` deletes them.

## 3. Topologies and switches

Set these variables before `make local-converge-up` (read by `infra/local/env.sh`, `up.sh` and
`down.sh`):

| Setting | Nodes |
|---|---|
| default | 3 edge nodes, 2 core nodes, 1 backup node |
| `LC_TOPOLOGY=node` | the production layout: 3 converged nodes `sa-lc-node-1` to `-3`, each in the `edge` and the `core` group, plus the backup node |
| `LC_TOPOLOGY=minimal` | 3 edge nodes, 1 core node, 1 backup node: the smallest set the roles converge, used by `make rotate-local` |
| `LC_BACKUP=s3` | replaces the backup node with `sa-lc-s3`, an S3 object storage stand-in (SeaweedFS 4.52, TLS with a private CA, host-style addressing under `s3.sa-local.test`) |
| `LC_PROBE=1` | adds `sa-lc-probe-1` on the public network only, as a probe host outside the platform's network; `site.yml`'s probe play converges it |

| Node | Location | Private address | Runs |
|---|---|---|---|
| `sa-lc-edge-1` | fsn1 | 10.99.1.12 | caddy, gateway, collector, nats, etcd |
| `sa-lc-edge-2` | nbg1 | 10.99.2.13 | same |
| `sa-lc-edge-3` | hel1 | 10.99.3.14 | same |
| `sa-lc-core-1` | fsn1 | 10.99.1.22 | patroni member, haproxy, umami, control plane, replayer, metricswriter, jobs, pgbackrest server, caddy |
| `sa-lc-core-2` | nbg1 | 10.99.2.23 | same |
| `sa-lc-backup-1` | hel1 | 10.99.3.31 | pgbackrest repository (posix) |
| `sa-lc-restore-1` | fsn1 | 10.99.1.41 | created only by the restore drill, removed after it |

In the `node` topology, `sa-lc-node-1` to `-3` take the edge nodes' addresses and run both
columns of services.

`make ops-local` (`tests/localconverge/ops-local.sh`) runs the operations checks on
`LC_TOPOLOGY=minimal LC_BACKUP=s3 LC_PROBE=1`: backup bucket, stanza and WAL archiving in `s3`
mode, a full backup, delete refusal and `--repo-target-time`, idempotency, Patroni failsafe with
etcd stopped, the etcd rejoin of a rebuilt edge node, the probe and the watchdog push, then tears
down. It refuses to start at a load average of 20 or more and waits before each
step until the load is below 20 (ten minutes at most). `LC_SRC` builds the images from another
checkout (for example `git archive HEAD` unpacked elsewhere) when the working tree is mid-change.

## 4. How it stands in for a cloud environment

Each node is `infra/local/node.Dockerfile`: `debian:trixie` (pinned by digest) with systemd as
PID 1, OpenSSH, sudo and Python, plus an `ops` user whose key is installed at start, which is
what cloud-init does on an Azure Debian 13 VM ([opentofu.md](opentofu.md#inventory-contract-opentofu-to-ansible)).
Nothing the roles configure is preinstalled: Docker Engine and Compose come from Docker's apt
repository at their pins, chrony, nftables and unattended-upgrades from Debian. Ansible connects
over SSH, so SSH hardening, `AllowUsers` and the firewall's SSH rule all run as they do on a VM.

- **Privileges.** The node containers run with `--privileged`, which gives them the
  capabilities Docker, nftables and systemd need. `/var/lib/docker` and `/var/lib/containerd`
  are volumes per node, because overlayfs cannot be stacked on the container's own overlay root.
- **Networks.** `sa-lc-public` (172.31.99.0/24, default route) stands in for the internet side.
  The private network 10.99.0.0/16 is split into one Docker network per location (`sa-lc-fsn1`,
  `sa-lc-nbg1`, `sa-lc-hel1`), joined by a router container (`sa-lc-router`,
  `infra/local/lib.sh`). Each node gets the route `10.99.0.0/16 via 10.99.<loc>.254` at every
  start (`node-init.sh`), as a cloud private network hands it out. The location names stand for
  three separate zones.
- **Latency.** `tc netem` on the router delays what it sends into each location: 1.5 ms toward
  fsn1 and nbg1, 11.5 ms toward hel1, 0.3 ms jitter. Round trips are about 3 ms between fsn1 and
  nbg1 and 23 ms to hel1.
- **Partition.** `lc_partition <location>` takes the router's interface into that location down,
  cutting it off the private network in both directions while its public addresses stay
  reachable; `lc_heal` restores it.
- **Registry.** A `registry:3.1.2` container (pinned by digest) on the public network plays the
  image registry. `infra/local/build-images.sh` builds every image from `deploy/images` and
  pushes it there; the nodes pull from it. The local images carry the mutable tag `lc`, and a
  rebuild under the same tag is not pulled again (`pull: missing`); releases use immutable tags.
- **Inventory.** `up.sh` writes an inventory with exactly the shape of the OpenTofu output, with
  `sa_environment: local`, `sa_provider: local` and the base domain `sa-local.test` (the reserved
  `.test` TLD). The controller's address (172.31.99.1) is the only admin CIDR. Local-only
  settings are in `infra/ansible/inventories/local/group_vars/all.yml`, each with its reason.
- **TLS.** Caddy uses its local CA (`sa_tls: internal`). `deploy/caddy/assemble_test.go`
  validates the assembled node configs with `SA_TLS` set to `cloudflare`,
  `cloudflare-delegated` and `internal` on the custom Caddy build.
- **Sign-in.** The control plane signs in against the test-only OAuth fixture
  (`tests/fixtures/githuboauth`), through an image built with `TAGS=oauthfixture` that exists
  only in the local registry. The fixture listens on port 8099 of the core node, which the local
  inventory opens; the core role refuses `core_oauth_fixture` in any other environment. The local
  operator is fixture user 1001.
- **Backups.** The default repository is a posix repository on `sa-lc-backup-1`, in a location
  with no database node, reached over pgBackRest's TLS protocol with the node certificates of the
  internal CA. `LC_BACKUP=s3` exercises the object store protocol instead. Cloud environments use
  `backup_repo_mode: azure` (the default), which needs the Azure instance metadata service for
  the managed identity ([backups.md](backups.md)).
- **Clock.** The nodes share the host's kernel clock and cannot set it, so chronyd runs with
  `-x` (measure, do not steer). The offset check runs for real.
- **Power off** in the drills is `docker kill` of a node container, with no clean shutdown of
  anything inside. Power on is `docker start`, which boots systemd and Docker again; compose
  restarts the services (`restart: unless-stopped`).

The cloud parts that only a cloud environment exercises (network security groups, Azure's NAT of
the public address, Traffic Manager, Let's Encrypt issuance by DNS-01, GitHub sign-in) are covered
by the production record ([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md)).

## 5. Converge records

**Default topology, 2026-11-12 and 2026-11-13.** `site.yml` converged the nodes from fresh
containers, and the next run reported `changed=0` on every node:

```
run 1: sa-lc-core-1 ok=91 changed=40 failed=0 | sa-lc-edge-1 ok=74 changed=32 | edge-2, edge-3 ok=67 changed=32
run 2: sa-lc-core-1 ok=82 changed=0  failed=0 | sa-lc-edge-1 ok=69 changed=0  | edge-2, edge-3 ok=62 changed=0
--- PASS: TestConvergedStack (5.31s)
    --- PASS: TestConvergedStack/web_path_through_every_edge_node_into_umami (3.15s)
    --- PASS: TestConvergedStack/otlp_metrics_through_every_edge_node_into_the_platform_database (2.11s)
```

The first converge took 177 s and the second 47 s, with images already in the local registry.
The checks around it:

- Every service ran on every node. NATS had three members, one meta leader, and the 10 streams
  and 6 consumers of `platformctl nats apply` at R3; etcd had three members at 3.6.16.
- Port 80 on every node answered 308 for any Host header. Stopping the collector on edge 1
  turned edge 1 to 503 and back to 308 once it was restarted; stopping NATS on edges 2 and 3
  turned edge 1 to 503 (`stream ... is not current`); stopping Umami turned the core node to 503.
- From a public address that is not an admin CIDR, 80 and 443 were open on every node and 22,
  4222, 6222, 8222, 2379, 9090, 5432, 8008, 3000, 5000 and 8085 closed.
- Every node was within 5 ms of NTP (`chronyc tracking`).
- `deploy.yml` with a new image tag recreated the services one node at a time (edge 1, 2, 3,
  then core 1), each gated on its port-80 answer.
- `tests/localconverge` (build tag `localconverge`) signed in as an operator, created a tenant
  and a site through the console API, sent one pageview through each edge node's Caddy and
  gateway into Umami, created an OTLP token and sent 5 requests through each edge node's
  collector into `api.metrics_minute`.

Image pulls run as a separate task retried five times, because a registry can answer a single
pull of a pinned digest with `not found`; every other compose failure fails the node.

**Production layout (`LC_TOPOLOGY=node`), 2026-11-13.** With release `v0.1.0`'s code and
`ANSIBLE_EXTRA="-e core_umami_processes=1"`, the first `make local-converge` ended with
`failed=0` on every node (`ok=238 changed=51` on node 1, `ok=190 changed=44` on nodes 2 and 3);
`patronictl list` showed node 1 `Leader`, node 2 `Sync Standby` and node 3 `Replica`, both
standbys `streaming` with lag 0; port 80 of every node answered 308 at `/` and at `/core`. The
second run ended with `failed=0` and `changed=2` per node, both in "Start the edge services":
the one-shot `volume-owners` container runs at every `compose up`. Idle memory after the first
run (`docker stats`): 1,012 MiB on node 1 and 941 MiB on node 2, 21 containers each.

**Private network transport.** `tests/localconverge/tls-audit.sh` captures the private network
with tcpdump sidecars while the end-to-end suite runs, checks that no flow carries readable
protocol data or any secret of the environment, and tries every private endpoint from a host
without a certificate. It ran on `LC_TOPOLOGY=minimal` with `-e core_umami_processes=1`.
