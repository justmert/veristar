# otel-rust-actix

A small accounts and payments API on actix-web 4.18.2, instrumented with `opentelemetry-instrumentation-actix-web` 0.25.0 (`metrics` feature) and `opentelemetry` 0.33.1. Rust 1.99.1 is pinned in `rust-toolchain.toml`.

The exporters use http/protobuf with the blocking reqwest client and rustls. That client must be built outside an async runtime, so telemetry is set up before the actix system starts. Exponential histograms use a view, which needs the `spec_unstable_metrics_views` SDK feature.

## Environment

| Variable | Values | Purpose |
|---|---|---|
| `TENANT_OTLP_ENDPOINT` | base URL, required | your own backend, e.g. `http://otel-collector:4318` |
| `PLATFORM_OTLP_ENDPOINT` | base URL, required | Veristar, `https://otlp.<BASE_DOMAIN>` |
| `PLATFORM_OTLP_TOKEN` | `sa_otlp_...`, required | sent as `Authorization: Bearer <token>` to the platform only |
| `EXPORT_SIGNALS` | `metrics,traces` (default), `traces` | `traces` registers no metric readers; the platform then derives request metrics from server spans |
| `METRICS_TEMPORALITY` | `cumulative` (default), `delta` | aggregation temporality of both metric exporters |
| `METRICS_HISTOGRAM` | `explicit` (default), `exponential` | histogram aggregation; exponential histograms arrive at the platform re-bucketed and marked approximate |
| `METRICS_EXPORT_INTERVAL_MS` | default `10000` | periodic reader interval |
| `OTEL_SERVICE_NAME` | default: the app name | `service.name` resource attribute |
| `PORT` | default `8080` | listen port |

Unknown values stop the app at startup. Each signal has two exporters: the copy to your own backend carries no token and is unfiltered, the platform copy carries the bearer token. On SIGTERM the app stops accepting requests, then flushes both providers.

Routes: `POST /accounts`, `GET /accounts/{id}`, `POST /accounts/{id}/payments`, `GET /accounts/{id}/payments`, plus an uninstrumented `GET /healthz`. Account lookups run in an internal span named `SA_MARKER_internal_span` with an event `SA_MARKER_span_event`; the platform tests use these markers to prove that only server spans are used.

`deploy/compose/examples.yml` runs this app next to stock collectors, and `tests/otelapps` drives it (`go test -tags otelapps ./tests/otelapps/`).

    docker build -t otel-rust-actix .
