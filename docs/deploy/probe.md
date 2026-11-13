---
title: "The probe outside Azure, with an uptime monitor"
sidebarTitle: "Probe"
description: "The probe that checks the platform from outside its hosting, with a status page and alerts."
---

The prober (`cmd/prober`) is the outside view of the platform: every 30 s it sends a real web
event and a real OTLP metric for the internal probe tenant, reads them back through the machine
API, checks the console, the Umami host, the tracker files against the release manifest, and
every node's certificate by address. It must not run inside the system it watches, so it runs on
a host outside Azure. This document runs it on a probe host, beside the uptime monitor
that already alerts the operator, and makes that monitor alert on its results.

Nothing here changes the probe host by itself. The steps below are for the operator to run there.

## 1. What runs

`infra/ansible/playbooks/probe-bundle.yml` renders a self-contained Docker Compose bundle on the
controller, from the same templates the observability role uses:

| File | Holds |
|---|---|
| `compose.yml` | `gatus` (5.38.0, pinned by digest) and `prober` (the release image, pulled by its verified digest); hardened like the platform's own services (read-only root, no capabilities, `no-new-privileges`) |
| `gatus.yml` | Gatus's own checks of the public hostnames and the external endpoints the prober pushes to |
| `prober.env`, `gatus.env` | the probe tenant's OTLP token, machine API key and site id, the Gatus push token (mode 0600) |
| `releases/` | the tracker release manifest the prober compares the served files with |

Gatus is published only on the address in `PROBE_BIND`; nothing listens publicly. The uptime monitor reads
Gatus's per-check status API, one monitor per check, and the monitor's existing notification
delivers the alert.

## 2. Render the bundle (controller)

Needs the environment's inventory (`make <env>-apply`), its SOPS file with the probe tenant
(`platformctl probe-tenant create`, [bootstrap.md](bootstrap.md) step 10) and the release
manifest:

```sh
cd infra/ansible
~/.local/share/veristar/ansible-venv/bin/ansible-playbook -i inventories/production/ \
  playbooks/probe-bundle.yml -e sa_release_manifest=<release-images.json>
```

Expected: the release verification, then
`probe bundle for production in .../.scratch/probe-bundle/production`. The directory is mode
0700 under `.scratch/`, which Git ignores.

## 3. Run it on the probe host (owner)

Copy the directory and start it. The probe host's containers reach host services at `172.17.0.1`
(the Docker bridge gateway), so Gatus is published there on port 8095. Check
first that nothing else uses the port: `ssh <probe host> 'ss -ltn "( sport = :8095 )"'` must list no
socket; otherwise change `probe_bundle_port` (`-e probe_bundle_port=<port>`).

```sh
rsync -a .scratch/probe-bundle/production/ <probe host>:/opt/veristar-probe/
ssh <probe host> 'cd /opt/veristar-probe && PROBE_BIND=172.17.0.1 docker compose up -d'
ssh <probe host> 'curl -fsS http://172.17.0.1:8095/api/v1/endpoints/statuses | head -c 300'
```

Expected: both containers `Up`, and a JSON list of endpoints within a minute. The bundle is its
own Compose project (`veristar-probe-production`) beside the host's other Compose projects, so
the host's other services are not touched. The probe host resolves names through its own filtering resolver; the platform's
hostnames are under `.app`. On 2026-11-13 a filtering resolver answered a made-up `.app` name with no
address (not `0.0.0.0`), so the TLD is not blocked as a whole. Check the real names once they
exist: `dig +short in.veristar.app @192.0.2.53` must not answer `0.0.0.0`.

Backups: the bundle holds only configuration and Gatus's history; the host's backup job already
covers the bundle directory unless an exclude matches.

## 4. Add the uptime monitors (owner, the monitor's web UI)

One monitor per check. For each, in the uptime monitor: add a new monitor.

| Field | Value |
|---|---|
| Monitor Type | HTTP(s) - Json Query |
| Friendly Name | `veristar <key>` |
| URL | `http://172.17.0.1:8095/api/v1/endpoints/<key>/statuses?page=1&pageSize=1` |
| Json Query | `$toMillis(results[0].timestamp) > $millis() - 180000 and results[0].success` |
| Expected Value | `true` |
| Heartbeat Interval | 60 |
| Retries | 2 |
| Notifications | the operator's notification channel |

The query is true only when the newest result is younger than three minutes and passed, so a
stopped prober alerts too, not only a failed check. Gatus page 1 with `pageSize=1` holds exactly
the newest result (checked with Gatus 5.38.0 on 2026-11-13).

The keys (Gatus key of group and name):

| Key | Alerts when |
|---|---|
| `slo_ingestion` | a web event or OTLP metric is refused on the public path (99.99 % target) |
| `slo_dashboard` | the console, the Umami host or the machine API fails (99.90 % target) |
| `slo_freshness-web`, `slo_freshness-api` | accepted probe data is not visible within 10 minutes |
| `probe_tracker-integrity` | a served tracker file differs from the release manifest |
| `nodes_<node>-<host>` | one node fails for one hostname by address (for example `nodes_production-edge-1-in-veristar-app`); add the ten of the environment |

The full list is at `http://172.17.0.1:8095/api/v1/endpoints/statuses` (field `key`). The
monitor's own status page is not public, so these monitors are for the operator, not a public
status page.

## 5. Public status page and the alert heartbeat

The bundle has no public listener. `status.<BASE_DOMAIN>` (the status page) and the
Alertmanager watchdog push are served only when a probe host is in the inventory's `probe`
group, with its own public address and Caddy (`site.yml`, play `Probe`). With the probe on the
probe host, the status page can be published by a Cloudflare Tunnel route to Gatus, for example
through a tunnel or proxy on the probe host, as a separate decision of the operator: it is a
change to the probe host and to the Cloudflare account.

The nightly `slo-report.yml` workflow reads Gatus's results from `STATUS_BASE_URL`
(`https://status.<an operator input>`), so the monthly SLO report needs that publication too. Until then
the Gatus history on the probe host holds the measurements (about a week of raw results), and
the uptime monitor keeps its own history per check.

## 6. What was checked

- `ansible-playbook --syntax-check` and `make ansible-lint` pass for the playbook and template
  (2026-11-13).
- The Gatus API answers used by the monitor query, with Gatus 5.38.0 and three pushed results:
  `pageSize=1` returned only the newest (`success: false` after `true`, `true`, `false`).
- Not run yet: the bundle against a live environment (there is none before the first apply).
