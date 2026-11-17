---
title: "OTLP collector (scf-otelcol)"
sidebarTitle: "OTLP collector"
description: "What the OTLP collector accepts and publishes, and the upstream behaviour its configuration rests on."
---

What the OTLP collector accepts, what it publishes, and the facts about the
pinned upstream components that its configuration rests on.
Everything below was read from the sources at collector core v0.165.0 /
v1.71.0 and contrib v0.165.1 (module cache, 2026-11-12) and is exercised by
`make collector-it` unless marked otherwise.

## Files

| Path | Purpose |
|---|---|
| `collector/builder-config.yaml` | ocb 0.165.0 manifest of the `scf-otelcol` distribution |
| `collector/otelcol.yaml` | the only configuration; every environment value is a required `${env:...}` |
| `collector/tenantauthextension` | `tenantauth`: bearer token to tenant |
| `collector/tenantguardprocessor` | `tenantguard`: auth check, rate limits, stamping, allowlists, route normalization |
| `collector/jetstreamexporter` | `jetstream`: publish per tenant to `API_METRICS` |
| `collector/internal/schema` | names of every field that may leave the collector |
| `internal/routenorm` | route normalization rule set, corpus in `testdata/corpus.tsv` |
| `internal/trafficclass` | traffic class rule set (preflights, health routes, probes), corpus in `testdata/corpus.tsv` |
| `deploy/images/collector.Dockerfile` | image build |
| `tests/collector` | integration suite (build tag `collectorit`) |

## Pipelines

```
traces:        otlp -> memory_limiter -> tenantguard -> signal_to_metrics (connector)
metrics/spans: signal_to_metrics -> memory_limiter -> transform/spans -> jetstream
metrics:       otlp -> memory_limiter -> tenantguard -> transform/tenant -> jetstream
logs:          otlp -> memory_limiter -> tenantguard -> nop
```

`tenantguard` runs exactly once per received signal: span-derived metrics
enter a separate pipeline after the connector. There is no trace exporter and
no batch processor anywhere.

## Upstream behaviour

### Component names

Snake case is current: `memory_limiter`, `signal_to_metrics` (deprecated
alias `signaltometrics`), `health_check` (not `healthcheck`), `transform`,
`nop`, `otlp`.

### Server authenticator

`extensionauth.Server.Authenticate(ctx, sources map[string][]string)`.
`confighttp` passes `r.Header` (canonical keys, `Authorization`);
`configgrpc` passes incoming metadata (lowercase, `authorization`).
`tenantauth` matches the key case-insensitively and accepts exactly one
value. The auth data is set through `client.Info.Auth` with attributes
`tenant_id` and `token_id`.

- gRPC: an error that is a gRPC status keeps its code, anything else becomes
  `Unauthenticated`.
- HTTP: `confighttp` answers **every** authenticator error with 401,
  whatever its gRPC code. SDKs treat 401 as permanent and drop the batch, so
  the collector must never answer before it can check tokens. See "Startup
  window" below.
- The auth interceptor wraps the body-size and decompression handlers, so an
  unauthenticated request is refused before its body is read.
- The OTLP receivers answer a request with zero items with success without
  calling the pipeline. Such requests are authenticated but never rate
  limited, and cost nothing downstream.

### Error mapping (otlpreceiver `internal/errors`)

The receiver converts a pipeline error with `status.FromError`; a non-status
error becomes `Unavailable` (permanent errors `Internal`). HTTP codes:
`ResourceExhausted` 429, `Unavailable` 503, `Unauthenticated` 401,
`InvalidArgument` 400, `Internal` 500. `tenantguard` returns
`ResourceExhausted` for rate limits and `Unauthenticated` without auth data;
`jetstream` returns `Unavailable` when a publish is not acknowledged, and a
permanent error, so 400 over HTTP, for an export larger than the message
limit after filtering (see "jetstream exporter").
`memory_limiter` returns a plain error, so refusals under memory pressure are
503 (retryable).

