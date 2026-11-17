---
title: "Capacity changes"
description: "Add resources before a limit is reached, or remove them when they are not used."
---

How to add resources before a limit is reached, or remove them when they are not used. An
environment runs three converged nodes, one per availability zone of the region (production:
Azure Denmark East), each running the edge and the core roles on one VM size, `node_vm_size`
(`Standard_B2as_v2`). Below, "edge nodes" and "core nodes" are the inventory groups: on a converged
node both apply.

The measured numbers behind the sizing:

- gateway throughput and capacity per node and per cluster: [gateway limits](../reference/gateway-limits.md);
- API metric storage: [API metrics capacity](../reference/capacity.md);
- replayer and metrics writer drain rates: the [local drill record](../reference/drills/2026-11-13.md);
- the availability effects of the topology: [availability](../reference/availability.md).

## Signals

| Signal | Where | Usual change |
|---|---|---|
| `DiskUsageHigh` (above 80% for 10 minutes) | any node | find what grew, then a larger server type or volume |
| `ReplicationLagHigh`, slow dashboards, high CPU on the leader | core nodes | larger `node_vm_size`, PostgreSQL parameters |
| `ReplayerLagHigh`, `WebEventsBacklogGrowing` or `WebEventsDelayed` that drains slower than it grows | core nodes | more Umami capacity on every core node (see the note on Umami below) |
| `MetricsWriterLagHigh` | core nodes | `METRICSWRITER_BATCH`, larger `node_vm_size` |
| Gateway or collector latency, `429` from rate limits for legitimate tenants | edge nodes | larger `node_vm_size`, `edge_gateway_env` and `edge_collector_env` limits |
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
raise `edge_nats_max_memory_store` on a larger `node_vm_size` and `VisitStateMaxBytes` with
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
   raw Umami events are kept `WEB_RAW_RETENTION_MONTHS` (25 by default, and at least 25) by `jobs retain-web`.

## Change a VM size

1. Set `node_vm_size` in `infra/tofu/envs/<env>/terraform.tfvars`; it sizes the three nodes and
   the restore node. Check the size is offered in zones 1, 2 and 3 and that the family's vCPU
   quota fits (production region `denmarkeast`):
   `az vm list-skus -l denmarkeast --size <size> -o table` and
   `az vm list-usage -l denmarkeast -o table`.
2. Plan and read it (`<env>` is `staging` or `production`):

   ```sh
   make <env>-plan
   ```

   An in-place update (`~`) of `azurerm_linux_virtual_machine` resizes the existing VM, which
   restarts it. A replacement (`-/+`) would build a new server; do not apply a plan that
   replaces servers you meant to resize. Production servers have delete and rebuild protection.
3. Drain the node first if the change restarts it ([edge-node-failure.md](edge-node-failure.md)
   step 2, with the 150 s wait of a converged node; on the node holding the Patroni leader,
   switch it over first, [database-failover.md](database-failover.md)). Change one node at a
   time with a targeted plan and apply of that node, or accept that `make <env>-apply` changes
   every node and do it in a quiet hour. A targeted change of one node:

   ```sh
   sops exec-env infra/secrets/<env>/tofu.enc.env "tofu -chdir=infra/tofu/envs/<env> plan \
     -var node_vm_size=<size> -target='module.node[\"<env>-node-<n>\"]' -out=resize.tfplan"
   sops exec-env infra/secrets/<env>/tofu.enc.env "tofu -chdir=infra/tofu/envs/<env> apply resize.tfplan"
   ```

   Expected: the plan shows `Plan: 0 to add, 1 to change, 0 to destroy.` with only `size`
   changing; the apply reports `Modifications complete` after about a minute, by which time the
   node has restarted on the new size, and its gateway starts about 50 s after the boot. When every node has the new size, set
   `node_vm_size` in `terraform.tfvars` so `make <env>-plan` shows `No changes.` again.
