---
title: "The external probe and status page in Azure East US"
sidebarTitle: "Probe"
description: "The probe that checks the platform from outside its region, with the public status page on status.veristar.app."
---

The prober (`cmd/prober`) is the outside view of the platform: every 30 s it sends a real web
event and a real OTLP metric for the internal probe tenant, reads them back through the machine
API, checks the console, the Umami host, the tracker files against the release manifest, and
every node's certificate by address. It must not run inside the system it watches, so it runs
on a separate Azure VM in **East US**, outside the production region (Denmark East) and outside
the production resource group. Gatus on the same VM keeps the results and serves the public
status page at **https://status.veristar.app**.

The VM is a separate host (4 vCPU, 31 GB, x86_64, public address
`192.0.2.10`). The probe is its own Compose project there
and shares nothing with the host's other sites except its one edge Caddy.

## 1. What runs where

| Where | What |
|---|---|
| `/opt/veristar-probe` (owner `probe`, mode 0700), Compose project `veristar-probe` | `veristar-probe-gatus` (Gatus 5.38.0, pinned by digest), `veristar-probe-prober` (the release's prober, pinned by its verified digest), `veristar-probe-sloreport` (`cmd/sloreport watch` on the distroless static base, pinned by digest) |
| `/opt/veristar-probe/gatus.yml`, `gatus-data/` | the checks and their history (SQLite, about one week of raw results); `gatus-data/` is owned by uid 8080, Gatus's container user |
| `/opt/veristar-probe/prober.env`, `gatus.env` | the probe tenant's OTLP token, machine API key and site id, the Gatus push token (mode 0600) |
| `/opt/veristar-probe/slo-data/` | one file per ended UTC day with the SLO results (section 6) |
| `/opt/veristar-probe/.docker/` | the registry login for `ghcr.io` with a read-only pull token (mode 0600), used only by this project |
| `/opt/edge/sites/veristar.caddy` (mode 0600) | the one site file of the host's edge Caddy for `status.veristar.app`: TLS, security headers, the routes below |

No container of the project publishes a port. The host's edge Caddy owns ports 80 and 443,
gets the certificate from Let's Encrypt (HTTP-01) and reaches `veristar-probe-gatus` and
`veristar-probe-prober` by name over the host's external Docker network `edge`. The prober and
the collector reach Gatus over the project's own network. Every container runs with a read-only
root file system, no capabilities, `no-new-privileges` and a memory limit.

Routes of `status.veristar.app` (template `probe-bundle-site.caddy.j2`):

| Path | Answer |
|---|---|
| `/` and Gatus's API | Gatus, read-only |
| `/_veristar/brand/mark.svg`, `/_veristar/brand/favicon.svg` | Veristar's mark and favicon, inline in the site file |
| `/internal/prober/metrics`, `/internal/gatus/metrics` | the prober's and Gatus's metrics, only with `Authorization: Bearer <probe_metrics_token>`; 404 otherwise |
| `/api/v1/endpoints/*/external` | 404: results are pushed only by the prober on the project network |
| `/api/v1/endpoints/monitoring_watchdog/external` | reserved for the Alertmanager watchdog from the core nodes' addresses; present only when the core nodes are wired to the probe (section 7) |

Headers on every answer: `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `Cross-Origin-Opener-Policy`,
`Permissions-Policy` and a `Content-Security-Policy` with `default-src 'none'`. Gatus sends none
of its own. Its page has one inline script (`window.config`, rendered from the `ui` block of
`gatus.yml`), allowed by its SHA-256 hash in `probe_bundle_csp_script_hashes`
(`playbooks/probe-bundle.yml`). A change of the `ui` block or of the Gatus version changes the
hash; measure it again (section 4) or the page stays blank.

## 2. Render the bundle (controller)

Needs the production inventory, the SOPS file with the probe tenant (`platformctl probe-tenant
create`, [bootstrap.md](bootstrap.md) step 10), the release manifest and the `sloreport` binary
of the checkout (`make build`):

```sh
make build
cd infra/ansible
~/.local/share/veristar/ansible-venv/bin/ansible-playbook -i inventories/production/ \
  playbooks/probe-bundle.yml -e sa_image_tag=<release> -e sa_release_manifest=<release-images.json>
```

The playbook first verifies every image of the release with the committed release key
(`verify-release.yml`, cosign, no transparency log while the repository is private), then writes
`.scratch/probe-bundle/production/` (mode 0700, ignored by Git): `compose.yml`, `gatus.yml`,
`prober.env`, `gatus.env`, `veristar.caddy`, `releases/` (the tracker release manifest the
prober compares the served files with) and `bin/sloreport`. Expected: `probe bundle for
production in .../.scratch/probe-bundle/production`.

## 3. Install or update on the host

The host is reached over SSH (`ssh probe-host`). Secrets travel only
over SSH standard input and are never on a command line.

```sh
# copy the bundle (the first time also creates the directories)
tar -C .scratch/probe-bundle/production -cf - . | ssh probe-host "
  sudo install -d -o probe -g probe -m 0700 /opt/veristar-probe &&
  tar -C /opt/veristar-probe -xf - &&
  sudo install -d -o 8080 -g 8080 -m 0700 /opt/veristar-probe/gatus-data &&
  install -d -m 0700 /opt/veristar-probe/.docker"

# registry login with the read-only pull token (.scratch/secrets/ghcr.env)
. .scratch/secrets/ghcr.env
printf '%s' "$GHCR_PULL_TOKEN" | ssh probe-host \
  "DOCKER_CONFIG=/opt/veristar-probe/.docker docker login ghcr.io -u justmert --password-stdin"

# verify the prober's digest with the release key, then pull by digest and start
cosign verify --key infra/ansible/vars/release-cosign.pub --insecure-ignore-tlog \
  ghcr.io/justmert/veristar/prober@<digest of release-images.json>
ssh probe-host "cd /opt/veristar-probe &&
  DOCKER_CONFIG=/opt/veristar-probe/.docker docker compose pull && docker compose up -d"
```

Then the site file, under the host's rule for its shared edge: validate before any reload,
remove the file and stop if validation fails, reload (never restart) the edge Caddy, and check
that every other site on the host answers as before.

```sh
ssh probe-host "install -m 0600 /opt/veristar-probe/veristar.caddy /opt/edge/sites/veristar.caddy &&
  docker compose -f /opt/edge/compose.yaml exec -T caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile ||
  rm -f /opt/edge/sites/veristar.caddy"
ssh probe-host "docker compose -f /opt/edge/compose.yaml exec -T caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile"
```

Expected: the three containers `Up`, `https://status.veristar.app/` answers 200 with a Let's
Encrypt certificate, and every check is green within a minute.

An update of the bundle only (a new release or a changed check) copies the changed files and runs
`docker compose up -d`; Gatus keeps its history in `gatus-data/`. A changed site file goes
through validate and reload again.

## 4. What the status page shows

Only public checks and the prober's end-to-end results, by Gatus group:

| Group | Checks |
|---|---|
| `hosts` (Gatus itself) | `in host` and `tracker file` (`in.veristar.app`, the latest tracker file and its size), `otlp host` (`otlp.veristar.app` refuses a request without a token with 401), `console` (`app.veristar.app`), `umami host` (`web.veristar.app/api/heartbeat`), `docs` (`docs.veristar.app`); certificates valid for at least 14 days |
| `slo` (prober) | `ingestion`, `dashboard`, `freshness web`, `freshness api`: the measures of the monthly SLO report |
| `probe` (prober) | `web event`, `otlp metric`, `console`, `web host`, `machine api`, `tracker integrity` (every served tracker file against the release manifest) |
| `nodes` (prober) | each node for each host name it serves, by its address with SNI: the certificate and the answer |

Each prober check is a Gatus external endpoint with a 2 minute heartbeat, so a stopped prober
turns its checks red as well. The page carries Veristar's name, mark and favicon and the brand's
dark palette in both of Gatus's modes (the `ui` block of `templates/gatus.yml.j2`). It names no
internal address and nothing else on the host.

To measure the inline script hash after a `ui` change:

```sh
curl -s https://status.veristar.app/ | python3 -c 'import sys,re,hashlib,base64
for s in re.findall(r"<script>(.*?)</script>", sys.stdin.read(), re.S):
    print("sha256-" + base64.b64encode(hashlib.sha256(s.encode()).digest()).decode())'
```

## 5. DNS

`status.veristar.app` is an A record to `192.0.2.10` in the Cloudflare zone, DNS only (not
proxied), TTL 300, so the edge Caddy obtains the certificate itself. The zone's CAA records
(`issue letsencrypt.org`, `issuewild ;`) allow that certificate. In code it is
`a_records = { status = "192.0.2.10" }` of `module "dns"` in
`infra/tofu/envs/production/main.tf` (resource `module.dns.cloudflare_dns_record.a["status"]`).

The record was created on 2026-11-14 through the Cloudflare API with exactly those attributes,
because the production state was then held by a separate infrastructure change. Before the next
production plan from this code, it is adopted into the state on an operator workstation (an `import` block
cannot stay in the code: `tofu test` refuses to import):

```sh
tofu -chdir=infra/tofu/envs/production import \
  'module.dns.cloudflare_dns_record.a["status"]' \
  dafaf68bd1781f17392e66ccd2a3c403/5c0941abaf2763b1a2e692608142d3a1
```

Expected: `Import successful!`, and the following `make production-plan` shows no change for
the record.

## 6. Where the monthly availability numbers come from

The prober's four `slo_*` results are the measurements of the SLO targets (99.99 % ingestion,
99.90 % dashboards, [../sla.md](../sla.md)). Gatus keeps raw results for about a week, so
`veristar-probe-sloreport` (`sloreport watch`) copies them out: every hour it looks at the last
six ended UTC days and writes each day that has results and no file yet to
`/opt/veristar-probe/slo-data/<YYYY-MM-DD>.json`, reading the public page
`https://status.veristar.app`, the same source a collection by hand uses. A day without any
result (before the probe started, or a complete probe outage) gets no file, so the report lists
it as missing and never as available.

