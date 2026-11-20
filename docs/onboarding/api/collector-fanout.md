---
title: "Fan-out from your own Collector"
sidebarTitle: "Collector fan-out"
description: "Forward metrics from an OpenTelemetry Collector you already run, without changing your apps."
icon: "share-nodes"
---

If your apps already send their telemetry to an OpenTelemetry Collector of your own, you do not
need to change the apps. Add a second exporter to the Collector instead: it forwards metrics and
traces to the platform, with the token, and everything to your own backend as before. Tested by
`TestCollectorFanout` in `tests/otelapps`, which runs the configuration below on the stock
`otel/opentelemetry-collector-contrib` 0.165.0 image and checks that metrics and traces arrive at
both a tenant backend and a platform stand-in that refuses requests without the token.

## The configuration

From [`examples/otel-collector-fanout/collector.yaml`](https://github.com/justmert/veristar/blob/main/examples/otel-collector-fanout/collector.yaml):

{/* include examples/otel-collector-fanout/collector.yaml region=config */}
```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  # bounds memory before anything is queued
  memory_limiter:
    check_interval: 1s
    limit_percentage: 80
    spike_limit_percentage: 20
  batch: {}

exporters:
  otlphttp/tenant:
    endpoint: ${env:TENANT_BACKEND_ENDPOINT}
  # the platform copy is the only one that carries the token
  otlphttp/platform:
    endpoint: ${env:PLATFORM_OTLP_ENDPOINT}
    headers:
      Authorization: Bearer ${env:PLATFORM_OTLP_TOKEN}
    # http/2 health checks: a connection silent for 15 s gets a ping, and one
    # unanswered for 5 s is closed, so a connection whose network path is gone
    # does not hold every later export for many minutes
    http2_read_idle_timeout: 15s
    http2_ping_timeout: 5s
    # retries the 429, 502 and 503 answers the platform gives for rate limits
    # and short outages, and queues in memory meanwhile
    retry_on_failure:
      enabled: true
    sending_queue:
      enabled: true

service:
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlphttp/tenant, otlphttp/platform]
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlphttp/tenant, otlphttp/platform]
    # the platform accepts logs and discards them, so they stay with the tenant
    logs:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlphttp/tenant]
```

It reads three environment variables:

| Variable | Value |
|---|---|
| `TENANT_BACKEND_ENDPOINT` | your own backend's OTLP/HTTP base URL |
| `PLATFORM_OTLP_ENDPOINT` | `https://otlp.analytics.example` (the console shows yours) |
| `PLATFORM_OTLP_TOKEN` | the `sa_otlp_...` token from the console |

Every component used here (`otlp` receiver, `memory_limiter`, `batch`, `otlphttp` exporter) is
part of the core and contrib distributions.

## Adding it to an existing configuration

If your Collector already has receivers, processors and an exporter, add two things:

1. the `otlphttp/platform` exporter with the `Authorization` header and the two HTTP/2 health
   check settings, and
2. that exporter added to the `metrics` and `traces` pipelines.

Keep it out of the `logs` pipeline: the platform accepts logs and discards them, so sending them
only costs bandwidth.

## Things to keep in mind

- **One token per Collector** is enough: the token identifies your project, and `service.name`
  from each app's resource tells the services apart.
- **Do not filter or rename** the HTTP duration metrics or their `http.*` attributes on the way to
  the platform; it needs `http.server.request.duration` (or `http.server.duration`) with the
  method, route and status code ([what-gets-dropped.md](what-gets-dropped.md)).
- **Do not aggregate across apps** before export (for example with a processor that merges
  services): the platform keeps `service.name` as a dimension.
- **Sampling.** If your Collector samples traces (tail sampling) and your apps send no metrics, the
  platform derives request counts from the sampled spans and undercounts. Export metrics, or send
  the platform copy from a pipeline before sampling.
- **HTTP/2 health checks.** The `otlphttp` exporter speaks HTTP/2 to the platform and sends no
  health checks unless you set `http2_read_idle_timeout` and `http2_ping_timeout`. Without them, a
  connection whose network path disappears without a close holds every export for many minutes
  ([troubleshooting](troubleshooting.md#exports-time-out-until-the-app-restarts)).
- **Retries.** The platform answers 429 when a project exceeds its rate limit and 502 or 503 during
  short outages; the exporter's retry and queue settings above handle both.
