---
title: "Verifying an environment"
sidebarTitle: "Verification"
description: "Checks that prove a converged environment works, from the outside in."
---

Checks that prove a converged environment works, from the outside in. Run them after
[bootstrap.md](bootstrap.md), after every rollout ([upgrade.md](upgrade.md)), and whenever you
doubt the state. Staging is shown; node addresses come from
`infra/ansible/inventories/staging/hosts.json`, private addresses and compose directories from
[../runbooks/overview.md](../runbooks/overview.md).

**Status:** never run against staging or production (they do not exist). The node-level checks
of steps 1, 3, 4, 5 and 7 ran on the local converge on 2026-11-12 and 2026-11-13
([local-converge.md](local-converge.md) section 3); step 2's tracker check ran against a local
gateway on 2026-11-13 ([../runbooks/tracker-release.md](../runbooks/tracker-release.md)).

## Steps

1. **Node health on port 80**, every edge and core node:

   ```sh
   for a in <public address of each node>; do echo "$a $(curl -s -o /dev/null -w '%{http_code}' http://$a/)"; done
   ```

   Expected: `308` for every node, whatever the Host header. `503` means a composite check
   failed or the node is drained ([../runbooks/edge-node-failure.md](../runbooks/edge-node-failure.md)).

2. **Public endpoints:**

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' https://app.<an operator input>/
   curl -s -o /dev/null -w '%{http_code}\n' https://web.<an operator input>/login
   curl -fsS -o sdk.js https://in.<an operator input>/v/1.1.0/sdk.js && sha384sum sdk.js
   curl -s -o /dev/null -w '%{http_code}\n' -X POST https://web.<an operator input>/api/send
   ```

   Expected: `200` for the console; `302` for `/login` (to `https://app.<an operator input>/`); the
   SHA-384 of `sdk.js` equals the `sha384` of version 1.1.0 in
   `packages/tracker/releases/manifest.json`; `403` for Umami's collect route, which must never be
   reachable from outside.

3. **Certificates**: [../runbooks/certificates.md](../runbooks/certificates.md) step 1 for each
   node and name. Expected: Let's Encrypt, more than 14 days left.

4. **NATS** from a core node:

   ```sh
   sudo curl -s --cacert /etc/veristar/tls/root/ca.crt --cert /etc/veristar/tls/root/node.crt --key /etc/veristar/tls/root/node.key 'https://10.20.1.11:8222/jsz' | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("meta_cluster"))'
   ```

   Expected: a leader and three peers, all current. Then `platformctl nats check` as in
   [../runbooks/overview.md](../runbooks/overview.md): every object `"action":"unchanged"` and no
   `nats topology differs from the spec` error.

5. **etcd and Patroni:**

   ```sh
   cd /opt/veristar/edge && sudo docker compose exec etcd etcdctl --endpoints=https://127.0.0.1:2379 --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key member list -w table
   cd /opt/veristar/core && sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list
   ```

   Expected: three `started` etcd members; one `Leader` and one `Sync Standby` in state
   `streaming` with lag 0.

6. **Backups** on the leader's node:

   ```sh
   sudo docker compose exec postgres pgbackrest --stanza=sa check
   systemctl list-timers 'sa-pgbackrest-*' 'sa-retain-web*'
   ```

   Expected: `check` exits 0 (a WAL segment reached the repository); timers for `full`, `diff`,
   `verify` and `retain-web`.

7. **Exposure.** From an address outside `<an operator input>`:

   ```sh
   for p in 22 4222 6222 8222 2379 2380 5432 5000 8008 3000 9090; do
     timeout 3 bash -c "</dev/tcp/<node public address>/$p" 2>/dev/null && echo "$p OPEN" || echo "$p closed"
   done
   ```

   Expected: every port `closed`; only 80 and 443 answer.

8. **Clock:** `chronyc tracking` on each node; the offset is under 1 s (the base role also
   checks this at every converge).

9. **End to end, web.** As the operator, create a test project and a site in the console, put the
   snippet the console shows into a page served from the site's registered origin, load it in a
   browser, and open the site's dashboard through the console. Expected: the pageview appears
   within the 10-minute freshness budget.

10. **End to end, API.** Create an OTLP token on the project's token page and run
    `examples/otel-go-nethttp` with `PLATFORM_OTLP_ENDPOINT=https://otlp.<an operator input>`,
    `PLATFORM_OTLP_TOKEN=<the token>` and `TENANT_OTLP_ENDPOINT` pointing at any OTLP receiver
    you run (see its README); send a few requests to it. Expected: the requests appear in the
    project's API dashboard. Remove the test project afterwards
    ([../runbooks/tenant-removal.md](../runbooks/tenant-removal.md)).

11. **Monitoring:** Prometheus targets all `UP` and no alert firing other than those explained
    by missing inputs (for example `BackupTooOld` before the first backup). Once the probe
    host runs ([probe.md](probe.md)), every measure on `status.<an operator input>` is up.
