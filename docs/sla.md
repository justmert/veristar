# Service targets

The operational targets the RFP sets for the 12-month service window (RFP 3.4), how each is
measured, and what stands behind them today. Support response and resolution targets are in
[support.md](support.md).

SCF has no formal SLA enforcement process (RFP section 6), and the service charges nothing, so
these are commitments of effort and transparency, not contractual guarantees with credits.
The tenant terms say the same (`docs/compliance/terms.md` section 7).

## Summary

| Target | Value | Allowed per 30-day month | Measured by |
|---|---|---|---|
| Ingestion availability | 99.99% | 4.32 min (259 s) | external prober, every 30 s |
| Dashboard availability | 99.90% | 43.2 min | external prober, every 30 s |
| Critical and high vulnerabilities | patched within 24 hours | | elapsed time from first signal to production, recorded in the finding's issue |
| Support response | within 5 business days | | [support.md](support.md) |
| Support resolution | within 10 business days | | [support.md](support.md) |

## How availability is measured

The `prober` is designed to run on its own small server at a different provider (Scaleway,
`fr-par`) from the system it watches (Hetzner), so a provider-wide outage of the platform
cannot also take down its measurement (that server is not yet created, an operator input. Every 30 seconds it pushes one result per measure to the Gatus status
page at `status.<BASE_DOMAIN>` (`docs/reference/observability.md`, Measures).

- **Ingestion** (`slo_ingestion`) is available in an interval when both answered: the gateway
  accepted the probe's web event with 202, and the collector accepted its OTLP metric with
  200. Both are real sends through the public endpoints, using an internal probe tenant that
  is hidden from the operator views.
- **Dashboards** (`slo_dashboard`) is available when the console, the web dashboard host and
  an authenticated metrics API call (`/web/hourly` with the probe's scoped key) all answered.
- **Freshness** is reported separately and is not part of either percentage: probe events
  older than 10 minutes must be visible through the metrics API, for web and for API data.

Gatus keeps raw results for about a week and hourly uptime for 30 days, which is shorter than
a month. So the workflow `slo-report.yml` copies each day's raw results into `docs/slo/data/`
every night, and on the first day of a month `cmd/sloreport` computes the month's
availability per measure from those files and opens a pull request with `docs/slo/<YYYY-MM>.md`.
The monthly report is the record of whether a target was met. A measure gets a verdict of
`met` only when the probe measured at least 95% of the covered range (coverage). Below that
floor the verdict is `not measured`, so a month with one day of clean probe data does not read
as compliant. The one exception is a measure whose failed intervals alone already exceed the
downtime budget: that is `missed` at any coverage, because the unmeasured time cannot undo it.
`TestVerdictCoverageFloor` (`internal/sloreport`) pins these rules. `TestCollectThenReport`
(`cmd/sloreport`) and the `internal/sloreport` tests check the computation against raw data.

## How the 24-hour patch target is measured

The steps are in [runbooks/security-patch.md](runbooks/security-patch.md). The clock starts
when the first signal is filed, not when someone reads it:

- `security.yml` checks every hour for upstream advisories at the pinned versions (Umami and
  the Next.js and React it ships, NATS, Caddy, etcd, the collector modules, PostgreSQL), runs
  Trivy on the deployed image digests, govulncheck and `pnpm audit`, and opens one issue
  labelled `security-finding` per finding;
- private reports arrive through GitHub private vulnerability reporting;
- Dependabot opens update pull requests.

The issue of each finding is closed with the elapsed time from the first signal to the
patched production. The internal target is 8 hours, leaving 16 hours of margin.

## Hosting for the service window

The providers, the monthly cost per environment (about 191 EUR plus USD 1 for production, read
2026-11-13) and the billing requirements for the 12 months of hosting are in
[deploy/prerequisites.md](deploy/prerequisites.md), "Hosting". Who holds the accounts and pays is
an open question to SCF and is not decided.

## What stands behind the targets today

Stated plainly, as of 2026-11-13. No production or staging environment exists yet: the domain,
cloud and DNS accounts are inputs that have not been supplied (plan inputs I4, I5, I6). The
targets are designed for, not yet demonstrated.

### Availability

The availability statement with the arithmetic is
[reference/availability.md](reference/availability.md). The design keeps ingestion separate
from everything that can be slow or down: three edge nodes in three locations accept events
and metrics into a replicated queue (NATS JetStream, three replicas) and answer without
touching PostgreSQL or Umami. The queue holds web events for 72 hours, so a full outage of the
core tier costs the dashboards, not ingestion.

Windows that follow from the configuration, not from measurement:

| Failure | Ingestion | Dashboards |
|---|---|---|
| One edge node lost | requests to it fail until DNS drops it: health check every 30 s, TTL 60 s, up to about 90 s; a browser that cannot connect tries the next address | none |
| Database leader lost | none | at most about 40 s until promotion |
| Whole core tier down | none, queued for up to 72 h | down for the whole outage |
| Full NATS restart | refused (503) until the queue has quorum again; visitors of that day counted again | none |

With those windows the 99.99% ingestion budget of 259 s a month absorbs about two unhandled
edge losses a month, and the 99.90% dashboard budget of 2,592 s absorbs about 64 database
failovers or one 43-minute core outage. Real windows can only be longer.

### Single points of failure

From [reference/availability.md](reference/availability.md):

- **The DNS provider (bunny.net).** All failover for edge and core depends on its health
  checks and record sets.
- **The single cloud provider (Hetzner).** All three locations, the private network and the
  object storage share one provider. An account suspension, a billing failure or a
  provider-wide outage takes everything.
- **Resolver caching.** Resolvers and browsers can keep a removed address past the TTL.
- **The port-80 health check sees no TLS failure.** A broken certificate on port 443 leaves the
  node in DNS. The prober checks each node's certificate by its own address, and the
  `CertificateExpiringSoon` alert fires 14 days before expiry (`docs/runbooks/alerts.md`).
- **One active replayer feeding a fixed number of Umami processes.** Each core node runs
  `core_umami_processes` Umami processes (default 4). The this stage load drill measured about
  280 web events a second with one process, below the 500 a second of ten times the expected
  peak; four processes drained about 2.3 times one process in a later local measurement, and
  the 10x drill was not rerun with them. Events are queued, not lost, but the dashboards can
  fall behind.
- **Two database nodes.** While the replica is down the leader runs alone; a second failure in
  that time loses what only the leader had, back to the last archived WAL (at most 60 s).
- **The backup repository** in one object storage location, not yet tested.

### What was and was not drilled

The drills of the design ran on 2026-11-13 on a local stand-in (six Debian 13 nodes on one
host, location delays simulated), not on staging. The record is
[reference/drills/2026-11-13.md](reference/drills/2026-11-13.md).

| Drill | Result |
|---|---|
| Load at 10 times the expected peak | ran: 149,995 of 149,995 web events and 347,988 OTLP exports acknowledged, none lost; gateway p99 126 ms, collector p99 81 ms. **Failed** its drain bound: the backlog took 1,101 s to empty against 600 s. Consumer settings were raised since; the web path is still limited by one Umami process |
| Second load run | invalid: the shared host was saturated |
| Edge node loss | invalid: the host froze for about 104 s at the moment of the power-off. It found a real defect (a NATS container not restarted after power-on); a fix is in place and not yet re-verified |
| NATS member loss, database leader loss, whole core tier down, location partition, full NATS restart, point-in-time restore, certificate renewal, rolling deploy under load, clock skew detection | **not run** |

Blocked regardless of the host, until staging exists: the real bunny.net removal timing,
backups on Hetzner Object Storage and real cross-location latency, and ACME certificate
issuance.

### Monitoring

35 alert rules (plus 5 recording rules) with promtool tests, the prober, the status page and
the SLO report were built and verified on a local test stack (the design). In the alert
fire-drills 16 alerts fired and resolved and 3 more fired for real without a drill
([runbooks/alerts.md](runbooks/alerts.md), "Fire-drills"). The observability role is applied
by `site.yml` on the edge and core nodes. Not yet done: delivery of alerts to the real
receiver (Discord webhook, an operator input and the probe server at the second provider an operator input.

### Patching

A full upgrade of Umami from 3.4.1 to 3.5.0 was rehearsed on 2026-11-13: from the detection
to every test gate green took 3 h 18 min ([runbooks/security-patch.md](runbooks/security-patch.md)
section 7). The rollout to staging and production was not rehearsed, because neither exists,
and keyless signing of releases cannot run while GitHub Actions fails on the account.
