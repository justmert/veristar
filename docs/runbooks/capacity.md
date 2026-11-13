---
title: "Capacity changes"
description: "Add resources before a limit is reached, or remove them when they are not used."
---

Adding resources before a limit is reached, or removing them when they are not used. The measured
numbers behind the sizing are in [../reference/capacity.md](../reference/capacity.md) (API metric
storage) and [../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md) (drain rates);
the availability effects of the topology are in
[../reference/availability.md](../reference/availability.md).

## Signals

| Signal | Where | Usual change |
|---|---|---|
| `DiskUsageHigh` (above 80% for 10 minutes) | any node | find what grew, then a larger server type or volume |
| `ReplicationLagHigh`, slow dashboards, high CPU on the leader | core nodes | larger core server type, PostgreSQL parameters |
| `ReplayerLagHigh` that drains slower than it grows | core nodes | more Umami capacity (see the note on Umami below) |
| `MetricsWriterLagHigh` | core nodes | `METRICSWRITER_BATCH`, larger core server type |
| Gateway or collector latency, `429` from rate limits for legitimate tenants | edge nodes | larger edge server type, `edge_gateway_env` and `edge_collector_env` limits |
| JetStream store close to `max_file_store` or `max_memory_store` | edge nodes | `edge_nats_max_file_store` (20G) and `edge_nats_max_memory_store` (512M), and a larger disk |
| `ReplayerVisitStateNearlyFull` or `ReplayerVisitStateFull`, replayer logs "visit_state is full" | edge nodes, replayer | `natsx.VisitStateMaxBytes` (384 MiB) with `edge_nats_max_memory_store`, see below |

## The visit_state cap

`visit_state` holds one duplicate-guard key for every delivered web event for 24 hours, plus one
visit record per visitor, in each NATS server's memory store, which it shares with
`edge_secrets`. It is capped at `natsx.VisitStateMaxBytes` (384 MiB of the 512 MiB store), so the
gateways can always create the next day's secrets: a full bucket refuses only the replayer's
writes. Delivery goes on: the replayer skips the refused duplicate guard and visit
writes and counts them, so events are stored without their duplicate guard and visit continuity
until keys expire. `ReplayerVisitStateNearlyFull` fires above 80 % and `ReplayerVisitStateFull`
when writes are skipped (`TestFullVisitStateKeepsDelivering`, `internal/replayer`). One key takes 74 bytes in the store (measured by
`TestVisitStateLeavesRoomForEdgeSecrets`, `internal/natsx`), so the cap holds about 5 million
delivered events a day, fewer with many distinct visitors. Above about 4 million events a day,
raise `edge_nats_max_memory_store` on a larger edge server type and `VisitStateMaxBytes` with
it, keeping a quarter of the store free (the test checks that ratio), then run
`platformctl nats apply`, which updates the bucket's limit in place.

## Measure first

1. **Disk on a node:** `df -h` and `sudo du -xh / --max-depth=2 | sort -h | tail -20`.
2. **API metric storage per row**, against the running database. On a core node, with the jobs
   role's credentials (the `jobs` container already has them):

   ```sh
   cd /opt/veristar/core && sudo docker compose exec jobs /service capacity
   ```

   It prints JSON with rows, bytes and bytes per row of `api.metrics_minute`, `api.metrics_hourly`
   and `api.metrics_daily`. Vacuum the three tables first or dead tuples count as bytes.
   Compare with the planning bound in [../reference/capacity.md](../reference/capacity.md)
   (about 500 bytes per minute row; 2,000 series need about 41 GiB for 30 days of minute rows).
3. **Retention settings** that set the size: `API_MINUTE_RETENTION_DAYS` (at least 8) and
   `API_HOURLY_RETENTION_MONTHS` (at least 24) on `jobs` and `controlplane`, from
   `core_api_minute_retention_days` and `core_api_hourly_retention_months` in the core role;
   raw Umami events are kept `WEB_RAW_RETENTION_MONTHS` (25 by default) by `jobs retain-web`.

## Change a VM size

1. Set `edge_vm_size` or `core_vm_size` in `infra/tofu/envs/<env>/terraform.tfvars`. Check the
   size is offered in every zone the tier uses and that the family's vCPU quota fits:
   `az vm list-skus -l swedencentral --size <size> -o table` and
   `az vm list-usage -l swedencentral -o table`.
2. Plan and read it:

   ```sh
   make staging-plan
   ```

   An in-place update (`~`) of `azurerm_linux_virtual_machine` resizes the existing VM, which
   restarts it. A replacement (`-/+`) would build a new server; do not apply a plan that
   replaces servers you meant to resize. Production servers have delete and rebuild protection.
3. Drain the node first if the change restarts it ([edge-node-failure.md](edge-node-failure.md)
   step 2; on a core node holding the Patroni leader, switch it over first,
   [database-failover.md](database-failover.md)). Change one node at a time by applying while
   only that node's change is in the plan, or accept that `make staging-apply` changes every
   node of a tier and do it in a quiet hour.
4. `make staging-apply`, then `make staging-deploy SA_IMAGE_TAG=<current release> SA_RELEASE_MANIFEST=<release-images.json>` so Ansible
   checks the node again, then undrain.

## Change a limit or a tunable

The roles' defaults are in `infra/ansible/roles/edge/defaults/main.yml` and
`infra/ansible/roles/core/defaults/main.yml` (`edge_gateway_env`, `edge_collector_env`,
`edge_nats_max_*`, `core_replayer_env`, `core_metricswriter_env`, `core_postgres_parameters`).
Override them for one environment in `infra/ansible/inventories/<env>/group_vars/all.yml`
(a map overrides the whole map, so copy every key), record the reason next to the value, and roll
out with `make <env>-deploy ROLLING=1 SA_IMAGE_TAG=<current release> SA_RELEASE_MANIFEST=<its release-images.json>`.

**Umami.** One Umami process is one Node.js process and drained about 280 events/s in the
this stage load run, below the 500/s of the 10x profile. The core role therefore runs
`core_umami_processes` Umami processes per core node (4 by default: `umami` and `umami-2` to
`umami-4`), and the replayer spreads visitors over all of them (`UMAMI_URL` lists each). The
reasoning and the per-count measurements are in the comment above `core_umami_processes` in
`infra/ansible/roles/core/defaults/main.yml`. To raise web drain, raise that count (it must stay
at most `REPLAYER_WORKERS`, 128) and give the core nodes the cores for it; each idle process holds
about 115 MB.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | this stage: `jobs capacity` after the 1,000,000-request volume run | 467.5 bytes per `metrics_minute` row | [../reference/capacity.md](../reference/capacity.md) |
| 2026-11-13 | this stage: replayer and writer tuning from the load run | web drain about 77/s to about 280/s, OTLP about 150/s to about 1,417/s | [../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md) |
| 2026-11-13 | this stage: the measurement code of step 2 (`go test -run TestMeasure ./internal/jobs/apicapacity/`, real PostgreSQL 18.6) | `metrics_minute` 111,840 rows, 507.7 bytes per row; `metrics_hourly` and `metrics_daily` 1,864 rows, 523.0 bytes per row | this page |

`jobs capacity` itself could not be run against the development stack in this stage: its platform
database was still at migration 14 (from 2026-11-12) and has no `api` tables, and the stack is
shared, so it was not migrated forward for this. Not yet exercised: a server type change and a
limit change rolled out to staging (inputs I5, I13, I14, I15).
