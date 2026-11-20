---
title: "Verifying an environment"
sidebarTitle: "Verification"
description: "Checks that prove a converged environment works, from the outside in."
icon: "circle-check"
---

These checks prove that a converged environment works, from the outside in. Run them after
[bootstrap.md](bootstrap.md), after every rollout ([upgrade.md](upgrade.md)), and whenever you
doubt the state. Staging is shown; node addresses come from
`infra/ansible/inventories/staging/hosts.json`, private addresses and compose directories from
[../runbooks/overview.md](../runbooks/overview.md). The results of these checks on production are
in [../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md).

## Steps

1. **Node health on port 80**, every edge and core node:

   ```sh
   for a in <public address of each node>; do echo "$a $(curl -s -o /dev/null -w '%{http_code}' http://$a/)"; done
   ```

   Expected: `308` for every node, whatever the Host header. `503` means a composite check
   failed or the node is drained ([../runbooks/edge-node-failure.md](../runbooks/edge-node-failure.md)).

2. **Public endpoints:**

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' https://app.<BASE_DOMAIN>/
   curl -fsS -o sdk.js https://in.<BASE_DOMAIN>/v/1.2.0/sdk.js && sha384sum sdk.js
   dig +short web.<BASE_DOMAIN>
   ```

   Expected: `200` for the console; the SHA-384 of `sdk.js` equals the `sha384` of version 1.2.0
   in `packages/tracker/releases/manifest.json`; no answer for `web.`, because no public host
   serves Umami ([ADR 0005](../adr/0005-umami-as-engine-only.md)). On a core node,
   `curl -s http://127.0.0.1:3300/api/heartbeat` answers `{"ok":true}` from Umami's loopback
   port, and nothing else on the node's public address does.

3. **Certificates**: [../runbooks/certificates.md](../runbooks/certificates.md) step 1 for each
   node and name. Expected: Let's Encrypt, more than 14 days left.

4. **NATS** from a core node:

   ```sh
   sudo curl -s --cacert /etc/veristar/tls/root/ca.crt --cert /etc/veristar/tls/root/node.crt --key /etc/veristar/tls/root/node.key 'https://10.20.1.11:8222/jsz' | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("meta_cluster"))'
   ```

   Expected: a meta leader and two current peers. Then `platformctl nats check` as in
   [../runbooks/overview.md](../runbooks/overview.md): every object `"action":"unchanged"` and no
   `nats topology differs from the spec` error.

5. **etcd and Patroni:**

   ```sh
   cd /opt/veristar/edge && sudo docker compose exec etcd etcdctl --endpoints=https://127.0.0.1:2379 --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/node.crt --key=/etc/etcd/tls/node.key member list -w table
   cd /opt/veristar/core && sudo docker compose exec postgres patronictl -c /etc/patroni/patroni.yml list
   ```

   Expected: three `started` etcd members; one `Leader`, one `Sync Standby` and one `Replica`,
   both standbys `streaming` with lag 0.

6. **Backups** on the leader's node:

   ```sh
   sudo docker compose exec postgres pgbackrest --stanza=sa check
   systemctl list-timers 'sa-pgbackrest-*' 'sa-retain-web*'
   ```

   Expected: `check` exits 0 (a WAL segment reached the repository); timers for `full`, `diff`,
   `verify` and `retain-web`.

7. **Exposure.** From an address outside `admin_cidrs`:

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
    `examples/otel-go-nethttp` with `PLATFORM_OTLP_ENDPOINT=https://otlp.<BASE_DOMAIN>`,
    `PLATFORM_OTLP_TOKEN=<the token>` and `TENANT_OTLP_ENDPOINT` pointing at any OTLP receiver
    you run (see its README); send a few requests to it. Expected: the requests appear in the
    project's API dashboard. Remove the test project afterwards
    ([../runbooks/tenant-removal.md](../runbooks/tenant-removal.md)).

11. **Monitoring:** every Prometheus target is `UP`, and the console's operator Alerts page
    ([../operators/alerts.md](../operators/alerts.md)) lists no firing alert other than one you
    can explain (for example `BackupTooOld` before the first backup). With the probe running
    ([probe.md](probe.md)), every measure on `status.<BASE_DOMAIN>` is up.

## Before the first real project

Run these once more on a new environment before it takes its first real project, in addition to
the steps above.

1. **Release signed.** Every image of the deployed release verifies with `cosign verify` as in
   [../security/supply-chain.md](../security/supply-chain.md). `verify-release.yml` runs the same
   check before every converge, so a converge that ended without error already passed it.

2. **PostgreSQL version.** On the leader's node:

   ```sh
   cd /opt/veristar/core && sudo docker compose exec postgres psql -U postgres -tAc 'show server_version'
   ```

   Expected: the `postgres_version` of `infra/ansible/vars/pins.yml`.

3. **Backup repository.** On the leader's node:
   `sudo docker compose exec postgres pgbackrest --stanza=sa info`. Expected: `status: ok` and at
   least one full backup. Then restore once, as in
   [../runbooks/point-in-time-restore.md](../runbooks/point-in-time-restore.md), and record how
   long it took: that is your measured recovery time.

4. **Alert rules loaded.** Tunnel to Prometheus ([../runbooks/overview.md](../runbooks/overview.md),
   "Prometheus and Grafana"), then:

   ```sh
   curl -s http://127.0.0.1:9090/api/v1/rules | python3 -c 'import json,sys; print(sum(len(g["rules"]) for g in json.load(sys.stdin)["data"]["groups"]))'
   ```

   Expected: the number of rules in `infra/ansible/roles/observability/files/rules/*.yml` of the
   release.

5. **Alerts reach the console.** On a core node, post a test alert to Alertmanager on the node's
   private address, over mutual TLS with the node certificate of the internal CA:

   ```sh
   sudo curl -fsS --cacert /etc/veristar/tls/root/ca.crt --cert /etc/veristar/tls/root/node.crt \
     --key /etc/veristar/tls/root/node.key -X POST -H 'Content-Type: application/json' \
     https://<private address>:9093/api/v2/alerts \
     -d '[{"labels":{"alertname":"VerificationTest","severity":"ticket"},"annotations":{"summary":"verification test alert"}}]'
   ```

   Expected: `VerificationTest` appears under Ticket on the console's Alerts page
   (`https://app.<BASE_DOMAIN>/operator/alerts`, [../operators/alerts.md](../operators/alerts.md))
   and disappears after it expires.

6. **Probe.** The checks of [probe.md](probe.md) section 8 pass, and the first daily SLO file
   appears the day after ([../runbooks/slo-report.md](../runbooks/slo-report.md)).

7. **Isolation gate on the deployed code.** Check out the commit the release tag points to
   (`git rev-list -n 1 <release>`) and run `make isolation`
   ([../security/threat-model.md](../security/threat-model.md)). Expected: every test passes. The
   gate builds the working copy, so it proves the deployed versions only at that commit.