### Batch processor

`batchprocessor` builds new contexts and drops `client.Info.Auth` unless
`metadata_keys` is set. It is not used. The tenant is stamped as resource
attributes by the first processor after `memory_limiter`, and the exporter
reads the tenant from the resource, never from the context.

### signal_to_metrics (alpha, stateless)

Alpha at v0.165.1 for all four signal-to-metrics types. Accepted because the
version is pinned and `tests/collector` checks its output (counts, routes,
bounds, attributes, removal of its instance attribute). Facts relied on:

- One aggregation per `ConsumeTraces` call, delta temporality, no state.
- Histogram value `Seconds(span.end_time - span.start_time)`, explicit
  `buckets` set to the canonical layout ([api-storage.md](api-storage.md),
  "Histogram contract").
- `attributes` without `optional` makes the attribute required; the method
  is required, route, status and error type are optional.
- `include_resource_attributes` is an include list; the five `scf`/service
  attributes and `scf.batch.id` are listed.
- It always adds `signal_to_metrics.service.instance.id` and names its own
  scope; `transform/spans` removes both.
- Data point timestamps are the collector's processing time, not the span
  end time, there is no start time, and points come out in Go map order.
  tenantguard's stamps fix both (`scf.span.minute`, `scf.batch.id`); see
  "Span-derived points carry the span end minute" below.
- Count is one per span (no `AdjustedCount()`), so sampled traces count only
  the sampled spans.

### transform

- `scale_metric(factor, unit)` scales explicit bounds, sum, min, max and
  exemplars of histograms; it refuses exponential histograms.
- `convert_exponential_histogram_to_histogram(distribution, bounds)`;
  `"uniform"` is deterministic (`"random"` is the default when empty).
- So a legacy exponential histogram in milliseconds is first converted onto
  the canonical bounds in milliseconds, then scaled. Scaling the
  milliseconds bounds `5 ... 10000` by `0.001` yields exactly the float64
  literals `0.005 ... 10` (checked), so converted data matches the canonical
  layout bit for bit.
- `scf.lossy = true` is set on exponential data points before conversion.
- `error_mode: ignore`: a failing statement leaves the metric as it was; the
  exporter then drops anything that is not an explicit histogram in seconds
  with a published name, and logs it.
- `filter` is not needed: `tenantguard` does the filtering in code.

### Receiver limits and compression

- HTTP `max_request_body_size: 4194304` is enforced on the received bytes and
  again on the decompressed body. `compression_algorithms` is set to
  `["", "gzip", "zstd"]` (the default list also has zlib, deflate, snappy,
  x-snappy-framed and lz4). v0.165.0 bounds the zstd decoder window by the
  body size, the reason the pin was taken.
- gRPC `max_recv_msg_size_mib: 4`. grpc-go checks the limit after
  decompression and answers `ResourceExhausted`.
- Tested: 5 MiB plain body, a 65 KB gzip and a 2 KB zstd payload that expand
  to 64 MiB, a 5 MiB gRPC message and a gzip-compressed 64 MiB gRPC message
  are all rejected, and the collector stays healthy.

### ocb, module layout and strict versioning

ocb 0.165.0 defaults `--skip-strict-versioning` to **true**; the Makefile
and the Dockerfile pass `--skip-strict-versioning=false`. With it, ocb
checks after `go mod tidy` that `otelcol` resolved to v0.165.x and every
listed component to its listed minor version, skipping components of the
module being built.

Manifest fields used: `gomod` (module and version), `import` (package path
inside the module), `name` (Go identifier), `replaces` (go.mod replace
lines, relative to `dist.output_path`). `path` is per component and emits one
`replace` per component; three components of one module would emit the same
replace three times, so the single `replaces` entry is used instead.

