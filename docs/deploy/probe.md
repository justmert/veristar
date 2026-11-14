---
title: "The probe outside the production deployment"
sidebarTitle: "Probe"
description: "The probe that checks the platform from outside its deployment, with the public status page."
---

The prober (`cmd/prober`) is the outside view of the platform: every 30 s it sends a real web
event and a real OTLP metric for the internal probe tenant, reads them back through the machine
API, checks the console, the Umami host, the tracker files against the release manifest, and
every node's certificate by address. It must not run inside the system it watches, so it runs on
its own machine, separate from the production nodes, their region and their availability zones.

## 1. Where it runs

| | |
|---|---|
| Host | a separate Azure VM of the operator in East US (United States), outside the production resource group and region (Denmark East) |
| Inventory | the host is in the inventory's `probe` group (tier `probe`), with its public address only; it has no address on the production private network |
| Converged by | `site.yml`, play `Probe`: the `base`, `secrets` and `observability` roles |
| Status (2026-11-14) | being set up; this page records the result once the host is converged |

The probe holds no analytics data. It sends synthetic traffic for the internal probe tenant
only, which is marked internal and hidden from operator views
([../compliance/data-residency.md](../compliance/data-residency.md)).

## 2. What runs on the probe host

| Service | Does |
|---|---|
| `prober` | the release image, pulled by its verified digest ([../security/supply-chain.md](../security/supply-chain.md) section 3); pushes one result per measure to Gatus every 30 s |
| Gatus | keeps every result, checks the public hostnames itself, and serves the status page |
| Caddy | the host's only public listener: `status.<BASE_DOMAIN>` (the public status page) with a Let's Encrypt certificate, plus the prober's and Gatus's metrics for the platform's Prometheus behind a bearer token (`probe.Caddyfile.j2`) |

The platform's Alertmanager pushes the watchdog heartbeat to the probe's Gatus every minute
(`rules/platform.yml`, `Watchdog`); a missing push is alerted like a failure, so a platform whose
alerting is broken is noticed from outside.

## 3. Converge the probe host

Needs the environment's inventory with the probe host in group `probe`, its SOPS file with the
probe tenant (`platformctl probe-tenant create`, [bootstrap.md](bootstrap.md) step 10), the
`status` DNS record pointing at the probe host, and the release manifest:

```sh
make production-deploy SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<release-images.json>
```

Expected: `failed=0` on the probe host, and `https://status.<BASE_DOMAIN>/` answers with the
status page within a minute. `make ops-local` converges the same play against a local probe
node and checks it end to end.

## 4. The measures

The keys (Gatus key of group and name):

| Key | Fails when |
|---|---|
| `slo_ingestion` | a web event or OTLP metric is refused on the public path (99.99 % target) |
| `slo_dashboard` | the console, the Umami host or the machine API fails (99.90 % target) |
| `slo_freshness-web`, `slo_freshness-api` | accepted probe data is not visible within 10 minutes |
| `probe_tracker-integrity` | a served tracker file differs from the release manifest |
| `nodes_<node>-<host>` | one node fails for one hostname by address (for example `nodes_production-node-1-in-veristar-app`), one per node and public hostname |
| `watchdog` | the platform's Alertmanager stopped pushing its heartbeat |

The full list is at `https://status.<BASE_DOMAIN>/api/v1/endpoints/statuses` (field `key`).

## 5. Alerts and the SLO report

Gatus delivers its alerts to the Discord webhook of plan an operator input when it is set. Input I8 is not
set, by operator decision, so the probe's failures are read on the status page, and the platform's
own alerts on the console's operator Alerts page ([../operators/alerts.md](../operators/alerts.md)).

The nightly `slo-report.yml` workflow reads Gatus's results from `STATUS_BASE_URL`
(`https://status.<an operator input>`) and runs once GitHub Actions is enabled, when the repository is
public. Until then Gatus on the probe host holds the measurements.

## 6. What was checked

- `ansible-playbook --syntax-check` and `make ansible-lint` pass for the `Probe` play
  (2026-11-13).
- `make ops-local` converges a probe node locally with the status page and the watchdog.
- Against production: pending the probe host's converge (section 1).