Each month the operator copies the files into the repository and writes the report
([../runbooks/slo-report.md](../runbooks/slo-report.md)):

```sh
ssh probe-host "tar -C /opt/veristar-probe/slo-data -cf - ." | tar -C docs/slo/data -xf -
dist/bin/sloreport report -month <YYYY-MM> -data docs/slo/data -out docs/slo
```

The scheduled GitHub workflow `slo-report.yml` is not used while GitHub Actions is off; the
collector on the probe host replaces its daily step.

## 7. Prometheus and the Alertmanager watchdog

The design also lets Prometheus on the core nodes scrape the prober's and Gatus's metrics (the
alert rules `ProbeIngestionFailing`, `ProbeFreshnessFailing`, `ProbeDashboardFailing`,
`ProbeNodeCheckFailing` and `CertificateExpiringSoon` read them) and lets Alertmanager push its
`Watchdog` heartbeat to Gatus. For a probe outside the inventory this is
`observability_probe_external: true` in `inventories/production/group_vars/all.yml`. The status
page already serves both metrics paths behind the probe metrics token (checked on 2026-11-14:
200 with the token, 404 without). To turn it on:

1. set `observability_probe_external: true` and converge the core nodes (`site.yml`, play
   Observability), which adds the `prober` and `gatus` scrape jobs on `status.veristar.app` and
   the `heartbeat` webhook receiver;
