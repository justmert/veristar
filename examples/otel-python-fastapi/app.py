"""fastapi example that dual-exports opentelemetry data.

one copy goes to the tenant's own backend and one to veristar. see
README.md for the environment contract. OTEL_SEMCONV_STABILITY_OPT_IN=http
makes the instrumentation emit http.server.request.duration in seconds;
without it the legacy http.server.duration in milliseconds is emitted.
"""

import os
import re
import threading
from contextlib import asynccontextmanager
from decimal import Decimal, InvalidOperation

from fastapi import Body, FastAPI
from fastapi.responses import JSONResponse, Response
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.sdk.metrics import (
    Counter,
    Histogram,
    MeterProvider,
    ObservableCounter,
    ObservableGauge,
    ObservableUpDownCounter,
    UpDownCounter,
)
from opentelemetry.sdk.metrics.export import AggregationTemporality, PeriodicExportingMetricReader
from opentelemetry.sdk.metrics.view import ExponentialBucketHistogramAggregation
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor


def choice(name: str, allowed: list[str], default: str) -> str:
    # unknown values are errors so a typo never silently runs another mode
    value = os.environ.get(name) or default
    if value not in allowed:
        raise SystemExit(f"{name} must be one of {', '.join(allowed)}, got {value!r}")
    return value


def required(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        raise SystemExit(f"{name} is required")
    return value.rstrip("/")


SERVICE_NAME = os.environ.get("OTEL_SERVICE_NAME") or "otel-python-fastapi"
TENANT_ENDPOINT = required("TENANT_OTLP_ENDPOINT")
PLATFORM_ENDPOINT = required("PLATFORM_OTLP_ENDPOINT")
PLATFORM_TOKEN = required("PLATFORM_OTLP_TOKEN")
METRICS_ENABLED = choice("EXPORT_SIGNALS", ["metrics,traces", "traces"], "metrics,traces") == "metrics,traces"
DELTA = choice("METRICS_TEMPORALITY", ["cumulative", "delta"], "cumulative") == "delta"
EXPONENTIAL = choice("METRICS_HISTOGRAM", ["explicit", "exponential"], "explicit") == "exponential"
try:
    INTERVAL_MS = int(os.environ.get("METRICS_EXPORT_INTERVAL_MS") or "10000")
except ValueError:
    INTERVAL_MS = 0
if INTERVAL_MS <= 0:
    raise SystemExit("METRICS_EXPORT_INTERVAL_MS must be a positive integer")
SEMCONV = os.environ.get("OTEL_SEMCONV_STABILITY_OPT_IN", "")

# the console's api connect page shows the lines between each
# docs:start and docs:end marker pair (apps/console/src/lib/otel-guides.ts),
# so the instructions are this code and cannot drift from it.

# docs:start telemetry
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
# docs:end telemetry

tracer = trace.get_tracer("otel-python-fastapi")


@asynccontextmanager
async def lifespan(_app: FastAPI):
    yield
    # uvicorn ends the lifespan after it stopped accepting requests, so every
    # handled request is part of the final export
    if meter_provider is not None:
        meter_provider.shutdown()
    tracer_provider.shutdown()
    print("telemetry flushed", flush=True)


app = FastAPI(title="otel-python-fastapi", lifespan=lifespan)
lock = threading.Lock()
accounts: dict[str, dict[str, str]] = {}
payments: dict[str, list[dict[str, str]]] = {}
counter = {"next": 0}

# a positive decimal with up to 7 fractional digits, the stellar amount precision
AMOUNT = re.compile(r"^\d+(\.\d{1,7})?$")


def lookup(account_id: str) -> dict[str, str] | None:
    # an internal span with an event, both carrying marker strings, so the
    # platform tests can prove non-server spans and events never leave its
    # collector while the tenant backend still receives them
    with tracer.start_as_current_span("SA_MARKER_internal_span", kind=trace.SpanKind.INTERNAL) as span:
        span.add_event("SA_MARKER_span_event", {"account.lookup": account_id})
        with lock:
            return accounts.get(account_id)


def error(status: int, message: str) -> JSONResponse:
    return JSONResponse({"error": message}, status_code=status)


@app.get("/healthz")
def healthz() -> Response:
    return Response(status_code=204)


@app.post("/accounts", status_code=201)
def create_account(body: dict = Body(default_factory=dict)):
    name = body.get("name")
    if not isinstance(name, str) or not name.strip():
        return error(400, "name is required")
    with lock:
        counter["next"] += 1
        account = {"id": str(counter["next"]), "name": name.strip()}
        accounts[account["id"]] = account
        payments[account["id"]] = []
    return account


@app.get("/accounts/{account_id}")
def get_account(account_id: str):
    account = lookup(account_id)
    if account is None:
        return error(404, "account not found")
    return account


@app.post("/accounts/{account_id}/payments", status_code=201)
def create_payment(account_id: str, body: dict = Body(default_factory=dict)):
    if lookup(account_id) is None:
        return error(404, "account not found")
    amount = body.get("amount")
    try:
        valid = isinstance(amount, str) and AMOUNT.match(amount) is not None and Decimal(amount) > 0
    except InvalidOperation:
        valid = False
    if not valid:
        return error(400, "amount must be a positive decimal")
    with lock:
        payment = {"id": str(len(payments[account_id]) + 1), "amount": amount}
        payments[account_id].append(payment)
    return payment


@app.get("/accounts/{account_id}/payments")
def list_payments(account_id: str):
    if lookup(account_id) is None:
        return error(404, "account not found")
    with lock:
        return {"payments": list(payments[account_id])}


# docs:start instrument
# health checks are excluded so probes never change the request counts
FastAPIInstrumentor.instrument_app(
    app, tracer_provider=tracer_provider, meter_provider=meter_provider, excluded_urls="/healthz"
)
# docs:end instrument


print(
    f"metrics={METRICS_ENABLED} delta={DELTA} exponential={EXPONENTIAL} interval={INTERVAL_MS}ms "
    f"OTEL_SEMCONV_STABILITY_OPT_IN={SEMCONV!r}",
    flush=True,
)
