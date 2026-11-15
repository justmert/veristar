---
title: "The external probe and status page"
sidebarTitle: "Probe"
description: "Install the prober and the public status page on a host outside the platform, behind that host's edge Caddy."
---

The prober (`cmd/prober`) is the outside view of the platform. Every 30 s it sends a real web
event and a real OTLP metric for the internal probe tenant, reads them back through the machine
API, and checks the console, the Umami host, the tracker files against the release manifest and
every node's certificate by address. It runs outside the system it watches. Gatus, on the same
host, keeps the results and serves the public status page on `status.<BASE_DOMAIN>`.

You have two ways to run it ([bootstrap.md](bootstrap.md) step 11):

- a dedicated host in the inventory's `probe` group, converged by `site.yml`, play "Probe";
- the Compose bundle of `playbooks/probe-bundle.yml` on a shared host that already runs one edge
  Caddy on a Docker network. This page describes the bundle.

Production uses the bundle on a separate virtual machine in Azure East US, outside the production
region and resource group. That host's edge Caddy serves several sites; the probe adds one site
file to it. The production status page is `https://status.veristar.app`.

## 1. What runs where

The paths below use `/opt/veristar-probe` for the bundle directory and `<edge dir>` for the
directory of the host's edge Caddy. Use whatever paths your host has.

