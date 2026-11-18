# otel-java-spring

A small accounts and payments API on Spring Boot 4.1.2 and Java 25, instrumented by the OpenTelemetry Java agent 2.33.1 without code changes. The agent is attached with `-javaagent` and configured by `otel.yaml`, a declarative configuration file with two exporters per signal. The app depends on `opentelemetry-api` 1.67.0 only for its own span.

## Semantic conventions

The agent emits `http.server.request.duration` in seconds with `http.route` from the Spring MVC mapping (`/accounts/{id}`). No opt-in variable is needed: the stable HTTP semantic conventions are the agent's default.

The agent records every request Tomcat serves, `/healthz` included. The platform marks health routes as infrastructure traffic and leaves them out of request counts, error rates and latency.

## Environment

| Variable | Values | Purpose |
|---|---|---|
| `TENANT_OTLP_ENDPOINT` | base URL, required | your own backend, e.g. `http://otel-collector:4318` |
| `PLATFORM_OTLP_ENDPOINT` | base URL, required | Veristar, `https://otlp.<BASE_DOMAIN>` |
| `PLATFORM_OTLP_TOKEN` | `sa_otlp_...`, required | sent as `Authorization: Bearer <token>` to the platform only |
| `EXPORT_SIGNALS` | `metrics,traces` (default), `traces` | `traces` starts the agent with `otel-traces.yaml`, which has no meter provider; the platform then derives request metrics from server spans |
| `METRICS_TEMPORALITY` | `cumulative` (default), `delta` | aggregation temporality of both metric exporters |
| `METRICS_HISTOGRAM` | `explicit` (default), `exponential` | histogram aggregation; exponential histograms arrive at the platform re-bucketed and marked lossy |
| `METRICS_EXPORT_INTERVAL_MS` | default `10000` | periodic reader interval |
| `OTEL_SERVICE_NAME` | default: the app name | `service.name` resource attribute |
| `PORT` | default `8080` | listen port |

Unknown values stop the app at startup (`start.sh`). Each signal has two exporters: the tenant copy is unauthenticated and unfiltered, the platform copy carries the bearer token. On SIGTERM Spring finishes the requests in flight and the agent flushes both providers in its shutdown hook; the app exits with 0.

Routes: `POST /accounts`, `GET /accounts/{id}`, `POST /accounts/{id}/payments`, `GET /accounts/{id}/payments`, plus `GET /healthz`. Account lookups run in an internal span named `SA_MARKER_internal_span` with an event `SA_MARKER_span_event`; the platform tests use these markers to prove that only server spans are used.

`deploy/compose/examples.yml` runs this app next to stock collectors, and `tests/otelapps` drives it (`go test -tags otelapps ./tests/otelapps/`).

    docker build -t otel-java-spring .
