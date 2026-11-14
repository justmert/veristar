---
title: "Bootstrap a new environment"
sidebarTitle: "Bootstrap"
description: "The first converge of an empty environment, end to end, with a check after each step."
---

The first converge of an empty environment, end to end, with a check after each step. Staging is
shown; production is the same with `production`. Before starting, [prerequisites.md](prerequisites.md)
is done and the inputs I1 to I6, I13, I14 and I15 exist (I8 for monitoring delivery).

**Status:** never run against a cloud account (the inputs do not exist yet, and the core role
refuses PostgreSQL below 18.7 outside the local converge). The same playbooks ran end to end on
the local converge on 2026-11-12 and 2026-11-13 ([local-converge.md](local-converge.md)
sections 3 and 5). Expected outputs quoted from those runs say so.

## Steps

1. **Secrets.** Follow [secrets.md](secrets.md) section 5: recipients, `.sops.yaml`,
   `infra/secrets/staging/secrets.enc.yaml` and `infra/secrets/staging/tofu.enc.env`.
   Check: `sops decrypt infra/secrets/staging/secrets.enc.yaml | grep -c ':'` prints a number
   and no error.

2. **Infrastructure.** Follow [opentofu.md](opentofu.md) section 6: `terraform.tfvars`,
   `make state-bootstrap ENV=staging`, `make staging-plan`, `make staging-apply`.
   Check: `infra/ansible/inventories/staging/hosts.json` exists with the three converged nodes
   (`staging-node-1` to `-3`, each in the `edge` and the `core` group), and
   `ssh ops@<node 1 address> true` succeeds.

3. **DNS.** Check that Cloudflare answers and points at Traffic Manager:

   ```sh
   dig +short NS <an operator input>
   dig +short A in.<an operator input>
   ```

   Expected: Cloudflare's name servers, then the CNAME to
   `<domain>-<env>-in.trafficmanager.net` followed by the three node addresses. Before the first
   converge no node answers port 80, so every endpoint is `Degraded` and Traffic Manager returns
   all of them, which is its documented behaviour when every endpoint fails.

4. **Non-secret Ansible variables.** Write `infra/ansible/inventories/staging/group_vars/all.yml`
   as in [ansible.md](ansible.md) section 8 step 1 (`sa_image_registry`,
   `sa_operator_github_ids`); `sa_tls: cloudflare` and `sa_public_bind_ipv4` are already there.

5. **Release.** A tag `v<release>` on the commit to deploy runs `release-images.yml`, which builds,
   scans, signs and attests the eleven platform images and attaches `release-images.json`
   ([../security/supply-chain.md](../security/supply-chain.md)). Download `release-images.json`
   from the release. While the repository is private GitHub Actions is off by operator decision;
   a release is then built and signed on a workstation with the same key
   ([../security/supply-chain.md](../security/supply-chain.md) section 2.1). Release `v0.1.0`
   exists and runs in production.

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

   `bootstrap.yml` (once, idempotent on every later run): checks `sa_operator_github_ids`
   an operator input, waits for a writable leader, creates the Umami role and database and the platform
   database, `platformctl db migrate`, `platformctl db roles`, starts Umami (which migrates its own
   database), `platformctl umami bootstrap` (replaces the seeded admin password),
   `platformctl db umami-retention-role`, `platformctl nats apply`.

   Expected: `PLAY RECAP` with `failed=0` everywhere. On the local converge the first run took
   177 s with images already in the registry.

7. **Second converge** (same command). Expected: `changed=0` on every node.

8. **Certificates.** Each node's Caddy asks Let's Encrypt for its names through DNS-01. Check
   each node as in [../runbooks/certificates.md](../runbooks/certificates.md) step 1. Expected:
   issuer Let's Encrypt for `in.`, `otlp.`, `app.` and `web.<an operator input>`.

9. **First operator sign-in.** An operator listed in I3 opens `https://app.<an operator input>/` and signs in
   with GitHub (OAuth App of an operator input, callback `https://app.<an operator input>/api/auth/github/callback`).
   Expected: the console shows the operator area.

10. **Probe tenant** (monitoring, the design). On the core node that holds the Patroni leader,
    with the operator of step 9:

    ```sh
    sudo docker run --rm --network veristar-core_default \
      --env-file /etc/veristar/env/platformctl.env \
      -v /etc/veristar/tls/ca:/etc/sa-tls:ro \
      -e PLATFORM_DB_HOST=haproxy -e PLATFORM_DB_PORT=5000 -e PLATFORM_DB_NAME=platform \
      -e PLATFORM_DB_SSLMODE=verify-full -e PLATFORM_DB_SSLROOTCERT=/etc/sa-tls/ca.crt \
      ghcr.io/justmert/veristar/platformctl:<release> \
      probe-tenant create -operator-github-id <operator github id> -domain probe.<an operator input> -origin https://probe.<an operator input>
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
    "Probe" play gives Caddy for `status.<an operator input>`, Gatus, the prober and the Alertmanager
    watchdog; or, as for production, the Compose bundle of `playbooks/probe-bundle.yml` on a
    shared host with an edge Caddy (a separate Azure VM in East US, [probe.md](probe.md)). It needs the probe tenant's values of step 10
    in the SOPS file. Expected: `https://status.<an operator input>/` answers and every measure of
    probe.md section 4 receives results every 30 s. `make ops-local` exercises the same play
    locally.

12. **SLO collection.** Set the repository variable `STATUS_BASE_URL` to `https://status.<an operator input>`
    so `slo-report.yml` can collect ([../runbooks/slo-report.md](../runbooks/slo-report.md)).

13. **Verify** with [verification.md](verification.md).
