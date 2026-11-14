---
title: "Operator runbooks"
sidebarTitle: "Overview"
description: "One runbook per operational task, and the conventions every runbook shares."
---

One runbook per operational task. Each one says what it is for, the numbered steps, how to
tell it worked, and when and where it was last exercised. "Exercised" means the steps (or the
exact code path they drive) were run for real and the result is recorded with its date and
source. Where nothing ran, the runbook says **not yet exercised** and why.

Plan inputs are written as `<an operator input>` and so on (the design; the list is in
[../deploy/prerequisites.md](../deploy/prerequisites.md)). Values you look up at the time,
such as a tenant id, are written as `<tenant id>`. Nothing in angle brackets is a real value.

## Index

| Runbook | Last exercised | Where |
|---|---|---|
| [edge-node-failure.md](edge-node-failure.md) | 2026-11-12 and 2026-11-13, in part | local converge, alert fire-drills |
| [queue-backlog-and-dlq.md](queue-backlog-and-dlq.md) | 2026-11-13, in part | this stage load run, alert fire-drills, dev stack |
| [database-failover.md](database-failover.md) | not yet exercised | needs the multi-node topology |
| [point-in-time-restore.md](point-in-time-restore.md) | not yet exercised | needs the multi-node topology and a backup repository |
| [disaster-recovery.md](disaster-recovery.md) (both database nodes lost) | not yet exercised; the restore node's inventory shape is tested | `make tofu-check`; the chain needs staging |
| [umami-upgrade.md](umami-upgrade.md) | 2026-11-13 (upgrade and rollback) | this stage patch rehearsal, this stage rollback rehearsal |
| [collector-upgrade.md](collector-upgrade.md) | 2026-11-13, build and gate only | this stage, local |
| [tracker-release.md](tracker-release.md) | 2026-11-13 (revocation) | this stage, local gateway |
| [operator-key-revocation.md](operator-key-revocation.md) | 2026-11-13, API path | this stage, `TestMachineAPI` |
| [mcp-access.md](mcp-access.md) (revoke and review AI clients) | 2026-11-14, API path | `TestMCP`, `TestIsolationGate/MCPRoutes` |
| [tenant-removal.md](tenant-removal.md) (suspension and removal) | 2026-11-13, API path | this stage, `TestLifecycle` |
| [erasure-requests.md](erasure-requests.md) | 2026-11-13, API path | this stage, `TestErasure` |
| [metric-inflation.md](metric-inflation.md) | 2026-11-13, queries only | this stage, scratch database |
| [security-incident.md](security-incident.md) | not yet exercised as a whole | parts rehearsed in this stage |
| [security-patch.md](security-patch.md) | 2026-11-13 | this stage patch rehearsal |
| [certificates.md](certificates.md) | 2026-11-13, expiry alert only | alert fire-drill |
| [dbip-update.md](dbip-update.md) | 2026-11-13 | this stage, local |
| [secret-rotation.md](secret-rotation.md) | 2026-11-13, platform role passwords only | this stage, scratch database |
| [operators.md](operators.md) | 2026-11-13, sync path | this stage, `TestSyncFlags`, `TestOperatorFlag`, `TestLifecycle` |
| [capacity.md](capacity.md) | 2026-11-13, measurement only | this stage, `TestMeasure` |
| [slo-report.md](slo-report.md) | 2026-11-13 | this stage collection, this stage report rerun |
| [alerts.md](alerts.md) | 2026-11-13, 16 fired and resolved, 3 fired only, of 38 alerts | alert fire-drills |

No runbook has been run against staging or production: neither exists yet (the first apply
waits for review and for inputs I13, I14 and I15). The plan requires every runbook to be executed on staging before this stage is
done; that requirement is still open, so the documentation phase is not done by
its own condition until staging exists.

## Conventions used in every runbook

### Reaching the nodes

Every server has one login, `ops`, reachable over SSH from the admin address ranges
(`<an operator input>`) with the keys of `<an operator input>`. Node names and addresses are in
`infra/ansible/inventories/<env>/hosts.json` (written by `make <env>-apply`). Names are
`<env>-edge-1` to `-3` and `<env>-core-1`, `-2`.

Private addresses follow a fixed scheme ([../deploy/opentofu.md](../deploy/opentofu.md)
section 2):

| Node | staging | production |
|---|---|---|
| edge 1 (zone 1) | 10.20.1.11 | 10.10.1.11 |
| edge 2 (zone 2) | 10.20.2.12 | 10.10.2.12 |
| edge 3 (zone 3) | 10.20.3.13 | 10.10.3.13 |
| core 1 (zone 1) | 10.20.1.21 | 10.10.1.21 |
| core 2 (zone 2) | 10.20.2.22 | 10.10.2.22 |

### Compose projects

| Tier | Directory | Services |
|---|---|---|
| edge | `/opt/veristar/edge` | `caddy`, `gateway`, `collector`, `nats`, `etcd` |
| core | `/opt/veristar/core` | `postgres`, `haproxy`, `umami` (and `umami-2` up to `umami-<core_umami_processes>`, 4 by default), `controlplane`, `replayer`, `metricswriter`, `jobs`, `caddy` |

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
is where `release-images.yml` publishes for repository `<an operator input>`.

### Running a jobs task by hand

`jobs run <task>` takes the same leader lock as the daemon. While a `jobs` daemon leads, it
refuses with `a jobs daemon is leading; it runs <task> itself` (`internal/jobs/scheduler`). So
either let the daemon run the task on its schedule (restart it with
`sudo docker compose restart jobs` to run its start-up pass), or stop `jobs` on **both** core
nodes and then run, on one of them:

```sh
cd /opt/veristar/core && sudo docker compose run --rm --no-deps jobs run <task>
```

Start `jobs` again on both nodes afterwards (`sudo docker compose start jobs`).

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
role to the edge and core nodes as its last play. The probe node (status page, Gatus, prober) is
not converged by any playbook yet: it has no private address, so the base role refuses it.
