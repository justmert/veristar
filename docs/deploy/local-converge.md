# Local converge: the Ansible roles against real Debian 13 systems

Status: first version (this stage, stream b). The real servers, domain and credentials
(inputs I2 to I6, I13, I14) do not exist yet, so the roles are verified against a local
stand-in for the staging topology: 3 edge nodes and 1 core node. This page records the
choice, how to run it, and what it does and does not prove.

## 1. The choice

The machine that runs it is an unprivileged container with Docker 29.3.1 and no nested
virtualisation. Options considered:

| Option | Verdict |
|---|---|
| VMs (QEMU, Vagrant, Multipass) | Not available: no `/dev/kvm` inside the container. |
| Nested container or systemd-nspawn | Needs privileges the container does not grant. |
| Ansible `community.docker.docker` connection into plain containers | Skips SSH, so SSH hardening, `AllowUsers` and the firewall's SSH rule are never exercised. Rejected as less faithful. |
| **Debian 13 systemd containers, privileged inside the container, reached over SSH** | **Chosen.** |

Each node is `infra/local/node.Dockerfile`: `debian:trixie` (pinned by digest) with
systemd as PID 1, OpenSSH, sudo and Python, plus an `ops` user whose key is installed at
start, which is what cloud-init does on a Hetzner Debian 13 server
(docs/deploy/opentofu.md section 1). Nothing the roles configure is preinstalled:
Docker Engine and Compose come from Docker's apt repository at their pins, chrony,
nftables and unattended-upgrades from Debian.

- `--privileged` gives the node the capabilities Docker, nftables and systemd need.
  They stay inside the container's user namespace, so the node cannot change the host clock
  or anything outside the container.
- `/var/lib/docker` and `/var/lib/containerd` are volumes per node, because overlayfs
  cannot be stacked on the container's own overlay root (the first attempt failed with
  `mount ... overlay ... invalid argument`).
- Two Docker networks stand in for the cloud networks: `sa-lc-public`
  (172.31.99.0/24, default route, `gw-priority=1`) and `sa-lc-private` (10.99.0.0/16,
  addresses `.11+index` edge and `.21+index` core per location subnet, as in
  opentofu.md section 2).
- A `registry:3.0.0` container (pinned by digest) on the public network plays the
  repository host's registry. `infra/local/build-images.sh` builds every image from
  `deploy/images` and pushes it there; the nodes pull from it.
- Ansible connects as `ops` over SSH with a throwaway key. The controller's address
  (172.31.99.1) is the only admin CIDR.

## 2. Running it

```sh
make ansible-venv                      # uv venv with requirements.txt, then sops and age
make local-converge-up                 # networks, registry, 4 nodes, secrets, inventory
make local-converge-images             # build and push every platform image
make local-converge                    # site.yml; run it twice, the second run changes nothing
make local-converge-e2e                # this stage and 8 paths through the converged stack
make local-converge-down               # removes containers, volumes, networks, images, state
```

Since 2026-11-13 `site.yml` ends with the `observability` play (this stage) on every edge
and core node. It needs somewhere to send alerts: the Discord webhook an operator input does not
exist locally, so pass the address of the test receiver instead, and run that receiver
on the controller, where its log shows every Alertmanager message:

```sh
go run ./tests/observability/receiver -listen 172.31.99.1:8099 &
make local-converge ANSIBLE_EXTRA='-e observability_test_webhook_base=http://172.31.99.1:8099'
```

Without one of the two the play stops at its input check and names both.

Everything generated stays under `.scratch/scratch/local-converge/` (SSH key, known
hosts, age identity, logs), `infra/secrets/local/` and
`infra/ansible/inventories/local/hosts.json`, all gitignored. The local secrets are
random values from `infra/secrets/new-secrets.py`, encrypted with SOPS to a throwaway
age identity; they are local-only and are deleted by `local-converge-down`.

