---
title: "Collector upgrade"
description: "Build, test and roll out a new version of the OTLP collector."
---

The OTLP collector (`scf-otelcol`) is a custom OpenTelemetry Collector distribution built with
ocb from `collector/builder-config.yaml`: upstream core v0.165.0 / v1.71.0, contrib v0.165.1,
and three components in this repository (`tenantauth`, `tenantguard`, `jetstream`). Why it is
built this way and what the strict version check does: [../reference/collector.md](../reference/collector.md)
"ocb, module layout and strict versioning". An upgrade moves every collector module together;
ocb's strict check refuses a mixed set.

## Steps

1. **Choose the target.** Skip a minor line released less than 30 days ago unless a security fix
   needs it. Give the reason for the chosen version in the commit message.

2. **Move every pin of the collector together**:

   - `collector/builder-config.yaml`: every `gomod` line of `go.opentelemetry.io/collector/...`
     and `github.com/open-telemetry/opentelemetry-collector-contrib/...`.
   - `go.mod`: the `go.opentelemetry.io/collector/...` requirements (the custom components
     live in the root module, which the generated module replaces, so the root go.mod takes part
     in version selection). Then `go mod tidy`.
   - `Makefile`: `OCB_VERSION`.
   - `deploy/images/collector.Dockerfile`: the `otel/opentelemetry-collector-builder` tag and
     index digest, resolved with `docker buildx imagetools inspect`. Check that image's Go
     version against `go.mod`; for 0.165.0 it shipped Go 1.27.3, which is why the Dockerfile
     copies `ocb` into the pinned Go image.

3. **Read the upstream changelogs** for every version passed, for the receiver limits, the
   `transform` and `memory_limiter` processors, the `confmap` providers and any rename of
   telemetry metrics (the alerts read `otelcol_receiver_accepted_metric_points` and friends,
   [../reference/observability.md](../reference/observability.md)).

4. **Build:**

   ```sh
   make collector-build
   ```

   Expected: ocb logs `Compiled {"binary": "./dist/scf-otelcol/scf-otelcol"}` and the target ends
   with `./dist/scf-otelcol/scf-otelcol components >/dev/null` exiting 0. A strict-versioning
   failure names the module that resolved to another version.

5. **Run the gates:**

   ```sh
   make collector-it      # the binary against real NATS with the production permissions
   make apipath           # the five example apps through edge Caddy into PostgreSQL
   make test
   ```

   `collector-it` must end with `ok  github.com/justmert/veristar/tests/collector`.
   Also run `promtool` rule tests if a telemetry metric name changed
   (`infra/ansible/roles/observability/files/rules/tests/`).

6. **Release and roll out** as any image change: tag, `release-images.yml`, then
   `make <env>-deploy ROLLING=1 SA_IMAGE_TAG=<release> SA_RELEASE_MANIFEST=<release-images.json>`
   (`<env>` is `staging` or `production`).
   Edge nodes are updated one at a time behind the drain, so OTLP clients move to the other
   nodes while one restarts. Check `IngestOTLPErrorRatioHigh` stays quiet and
   `API_METRICS` drains.

7. **Rollback** is a redeploy of the previous release tag; the collector keeps no state of its
   own (tokens come from the `registry_otlp` bucket, data goes to `API_METRICS`).

## Exercised

| Date | What ran | Result |
|---|---|---|
| 2026-11-13 | Steps 4 and 5 locally (`make collector-build`, `make collector-it`) at the current pins, as a rebuild | build 6.4 s with a warm module cache, `components` check passed; `TestCollector` passed 13 of 13 subtests in 42.8 s, including NATS stop and recovery and a collector restart under load through edge Caddy |