**Decision: the custom components live in the root module.** The repository
is one Go module, the components import `internal/registry`, `internal/natsx`,
`internal/redact`, `internal/routenorm` and `internal/obs`, and the
metrics writer needs the same `pdata` v1.71.0 in the root module
anyway. Because the generated module replaces the root module with this
checkout, the root go.mod takes part in version selection: it pins the
collector core modules at v1.71.0 / v0.165.0, and ocb's strict check then
confirms that nothing pulled them elsewhere. A separate `collector/go.mod`
would be allowed to import `internal/` (the path prefix matches) but would
need its own copy of every shared requirement, which could drift from the
root (nats.go, otel, grpc) without any check noticing. The cost of the
chosen layout is a larger root go.mod (collector packages and their
dependencies).

Three consequences found while building the image:

- ocb creates only the last directory of `output_path` (`mkdir`), so
  `dist/` must exist.
- `go mod tidy` in the generated module also resolves the imports of the
  tests of every root package it uses, so the image build copies `db` and
  `tests/fixtures` although the binary contains neither.
- The `otel/opentelemetry-collector-builder:0.165.0` image ships Go 1.27.3
  with `GOTOOLCHAIN=local`; the root module needs 1.27.4. The Dockerfile
  copies the `ocb` binary out of that image into `golang:1.27.4`.

Only the env and file confmap providers are compiled in, so a configuration
cannot be fetched over the network.

### Upstream alternatives checked

- **nats exporter** (`exporter/natsexporter` v0.165.1): stability
  *development*, no distribution. At v0.165.1 it has a `jetstream` option (acknowledged publish, optional
  `publish_timeout`). It still sets no `Nats-Msg-Id` and no expected stream,
  so a retried export would be stored twice, and its subject is an OTTL
  expression rather than a value checked against the tenant. The custom
  `jetstream` exporter stays.
- **Rate limiting**: no rate-limiting processor exists in contrib v0.165.1.
- **Bearer to tenant**: `bearertokenauth` compares against fixed tokens and
  exposes no tenant. Hence `tenantauth`.

### SEP-23 strkeys

From SEP-23 v1.4.0 and `internal/redact`: G, C, L, S, T, X 56 characters,
B 58, M and W 69, P variable. The shortest valid signed payload (4-byte payload) is **69** characters, not 56; the longest
(64-byte payload) is 165. Alphabet `A-Z2-7`. The corpus uses every valid
SEP-23 vector plus the S, T and X vectors of go-stellar-sdk v0.8.0 (the SEP
lists none for those three), and the SEP's invalid vectors.

### Startup window

Before the first `registry_otlp` snapshot is loaded, a 401 would lose data
on every collector restart. Three things close the window:

1. `tenantauth.Start` returns only after the first full snapshot is loaded
   (bounded by `snapshot_timeout`, 60 s; on timeout the collector exits). It
   reports component status `Starting`, then `OK`. The collector starts its
   receivers only after all extensions, so no OTLP port is open until then.
2. `health_check` (v1, listed first in `service.extensions`) answers 503
   until the collector reports ready, which happens after every component
   started, so it reflects the snapshot.
3. Edge Caddy health-checks that endpoint every second for the OTLP/HTTP
   proxy (`COLLECTOR_HEALTH_UPSTREAM`). While the collector is down or not
   ready Caddy answers 502 (connection refused) or 503 (no healthy
   upstream); SDKs retry both. The gRPC proxy has no active health check:
   Caddy sends health checks over the proxy's transport, which is h2c there,
   and the health endpoint speaks only HTTP/1.1 (checked: the check fails
   with an HTTP/2 framing error). Point 1 already keeps the gRPC port closed
   until the snapshot loads, so gRPC clients get 502 (`Unavailable`), which
   they retry.

`tests/collector` restarts the collector while the Go SDK exports through
the real edge Caddy every 200 ms: 1,221 of 1,221 recorded requests arrived,
the SDK saw 502 and 503 during the restart and no 401.

### Caddy

gRPC and OTLP/HTTP share `otlp.<BASE_DOMAIN>` on 443. Caddy matches gRPC
by `Content-Type: application/grpc*` and proxies it with
`transport http { versions h2c }` to the collector's gRPC listener; POST
to `/v1/metrics`, `/v1/traces` and `/v1/logs` goes to the HTTP listener
with a 4 MB wire limit; anything else is 404.

