---
title: "Python"
description: "Dual export from a Python service on FastAPI with the OpenTelemetry Python SDK."
icon: "python"
iconType: "brands"
---

Dual export from a Python service. Built from the example
[`examples/otel-python-fastapi`](https://github.com/justmert/veristar/tree/main/examples/otel-python-fastapi): FastAPI 0.161.1 on uvicorn
0.54.0 with `opentelemetry-instrumentation-fastapi` 0.66b2 and the OpenTelemetry Python SDK 1.45.2,
on Python 3.14.10 with uv. `tests/otelapps` runs it against stock collectors in every export mode,
including both semantic convention settings below, and the `apipath` tag runs it against the real
platform collector. Read the [API quickstart](quickstart.md) first for the token and the
environment variables.

## Packages

{/* include examples/otel-python-fastapi/pyproject.toml region=dependencies */}
```toml
dependencies = [
    "fastapi==0.161.1",
    "uvicorn==0.54.0",
    "opentelemetry-api==1.45.2",
    "opentelemetry-sdk==1.45.2",
    "opentelemetry-exporter-otlp-proto-http==1.45.2",
    "opentelemetry-instrumentation-fastapi==0.66b2",
]
```

The instrumentation packages are published only as beta versions (`0.66b2`); pin them exactly, as
here, to the release that matches the SDK version.

## OTEL_SEMCONV_STABILITY_OPT_IN

Set `OTEL_SEMCONV_STABILITY_OPT_IN=http`. The example's image sets it:

{/* include examples/otel-python-fastapi/Dockerfile region=semconv */}
```dockerfile
ENV OTEL_SEMCONV_STABILITY_OPT_IN=http
```

| Value | The FastAPI instrumentation emits | Platform |
|---|---|---|
| `http` | `http.server.request.duration` in seconds, with `http.route` and the current attribute names | stored as sent |
| empty or unset | the legacy `http.server.duration` in milliseconds, with `http.target` holding the route template and the old attribute names | converted to seconds and to the current names; tested |
| `http/dup` | both metrics for every request | the platform keeps `http.server.request.duration` and drops `http.server.duration` when one instrumentation sends both, so each request is counted once (tested in the collector's unit tests). `http` still saves sending the duplicate. |

Both tested settings give the same numbers in the dashboard; `http` avoids the conversion.

## The SDK setup

Two exporters per signal; only the platform exporters carry the token:

{/* include examples/otel-python-fastapi/app.py region=telemetry */}
```python
AUTH = {"Authorization": f"Bearer {PLATFORM_TOKEN}"}
resource = Resource.create({"service.name": SERVICE_NAME})

tracer_provider = TracerProvider(resource=resource)
tracer_provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=f"{TENANT_ENDPOINT}/v1/traces")))
tracer_provider.add_span_processor(
    BatchSpanProcessor(OTLPSpanExporter(endpoint=f"{PLATFORM_ENDPOINT}/v1/traces", headers=AUTH))
)
trace.set_tracer_provider(tracer_provider)


def metric_reader(endpoint: str, headers: dict[str, str] | None) -> PeriodicExportingMetricReader:
    # delta follows the spec's delta preference: monotonic instruments and
    # histograms switch, up-down counters and gauges stay cumulative
    temporality = None
    if DELTA:
        temporality = {
            Counter: AggregationTemporality.DELTA,
            Histogram: AggregationTemporality.DELTA,
            ObservableCounter: AggregationTemporality.DELTA,
            UpDownCounter: AggregationTemporality.CUMULATIVE,
            ObservableUpDownCounter: AggregationTemporality.CUMULATIVE,
            ObservableGauge: AggregationTemporality.CUMULATIVE,
        }
    aggregation = {Histogram: ExponentialBucketHistogramAggregation()} if EXPONENTIAL else None
    exporter = OTLPMetricExporter(
        endpoint=f"{endpoint}/v1/metrics",
        headers=headers,
        preferred_temporality=temporality,
        preferred_aggregation=aggregation,
    )
    return PeriodicExportingMetricReader(exporter, export_interval_millis=INTERVAL_MS)


meter_provider = None
if METRICS_ENABLED:
    meter_provider = MeterProvider(
        resource=resource,
        metric_readers=[metric_reader(TENANT_ENDPOINT, None), metric_reader(PLATFORM_ENDPOINT, AUTH)],
    )
```

## Instrumenting the app

Pass both providers to the instrumentor explicitly. Health checks are excluded so probes do not
count as API traffic:

{/* include examples/otel-python-fastapi/app.py region=instrument */}
```python
# health checks are excluded so probes never change the request counts
FastAPIInstrumentor.instrument_app(
    app, tracer_provider=tracer_provider, meter_provider=meter_provider, excluded_urls="/healthz"
)
```

FastAPI's instrumentation sets `http.route` from the path template (`/accounts/{account_id}`);
see [http-route.md](http-route.md).

## Notes

- **Shut down cleanly.** Shut down the meter provider and the tracer provider at the end of the
  app's lifespan, after uvicorn stopped accepting requests, so the last interval is exported (see
  `lifespan` in [`app.py`](https://github.com/justmert/veristar/blob/main/examples/otel-python-fastapi/app.py)).
- **Delta temporality** follows the specification's delta preference: counters and histograms
  switch to delta, up-down counters and gauges stay cumulative.

## Export modes the tests cover

| Variable | Values |
|---|---|
| `EXPORT_SIGNALS` | `metrics,traces` (default) or `traces` |
| `METRICS_TEMPORALITY` | `cumulative` (default) or `delta` |
| `METRICS_HISTOGRAM` | `explicit` (default) or `exponential` |
| `OTEL_SEMCONV_STABILITY_OPT_IN` | `http` (default in the image) or empty |
