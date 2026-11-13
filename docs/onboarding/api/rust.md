---
title: "Rust"
description: "Dual export from a Rust service on actix-web with the OpenTelemetry Rust SDK."
---

Dual export from a Rust service on actix-web. Built from the example
[`examples/otel-rust-actix`](https://github.com/justmert/veristar/tree/main/examples/otel-rust-actix): actix-web 4.18.2 with
`opentelemetry-instrumentation-actix-web` 0.25.0 (`metrics` feature) and `opentelemetry` 0.33.1,
on Rust 1.99.1. `tests/otelapps` runs it against stock collectors in every export mode, and the
`apipath` tag runs it against the real platform collector. Read the
[API quickstart](quickstart.md) first for the token and the environment variables.

## Crates

{/* include examples/otel-rust-actix/Cargo.toml region=dependencies */}
```toml
[dependencies]
actix-web = { version = "=4.18.2", default-features = false, features = ["macros"] }
opentelemetry = { version = "=0.33.1", features = ["trace", "metrics"] }
# views are needed to switch histograms to base2 exponential aggregation
opentelemetry_sdk = { version = "=0.33.1", features = ["trace", "metrics", "spec_unstable_metrics_views"] }
# http/protobuf with the blocking reqwest client, which the sdk's batch span
# processor and periodic reader drive from their own background threads, and
# rustls so https platform endpoints work without a system openssl
opentelemetry-otlp = { version = "=0.33.1", default-features = false, features = ["http-proto", "reqwest-blocking-client", "reqwest-rustls", "trace", "metrics"] }
opentelemetry-instrumentation-actix-web = { version = "=0.25.0", features = ["metrics"] }
serde = { version = "1", features = ["derive"] }
serde_json = "1"
tokio = { version = "=1.54.1", features = ["macros", "rt-multi-thread", "signal"] }
```

## The SDK setup

One tracer provider and one meter provider, each with two exporters; only the platform exporters
carry the token. The exporters use HTTP with protobuf through the blocking reqwest client and
rustls, so an https endpoint works without a system OpenSSL. That client must be built outside an
async runtime, so the example sets telemetry up before the actix system starts:

{/* include examples/otel-rust-actix/src/main.rs region=telemetry */}
```rust
// one tracer provider and one meter provider, each with two exporters, so
// both backends see identical data from the same sdk state
fn setup_telemetry(c: &Config) -> Result<(SdkTracerProvider, Option<SdkMeterProvider>), BoxError> {
    let resource = Resource::builder()
        .with_service_name(c.service_name.clone())
        .build();
    let auth = HashMap::from([(
        "Authorization".to_owned(),
        format!("Bearer {}", c.platform_token),
    )]);

    let tenant_spans = SpanExporter::builder()
        .with_http()
        .with_endpoint(format!("{}/v1/traces", c.tenant_endpoint))
        .build()?;
    let platform_spans = SpanExporter::builder()
        .with_http()
        .with_endpoint(format!("{}/v1/traces", c.platform_endpoint))
        .with_headers(auth.clone())
        .build()?;
    let tracer_provider = SdkTracerProvider::builder()
        .with_resource(resource.clone())
        .with_span_processor(BatchSpanProcessor::builder(tenant_spans).build())
        .with_span_processor(BatchSpanProcessor::builder(platform_spans).build())
        .build();
    global::set_tracer_provider(tracer_provider.clone());
    global::set_text_map_propagator(TraceContextPropagator::new());

    if !c.metrics_enabled {
        return Ok((tracer_provider, None));
    }
    let temporality = if c.delta {
        Temporality::Delta
    } else {
        Temporality::Cumulative
    };
    let reader =
        |endpoint: &str, headers: Option<HashMap<String, String>>| -> Result<_, BoxError> {
            let mut b = MetricExporter::builder()
                .with_http()
                .with_endpoint(format!("{endpoint}/v1/metrics"))
                .with_temporality(temporality);
            if let Some(h) = headers {
                b = b.with_headers(h);
            }
            Ok(PeriodicReader::builder(b.build()?)
                .with_interval(c.interval)
                .build())
        };
    let mut builder = SdkMeterProvider::builder()
        .with_resource(resource)
        .with_reader(reader(&c.tenant_endpoint, None)?)
        .with_reader(reader(&c.platform_endpoint, Some(auth))?);
    if c.exponential {
        // max size and scale are the sdk defaults for base2 exponential histograms
        builder = builder.with_view(|i: &Instrument| {
            (i.kind() == InstrumentKind::Histogram).then(|| {
                Stream::builder()
                    .with_aggregation(Aggregation::Base2ExponentialHistogram {
                        max_size: 160,
                        max_scale: 20,
                        record_min_max: true,
                    })
                    .build()
                    .expect("a base2 exponential histogram stream with valid limits")
            })
        });
    }
    let meter_provider = builder.build();
    global::set_meter_provider(meter_provider.clone());
    Ok((tracer_provider, Some(meter_provider)))
}
```

## Instrumenting the server

Wrap the routes with `RequestMetrics` and `RequestTracing`. The health check route sits outside
the wrapped scope, so probes do not count as API traffic:

{/* include examples/otel-rust-actix/src/main.rs region=middleware */}
```rust
.service(
    web::scope("")
        .wrap(RequestMetrics::default())
        .wrap(RequestTracing::new())
        .route("/accounts", web::post().to(create_account))
        .route("/accounts/{id}", web::get().to(get_account))
        .route("/accounts/{id}/payments", web::post().to(create_payment))
        .route("/accounts/{id}/payments", web::get().to(list_payments)),
)
```

The middleware sets `http.route` from the actix route pattern (`/accounts/{id}`); see
[http-route.md](http-route.md).

## Notes

- **Shut down cleanly.** After `HttpServer::run` returns (actix stops gracefully on `SIGTERM`),
  call `shutdown()` on the meter provider and then the tracer provider, so the last interval is
  exported.
- **Exponential histograms** need a view, which needs the `spec_unstable_metrics_views` feature
  of `opentelemetry_sdk`. The default explicit buckets need nothing extra.

## Export modes the tests cover

| Variable | Values |
|---|---|
| `EXPORT_SIGNALS` | `metrics,traces` (default) or `traces` |
| `METRICS_TEMPORALITY` | `cumulative` (default) or `delta` |
| `METRICS_HISTOGRAM` | `explicit` (default) or `exponential` |
