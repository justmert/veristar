---
title: "Edge node failure"
description: "Diagnose and recover an edge node that answers badly or not at all."
icon: "network-wired"
---

An edge node (Caddy, gateway, collector, one NATS server, one etcd member) answers badly or not
at all. Alerts that lead here: `EdgeNodeNotReady`, `ProbeNodeCheckFailing`, `NATSServerDown`,
`EtcdMembersDown`, `IngestWebErrorRatioHigh`, `IngestOTLPErrorRatioHigh` ([alerts.md](alerts.md)).
Conventions (node names, addresses, compose directories) are in [overview.md](overview.md).

## What protects ingestion while you work

- Port 80 of every node answers the node's composite health: 308 healthy, 503 not
  ([../deploy/ansible.md](../deploy/ansible.md) section 5). Traffic Manager probes port 80 every
  10 s and drops a node after three failed probes from the `in` and `otlp` answers (answer
  TTL 10 s). A browser that cannot connect to one address tries the next.
- NATS keeps every stream at three replicas; two of three servers keep publishing. etcd keeps
  Patroni's leader key with two of three members.
- The configured window for clients pinned to a dead address is up to about 45 s
  ([../reference/availability.md](../reference/availability.md)); on production a powered-off
  node left the `in` answer after 41.9 s and 41.8 s in two runs
  ([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md)). `az network traffic-manager endpoint list -g veristar-<env> --profile-name
  <domain>-<env>-in --type externalEndpoints -o table` shows each node's monitor status.

## Steps

