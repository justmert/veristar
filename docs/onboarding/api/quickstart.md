# API analytics quickstart

API analytics counts the HTTP requests your backend serves: volume, status codes and latency
percentiles (p50, p95) per service, method and route. Your app sends them with OpenTelemetry, the
vendor-neutral standard, by adding a **second exporter** next to the one it already has. Your own
observability backend keeps receiving everything as before.

The examples use the reserved domain `analytics.example`: the OTLP endpoint is
`https://otlp.analytics.example` and the console `https://app.analytics.example`. The console shows
the real values.

## How it works

```text
your app ──(OTLP, no token)──────────────> your own backend: everything, unchanged
         └─(OTLP, Bearer sa_otlp_...)────> otlp.analytics.example: request durations only
```

This is called dual export: one OpenTelemetry SDK, two exporters per signal, both fed from the same
data. The platform copy carries a token that identifies your project. The platform keeps only the
HTTP server request duration metric with method, route, status code and error type, and discards
the rest on arrival ([what-gets-dropped.md](what-gets-dropped.md)).

If your apps already export to an OpenTelemetry Collector of your own, add the second exporter
there instead, once for all apps: [collector-fanout.md](collector-fanout.md).

## 1. Enroll your project

Sign in to the console with GitHub and choose **Enroll a project**, as for web analytics
([web quickstart](../web/quickstart.md#1-enroll-your-project)). A project can have sites and APIs.

## 2. Create a token

Open the project's **API analytics setup** page. It shows the OTLP endpoint for your service and
lets project members create tokens: give the token a name and an environment (for example
`production`), then **Create token**. The token (`sa_otlp_` followed by 40 characters) is shown
once. Store it as a secret of your app, for example in your secret manager or deployment
environment. It is never shown again; if it is lost, create a new one and revoke the old one.

Tokens can be created only once the project is active (not while it waits for review or is
suspended) and an owner of the project has accepted the current terms of service. Create one token per app or environment, so you
can revoke one without touching the others.

## 3. Add the second exporter

Follow the guide for your language. Each is built from an example app that the platform's
integration tests run against stock collectors and against the real platform collector:

| Language | Example | Guide |
|---|---|---|
| Node.js | Express 5 | [node.md](node.md) |
| Go | `net/http` | [go.md](go.md) |
| Python | FastAPI | [python.md](python.md) |
| Rust | actix-web | [rust.md](rust.md) |

All four use the same environment variables:

| Variable | Value |
|---|---|
| `TENANT_OTLP_ENDPOINT` | your own backend, for example `http://otel-collector:4318` |
| `PLATFORM_OTLP_ENDPOINT` | `https://otlp.analytics.example` (the console shows yours) |
| `PLATFORM_OTLP_TOKEN` | the token from step 2 |
| `OTEL_SERVICE_NAME` | the name of the service, shown as its own row in the dashboard |

The platform accepts OTLP over HTTP (`/v1/metrics`, `/v1/traces`, `/v1/logs`, protobuf or JSON,
optionally gzip or zstd compressed) and OTLP over gRPC, both on port 443.

## 4. Make sure requests carry a route

Requests are grouped by `http.route`, the route template such as `/accounts/{id}`. Most framework
instrumentations set it; [http-route.md](http-route.md) shows how for each example and what happens
without it.

## 5. See the data

Deploy, send some requests, and open the project's **API traffic** page in the console. Request
counts, status codes and latency appear per service and route. Metrics arrive at the SDK's export
interval (60 seconds by default in most SDKs; the examples use 10 seconds), so allow a minute.

If nothing appears, see [troubleshooting.md](troubleshooting.md).

## Metrics or traces

Send metrics if you can. The platform reads `http.server.request.duration` (or the older
`http.server.duration`) and counts every request. If your app sends only traces, the platform
derives the same numbers from its server spans and then discards the spans; the dashboard labels
such a service "from traces". Spans undercount when your SDK samples traces, metrics never do.
