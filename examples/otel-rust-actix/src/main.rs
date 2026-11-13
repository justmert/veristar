//! actix-web example that dual-exports opentelemetry data: one copy to the
//! tenant's own backend and one to veristar. see README.md for the
//! environment contract.

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;

use actix_web::{App, HttpResponse, HttpServer, web};
use opentelemetry::trace::{Span, SpanKind, Tracer};
use opentelemetry::{KeyValue, global};
use opentelemetry_instrumentation_actix_web::{RequestMetrics, RequestTracing};
use opentelemetry_otlp::{MetricExporter, SpanExporter, WithExportConfig, WithHttpConfig};
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::metrics::{
    Aggregation, Instrument, InstrumentKind, PeriodicReader, SdkMeterProvider, Stream, Temporality,
};
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::{BatchSpanProcessor, SdkTracerProvider};
use serde::Deserialize;
use serde_json::json;

struct Config {
    port: u16,
    service_name: String,
    tenant_endpoint: String,
    platform_endpoint: String,
    platform_token: String,
    metrics_enabled: bool,
    delta: bool,
    exponential: bool,
    interval: Duration,
}

fn env_or(name: &str, default: &str) -> String {
    match std::env::var(name) {
        Ok(v) if !v.is_empty() => v,
        _ => default.to_owned(),
    }
}

fn required(name: &str) -> Result<String, String> {
    match std::env::var(name) {
        Ok(v) if !v.is_empty() => Ok(v.trim_end_matches('/').to_owned()),
        _ => Err(format!("{name} is required")),
    }
}

// unknown values are errors so a typo never silently runs another mode
fn choice(name: &str, allowed: &[&str], default: &str) -> Result<String, String> {
    let v = env_or(name, default);
    if allowed.contains(&v.as_str()) {
        Ok(v)
    } else {
        Err(format!(
            "{name} must be one of {}, got {v:?}",
            allowed.join(", ")
        ))
    }
}

fn load_config() -> Result<Config, String> {
    let interval: u64 = env_or("METRICS_EXPORT_INTERVAL_MS", "10000")
        .parse()
        .ok()
        .filter(|v| *v > 0)
        .ok_or("METRICS_EXPORT_INTERVAL_MS must be a positive integer")?;
    Ok(Config {
        port: env_or("PORT", "8080")
            .parse()
            .map_err(|_| "PORT must be a port number")?,
        service_name: env_or("OTEL_SERVICE_NAME", "otel-rust-actix"),
        tenant_endpoint: required("TENANT_OTLP_ENDPOINT")?,
        platform_endpoint: required("PLATFORM_OTLP_ENDPOINT")?,
        platform_token: required("PLATFORM_OTLP_TOKEN")?,
        metrics_enabled: choice(
            "EXPORT_SIGNALS",
            &["metrics,traces", "traces"],
            "metrics,traces",
        )? == "metrics,traces",
        delta: choice(
            "METRICS_TEMPORALITY",
            &["cumulative", "delta"],
            "cumulative",
        )? == "delta",
        exponential: choice(
            "METRICS_HISTOGRAM",
            &["explicit", "exponential"],
            "explicit",
        )? == "exponential",
        interval: Duration::from_millis(interval),
    })
}

type BoxError = Box<dyn std::error::Error + Send + Sync>;

// the console's api analytics setup page shows the lines between each
// docs:start and docs:end marker pair (apps/console/src/lib/otel-guides.ts),
// so the instructions are this code and cannot drift from it.

// docs:start telemetry
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

// docs:end telemetry

#[derive(Default)]
struct Store {
    next: u64,
    accounts: HashMap<String, String>,
    payments: HashMap<String, Vec<(String, String)>>,
}

type Shared = web::Data<Mutex<Store>>;

#[derive(Deserialize)]
struct AccountBody {
    #[serde(default)]
    name: String,
}

#[derive(Deserialize)]
struct PaymentBody {
    #[serde(default)]
    amount: String,
}

fn error(status: actix_web::http::StatusCode, message: &str) -> HttpResponse {
    HttpResponse::build(status).json(json!({ "error": message }))
}

// lookup runs in an internal span with an event. both carry marker strings so
// the platform tests can prove non-server spans and events never leave its
// collector, while the tenant backend still receives them.
fn lookup(store: &Shared, id: &str) -> Option<String> {
    let tracer = global::tracer("otel-rust-actix");
    let mut span = tracer
        .span_builder("SA_MARKER_internal_span")
        .with_kind(SpanKind::Internal)
        .start(&tracer);
    span.add_event(
        "SA_MARKER_span_event",
        vec![KeyValue::new("account.lookup", id.to_owned())],
    );
    let found = store.lock().expect("store lock").accounts.get(id).cloned();
    span.end();
    found
}

