---
title: "Stellar Anchor Platform"
description: "Request metrics from the Stellar Anchor Platform's SEP server and platform server, with the OpenTelemetry Java agent."
icon: "anchor"
---

Count the requests your anchor's SEP endpoints and platform API serve, per route and status,
without changing the Anchor Platform. The
[Stellar Anchor Platform](https://github.com/stellar/anchor-platform) is a Spring Boot application,
so the OpenTelemetry Java agent instruments it when the JVM starts. Built from the
example [`examples/otel-anchor-platform`](https://github.com/justmert/veristar/tree/main/examples/otel-anchor-platform): Anchor Platform 5.0.0
(`stellar/anchor-platform`) with the OpenTelemetry Java agent 2.33.1. `TestAnchorPlatform` in
`tests/otelapps` runs it against stock collectors. Read the [API quickstart](quickstart.md) first
for the token.

The Anchor Platform has no OTLP exporter of its own: its `metrics` setting exposes Prometheus
metrics on each server's management port, and its `sentry` setting reports to Sentry. Both keep
working next to the agent.

## Add the agent to the image

Build your image from the Anchor Platform image and add the agent jar, pinned by version and
checksum:

{/* include examples/otel-anchor-platform/Dockerfile region=agent */}
```dockerfile
WORKDIR /otel
ADD --chmod=444 --checksum=sha256:e43a270df53c6f423e0ab157be9c27ccc01d5dc3232e162fe54777573db2f149 \
    https://repo1.maven.org/maven2/io/opentelemetry/javaagent/opentelemetry-javaagent/2.33.1/opentelemetry-javaagent-2.33.1.jar \
    /otel/opentelemetry-javaagent.jar
WORKDIR /app
# the image's own entrypoint (/app/start.sh) runs java as a child of bash,
# which does not pass sigterm on, so the agent never flushes on a stop. java
# as the entrypoint receives it directly. JAVA_TOOL_OPTIONS takes the jvm
# options start.sh read from JVM_FLAGS.
ENTRYPOINT ["java", "-javaagent:/otel/opentelemetry-javaagent.jar", "-jar", "/app/anchor-platform-runner.jar"]
```

The image's own entrypoint, `/app/start.sh`, starts Java as a child of bash. As the container's
first process, bash does not pass SIGTERM on, so `docker stop` ends in SIGKILL after the timeout and
the agent never exports its last interval. Java as the entrypoint receives SIGTERM directly; the
agent then exports in its shutdown hook. Pass JVM options in `JAVA_TOOL_OPTIONS`, which the JVM
reads itself, instead of the image's `JVM_FLAGS`.

## Configure the agent

The agent reads `OTEL_*` variables. Metrics and traces each have their own endpoint: metrics go to
the platform with your token, traces to your own OTLP backend, and logs nowhere:

{/* include examples/otel-anchor-platform/compose.yaml region=otel */}
```yaml
# the java agent reads its settings from OTEL_* variables. metrics and traces
# have their own endpoint, so each signal goes to one backend.
x-otel: &otel
  OTEL_METRICS_EXPORTER: otlp
  OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: ${PLATFORM_OTLP_ENDPOINT:?set PLATFORM_OTLP_ENDPOINT}/v1/metrics
  # the space in "Bearer <token>" is written %20: header values are url-encoded
  OTEL_EXPORTER_OTLP_METRICS_HEADERS: Authorization=Bearer%20${PLATFORM_OTLP_TOKEN:?set PLATFORM_OTLP_TOKEN}
  OTEL_METRIC_EXPORT_INTERVAL: ${METRICS_EXPORT_INTERVAL_MS:-60000}
  # set to none if the anchor keeps no traces of its own
  OTEL_TRACES_EXPORTER: otlp
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: ${TENANT_OTLP_ENDPOINT:?set TENANT_OTLP_ENDPOINT}/v1/traces
  OTEL_LOGS_EXPORTER: none
```

- **`OTEL_EXPORTER_OTLP_METRICS_HEADERS`** writes the space in `Bearer <token>` as `%20`, because
  the agent URL-decodes header values.
- **`OTEL_TRACES_EXPORTER: none`** sends no traces when your anchor keeps none, or keeps them in
  Sentry only.
- **`OTEL_METRIC_EXPORT_INTERVAL`** is in milliseconds; the agent's default of 60000 suits
  production.

The platform keeps only `http.server.request.duration` and discards the agent's JVM, database
connection pool and HTTP client metrics on arrival ([what-gets-dropped.md](what-gets-dropped.md)).
Your own backend receives the full traces: server spans, JDBC queries and outgoing calls.

## Run each server in its own container

Start the SEP server and the platform server as separate containers, each with its own
`OTEL_SERVICE_NAME`, so the dashboard shows them as two services:

{/* include examples/otel-anchor-platform/compose.yaml region=services */}
```yaml
sep-server:
  build: .
  image: anchor-platform-otel:5.0.0
  command: ["--sep-server"]
  env_file: anchor.env
  environment:
    <<: [*otel, *anchor-secrets]
    OTEL_SERVICE_NAME: anchor-sep-server
  volumes: ["./config:/config:ro"]
  ports: ["127.0.0.1:8080:8080"]
  depends_on:
    db: { condition: service_healthy }

platform-server:
  build: .
  image: anchor-platform-otel:5.0.0
  command: ["--platform-server"]
  env_file: anchor.env
  environment:
    <<: [*otel, *anchor-secrets]
    OTEL_SERVICE_NAME: anchor-platform-server
  volumes: ["./config:/config:ro"]
  ports: ["127.0.0.1:8085:8085"]
  depends_on:
    db: { condition: service_healthy }
    sep-server: { condition: service_started }
```

A single container started with `--sep-server --platform-server` serves both from one JVM; the agent
then reports both under one service name. The event processor and the Stellar observer serve only
their health endpoints, so they need no agent for API analytics.

## Routes

The agent's Spring MVC instrumentation sets `http.route` to the controller's mapping. No route
needs setting by hand. `TestAnchorPlatform` checks each of these against what reaches the platform
stand-in and the tenant backend:

| Request | Service | Route | Status |
|---|---|---|---|
| `GET /.well-known/stellar.toml` | `anchor-sep-server` | `/.well-known/stellar.toml` | 200 |
| `GET /sep24/info` | `anchor-sep-server` | `/sep24/info` | 200 |
| `GET /auth?account=G...` | `anchor-sep-server` | `/auth` | 200 |
| `POST /sep24/transactions/deposit/interactive` | `anchor-sep-server` | `/sep24/transactions/deposit/interactive` | 200 |
| `GET /sep24/transaction?id=...` with a SEP-10 token | `anchor-sep-server` | `/sep24/transaction` | 200 |
| `GET /sep24/transaction?id=...` without a token | `anchor-sep-server` | `/sep24/transaction` | 403 |
| a path no controller serves | `anchor-sep-server` | `/**` | 404 |
| `GET /transactions/<id>` | `anchor-platform-server` | `/transactions/{id}` | 200, 404 |
| `GET /transactions?sep=24` | `anchor-platform-server` | `/transactions` | 200 |
| `GET /health` | `anchor-platform-server` | `/health` | 200 |

Requests the SEP-10 token filter refuses carry the route of the endpoint they asked for, so the
403s show next to the 200s of the same endpoint. Query strings never become part of a route. The
platform marks `/health` as infrastructure traffic and leaves it out of request counts, error
rates and latency.

## Try it locally

The example runs the SEP server and the platform server on PostgreSQL with SEP-1, SEP-10 and
SEP-24 for native XLM. Generate a signing seed with any Stellar SDK or the Stellar CLI. Set
`TENANT_OTLP_ENDPOINT` to an OTLP/HTTP backend the containers reach (the value below is an
example), or change `OTEL_TRACES_EXPORTER` in `compose.yaml` to `none`. Then:

```sh
export ANCHOR_SEP10_SIGNING_SEED=S... ANCHOR_JWT_SECRET="$(openssl rand -hex 32)" \
  ANCHOR_DB_PASSWORD="$(openssl rand -hex 16)" \
  TENANT_OTLP_ENDPOINT=http://otel-collector:4318 \
  PLATFORM_OTLP_ENDPOINT=https://otlp.analytics.example PLATFORM_OTLP_TOKEN=sa_otlp_...
docker compose -f examples/otel-anchor-platform/compose.yaml up -d --build
curl -s http://127.0.0.1:8080/sep24/info
```

Open the project's **API** page: `anchor-sep-server` shows `GET /sep24/info` after the
next export.
