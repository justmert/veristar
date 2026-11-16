# Veristar

Multi-tenant web and API analytics for projects building on Stellar, run as a shared, free
service for the Stellar Community Fund (SCF). Projects get private dashboards of their own
traffic; designated SCF operators get a cross-project view built from aggregates, for
funding and ecosystem-visibility decisions.

- **Web analytics.** A small, version-pinned script sends page views and custom events to an
  edge gateway. The gateway checks each event (a proof of work issued by the server, the
  page's origin, rate limits, a same-network check), keeps the client address in memory only
  for the request, and queues the event. A replayer delivers it to an unmodified
  [Umami](https://umami.is) 3.5.0, the engine that stores the events and computes the
  metrics on the private network; each project reads them in the console's web dashboard. No cookies and no
  stored IP addresses; visitors are counted per UTC day with a secret that is destroyed at
  midnight. Whether a site may run it without asking for consent depends on the country, and
  a consent mode exists where it may not
  ([docs/compliance/jurisdictions.md](docs/compliance/jurisdictions.md)).
- **API analytics.** Tenant applications dual-export OpenTelemetry data to their own backend
  and to the SCF collector. The collector drops traces, spans and logs, keeps HTTP request
  counts, status codes and latency histograms per normalized route, and stores them in
  PostgreSQL for at least 24 months.

How it fits together: [docs/architecture.md](docs/architecture.md). Why Umami and why beside
it rather than a fork: [docs/adr](docs/adr).

## Production

Veristar runs in Azure Denmark East (EU) on three converged nodes, one in each availability
zone, behind health-checked DNS from Azure Traffic Manager. Every node runs the ingestion edge
and the core services, PostgreSQL runs as three Patroni members, and backups go to
zone-redundant Blob Storage in the same region. Node loss, database leader loss, point-in-time
restore and rolling deploys were drilled on production
([docs/reference/drills/2026-11-13-azure.md](docs/reference/drills/2026-11-13-azure.md)).
Measured gateway capacity is in
[docs/reference/gateway-limits.md](docs/reference/gateway-limits.md).

- Console: `https://app.veristar.app`, with the MCP server for AI clients at
  `https://app.veristar.app/mcp`.
- Public status page: `https://status.veristar.app`, served by an external probe on a separate
  virtual machine in Azure East US ([docs/deploy/probe.md](docs/deploy/probe.md)).
- Release images are signed with cosign and verified before any node pulls them
  ([docs/security/supply-chain.md](docs/security/supply-chain.md)).
- Service targets and how they are measured: [docs/sla.md](docs/sla.md).

## For projects (tenants)

1. Sign in at `https://app.<BASE_DOMAIN>` with GitHub and enroll your project.
2. Add a site, copy its snippet, and put it in your pages: [docs/onboarding](docs/onboarding).
3. For API metrics, create an OTLP token and add a second exporter to your application.
4. Questions and problems: [docs/support.md](docs/support.md). The service is free, and
   charges no fees to anyone.

Privacy and the right to erasure: [docs/compliance](docs/compliance).

## For SCF operators

[docs/operators](docs/operators): review and enrollment modes, removal, reading the global
views, and API keys for machine consumers.

## For whoever runs the service

- Deploy: [docs/deploy](docs/deploy)
- Runbooks: [docs/runbooks](docs/runbooks)
- Service targets and how they are measured: [docs/sla.md](docs/sla.md)
- Security policy and reporting a vulnerability: [docs/security/SECURITY.md](docs/security/SECURITY.md)
- Handing the service to a new operator: [docs/handover/plan.md](docs/handover/plan.md)

## Repository layout

```
cmd/                 gateway, replayer, metricswriter, controlplane, jobs, platformctl,
                     prober, sloreport, issueage
internal/            the Go packages behind them
collector/           the OpenTelemetry Collector distribution and its three custom components
db/                  goose migrations and sqlc queries
apps/console/        the console (Vite, React, Tailwind, shadcn/ui)
packages/tracker/    tracker source and its immutable releases
examples/            sample tenant sites and apps, also used as test targets
deploy/              container images, Caddy configuration, the local development stack
infra/               OpenTofu, Ansible and encrypted secrets
tests/               end-to-end, isolation, load, ad-block, privacy, drill and other suites
docs/                everything above in prose
```

## Changes

What each release changed: [CHANGELOG.md](CHANGELOG.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Licence

The repository is licensed under the [Apache License 2.0](LICENSE).

`packages/tracker` is licensed under the MIT License, because its source is derived from
Umami's MIT-licensed tracker. It keeps Umami's copyright notice. See [NOTICE](NOTICE) and
[ADR 0003](docs/adr/0003-licensing.md).

Country data: IP Geolocation by [DB-IP](https://db-ip.com), licensed under CC BY 4.0.
