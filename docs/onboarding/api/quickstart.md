---
title: "API analytics quickstart"
sidebarTitle: "Quickstart"
description: "Count your backend's HTTP requests, status codes and latency by adding a second OpenTelemetry exporter."
icon: "server"
---

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

Sign in to the console with GitHub, a Stellar wallet or a passkey ([Signing in](../sign-in.md)) and
choose **Enroll a project**, as for web analytics
([web quickstart](../web/quickstart.md#1-enroll-your-project)). A project can have sites and APIs.

## 2. Create a token

Open the project's **Connect API** page (Setup in the sidebar). It shows the OTLP endpoint for your
service and lets any project member create tokens: in the **Create an ingest token** step, give the
token a **Token name** and, optionally, an **Environment** (for example `production`), then choose **Create token**. The token (`sa_otlp_`
followed by 40 characters) is shown once. Store it as a secret of your app, for example in your
secret manager or deployment environment. If you lose it, create a new one and revoke the old one.

You create tokens once the project is active (approved, not waiting for review and not suspended)
and an owner of the project has accepted the current terms of service. Create one token per app or
environment, so you can revoke one without touching the others.

## 3. Add the second exporter

Follow the guide for your language. Each is built from an example app that the platform's
integration tests run against stock collectors and against the real platform collector:

<CardGroup cols={2}>
  <Card horizontal title="Node.js" icon="node-js" iconType="brands" className="tone-api" href="/onboarding/api/node">
    Express 5
  </Card>
  <Card horizontal title="Go" icon="golang" iconType="brands" className="tone-api" href="/onboarding/api/go">
    `net/http`
  </Card>
  <Card horizontal title="Python" icon="python" iconType="brands" className="tone-api" href="/onboarding/api/python">
    FastAPI
  </Card>
  <Card horizontal title="Rust" icon="rust" iconType="brands" className="tone-api" href="/onboarding/api/rust">
    actix-web
  </Card>
  <Card horizontal title="Java" icon="java" iconType="brands" className="tone-api" href="/onboarding/api/java">
    Spring Boot with the OpenTelemetry Java agent
  </Card>
</CardGroup>

All five use the same environment variables:

| Variable | Value |
|---|---|
| `TENANT_OTLP_ENDPOINT` | your own backend, for example `http://otel-collector:4318` |
| `PLATFORM_OTLP_ENDPOINT` | `https://otlp.analytics.example` (the console shows yours) |
| `PLATFORM_OTLP_TOKEN` | the token from step 2 |
| `OTEL_SERVICE_NAME` | the name of the service, shown as its own row in the dashboard |

The platform accepts OTLP over HTTP (`/v1/metrics`, `/v1/traces`, `/v1/logs`, protobuf or JSON,
optionally gzip or zstd compressed) and OTLP over gRPC, both on port 443.

Running the Stellar Anchor Platform? [anchor-platform.md](anchor-platform.md) attaches the Java
agent to its SEP server and platform server without code changes.

## 4. Make sure requests carry a route

Requests are grouped by `http.route`, the route template such as `/accounts/{id}`. In all five
examples the framework instrumentation sets it. [http-route.md](http-route.md) shows who sets it in
each example, how to set it yourself and what happens without it.

## 5. See the data

Deploy, send some requests, and open the project's **API** page in the
console, or choose **See the traffic** in the **First data** step of **Connect API**. Request counts, status codes and latency appear per service and route.
Metrics arrive at the SDK's export interval (the OpenTelemetry default is 60 seconds; the examples
use 10 seconds), so allow a minute.

If nothing appears, see [troubleshooting.md](troubleshooting.md).

## Metrics or traces

Send metrics if you can. The platform reads `http.server.request.duration` (or the older
`http.server.duration`) and counts every request. If your app sends only traces, the platform
derives the same numbers from its server spans and then discards the spans; the dashboard labels
such a service "from traces". Spans undercount when your SDK samples traces, metrics never do.