`COLLECTOR_GRPC_UPSTREAM`, `COLLECTOR_HTTP_UPSTREAM` and
`COLLECTOR_HEALTH_UPSTREAM` are parse-time variables like the others: a
missing one fails `caddy validate` and startup (`edge_test.go` checks each).
The OTLP site has its own access log with the same field filter as the
other sites (three filtered loggers in total).

`deploy/caddy/edge_otlp_test.go` proves the routing with a real gRPC OTLP
service and an HTTP handler behind a real Caddy: the bearer token reaches the
upstream, forwarded-address headers do not, the size limit and 404s hold,
an unready health endpoint turns requests into 503 until it recovers, the
OTLP access lines are filtered, and nothing Caddy wrote contains the user
agent or the forwarded address.

### Request duration metrics

The collector stores server request duration: `http.server.request.duration`
(stable, seconds) and the legacy `http.server.duration` (milliseconds or
seconds). `http.client.duration` is the deprecated client-side metric of the
HTTP semantic conventions and measures outgoing calls, not the API's own
requests, so it is not one of the kept metrics (see "tenantguard" below).

## Behaviour

### tenantauth

- Token format `sa_otlp_` + 40 base62 characters, else 401 without lookup.
- Lookup by `registry.HashOTLPToken` in a `registry.Watcher` snapshot of
  `registry_otlp`; only `status: active` passes. Suspension and removal of a
  tenant are expressed by the control plane as token status or deletion.
- Start blocks until the first snapshot is loaded (see "Startup window").
  A request that still meets an unloaded snapshot is refused (fails closed).
  After a NATS outage the last snapshot keeps serving.
- Measured: revocation and deletion take effect in well under a second in
  tests (requirement: 5 s).

### tenantguard

- No auth data: `Unauthenticated`.
- Token buckets per tenant for requests and for items (data points, spans,
  log records counted on arrival). Shared by the metrics, traces and logs
  instances of one configured processor. Per collector process: with three
  edge nodes a tenant can reach up to three times the configured rate.
  A request with more items than `items_burst` can never pass.
- Resource: `scf.resource.hash` is SHA-256 over the original resource
  attributes (sorted keys, type and value). Then the resource is reduced to
  `service.name` (redacted; invalid UTF-8 and control characters replaced
  with U+FFFD, then cut to at most 128 bytes on a rune boundary, so the
  value is always storable), `scf.tenant.id` and
  `scf.token.id` from the credential, the hash, and `scf.flags` (bit 1: a
  secret seed was redacted). Forged `scf.*` or `tenant.id` attributes are
  dropped with everything else.
- Scope name, version and attributes, schema URLs, descriptions, metadata
  and exemplars are cleared.
- Metrics kept: `http.server.request.duration` in `s`,
  `http.server.duration` in `ms` or `s`, histogram or exponential histogram
  only. `scf.http.server.request.duration.from_spans` from a tenant is
  dropped: only the connector may produce it.
- `OTEL_SEMCONV_STABILITY_OPT_IN=http/dup`: an instrumentation scope that
  sends both `http.server.request.duration` and `http.server.duration`
  records every request under both names, and after `transform/tenant`
  scales and renames the legacy one both would be stored. When a scope
  holds a kept stable metric, its legacy metrics are dropped. A legacy
  metric in another scope comes from another library and is kept.
- Data point attributes: `http.request.method` (from `http.method`;
  upper-cased first, as semconv allows for instrumentation that treats
  methods case-insensitively; values outside the semconv set
  CONNECT, DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT, TRACE, QUERY become
  `_OTHER`), `http.route` (normalized with `internal/routenorm` rule set 2;
  from `url.path` or legacy `http.target` when absent),
  `http.response.status_code` (from `http.status_code`, int or string,
  100 to 599), `error.type` (redacted; longer than 64 bytes or not storable
  text becomes `_OTHER`), and `scf.traffic.class` for infrastructure traffic
  (see "Traffic classes" below), and `scf.stream` on cumulative points
  (next item). Nothing else.