4. Check the node, undrain it, then converge. On the node,
   `sudo docker exec veristar-core-caddy-1 wget -qO- http://gateway:9090/edgez` answers
   `healthy`, `patronictl list` shows its member `streaming` with lag 0, and its NATS server
   is current ([edge-node-failure.md](edge-node-failure.md) step 6). Then remove the drain flag
   as in step 5 there (port 80 answers 308) and run the converge so Ansible checks every node:

   ```sh
   make <env>-deploy SA_IMAGE_TAG=<current release> SA_RELEASE_MANIFEST=<release-images.json>
   ```

   Expected: `failed=0` on every node. Run it after the undrain: `site.yml` waits for each
   node's composite health on port 80, which a drained node answers with 503, so it stops at
   "Wait for this node's composite health on port 80". A rolling deploy limited to the node
   (`ROLLING=1`, `--limit <env>-node-<n>,localhost`) stops as well, at "Find the patroni
   leader", because the limit leaves out the leader's node.

## Change a limit or a tunable

The roles' defaults are in `infra/ansible/roles/edge/defaults/main.yml` and
`infra/ansible/roles/core/defaults/main.yml` (`edge_gateway_env`, `edge_collector_env`,
`edge_nats_max_*`, `core_replayer_env`, `core_metricswriter_env`, `core_postgres_parameters`).
Override them for one environment in `infra/ansible/inventories/<env>/group_vars/all.yml`
(a map overrides the whole map, so copy every key), record the reason next to the value, and roll
out with `make <env>-deploy ROLLING=1 SA_IMAGE_TAG=<current release> SA_RELEASE_MANIFEST=<its release-images.json>`.

**Umami.** One Umami process is one Node.js process and drained about 280 events/s in the load
run of 2026-11-13 ([local drill record](../reference/drills/2026-11-13.md)), below the 500/s of the
10x load profile. The core role therefore runs `core_umami_processes` Umami processes per core node
(2 on the 2-vCPU `Standard_B2as_v2` node: `umami` and `umami-2`), and the replayer spreads visitors
over all of them (`UMAMI_URL` lists each). The reasoning and the per-count measurements are in the comment above `core_umami_processes` in
`infra/ansible/roles/core/defaults/main.yml`. To raise web drain, raise that count (it must stay
at most `REPLAYER_WORKERS`, 128) and give the nodes the cores for it; each idle process holds
about 115 MB. Every core node's replayer delivers its share of the 32 web event partitions to
its own processes, and a node that is down leaves its share to the others, so every core node
needs the size. Adding core nodes adds their Umami processes to the delivery
([../reference/capacity.md](../reference/capacity.md), "Web events end to end", has the
measured rates by node count).

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | `jobs capacity` after the 1,000,000-request volume run | 467.5 bytes per `metrics_minute` row | [../reference/capacity.md](../reference/capacity.md) |
| 2026-11-13 | Replayer and metrics writer tuning from the load run | web drain from about 77/s to about 280/s, OTLP from about 150/s to about 1,417/s | [../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md) |
| 2026-11-13 | The measurement code of step 2 (`go test -run TestMeasure ./internal/jobs/apicapacity/`, real PostgreSQL 18.6) | `metrics_minute` 111,840 rows, 507.7 bytes per row; `metrics_hourly` and `metrics_daily` 1,864 rows, 523.0 bytes per row | this page |
| 2026-11-14 | Production: `production-node-2` from `Standard_B2as_v2` to `Standard_B4as_v2` and back with targeted applies, drained, under 4 page views a second | each apply about a minute (1 min 11 s, 59 s), gateway up about 50 s after each boot, 5 min 0 s at the larger size; Patroni lag 0, NATS routes, etcd and the 12 Traffic Manager endpoints back; the final plan `No changes.`; 10,471 of 10,480 events acknowledged and **0 acknowledged events lost**. Steps 3 and 4 were corrected in this run | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "VM size change of one node and back" |
