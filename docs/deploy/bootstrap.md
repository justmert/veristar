---
title: "Bootstrap a new environment"
sidebarTitle: "Bootstrap"
description: "The first converge of an empty environment, end to end, with a check after each step."
---

This page takes an empty environment through its first converge, end to end, with a check after
each step. Staging is shown; production is the same with `production`. Before you start, finish
[prerequisites.md](prerequisites.md) and collect the values it lists. Production was bootstrapped
with these steps on 2026-11-13 ([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md)).

## Steps

1. **Secrets.** Follow [secrets.md](secrets.md) section 5: recipients, `.sops.yaml`,
   `infra/secrets/staging/secrets.enc.yaml` and `infra/secrets/staging/tofu.enc.env`.
   Check: `sops decrypt infra/secrets/staging/secrets.enc.yaml | grep -c ':'` prints a number
   and no error.

2. **Infrastructure.** Follow the [OpenTofu procedure](opentofu.md#procedure): `terraform.tfvars`,
   `make state-bootstrap ENV=staging`, `make staging-plan`, `make staging-apply`.
   Check: `infra/ansible/inventories/staging/hosts.json` exists with the three converged nodes
   (`staging-node-1` to `-3`, each in the `edge` and the `core` group), and
   `ssh ops@<node 1 address> true` succeeds.

3. **DNS.** Check that Cloudflare answers and points at Traffic Manager:

   ```sh
   dig +short NS <BASE_DOMAIN>
   dig +short A in.<BASE_DOMAIN>
   ```

   Expected: Cloudflare's name servers, then the CNAME to
   `<domain>-<env>-in.trafficmanager.net` followed by the three node addresses. Before the first
   converge no node answers port 80, so every endpoint is `Degraded` and Traffic Manager returns
   all of them, which is its documented behaviour when every endpoint fails.

4. **Non-secret Ansible variables.** Write `infra/ansible/inventories/staging/group_vars/all.yml`
   as in [ansible.md](ansible.md) section 7 step 1 (`sa_image_registry`,
   `sa_operator_github_ids`); `sa_tls: cloudflare` and `sa_public_bind_ipv4` are already there.

5. **Release.** A tag `v<release>` on the commit to deploy runs `release-images.yml`, which builds,
   scans, signs and attests the eleven platform images and attaches `release-images.json`
   ([../security/supply-chain.md](../security/supply-chain.md)). Download `release-images.json`
   from the release. `scripts/supply-chain/release-workstation.sh` builds the same release from a
   workstation, with the same gate and signing key
   ([../security/supply-chain.md](../security/supply-chain.md) section 2.1).

6. **First converge:**

   ```sh
   make staging-deploy SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<path to release-images.json>
   ```

   What happens, in order (`infra/ansible/playbooks/site.yml`): the base role (users, SSH
   hardening, host firewall, chrony with an offset check, Docker at its pins) and the secrets role
   on every node; NATS and etcd on the edge nodes; postgres-ha (Patroni) and HAProxy on the core
   nodes; the pgBackRest settings and schedules; `bootstrap.yml` on the Patroni leader; the edge
   and core services; the observability role on edge and core nodes. On the three converged
   nodes of staging and production every node is in both groups, so each step runs on all three.

   `bootstrap.yml` (once, idempotent on every later run): checks `sa_operator_github_ids`,
   waits for a writable leader, creates the Umami role and database and the platform
   database, `platformctl db migrate`, `platformctl db roles`, starts Umami (which migrates its own
   database), `platformctl umami bootstrap` (replaces the seeded admin password),
   `platformctl db umami-retention-role`, `platformctl nats apply`.

   Expected: `PLAY RECAP` with `failed=0` everywhere. The first production converge took
   11 min 19 s.

7. **Second converge** (same command). Expected: `failed=0` on every node, with only the
   changes [ansible.md](ansible.md) section 7 step 3 describes.

8. **Certificates.** Each node's Caddy asks Let's Encrypt for its names through DNS-01. Check
   each node as in [../runbooks/certificates.md](../runbooks/certificates.md) step 1. Expected:
   issuer Let's Encrypt for `in.`, `otlp.`, `app.` and `web.<BASE_DOMAIN>`.

9. **First operator sign-in.** An operator listed in `sa_operator_github_ids` opens
   `https://app.<BASE_DOMAIN>/` and signs in with GitHub (the environment's OAuth App, callback `https://app.<BASE_DOMAIN>/api/auth/github/callback`).
   Expected: the console shows the operator area and asks for a passkey or security key.
   Register one within 24 hours of the control plane's first start; the operator area opens.
   Add the other operators on the Operators page ([../operators/roster.md](../operators/roster.md)).

10. **Probe tenant** (monitoring). On the core node that holds the Patroni leader,
    with the operator of step 9:

    ```sh
    sudo docker run --rm --network veristar-core_default \
      --env-file /etc/veristar/env/platformctl.env \
      -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
      -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
      -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
      ghcr.io/justmert/veristar/platformctl:<release> \
      probe-tenant create -operator-github-id <operator github id> -domain probe.<BASE_DOMAIN> -origin https://probe.<BASE_DOMAIN>
    ```

    It prints `tenant_id=`, `site_id=`, `otlp_token=` and `machine_api_key=` once. Put
    `site_id`, `otlp_token` and `machine_api_key` into the SOPS file as `probe_site_id`,
    `probe_otlp_token` and `probe_machine_api_key`
    (`sops --config infra/secrets/.sops.yaml edit infra/secrets/staging/secrets.enc.yaml`). Then
    restart `jobs` on every node (`sudo docker compose restart jobs` in
    `/opt/veristar/core`): a new jobs leader runs every task at once, including the
    reconcile that provisions the probe's Umami websites and registry entries.

11. **Probe.** The prober runs on its own host outside the environment. Either a dedicated
    host in the inventory's `probe` group with its own public address, which `site.yml`'s
    "Probe" play gives Caddy for `status.<BASE_DOMAIN>`, Gatus, the prober and the Alertmanager
    watchdog; or, as for production, the Compose bundle of `playbooks/probe-bundle.yml` on a
    shared host with an edge Caddy (for production, a separate virtual machine in Azure East US,
    [probe.md](probe.md)). It needs the probe tenant's values of step 10
    in the SOPS file. Expected: `https://status.<BASE_DOMAIN>/` answers and every measure of
    probe.md section 4 receives results every 30 s. `make ops-local` exercises the same play
    locally.

12. **SLO collection.** Set the repository variable `STATUS_BASE_URL` to `https://status.<BASE_DOMAIN>`
    so `slo-report.yml` can collect ([../runbooks/slo-report.md](../runbooks/slo-report.md)).

13. **Verify** with [verification.md](verification.md).