The inventory `up.sh` writes has exactly the shape of the OpenTofu output
(opentofu.md section 1) with `sa_environment: local`, `sa_provider: local` and the base
domain `sa-local.test` (the reserved `.test` TLD). Local-only settings are in
`infra/ansible/inventories/local/group_vars/all.yml`, each with its reason.

## 3. What was verified (2026-11-12)

- `ansible-lint` (production profile) and `yamllint -s` pass. `deploy/caddy/assemble_test.go`
  validates both assembled node configs with `SA_TLS` bunny and internal on the custom
  Caddy build and refuses an unset or unknown value.
- `site.yml` converged the four nodes from fresh containers, and the next run reported
  `changed=0` on every node (section 5 has the recap).
- Every service ran on every node: on each edge node Caddy, gateway, collector, NATS and
  etcd; on the core node postgres-ha (Patroni leader, PostgreSQL 18.6), HAProxy, Umami,
  control plane, replayer, metricswriter, jobs, Caddy with the console, and the oauth
  fixture.
- NATS: three members, one meta leader, the 10 streams and 6 consumers of
  `platformctl nats apply` at R3. etcd: three members at 3.6.16, one leader.
- Port 80 on every node answered 308 (healthy) for any Host header. Stopping the
  collector on edge 1 turned edge 1 to 503 and back to 308 once it was restarted.
  Stopping NATS on edges 2 and 3 turned edge 1 to 503 (its NATS reported
  `stream ... is not current`). Stopping Umami turned the core node to 503.
- From an address on the public network that is not an admin CIDR: 80 and 443 open on
  every node; 22, 4222, 6222, 8222, 2379, 9090, 5432, 8008, 3000, 5000 and 8085 closed.
- Clock check: every node within 5 ms of NTP (`chronyc tracking`).
- `deploy.yml` with a new image tag recreated the services one node at a time (edge 1, 2,
  3, then core 1), each gated on its port-80 answer before the next.
- `tests/localconverge` (build tag `localconverge`): sign-in through the core Caddy and
  the fixture as an operator, tenant and site created through the console API, one
  pageview through each edge node's Caddy and gateway, all three stored by Umami
  (`website_event` paths `/converge/sa-lc-edge-1..3`), an OTLP token created through the
  API and one delta histogram of 5 requests sent through each edge node's collector,
  15 requests from 3 services in `api.metrics_minute`.

## 4. Limits: what this does not prove

- **Not real servers, network or DNS.** No Hetzner firewall, no private network latency
  between locations, no bunny.net health checks. Hostnames are mapped by the test client.
- **No public TLS.** Caddy uses its local CA (`sa_tls: internal`); DNS-01 through
  `caddy-dns/bunny` is configured (`sa_tls: bunny`), compiles and validates, but has not
  issued a certificate: blocked on I4 and I6.
- **Clock.** The nodes share this machine's kernel clock and may not set it, so chronyd
  runs with `-x` (measure, do not steer). The offset check runs for real; clock steering
  does not.
- **No GitHub.** Sign-in goes to the test-only oauth fixture (`tests/fixtures/githuboauth`),
  through a control plane image built with `TAGS=oauthfixture` that exists only in the
  local registry. The fixture listens on port 8099 of the core node, which the local
  inventory opens; no other environment may set `core_oauth_fixture` (the core role
  refuses). Real sign-in is blocked on I2, operator ids on I3 (the local operator is
  fixture user 1001).
- **PostgreSQL 18.6.** The local inventory sets `postgres_allow_below_min`; staging and
  production are refused until 18.7 exists.
- **Two core nodes since this stage** (section 6); this stage ran one.
- **Scripted events, not browsers.** The web path check uses `tests/gateway/gwclient`,
  which solves the proof in Go. The browser matrices stay with the this stage to 5 suites,
  which start their own stacks and were not re-pointed at this one.
- **Mutable local tag.** The local images are tagged `lc`; a rebuild under the same tag
  is not pulled again (`pull: missing`). Real releases use immutable tags.

## 5. Converge record