// a positive decimal with up to 7 fractional digits, the stellar amount precision
fn valid_amount(a: &str) -> bool {
    let (int, frac) = a.split_once('.').unwrap_or((a, ""));
    let digits = |s: &str| s.bytes().all(|b| b.is_ascii_digit());
    !int.is_empty()
        && digits(int)
        && digits(frac)
        && (a.contains('.') == !frac.is_empty())
        && frac.len() <= 7
        && a.bytes().any(|b| (b'1'..=b'9').contains(&b))
}

async fn healthz() -> HttpResponse {
    HttpResponse::NoContent().finish()
}

async fn create_account(store: Shared, body: web::Json<AccountBody>) -> HttpResponse {
    let name = body.name.trim();
    if name.is_empty() {
        return error(actix_web::http::StatusCode::BAD_REQUEST, "name is required");
    }
    let mut s = store.lock().expect("store lock");
    s.next += 1;
    let id = s.next.to_string();
    s.accounts.insert(id.clone(), name.to_owned());
    s.payments.insert(id.clone(), Vec::new());
    HttpResponse::Created().json(json!({ "id": id, "name": name }))
}

async fn get_account(store: Shared, id: web::Path<String>) -> HttpResponse {
    match lookup(&store, &id) {
        Some(name) => HttpResponse::Ok().json(json!({ "id": *id, "name": name })),
        None => error(actix_web::http::StatusCode::NOT_FOUND, "account not found"),
    }
}

async fn create_payment(
    store: Shared,
    id: web::Path<String>,
    body: web::Json<PaymentBody>,
) -> HttpResponse {
    if lookup(&store, &id).is_none() {
        return error(actix_web::http::StatusCode::NOT_FOUND, "account not found");
    }
    if !valid_amount(&body.amount) {
        return error(
            actix_web::http::StatusCode::BAD_REQUEST,
            "amount must be a positive decimal",
        );
    }
    let mut s = store.lock().expect("store lock");
    let list = s
        .payments
        .get_mut(id.as_str())
        .expect("payments exist for every account");
    let pid = (list.len() + 1).to_string();
    list.push((pid.clone(), body.amount.clone()));
    HttpResponse::Created().json(json!({ "id": pid, "amount": body.amount }))
}

async fn list_payments(store: Shared, id: web::Path<String>) -> HttpResponse {
    if lookup(&store, &id).is_none() {
        return error(actix_web::http::StatusCode::NOT_FOUND, "account not found");
    }
    let s = store.lock().expect("store lock");
    let list: Vec<_> = s.payments[id.as_str()]
        .iter()
        .map(|(pid, amount)| json!({ "id": pid, "amount": amount }))
        .collect();
    HttpResponse::Ok().json(json!({ "payments": list }))
}

fn main() -> Result<(), BoxError> {
    let c = load_config()?;
    // the blocking http client must be built outside any async runtime, so
    // telemetry is set up before the actix system starts
    let (tracer_provider, meter_provider) = setup_telemetry(&c)?;
    let store: Shared = web::Data::new(Mutex::new(Store::default()));
    println!(
        "listening on :{} metrics={} delta={} exponential={} interval={:?}",
        c.port, c.metrics_enabled, c.delta, c.exponential, c.interval
    );

    // actix handles sigterm with a graceful stop; run returns once in-flight
    // requests finished, and the flush below exports every handled request
    actix_web::rt::System::new().block_on(
        HttpServer::new(move || {
            App::new()
                .app_data(store.clone())
                // health checks sit outside the instrumented scope so probes
                // never change the request counts
                .route("/healthz", web::get().to(healthz))
                // docs:start middleware
                .service(
                    web::scope("")
                        .wrap(RequestMetrics::default())
                        .wrap(RequestTracing::new())
                        .route("/accounts", web::post().to(create_account))
                        .route("/accounts/{id}", web::get().to(get_account))
                        .route("/accounts/{id}/payments", web::post().to(create_payment))
                        .route("/accounts/{id}/payments", web::get().to(list_payments)),
                )
                // docs:end middleware
        })
        .bind(("0.0.0.0", c.port))?
        .run(),
    )?;

    if let Some(mp) = meter_provider {
        mp.shutdown()?;
    }
    tracer_provider.shutdown()?;
    println!("telemetry flushed, exiting");
    Ok(())
}