| Where | What |
|---|---|
| `/opt/veristar-probe`, Compose project `veristar-probe` | `veristar-probe-gatus` (Gatus, pinned by digest), `veristar-probe-prober` (the release's prober, pinned by its verified digest), `veristar-probe-sloreport` (`cmd/sloreport watch` on the distroless static base, pinned by digest) |
| `gatus.yml`, `gatus-data/` | the checks and their history (SQLite, about one week of raw results); `gatus-data/` is owned by uid 8080, Gatus's container user |
| `prober.env`, `gatus.env` | the probe tenant's OTLP token, machine API key and site id, and the Gatus push token (mode 0600) |
| `slo-data/` | one file per ended UTC day with the SLO results (section 6), written as uid 1000 |
| `releases/` | the tracker release manifest the prober compares the served files with |
| `bin/sloreport` | the static `sloreport` binary of your checkout |
| `<edge dir>/sites/veristar.caddy` (mode 0600) | the one site file for `status.<BASE_DOMAIN>`: TLS, security headers and the routes below |

No container of the project publishes a port. The host's edge Caddy owns ports 80 and 443, gets
the certificate from Let's Encrypt (HTTP-01) and reaches `veristar-probe-gatus` and
`veristar-probe-prober` by name over the host's external Docker network `edge`. The prober and
the collector reach Gatus over the project's own network. Every container runs with a read-only
root file system, no capabilities, `no-new-privileges` and a memory limit.

The playbook variables `probe_bundle_edge_network` (default `edge`), `probe_bundle_site_file`
(`veristar.caddy`) and `probe_bundle_uid` (`1000`, the uid that writes `slo-data/`) match the bundle to
your host. Pass them with `-e` when your host differs.

Routes of `status.<BASE_DOMAIN>` (template
`infra/ansible/roles/observability/templates/probe-bundle-site.caddy.j2`):

| Path | Answer |
|---|---|
| `/` and Gatus's API | Gatus, read-only |
| `/_veristar/brand/mark.svg`, `/_veristar/brand/favicon.svg` | Veristar's mark and favicon, inline in the site file |
| `/internal/prober/metrics`, `/internal/gatus/metrics` | the prober's and Gatus's metrics, only with `Authorization: Bearer <probe_metrics_token>`; 404 otherwise |
| `/api/v1/endpoints/*/external` | 404: only the prober pushes results, over the project network |
| `/api/v1/endpoints/monitoring_watchdog/external` | the Alertmanager watchdog, from the core nodes' public addresses only; present when the core nodes are wired to the probe (section 7) |

Headers on every answer: `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `Cross-Origin-Opener-Policy`,
`Permissions-Policy` and a `Content-Security-Policy` with `default-src 'none'`. Gatus sends none
of its own. Its page has one inline script (`window.config`, rendered from the `ui` block of
`gatus.yml`), allowed by its SHA-256 hash in `probe_bundle_csp_script_hashes`
(`playbooks/probe-bundle.yml`). A change of the `ui` block or of the Gatus version changes the
hash; measure it again (section 4), or the page stays blank.

## 2. Render the bundle (controller)

You need the environment's inventory, the SOPS file with the probe tenant's values
(`platformctl probe-tenant create`, [bootstrap.md](bootstrap.md) step 10), the release manifest
and the `sloreport` binary of the checkout:

```sh
make build
cd infra/ansible
~/.local/share/veristar/ansible-venv/bin/ansible-playbook -i inventories/<env>/ \
  playbooks/probe-bundle.yml -e sa_image_tag=<release> -e sa_release_manifest=<release-images.json>
```

The playbook first verifies every image of the release (`verify-release.yml`, the same check as
every deploy, [../security/supply-chain.md](../security/supply-chain.md)), so the compose file
pulls only verified digests. It touches no server. It writes the bundle to
`.scratch/probe-bundle/<env>/` at the repository root (mode 0700, ignored by Git): `compose.yml`,
`gatus.yml`, `prober.env`, `gatus.env`, `veristar.caddy`, `releases/`, `bin/sloreport` and an
empty `slo-data/`.

Expected: the last task prints `probe bundle for <env> in .../.scratch/probe-bundle/<env>`.

## 3. Install or update on the host

Copy the bundle over SSH, so the secrets in it never appear on a command line. From the
repository root:

```sh
tar -C .scratch/probe-bundle/<env> -cf - . | ssh <probe host> '
  sudo install -d -o "$(id -u)" -g "$(id -g)" -m 0700 /opt/veristar-probe &&
  tar -C /opt/veristar-probe -xf - &&
  sudo install -d -o 8080 -g 8080 -m 0700 /opt/veristar-probe/gatus-data'
```

The SSH user on the host owns the bundle directory; its uid matches `probe_bundle_uid`. The image
packages are public, so the host pulls without signing in to the registry. A host that signed in
under an earlier version of this procedure still holds that credential in
`/opt/veristar-probe/.docker`; remove it:

```sh
ssh <probe host> 'rm -rf /opt/veristar-probe/.docker'
```

Start the project:

```sh
ssh <probe host> 'cd /opt/veristar-probe && docker compose pull && docker compose up -d'
```

Then add the site file to the edge Caddy. Validate before you reload, and remove the file if
validation fails, so a broken file never reaches the other sites on the host. Reload the edge
Caddy; do not restart it.

```sh
ssh <probe host> 'install -m 0600 /opt/veristar-probe/veristar.caddy <edge dir>/sites/veristar.caddy &&
  docker compose -f <edge dir>/compose.yaml exec -T caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile ||
  { rm -f <edge dir>/sites/veristar.caddy; exit 1; }'
ssh <probe host> 'docker compose -f <edge dir>/compose.yaml exec -T caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile'
```

The edge Caddy's Caddyfile imports the files in its `sites/` directory and its container is on
the Docker network `edge`. Before and after the reload, record the status code and certificate
end date of every other site on the host; they stay the same.

**Updates.** For a new release or a changed check, render the bundle again, copy the changed
files and run `docker compose up -d` in `/opt/veristar-probe`. Gatus keeps its history in
`gatus-data/`. A changed site file goes through validate and reload again.

## 4. What the status page shows

Only public checks and the prober's end-to-end results, by Gatus group:

| Group | Checks |
|---|---|
| `hosts` (Gatus itself) | `in host` and `tracker file` (`in.<BASE_DOMAIN>`, the latest tracker file and its size), `otlp host` (`otlp.<BASE_DOMAIN>` refuses a request without a token with 401), `console` (`app.<BASE_DOMAIN>`), `umami host` (`web.<BASE_DOMAIN>/api/heartbeat`), `docs` (when `observability_probe_docs_url` is set); each certificate valid for at least 14 days |
| `slo` (prober) | `ingestion`, `dashboard`, `freshness web`, `freshness api`: the measures of the monthly SLO report |
| `probe` (prober) | `web event`, `otlp metric`, `console`, `web host`, `machine api`, `tracker integrity` (every served tracker file against the release manifest) |
| `nodes` (prober) | each node for each host name it serves, by its address with SNI: the certificate and the answer |
| `monitoring` | `watchdog`, the Alertmanager heartbeat (section 7) |

Each prober check is a Gatus external endpoint with a 2 minute heartbeat, so a stopped prober
turns its checks red as well. Gatus keys join the group and the name, for example
`slo_ingestion` or `probe_otlp-metric`. The page carries Veristar's name, mark, favicon and dark
palette in both of Gatus's modes (the `ui` block of `templates/gatus.yml.j2`). It names no
internal address and nothing else on the host.

To measure the inline script hash after a `ui` change:

```sh
curl -s https://status.<BASE_DOMAIN>/ | python3 -c 'import sys,re,hashlib,base64
for s in re.findall(r"<script>(.*?)</script>", sys.stdin.read(), re.S):
    print("sha256-" + base64.b64encode(hashlib.sha256(s.encode()).digest()).decode())'
```

Put the printed value into `probe_bundle_csp_script_hashes` and render the bundle again.

## 5. DNS

`status.<BASE_DOMAIN>` is an A record to the probe host's public address in the Cloudflare zone,
DNS only (not proxied), TTL 300, so the edge Caddy obtains the certificate itself. The zone's CAA
records (`issue letsencrypt.org`, `issuewild ;`) allow that certificate. In code it is the
`a_records = { status = "<probe host address>" }` argument of `module "dns"` in
`infra/tofu/envs/<env>/main.tf`, applied with the rest of the environment
([opentofu.md](opentofu.md)). The inventory module adds the name to `sa_hostnames`, which the
bundle's templates read.

## 6. Where the monthly availability numbers come from

The prober's four `slo_*` results measure the SLO targets (99.99 % ingestion, 99.90 %
dashboards, [../sla.md](../sla.md)). Gatus keeps raw results for about a week, so
`veristar-probe-sloreport` (`sloreport watch`) copies them out. Every hour it looks at the last
six ended UTC days and writes each day that has results and no file yet to
`/opt/veristar-probe/slo-data/<YYYY-MM-DD>.json`. It reads the public page
`https://status.<BASE_DOMAIN>`, the same source a collection by hand uses. A day without any
result gets no file, so the report lists it as missing and never as available.

Each month, copy the files into the repository and write the report
([../runbooks/slo-report.md](../runbooks/slo-report.md)):

```sh
ssh <probe host> 'tar -C /opt/veristar-probe/slo-data -cf - .' | tar -C docs/slo/data -xf -
dist/bin/sloreport report -month <YYYY-MM> -data docs/slo/data -out docs/slo
```

The scheduled workflow `slo-report.yml` is the other way to collect the same days; the runbook
describes both.

## 7. Prometheus and the Alertmanager watchdog

Prometheus on the core nodes scrapes the prober's and Gatus's metrics through the status page
(the alert rules `ProbeIngestionFailing`, `ProbeFreshnessFailing`, `ProbeDashboardFailing`,
`ProbeNodeCheckFailing` and `CertificateExpiringSoon` read them), and Alertmanager pushes its
`Watchdog` heartbeat to Gatus. For a probe outside the inventory, this is
`observability_probe_external: true` in `inventories/<env>/group_vars/all.yml`. Production has it
set. To turn it on:

1. Set `observability_probe_external: true` and converge the core nodes (`site.yml`, play
   "Observability"). This adds the `prober` and `gatus` scrape jobs on `status.<BASE_DOMAIN>` and
   the `heartbeat` webhook receiver.
2. Render the bundle again (section 2). It then adds the `watchdog` check and its route for the
   core nodes' addresses. Copy `gatus.yml` and `veristar.caddy`, run `docker compose up -d` and
   `docker compose restart gatus` in `/opt/veristar-probe` (Gatus does not reload a changed
   `gatus.yml` on its own), then validate and reload the edge Caddy as in section 3.
3. Check that Prometheus lists both jobs `up` and that `monitoring_watchdog` is green.

## 8. Verify

After an install or an update:

```sh
ssh <probe host> 'cd /opt/veristar-probe && docker compose ps'
curl -sI https://status.<BASE_DOMAIN>/ | head -n 1
curl -s https://status.<BASE_DOMAIN>/api/v1/endpoints/statuses \
  | python3 -c 'import json,sys; print([e["key"] for e in json.load(sys.stdin) if e["key"].startswith("slo_")])'
curl -s -o /dev/null -w '%{http_code}\n' https://status.<BASE_DOMAIN>/internal/prober/metrics
```

Expected:

- the three containers are `Up`;
- the status page answers `200` with a Let's Encrypt certificate;
- the four `slo_` keys are listed and every check turns green within a minute;
- the metrics path answers `404` without the token.

Every other site on the host answers with the status codes and certificates you recorded before
the reload. The production record of these checks is in
[../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), section
"External probe and status page".

## 9. Rotate the probe tenant's OTLP token

`platformctl probe-tenant rotate-otlp-token` revokes the old token in the same transaction that
creates the new one, and the control plane writes both to the token registry within seconds. The
prober fails its OTLP checks until it runs with the new value, so have every step ready before
you rotate:

1. On the core node that holds the Patroni leader, run the release's `platformctl` with the
   flags of [bootstrap.md](bootstrap.md) step 10 and the argument `probe-tenant
   rotate-otlp-token`. It prints `otlp_token=` once and `revoked=` with the number of revoked
   tokens. Keep the value in a shell variable; do not print it.
2. Write it into the SOPS file as `probe_otlp_token`
   (`sops set --value-stdin infra/secrets/<env>/secrets.enc.yaml '["probe_otlp_token"]'`
   with the JSON-encoded value on standard input).
3. Render the bundle (section 2) and copy only `prober.env` to `/opt/veristar-probe/prober.env`
   on the host, mode 0600, over SSH standard input.
4. Recreate the prober so it reads the new file: `docker compose up -d prober` in
   `/opt/veristar-probe`. `docker compose restart prober` is not enough, because a restart keeps
   the environment the container was created with.

Expected: `probe_otlp-metric` and `slo_ingestion` are green at the next 30 s round.