- Route normalization rule set 2 adds to rule set 1: a
  segment that decodes to invalid UTF-8 or a control character becomes
  `{invalid}`; every run of digits between separators (`- _ . , ~ : + ; =`)
  becomes `{id}` (`order-12345`, `123.json`, `42,43`, `-5`, `1.5`); a
  segment with a space or an `@` that is not an email becomes `{text}`;
  a route longer than 512 bytes keeps its leading whole segments and ends
  in `/{long}`. File names with letters (`john.doe`, `app.3f9a2b1c.js`) are
  kept: they cannot be told from route words.
- Cumulative data points (histogram and exponential histogram) get
  `scf.stream`: 32 hex digits, the first 128 bits of a SHA-256 over the
  scope name, version and attributes, the metric name and unit as received,
  and the point's original attributes (sorted keys, type and value), taken
  before the scope is cleared and the attributes are rewritten. The rewriting
  above can make two source streams of one resource identical (two scopes
  recording the same metric, a legacy metric renamed after this processor,
  an unknown or lower-case method, a long `error.type`, a route literal the
  normalizer replaces, an attribute that is dropped); the stream id keeps
  them apart in the writer's stream state, which counts a cumulative stream
  by the difference between its exports. It is written after the tenant's
  attributes are cleared, so a tenant cannot set it. It is part of the
  stream identity only, never a dimension of a stored row. Delta points get
  none: each is counted as it is.
- Points the writer could not store are dropped one by one and counted in
  the collector metric `tenantguard_invalid_items` (labels `signal` and
  `reason`, no tenant), so the rest of the export goes on: a time of 0 or
  past 2262 (`2^63` nanoseconds), a start after the time (`time`); a
  temporality other than delta or cumulative, the whole metric
  (`temporality`); explicit bounds that are not finite and strictly
  increasing, or bucket counts that are not one more than the bounds,
  including a point without buckets (`buckets`); a count or a bucket count
  above 10^12 (`count`); a sum that is NaN or infinite (`sum`). A stale
  marker (no recorded value) is checked for its times only.
- Spans kept: kind SERVER with an HTTP method, a start time, and an end
  time that is not before the start, after the first minute of the epoch
  and before 2262 (others are dropped and counted as `time`, since they have
  no duration or minute). Name set to the method;
  events, links, status message and trace state removed. Each kept span
  gets `scf.span.minute`, the start of the UTC minute it ended in (unix
  nanoseconds), and every resource of the export gets `scf.batch.id`, the
  SHA-256 of the filtered export. Both exist only inside the collector.
- Logs: authenticated, rate limited, answered with success, dropped.

### jetstream exporter

- Splits by `scf.tenant.id` (must be a canonical UUID, else a permanent
  error: the pipeline is misconfigured), checks every part against
  `collector/internal/schema` again, marshals with `pmetric.ProtoMarshaler`,
  and publishes with `natsx.PublishDedup` to `api.metrics.<tenant>`,
  expected stream `API_METRICS`. `Nats-Msg-Id` is the hex SHA-256 of the
  payload for tenant metrics, and for span-derived points the SHA-256 of
  the tenant and the sorted `scf.batch.id` values of their resources.
- Span-derived points: the exporter sets each point's time to its
  `scf.span.minute` and removes that attribute and `scf.batch.id`. A span
  point without either stamp is a permanent error: the connector
  configuration lost them, and publishing would bring back the double
  counting they prevent.