Fresh nodes (images already in the local registry), 2026-11-13. First converge 177 s,
second 47 s, then `make local-converge-e2e`:

```
run 1: sa-lc-core-1 ok=91 changed=40 failed=0 | sa-lc-edge-1 ok=74 changed=32 | edge-2, edge-3 ok=67 changed=32
run 2: sa-lc-core-1 ok=82 changed=0  failed=0 | sa-lc-edge-1 ok=69 changed=0  | edge-2, edge-3 ok=62 changed=0
--- PASS: TestConvergedStack (5.31s)
    --- PASS: TestConvergedStack/web_path_through_every_edge_node_into_umami (3.15s)
    --- PASS: TestConvergedStack/otlp_metrics_through_every_edge_node_into_the_platform_database (2.11s)
```

An earlier fresh run failed once on edge 3 when quay.io answered one pull of the pinned
etcd digest with `not found` (the same digest pulled on the other nodes). Image pulls are
now a separate task retried five times; every other compose failure still fails the node.

## 6. this stage topology: locations, latency, backups

this stage (high availability and drills) extends the topology to the shape of plan
section 4.1 plus a backup node:

| Node | Location | Private | Runs |
|---|---|---|---|
| `sa-lc-edge-1` | fsn1 | 10.99.1.12 | caddy, gateway, collector, nats, etcd |
| `sa-lc-edge-2` | nbg1 | 10.99.2.13 | same |
| `sa-lc-edge-3` | hel1 | 10.99.3.14 | same |
| `sa-lc-core-1` | fsn1 | 10.99.1.22 | patroni member, haproxy, umami, control plane, replayer, metricswriter, jobs, pgbackrest server, caddy |
| `sa-lc-core-2` | nbg1 | 10.99.2.23 | same |
| `sa-lc-backup-1` | hel1 | 10.99.3.31 | pgbackrest repository (posix) |
| `sa-lc-restore-1` | fsn1 | 10.99.1.41 | created only by the restore drill, removed after it |

- **Locations are separate Docker networks** (`sa-lc-fsn1`, `sa-lc-nbg1`,
  `sa-lc-hel1`). A router container (`sa-lc-router`, `infra/local/lib.sh`) joins
  them; each node gets the route `10.99.0.0/16 via 10.99.<loc>.254` at every
  start (`node-init.sh`), as a cloud private network would hand it out. Docker
  drops packets routed between its bridges unless the source bridge is trusted, so
  each location network trusts the other two (`trusted_host_interfaces`).
- **Latency.** `tc netem` on the router delays what it sends into each location:
  1.5 ms toward fsn1 and nbg1, 11.5 ms toward hel1, 0.3 ms jitter. Round trips are
  therefore about 3 ms fsn1 to nbg1 and 23 ms to hel1 (measured with TCP connects:
  4.8 ms and 24.3 ms including the handshake). These numbers are assumptions, not
  measurements of Hetzner's network; the real cross-location latency is blocked on
  an operator input.
- **Partition.** `lc_partition <location>` takes the router's interface into that
  location down, cutting it off the private network in both directions while its
  public addresses stay reachable; `lc_heal` restores it.
- **Backups.** No object storage exists an operator input, so the pgbackrest repository is
  a posix repository on `sa-lc-backup-1` in hel1, a location with no database node,
  reached over pgbackrest's TLS protocol with a throwaway CA the backup role creates
  under `.scratch/scratch/local-converge/pgbackrest-tls`. This is a stand-in, not an
  equivalent: object storage is not a server pgbackrest talks to, has its own
  durability and latency, and `repo1-s3-uri-style=host` against Hetzner was never
  tested. The S3 settings stay in `roles/backup` (`backup_repo_mode: s3`, the
  default) and are refused until `sa_object_storage` and the S3 keys exist.
- **Power off** in the drills is `docker kill` of a node container: no clean
  shutdown of anything inside. Power on is `docker start`, which boots systemd and
  Docker again; compose restarts the services (`restart: unless-stopped`).
