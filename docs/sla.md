---
title: "Service targets"
description: "The operational targets for ingestion, dashboards and security patching, and how each one is measured."
---

Veristar has operational targets for ingestion, dashboards, security patching and support. This
page lists each target, how it is measured, and what the production design and drills put behind
it. Support targets and the issue tracker are on [support.md](support.md).

The service charges nothing, and these targets carry no credits or contractual remedy. The
tenant terms state the same (`docs/compliance/terms.md` section 7).

## Summary

| Target | Value | Allowed per 30-day month | Measured by |
|---|---|---|---|
| Ingestion availability | 99.99% | 4.32 min (259 s) | external prober, every 30 s |
| Dashboard availability | 99.90% | 43.2 min | external prober, every 30 s |
| Critical and high vulnerabilities | patched within 24 hours | | elapsed time from first signal to production, recorded in the finding's issue |
| Support response | within 5 business days | | [support.md](support.md) |
| Support resolution | within 10 business days | | [support.md](support.md) |

## How availability is measured

The prober (`cmd/prober`) runs on a separate virtual machine in Azure East US, outside the
production region, so an outage of the platform cannot also take down its measurement
([deploy/probe.md](deploy/probe.md)). Every 30 seconds it pushes one result per measure to Gatus
beside it, which keeps the history and publishes it on the public status page,
[status.veristar.app](https://status.veristar.app) (`status.<BASE_DOMAIN>` for any deployment).
The measures are listed in [reference/observability.md](reference/observability.md).

- **Ingestion** (`slo_ingestion`) is available in an interval when both sends succeeded: the
  gateway accepted the probe's web event with 202, and the collector accepted its OTLP metric
  with 200. Both are real sends through the public endpoints, for an internal probe tenant that
  is hidden from the operator views.
- **Dashboards** (`slo_dashboard`) is available when the console and an authenticated metrics
  API call (`/web/hourly` with the probe's scoped key, which reads Umami live) both answered.
  The web dashboard is a console page, so the console check covers its host.
- **Freshness** is reported separately and is not part of either percentage: probe events older
  than 10 minutes must be visible through the metrics API, for web and for API data.

Gatus keeps raw results for about a week (20,160 results per check at 30 s) and hourly uptime for
30 days, which is shorter than a month. So each day's raw results are copied into
`docs/slo/data/` as one JSON file per UTC day: by `sloreport watch` on the probe host every hour,
or by the scheduled workflow `slo-report.yml`. `cmd/sloreport report` then computes the month's
availability per measure from those files and writes `docs/slo/<YYYY-MM>.md`
([runbooks/slo-report.md](runbooks/slo-report.md)). The monthly report is the record of whether a
target was met.

A measure gets the verdict `met` only when the probe measured at least 95% of the month
(coverage). Below that floor the verdict is `not measured`, so a month with one day of clean probe
data does not read as compliant. A measure whose failed intervals alone exceed the downtime budget
is `missed` at any coverage, because unmeasured time cannot undo it. `TestVerdictCoverageFloor`
(`internal/sloreport`) pins these rules; `TestCollectThenReport` (`cmd/sloreport`) and the
`internal/sloreport` tests check the computation against raw data.

## How the 24-hour patch target is measured

The steps are in [runbooks/security-patch.md](runbooks/security-patch.md). The clock starts when
the first signal is filed, not when someone reads it:

- `security.yml` checks every hour for upstream advisories at the pinned versions (Umami and the
  Next.js and React it ships, NATS, Caddy, etcd, the collector modules, PostgreSQL), runs Trivy on
  the deployed image digests, govulncheck and `pnpm audit`, and opens one issue labelled
  `security-finding` per finding;
- private reports arrive through GitHub private vulnerability reporting;
- Dependabot opens update pull requests.

Each finding's issue is closed with the elapsed time from the first signal to the patched
production. The internal target for that path is 8 hours, which leaves 16 hours of margin.

## Hosting

Production runs on Microsoft Azure in Denmark East, with Cloudflare DNS. The providers, the
monthly cost and the billing requirements are in [deploy/prerequisites.md](deploy/prerequisites.md).

## What stands behind the targets

Production runs in Azure Denmark East on three converged nodes, one in each of availability zones
1, 2 and 3, behind Traffic Manager health-checked DNS. The converge, the checks and the recovery
drills are in [reference/drills/2026-11-13-azure.md](reference/drills/2026-11-13-azure.md).

### Availability

The availability statement with the arithmetic is
[reference/availability.md](reference/availability.md). Every node runs the ingestion edge and
the core: Caddy, the gateway and the collector accept events and metrics into a replicated queue
(NATS JetStream, three replicas, one per zone) and answer without touching PostgreSQL or Umami.
The queue holds web events for 72 hours, so a full outage of the database or of Umami costs the
dashboards, not ingestion. PostgreSQL runs as three Patroni members, one per zone.

Windows that follow from the configuration:

| Failure | Ingestion | Dashboards |
|---|---|---|
| One node or zone lost | requests to it fail until Traffic Manager drops it: probe every 10 s, three failures, 10 s answer TTL, up to about 45 s; a browser that cannot connect tries the next address of the same answer | up to about 130 s for clients of that address (30 s probing) |
| Database leader lost | none | at most about 40 s until promotion |
| Database or Umami down on every node | none, queued for up to 72 h | down for the whole outage |
| Full NATS restart | refused (503) until the queue has quorum again; visitors of that day counted again | none |

With those windows the 99.99% ingestion budget of 259 s a month absorbs five node losses a month,
and the 99.90% dashboard budget of 2,592 s absorbs 19 node losses, 64 database failovers or one
43-minute database outage.

Measured on production ([reference/drills/2026-11-13-azure.md](reference/drills/2026-11-13-azure.md)):

| Drill | Result |
|---|---|
| Node powered off for 300 s, 4 page views a second with HTTP keep-alive | 3,341 events sent, 3,341 acknowledged, none answered 503, none lost; the node left the `in` answer 41.8 s after the power-off; the two other nodes stayed in the answer throughout |
| Database leader powered off | writes resumed through HAProxy 30.3 s after the power-off, on the new leader; the newest committed row was present; the old leader streamed again as a replica 64 s after its start |
| Point-in-time restore on a separate restore node | promoted at the target time, both databases present, the newest row matching the running cluster |

The gateway capacity of a production node, measured with release v0.2.1, is in
[reference/gateway-limits.md](reference/gateway-limits.md), "Capacity".

### Dependencies

From [reference/availability.md](reference/availability.md), "Dependencies the targets rest on":
one Azure region with three availability zones (the loss of any one zone is tolerated), Traffic
Manager and Cloudflare for DNS failover, resolver caching (MultiValue answers carry every healthy
address), the port-80 health check that does not see TLS failures (the prober checks every node's
certificate and `CertificateExpiringSoon` fires 14 days ahead), the web event drain rate of two
Umami processes per node (bursts queue and drain after), three database members, and billing.

### Monitoring

50 alert rules (plus 6 recording rules) with promtool tests, the prober, the status page and the
SLO report make up the monitoring baseline. In the alert fire-drills 16 alerts fired and resolved,
and 3 more fired for real without a drill ([runbooks/alerts.md](runbooks/alerts.md),
"Fire-drills"). On production the observability role runs on every node, and Prometheus has 43
active targets, all `up`. Firing alerts are listed on the console's operator Alerts page
([operators/alerts.md](operators/alerts.md)), the alert channel; a push receiver can be added
([runbooks/alerts.md](runbooks/alerts.md), "Adding a receiver"). The prober and the status page
run on the probe host in Azure East US ([deploy/probe.md](deploy/probe.md)).

### Patching

A full upgrade of Umami from 3.4.1 to 3.5.0 was rehearsed on 2026-11-13: from detection to every
test gate green took 3 h 18 min ([runbooks/security-patch.md](runbooks/security-patch.md)
section 7). Rolling deploys ran on production on 2026-11-14, among them the release v0.2.1, with
`failed=0` on every node. Releases are signed with cosign, and every playbook that pulls an image
verifies its signature first ([security/supply-chain.md](security/supply-chain.md)).