- Message size: every NATS server of the platform takes messages up to
  8 MiB (`max_payload: 8388608` in the edge role's `nats.conf`, the dev
  stack's `deploy/compose/nats-dev.conf` and the test servers;
  `natsx.MaxPayload`). The server default of 1 MiB is below one export of a
  few thousand histogram points, about 400 bytes each after filtering. A
  tenant's part of an export larger than 8 MiB less 4 KiB for headers
  (`MaxPartBytes`) can never be stored, so the whole export is refused as a
  permanent error before anything is published: HTTP 400, gRPC
  `InvalidArgument`, and the SDK drops it. A 4 MiB request stays below the
  limit unless filtering adds more than it removes, as converting many tiny
  exponential histograms to explicit buckets does. A part above the limit
  the server announced at connect (a server not yet reconfigured, or a
  connection made before its limit was raised, which keeps the old limit
  until it reconnects) is answered 503 and the connection is renewed, so
  the retry gets the new limit.
- Synchronous, no sending queue, no retry, `publish_timeout` 5 s. Failure is
  `Unavailable` (503); the SDK retries the same bytes, so a publish that was
  stored but not acknowledged is deduplicated by the stream's 2 minute
  window and by the writer's `api.ingest_dedup` (8 days). For tenant
  metrics the payload is the same bytes. For spans the connector aggregates
  the retry again at another moment and in another map order, so the
  payload differs; the batch id is what makes the message id equal. Two different exports differ at least in their span ids, so they
  never share an id.
- The NATS reconnect buffer is off (`ReconnectBufSize(-1)`): while
  disconnected a publish fails at once instead of being flushed into the
  stream later after the client was already told 503. Found by the
  integration suite, where the buffered publish arrived after the restart.
- Connects as the `collector` NATS user (publish `api.metrics.*`, watch
  `registry_otlp`); the suite runs NATS with the real permission set.

## Traffic classes

Load balancer health checks, Kubernetes probes, uptime monitors and CORS preflights reach a service
as ordinary HTTP requests, so a service that answers its own load balancer every few seconds would
look busier, and usually faster, than its users make it. `tenantguard` classifies every data point
and every server span with `internal/trafficclass` while it still holds the original attributes,
then drops the user agent with everything else that is not published. Infrastructure traffic gets
the point attribute `scf.traffic.class`; application traffic gets none, so its streams keep the
identity they had before the classifier existed. The connector copies the attribute onto the
span-derived points, the writer stores it as the `traffic_class` column
([api-storage.md](api-storage.md)), and every headline number leaves those rows out
([control-plane-api.md](control-plane-api.md) 7.4).

The rule set is hard-coded and versioned like the route normalizer: no tenant setting changes it,
so every project is classified the same way. Rule set version 1 applies these rules in order to the
upper-cased method, the route after `internal/routenorm` and the user agent:

1. **`preflight`**: every `OPTIONS` request.
2. **`health`**: a route made of up to three prefix segments and a health name, optionally followed
   by one check segment. Matching ignores case and trailing slashes.
   - Health names: `health`, `healthz`, `_health`, `__health`, `healthcheck`, `health-check`,
     `health_check`, `livez`, `readyz`, `startupz`, `liveness_check`, `readiness_check`, `ping`,
     `_ping`, `status`, `_status`, `heartbeat`, `metrics`; `healthy` and `ready` right after `-`;
     `prometheus` right after `actuator`.
   - Prefix segments: `api`, `apis`, `rest`, a version such as `v1`, `v2beta` or `v3alpha`,
     `actuator`, `management`, `manage`, `admin`, `q`, `_ah`, `-`, `_`, `internal`, `_internal`,
     `system`, `sys`, `ops`, `monitoring`, `monitor`, `.well-known`.
   - Check segments: `live`, `liveness`, `ready`, `readiness`, `started`, `startup`, `ping`,
     `check`, `all`.