1. **Find the node.** From the alert labels, or from outside:

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' http://<node public address>/
   ```

   Expected on a healthy node: `308`. A failing node answers `503` (a check failed, or the node
   is drained) or does not connect.

2. **Take it out of DNS on purpose** if it is up but misbehaving (wrong answers, high latency),
   so Traffic Manager stops sending traffic to it while you work:

   ```sh
   sudo mkdir -p /etc/veristar/drain
   echo "manual $(date -u +%FT%TZ)" | sudo tee /etc/veristar/drain/draining
   curl -s -o /dev/null -w '%{http_code}\n' http://<node public address>/
   ```

   Expected: `503` at once. Wait 120 s before stopping anything (three 10 s probes, the
   5 s timeout and the 10 s answer TTL of `in` and `otlp`, with margin). On a converged or core
   node, which is also in the `app` answers (three 30 s probes, a 10 s timeout and a
   30 s TTL), wait 150 s. These are the waits `deploy.yml` and `reboot.yml` use for edge and
   core nodes.

3. **See which part fails.** On the node:

   ```sh
   cd /opt/veristar/edge && sudo docker compose ps
   sudo docker compose exec caddy wget -qO- http://gateway:9090/edgez; echo
   sudo docker compose exec caddy wget -qO- http://gateway:9090/readyz; echo
   ```

   `/edgez` is the composite answer behind port 80 (gateway ready, collector health check, local
   NATS current with a meta leader). Every service must show `running (healthy)` in `ps`, apart
   from `gateway` and `collector`, which have no compose health check.

4. **Restart what is down**, one service at a time, and read its log first:

   ```sh
   sudo docker compose logs --since 30m --tail 200 <service>
   sudo docker compose up -d <service>
   ```

   - `nats` down: start it; it rejoins and catches up. Never delete a NATS store while another
     NATS server is also down (NATSQuorumLost in [alerts.md](alerts.md)).
   - `etcd` down: start it. If two members are down, Patroni cannot renew its leader key and the
     primary demotes itself after 30 s; start the members before anything else.
   - `gateway` not ready with `daily_secrets` failing: see VisitorSecretMissing in
     [alerts.md](alerts.md).
   - After a power loss Docker starts the projects through `sa-edge-start.service` (installed
     by the edge role because a start that fails during daemon start-up is not retried by the
     restart policy). Check it with `systemctl status sa-edge-start.service`.

5. **Put it back.** When `/edgez` answers and port 80 would be healthy:

   ```sh
   sudo rm /etc/veristar/drain/draining
   curl -s -o /dev/null -w '%{http_code}\n' http://<node public address>/
   ```

   Expected: `308`. Traffic Manager adds the address back after its next successful probe.

6. **Check the cluster** on the node:

   ```sh
   curl -s 'http://127.0.0.1:8222/jsz?streams=true' | head -c 600; echo
   curl -s http://127.0.0.1:8222/healthz; echo
   ```

   Expected: a `"meta_cluster"` object with a `"leader"` and the other two peers current, and
   `{"status":"ok"}` from `/healthz`. From another node, read the same port through the
   monitoring relay as in [overview.md](overview.md#nats-state-without-a-nats-client).

## A node that is gone for good

The VM is lost (deleted, or unbootable). Replace it with OpenTofu, keeping its public IPv4
address, which is a separate resource and survives the VM, so the Traffic Manager endpoint
stays valid:

1. Plan the replacement of that one server (staging shown; the node name is the inventory name):

   ```sh
   sops exec-env infra/secrets/staging/tofu.enc.env \
     'tofu -chdir=infra/tofu/envs/staging plan -input=false -replace=module.node["staging-node-2"].azurerm_linux_virtual_machine.this -out=tfplan'
   ```

   Expected: `Plan: 1 to add, 0 to change, 1 to destroy.` with only that VM. Production VMs
   carry a delete lock, so the apply fails there until `protect` is turned off for that apply
   ([../deploy/opentofu.md](../deploy/opentofu.md)).

2. `make staging-apply`. Expected: `Apply complete!` and a rewritten
   `infra/ansible/inventories/staging/hosts.json`.

3. Converge: `make staging-deploy SA_IMAGE_TAG=<release tag> SA_RELEASE_MANIFEST=<release-images.json>` (see
   [../deploy/ansible.md](../deploy/ansible.md)). NATS on the new node starts with an empty store
   and catches up from the other two, because a JetStream peer is identified by its server name.

4. **etcd rejoins on its own.** The edge role (`roles/edge/tasks/etcd.yml`) sees that the new
   node has no etcd data volume while the other two members answer, removes the stale member of
   this node's name (or an unstarted member at its address) through a healthy peer, adds the
   node again and starts it with `--initial-cluster-state=existing`. It records that in
   `/etc/veristar/edge/etcd-initial-state`, so later converges change nothing. Check
   from any edge node that three members are `started` and healthy:

   ```sh
   cd /opt/veristar/edge && sudo docker compose exec etcd etcdctl --endpoints=https://127.0.0.1:2379 \
     --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key \
     member list -w table
   ```

   If the converge failed between the member add and the start, converge again: the unstarted
   member at this address is removed and added once more. If only the etcd volume of a running
   node was lost, delete the marker file on that node first, so the role decides again.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | Local converge: collector stopped on edge 1, then started; NATS stopped on edges 2 and 3 | port 80 turned 503 and back to 308; with two NATS servers down edge 1 answered 503 (`stream ... is not current`) | [../deploy/local-converge.md](../deploy/local-converge.md) section 3 |
| 2026-11-13 | Alert fire-drill: gateway of edge-2 stopped | EdgeNodeNotReady and ProbeNodeCheckFailing fired and resolved; the `in` hostname check and ingestion stayed up | [alerts.md](alerts.md), fire-drills |
| 2026-11-13, 2026-11-14 | Production: a converged node powered off for 300 s, three runs, the last under 4 page views a second | Traffic Manager removed the node from `in` after 41.8 to 41.9 s; last run 3,341 of 3,341 events acknowledged, none answered 503, none lost; in the second and third runs every container ran again after power-on without a manual start (`sa-edge-start.service`) | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Node loss" |
| 2026-11-14 | Production: rolling `deploy.yml` of release `v0.2.1` | every node drained with the flag file of step 2, updated and put back; no drain flag left | [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), "Release v0.2.1 rollout and gateway capacity" |
