# Edge node failure

An edge node (Caddy, gateway, collector, one NATS server, one etcd member) answers badly or not
at all. Alerts that lead here: `EdgeNodeNotReady`, `ProbeNodeCheckFailing`, `NATSServerDown`,
`EtcdMembersDown`, `IngestWebErrorRatioHigh`, `IngestOTLPErrorRatioHigh` ([alerts.md](alerts.md)).
Conventions (node names, addresses, compose directories) are in [README.md](README.md).

## What protects ingestion while you work

- Port 80 of every node answers the node's composite health: 308 healthy, 503 not
  ([../deploy/ansible.md](../deploy/ansible.md) section 5). bunny.net checks port 80 every 30 s
  and drops a node that answers 5xx or not at all from the `in` and `otlp` record sets
  (TTL 60 s). A browser that cannot connect to one address tries the next.
- NATS keeps every stream at three replicas; two of three servers keep publishing. etcd keeps
  Patroni's leader key with two of three members.
- The configured window for clients pinned to a dead address is up to about 90 s
  ([../reference/availability.md](../reference/availability.md)). It has not been measured
  against bunny.net an operator input.

## Steps

1. **Find the node.** From the alert labels, or from outside:

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' http://<node public address>/
   ```

   Expected on a healthy node: `308`. A failing node answers `503` (a check failed, or the node
   is drained) or does not connect.

2. **Take it out of DNS on purpose** if it is up but misbehaving (wrong answers, high latency),
   so the DNS monitor stops sending traffic to it while you work:

   ```sh
   sudo mkdir -p /etc/veristar/drain
   echo "manual $(date -u +%FT%TZ)" | sudo tee /etc/veristar/drain/draining
   curl -s -o /dev/null -w '%{http_code}\n' http://<node public address>/
   ```

   Expected: `503` at once. Wait 120 s before stopping anything (30 s check plus 60 s TTL,
   with margin; the same wait `deploy.yml` uses).

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

   Expected: `308`. bunny.net adds the address back after its next successful check.

6. **Check the cluster** from a core node:

   ```sh
   curl -s 'http://10.20.1.11:8222/jsz?streams=true' | head -c 600; echo
   ```

   (production: `10.10.1.11`). Expected: a `"meta_cluster"` object with a `"leader"` and three
   replicas, all current. The node's own `/healthz` answers `{"status":"ok"}`:

   ```sh
   curl -s http://<node private address>:8222/healthz; echo
   ```

## A node that is gone for good

The server is lost (hardware, deleted, or unbootable). Replace it with OpenTofu, keeping its
primary IPv4 and IPv6 addresses, which are separate resources and survive the server:

1. Plan the replacement of that one server (staging shown; the node name is the inventory name):

   ```sh
   sops exec-env infra/secrets/staging/tofu.enc.env \
     'tofu -chdir=infra/tofu/envs/staging plan -input=false -replace=module.node["staging-edge-2"].hcloud_server.this -out=tfplan'
   ```

   Expected: `Plan: 1 to add, 0 to change, 1 to destroy.` with only that server. Production
   servers carry rebuild and delete protection, so the plan fails there until `protect` is
   turned off for that apply ([../deploy/opentofu.md](../deploy/opentofu.md)).

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
   cd /opt/veristar/edge && sudo docker compose exec etcd etcdctl --endpoints=http://127.0.0.1:2379 member list -w table
   ```

   If the converge failed between the member add and the start, converge again: the unstarted
   member at this address is removed and added once more. If only the etcd volume of a running
   node was lost, delete the marker file on that node first, so the role decides again.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-12 | Local converge: collector stopped on edge 1, then started; NATS stopped on edges 2 and 3 | port 80 turned 503 and back to 308; with two NATS servers down edge 1 answered 503 (`stream ... is not current`) | [../deploy/local-converge.md](../deploy/local-converge.md) section 3 |
| 2026-11-13 | Alert fire-drill: gateway of edge-2 stopped | EdgeNodeNotReady and ProbeNodeCheckFailing fired and resolved; the `in` hostname check and ingestion stayed up | [alerts.md](alerts.md), fire-drills |
| 2026-11-13 | this stage drill: edge 2 powered off under load | invalid: the shared host froze for about 104 s at the power-off; finding: edge 2's NATS stayed `Exited (255)` after power-on, which led to `sa-edge-start.service` (not yet re-verified by a power-off) | [../reference/drills/2026-11-13.md](../reference/drills/2026-11-13.md) |

Not yet exercised: the automatic etcd rejoin of step 4 (`make ops-local` steps `rebuild-edge3`
and `etcd` drive it, but the run of 2026-11-13 was stopped before them, see the O-6 resolution
in the review notes), the manual drain and undrain of steps 2 and 5 by hand (the same flag file was
used by `deploy.yml` in the local converge), DNS removal by bunny.net an operator input, and the
replacement of a lost node (needs staging, inputs I5 and I6). The multi-node topology cannot
run on the shared development host, so none of this was repeated in this stage.
