# otel-go-nethttp

A small accounts and payments API on Go `net/http` routing patterns (Go 1.22 or newer), instrumented with `otelhttp` v0.72.0 and the OpenTelemetry Go SDK v1.47.0.

This example has its own `go.mod`, so a tenant can copy the directory as is and the platform module does not pick up SDK dependencies.

`otelhttp` puts `http.route` on metrics but not on the server span, so each handler copies its pattern onto the span.

## Environment

| Variable | Values | Purpose |
|---|---|---|
| `TENANT_OTLP_ENDPOINT` | base URL, required | your own backend, e.g. `http://otel-collector:4318` |
| `PLATFORM_OTLP_ENDPOINT` | base URL, required | Stellar Analytics, `https://otlp.<BASE_DOMAIN>` |
| `PLATFORM_OTLP_TOKEN` | `sa_otlp_...`, required | sent as `Authorization: Bearer <token>` to the platform only |
| `EXPORT_SIGNALS` | `metrics,traces` (default), `traces` | `traces` registers no metric readers; the platform then derives request metrics from server spans |
| `METRICS_TEMPORALITY` | `cumulative` (default), `delta` | aggregation temporality of both metric exporters |
| `METRICS_HISTOGRAM` | `explicit` (default), `exponential` | histogram aggregation; exponential histograms arrive at the platform re-bucketed and marked lossy |
| `METRICS_EXPORT_INTERVAL_MS` | default `10000` | periodic reader interval |
| `OTEL_SERVICE_NAME` | default: the app name | `service.name` resource attribute |
| `PORT` | default `8080` | listen port |

Unknown values stop the app at startup. Each signal has two exporters: the tenant copy is unauthenticated and unfiltered, the platform copy carries the bearer token. On SIGTERM the app stops accepting requests, then flushes both providers.

Routes: `POST /accounts`, `GET /accounts/{id}`, `POST /accounts/{id}/payments`, `GET /accounts/{id}/payments`, plus an uninstrumented `GET /healthz`. Account lookups run in an internal span named `SA_MARKER_internal_span` with an event `SA_MARKER_span_event`; the platform tests use these markers to prove that only server spans are used.

`deploy/compose/examples.yml` runs this app next to stock collectors, and `tests/otelapps` drives it (`go test -tags otelapps ./tests/otelapps/`).

    docker build -t otel-go-nethttp .
