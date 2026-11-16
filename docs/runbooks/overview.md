---
title: "Operator runbooks"
sidebarTitle: "Overview"
description: "One runbook per operational task, and the conventions every runbook shares."
---

One runbook per operational task. Each runbook states when to use it, the numbered steps, how
to tell each step worked, and a record of where and when its steps ran.

Placeholders are written in angle brackets: `<BASE_DOMAIN>` for the environment's domain
(production: `veristar.app`), `<env>` for `staging` or `production`, and values you look up at
the time, such as `<tenant id>`. Nothing in angle brackets is a real value. The values an
environment is built from are listed in [../deploy/prerequisites.md](../deploy/prerequisites.md).

## Index

### Incidents

| Runbook | Use it when |
|---|---|
| [alerts.md](alerts.md) | an alert fires: the meaning and first action of each of 39 alerts |
| [edge-node-failure.md](edge-node-failure.md) | a node's gateway, collector, NATS server or etcd member answers badly or not at all, or a VM is lost |
| [database-failover.md](database-failover.md) | the PostgreSQL leader changed or is missing, or a replica needs a rebuild |
| [disaster-recovery.md](disaster-recovery.md) | no database member holds good data and the cluster is rebuilt from backups |
| [point-in-time-restore.md](point-in-time-restore.md) | data was destroyed and you need the database as it was at a past moment |
| [queue-backlog-and-dlq.md](queue-backlog-and-dlq.md) | a queue grows or a dead-letter queue holds messages |
| [certificates.md](certificates.md) | a node serves an expiring, expired or wrong certificate |
| [security-incident.md](security-incident.md) | something suggests an attack or a breach |
| [metric-inflation.md](metric-inflation.md) | a project's numbers look higher than its real use |

### Routine operations

| Runbook | Use it when |
|---|---|
| [capacity.md](capacity.md) | adding or removing resources |
| [security-patch.md](security-patch.md) | a critical or high vulnerability needs a patch within 24 hours |
| [secret-rotation.md](secret-rotation.md) | rotating a credential on schedule or after an exposure |
| [collector-upgrade.md](collector-upgrade.md) | upgrading the OpenTelemetry Collector |
| [umami-upgrade.md](umami-upgrade.md) | upgrading Umami, or rolling an upgrade back |
| [tracker-release.md](tracker-release.md) | releasing or revoking a tracker version |
| [dbip-update.md](dbip-update.md) | loading the monthly DB-IP country file |
| [slo-report.md](slo-report.md) | producing the monthly service level report |

### People and projects

| Runbook | Use it when |
|---|---|
| [operators.md](operators.md) | adding or removing an operator |
| [operator-key-revocation.md](operator-key-revocation.md) | an operator API key must stop working |
| [mcp-access.md](mcp-access.md) | revoking an AI client's access or investigating its tool calls |
| [tenant-removal.md](tenant-removal.md) | suspending, resuming or removing a project |
| [erasure-requests.md](erasure-requests.md) | handling a right-to-erasure request |

The production drills (node loss, database leader loss, point-in-time restore, rolling deploy)
are recorded in [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md),
and the drills on the local test topology in
[../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md).

## Conventions used in every runbook

### Reaching the nodes

Every server has one login, `ops`, reachable over SSH from the admin address ranges
(`admin_cidrs`) with the admin SSH public keys (`admin_ssh_public_keys`). Node names and addresses are in
`infra/ansible/inventories/<env>/hosts.json` (written by `make <env>-apply`). Names are
`<env>-node-1` to `-3`: three converged nodes, one per availability zone, each in the `edge` and
the `core` group, so a runbook step for "an edge node" or "a core node" runs on any of them.
`<env>-restore-1` exists only during a restore.