2. render the bundle again (it then adds the `monitoring watchdog` check and its route for the
   core nodes' addresses), copy `gatus.yml` and `veristar.caddy`, `docker compose up -d`, and
   validate and reload the edge Caddy as in section 3;
3. check that Prometheus lists both jobs `up` and that `monitoring_watchdog` is green.

Until then the probe's alerts are Gatus's red checks on the status page; with an operator input unset
there is no push receiver either way.

Production has it on since 2026-11-14 (release `v0.2.1`): both jobs `up`, `monitoring_watchdog`
green ([../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md),
"Release v0.2.1 rollout and gateway capacity").

## 8. What was checked (2026-11-14)

The deployment record is in
[../reference/drills/2026-11-13-azure.md](../reference/drills/2026-11-13-azure.md), section
"External probe and status page". In short: release `v0.1.0`, prober digest
`sha256:84fa251c...0da72` verified with the release key; the status page answers 200 with a Let's
Encrypt certificate; all 28 checks green with real end-to-end successes; a deliberate failure of
the `docs` check turned red and back; the other sites on the host answered with the same status
codes and certificates before and after the edge reload.

## 9. Rotate the probe tenant's OTLP token

`platformctl probe-tenant rotate-otlp-token` revokes the old token in the same transaction that
creates the new one, and the control plane writes both to the token registry within seconds, so
the prober fails its OTLP checks until it runs with the new value. Have every step ready before
you rotate:

1. On the core node that holds the Patroni leader, run the release's `platformctl` with the
   flags of [bootstrap.md](bootstrap.md) step 10 and the argument `probe-tenant
   rotate-otlp-token`. It prints `otlp_token=` once and `revoked=` with the number of revoked
   tokens. Keep the value in a shell variable; do not print it.
2. Write it into the SOPS file as `probe_otlp_token`
   (`sops set --value-stdin infra/secrets/production/secrets.enc.yaml '["probe_otlp_token"]'`
   with the JSON-encoded value on standard input).
3. Render the bundle (section 2) and copy only `prober.env` to `/opt/veristar-probe/prober.env`
   on the host, mode 0600, over SSH standard input.
4. Recreate the prober so it reads the new file:
   `docker compose up -d prober` in `/opt/veristar-probe`. `docker compose restart prober` is
   not enough: a restart keeps the environment the container was created with.

Expected: `probe_otlp-metric` and `slo_ingestion` are green at the next 30 s round.
