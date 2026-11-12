# Stellar Analytics

Multi-tenant web and API analytics for projects building on Stellar, run by the
Stellar Community Fund (SCF) as a shared, free service.

- Web analytics: a small, version-pinned browser script sends page views and
  events to an edge gateway, which checks them and passes them on to an
  unmodified Umami instance. Tenants get their own dashboard. SCF operators get
  a cross-project view built from aggregates.
- API analytics: tenant applications dual-export OpenTelemetry data to an SCF
  collector. Traces, spans and logs are dropped. Request counts, status codes
  and latency histograms per normalized route are stored in PostgreSQL.

## Status

Under construction, this stage (repository foundation and local development
stack). Nothing here is ready for production use yet.

## Repository layout

```
go.mod                       module github.com/justmert/stellar-analytics
cmd/                         gateway, replayer, metricswriter, controlplane, jobs, platformctl, prober, sloreport
internal/                    config, obs, db, natsx, registry, events, umami, gateway, replayer,
                             routenorm, histo, metricswriter, controlplane, jobs
collector/                   builder-config.yaml, otelcol.yaml, tenantauthextension/,
                             tenantguardprocessor/, jetstreamexporter/
db/migrations/  db/queries/  goose migrations, sqlc queries
apps/console/                Vite + React + Tailwind + shadcn/ui
packages/tracker/            tracker source, build script, releases/<version>/sdk.js, releases/manifest.json
examples/                    tenant-side sample apps and sites (also used as test targets)
deploy/compose/  deploy/images/  deploy/caddy/
infra/tofu/  infra/ansible/  infra/secrets/
tests/e2e/  tests/isolation/  tests/load/  tests/adblock/
docs/                        adr/, deploy/, runbooks/, onboarding/, compliance/, security/, reference/, slo/
```

Not every directory exists yet. They are added by the phase that needs them.

## Design decisions

Architecture decision records are in [docs/adr](docs/adr):

- [0001: Umami as the base platform](docs/adr/0001-base-platform.md)
- [0002: Separate services beside Umami, not a fork](docs/adr/0002-overlay-not-fork.md)
- [0003: Licensing](docs/adr/0003-licensing.md)

## Licence

The repository is licensed under the [Apache License 2.0](LICENSE).

`packages/tracker` is licensed under the MIT License, because its source is
derived from Umami's MIT-licensed tracker. It keeps Umami's copyright notice.
See [NOTICE](NOTICE) and [ADR 0003](docs/adr/0003-licensing.md).