Private addresses follow a fixed scheme ([../deploy/opentofu.md](../deploy/opentofu.md#layout)):

| Node | staging | production |
|---|---|---|
| node 1 (zone 1) | 10.20.1.11 | 10.10.1.11 |
| node 2 (zone 2) | 10.20.2.12 | 10.10.2.12 |
| node 3 (zone 3) | 10.20.3.13 | 10.10.3.13 |
| restore 1 (`restore_zone`, only during a restore) | 10.20.`<zone>`.41 | 10.10.`<zone>`.41 |

Every node has both compose projects below.

### Compose projects

| Tier | Directory | Services |
|---|---|---|
| edge | `/opt/veristar/edge` | `caddy`, `gateway`, `collector`, `nats`, `etcd` |
| core | `/opt/veristar/core` | `postgres`, `haproxy`, `umami` (and `umami-2` up to `umami-<core_umami_processes>`, 2 on the `Standard_B2as_v2` nodes), `controlplane`, `replayer`, `metricswriter`, `jobs`, `caddy` |

Every compose command below runs in that directory with `sudo`, for example:

```sh
cd /opt/veristar/core && sudo docker compose ps
```

Env files with credentials are in `/etc/veristar/env/` (root only). Never copy them off
the node.

### Which core node holds the database

```sh
cd /opt/veristar/core && sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list
```

The member with role `Leader` is the primary. Run every `psql` command of these runbooks on that
node:

```sh
cd /opt/veristar/core && sudo docker compose exec postgres psql -U postgres -d platform
```

Use `-d umami` for the Umami database.

### Running platformctl on a core node

This is the same command `infra/ansible/playbooks/bootstrap.yml` runs. Replace the NATS addresses
with the production ones from the table above when working on production, and
`<release tag>` with the deployed `SA_IMAGE_TAG`:

```sh
sudo docker run --rm --network veristar-core_default \
  --env-file /etc/veristar/env/platformctl.env \
  -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
  -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
  -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
  -e UMAMI_DB_NAME=umami -e UMAMI_URL=http://umami:3000 \
  -e NATS_URL=tls://10.20.1.11:4222,tls://10.20.2.12:4222,tls://10.20.3.13:4222 \
  -e NATS_TLS_CA=/etc/sa-tls/ca.crt \
  -e NATS_REPLICAS=3 \
  ghcr.io/justmert/veristar/platformctl:<release tag> nats check
```

The image name is `<sa_image_registry>/platformctl:<tag>`; `ghcr.io/justmert/veristar`
is where `release-images.yml` publishes for the repository `github.com/justmert/veristar`.

### Running a jobs task by hand

`jobs run <task>` takes the same leader lock as the daemon. While a `jobs` daemon leads, it
refuses with `a jobs daemon is leading; it runs <task> itself` (`internal/jobs/scheduler`). So
either let the daemon run the task on its schedule (restart it with
`sudo docker compose restart jobs` to run its start-up pass), or stop `jobs` on **both** core
nodes and then run, on one of them:

```sh
cd /opt/veristar/core && sudo docker compose run --rm --no-deps jobs run <task>
```

Start `jobs` again on every core node afterwards (`sudo docker compose start jobs`).

### NATS state without a NATS client

No NATS command-line client is pinned or installed by any role. Stream and consumer counters are
read from the NATS monitoring port on the private network, from any core node. The edge
relay serves it over TLS and asks for a client certificate of the internal CA:

```sh
sudo curl -s --cacert /etc/veristar/tls/root/ca.crt --cert /etc/veristar/tls/root/node.crt --key /etc/veristar/tls/root/node.key 'https://10.20.1.11:8222/jsz?streams=true&consumers=true'
```

The JSON lists every stream with `state.messages`, `state.first_seq` and its consumers'
`num_pending` and `num_ack_pending`. `platformctl nats check` reports any difference between
the server and the declared topology without changing anything.

### Prometheus and Grafana

```sh
ssh -L 9090:127.0.0.1:9090 -L 3000:127.0.0.1:3000 ops@<core node public address>
```

Then `http://127.0.0.1:9090` and `http://127.0.0.1:3000`. `site.yml` applies the observability
role to the edge and core nodes as its last play. A probe host (status page, Gatus, prober) in
the inventory's `probe` group is converged by the play `Probe`; production's probe is a
Compose bundle on a separate virtual machine in Azure East US ([../deploy/probe.md](../deploy/probe.md)).