3. **`probe`**: a user agent of a health checker or uptime monitor, or a
   `user_agent.synthetic.type` of `test`.
   - Matched at the start of the user agent: `ELB-HealthChecker/`, `kube-probe/`, `GoogleHC`,
     `Envoy/HC`, `Consul Health Check`, `Nomad/`, `GoogleStackdriverMonitoring-UptimeChecks`,
     `Edge Health Probe`, `Uptime-Kuma/`, `Datadog/Synthetics`, `Better Stack Better Uptime Bot`,
     `Better Uptime Bot`.
   - Matched as a token inside a browser-like user agent: `UptimeRobot/`,
     `Cloudflare-Healthchecks/`, `Cloudflare-Traffic-Manager/`, `(StatusCake)`,
     `DatadogSynthetics`.
   - The exact user agent `AlwaysOn`, Azure App Service's warm-up request.
4. **`app`**: everything else.

The conventions behind the lists:

| Source | Paths or user agent |
|---|---|
| [Kubernetes API health endpoints](https://kubernetes.io/docs/reference/using-api/health-checks/) | `/livez`, `/readyz`, `/healthz`, `/livez/<check>`; kubelet probes send `kube-probe/<major>.<minor>` ([source](https://github.com/kubernetes/kubernetes/blob/master/pkg/probe/http/request.go)) |
| [Spring Boot Actuator](https://docs.spring.io/spring-boot/reference/actuator/endpoints.html) | `/actuator/health`, `/actuator/health/liveness`, `/actuator/health/readiness`, `/actuator/prometheus` |
| [Quarkus SmallRye Health](https://quarkus.io/guides/smallrye-health) | `/q/health`, `/q/health/live`, `/q/health/ready`, `/q/health/started` |
| [MicroProfile Health 4.0](https://download.eclipse.org/microprofile/microprofile-health-4.0/microprofile-health-spec-4.0.html) | `/health`, `/health/live`, `/health/ready`, `/health/started` |
| [ASP.NET Core health checks](https://learn.microsoft.com/en-us/aspnet/core/host-and-deploy/health-checks) | `/healthz`, `/healthz/ready`, `/healthz/live` |
| [App Engine flexible app.yaml](https://docs.cloud.google.com/appengine/docs/flexible/reference/app-yaml) | `/_ah/health`, `/liveness_check`, `/readiness_check` |
| [Prometheus management API](https://prometheus.io/docs/prometheus/latest/management_api/) | `/-/healthy`, `/-/ready`; scrapes read `/metrics` |
| [Traefik ping](https://doc.traefik.io/traefik/reference/install-configuration/observability/healthcheck/) | `/ping` |
| [AWS Application Load Balancer](https://docs.aws.amazon.com/elasticloadbalancing/latest/application/lambda-functions.html) | `ELB-HealthChecker/2.0` |
| [Google Cloud health checks](https://docs.cloud.google.com/apigee/docs/api-platform/debug/health-check) | `GoogleHC` |
| [Envoy active health checking](https://github.com/envoyproxy/envoy/blob/main/source/common/http/headers.h) | `Envoy/HC` |
| [Consul HTTP checks](https://github.com/hashicorp/consul/blob/main/agent/checks/check.go) | `Consul Health Check` |
| [Nomad service checks](https://github.com/hashicorp/nomad/blob/main/helper/useragent/useragent.go) | `Nomad/<version>` |
| [Cloud Monitoring uptime checks](https://docs.cloud.google.com/monitoring/uptime-checks/using-uptime-checks) | `GoogleStackdriverMonitoring-UptimeChecks(https://cloud.google.com/monitoring)` |
| [Cloudflare crawlers](https://developers.cloudflare.com/fundamentals/reference/cloudflare-site-crawling/) | `Cloudflare-Healthchecks/1.0`, `Cloudflare-Traffic-Manager/1.0` |
| [Azure Front Door health probes](https://learn.microsoft.com/en-us/azure/frontdoor/health-probes) | `Edge Health Probe` |
| [Uptime Kuma](https://github.com/louislam/uptime-kuma/blob/master/server/uptime-kuma-server.ts) | `Uptime-Kuma/<version>` |
| [UptimeRobot](https://help.uptimerobot.com/en/articles/11358489-what-is-the-uptimerobot-user-agent-string) | `UptimeRobot/2.0` |
| [Better Stack](https://betterstack.com/docs/uptime/frequently-asked-questions) | `Better Stack Better Uptime Bot`, `Better Uptime Bot` |
| [Datadog Synthetics](https://docs.datadoghq.com/synthetics/guide/identify_synthetics_bots/) | `Datadog/Synthetics`, `DatadogSynthetics` |
| [StatusCake](https://www.statuscake.com/kb/knowledge-base/testing-websites-using-incapsula/) | `(StatusCake)` |
| [Azure App Service Always On](https://devblogs.microsoft.com/premier-developer/404-response-code-caused-by-app-services-alwayson-feature/) | `AlwaysOn` |

What the instrumentation records decides which rules can apply:

- **Routes and methods** are on every point, so rules 1 and 2 classify metrics and spans alike.
- **The user agent** (`user_agent.original`) is a recommended attribute of HTTP server spans in the
  semantic conventions and the Go, Node.js, Python and Java instrumentations record it there. The
  `http.server.request.duration` metric does not carry it
  ([HTTP metrics](https://github.com/open-telemetry/semantic-conventions/blob/v1.45.0/docs/http/http-metrics.md)),
  so rule 3 classifies span-derived points by user agent, and metric points only through
  `user_agent.synthetic.type`, an opt-in attribute of the metric that the Python ASGI and WSGI
  instrumentations set from the user agent by default.
- A probe that sends a custom user agent to an application route is application traffic: nothing
  in the request tells it apart.

The rule set is tested with a golden corpus of more than 150 requests
(`internal/trafficclass/testdata/corpus.tsv`), each run through `internal/routenorm` first as
`tenantguard` does. A change of a rule raises `trafficclass.Version`, adds its row to
`api.traffic_class_rules` in a migration, and leaves stored rows as they were classified.

## Span-derived points carry the span end minute

Every `scf.http.server.request.duration.from_spans` data point has
`time_unix_nano` set to the start of the UTC minute its spans ended in, and
`start_time_unix_nano` is 0. Temporality is delta, one point per attribute
set and end minute per received payload: `scf.span.minute` is one of the
connector's point attributes, so spans of different minutes become
different points. Spans an SDK held back (batching, retries, a collector
restart) land in the minute the requests ended, as tenant metrics
(`http.server.request.duration`) keep the SDK's own timestamps. The time comes from the
client's clock, like a tenant metric's; a point more than 10 minutes in the
future or older than 7 days is refused by the writer.

## Limits in otelcol.yaml

| Setting | Source |
|---|---|
| HTTP body 4 MiB, gRPC message 4 MiB | fixed in `otelcol.yaml` |
| one tenant's part of an export after filtering, 8 MiB less 4 KiB | NATS `max_payload`, `natsx.MaxPayload` |
| `memory_limiter` `limit_mib`, `spike_limit_mib` | env, dev 512 / 128 |
| rate limits | env, dev 50 req/s burst 100, 50,000 items/s burst 200,000 per tenant and node |
| `publish_timeout` | 5 s |
| `snapshot_timeout` | 60 s |
| idle bucket TTL | 10 min |

## Verification

```
make collector-build    # ocb with strict versioning
make collector-it       # binary + real NATS + Go otlpmetrichttp/grpc v1.47.0
go test -race ./internal/routenorm/ ./internal/trafficclass/ ./collector/... ./deploy/caddy/
```

The suite covers HTTP cumulative and gRPC delta exports, the legacy
millisecond metric, an exponential histogram, a traces-only app instrumented
with `otelhttp`, logs over HTTP and gRPC, forged tenant attributes, unknown
and revoked tokens (401 and `Unauthenticated`), 429 followed by a successful
SDK retry, NATS stopped (503) and restarted with every record arriving
exactly once, a collector restart under load through the edge Caddy with no
record lost, oversize and decompression-bomb requests, a fresh secret seed
in a URL that appears in no queued byte and no collector log line, and a
final scan of every queued message for names, attributes and marker strings.
