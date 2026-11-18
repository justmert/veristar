#!/bin/sh
# checks the environment contract of README.md, maps the switches onto the
# variables otel.yaml reads and starts the app with the agent attached.
# unknown values are errors so a typo never silently runs another mode.
set -eu

fail() {
  echo "$1" >&2
  exit 64
}

for name in TENANT_OTLP_ENDPOINT PLATFORM_OTLP_ENDPOINT PLATFORM_OTLP_TOKEN; do
  eval "value=\${$name:-}"
  [ -n "$value" ] || fail "$name is required"
done
# otel.yaml appends /v1/<signal> to the base urls
TENANT_OTLP_ENDPOINT=${TENANT_OTLP_ENDPOINT%/}
PLATFORM_OTLP_ENDPOINT=${PLATFORM_OTLP_ENDPOINT%/}
export TENANT_OTLP_ENDPOINT PLATFORM_OTLP_ENDPOINT

case "${EXPORT_SIGNALS:-metrics,traces}" in
  metrics,traces) config=/app/otel.yaml ;;
  traces) config=/app/otel-traces.yaml ;;
  *) fail "EXPORT_SIGNALS must be one of metrics,traces, traces, got '$EXPORT_SIGNALS'" ;;
esac
case "${METRICS_TEMPORALITY:-cumulative}" in
  cumulative | delta) OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=${METRICS_TEMPORALITY:-cumulative} ;;
  *) fail "METRICS_TEMPORALITY must be one of cumulative, delta, got '$METRICS_TEMPORALITY'" ;;
esac
case "${METRICS_HISTOGRAM:-explicit}" in
  explicit) OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION=explicit_bucket_histogram ;;
  exponential) OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION=base2_exponential_bucket_histogram ;;
  *) fail "METRICS_HISTOGRAM must be one of explicit, exponential, got '$METRICS_HISTOGRAM'" ;;
esac
export OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION
METRICS_EXPORT_INTERVAL_MS=${METRICS_EXPORT_INTERVAL_MS:-10000}
case "$METRICS_EXPORT_INTERVAL_MS" in
  '' | *[!0-9]* | 0) fail "METRICS_EXPORT_INTERVAL_MS must be a positive integer" ;;
esac
export METRICS_EXPORT_INTERVAL_MS

echo "config=$config temporality=$OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE histogram=$OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION interval=${METRICS_EXPORT_INTERVAL_MS}ms"
# docs:start run
# exec, so the jvm is the process that receives sigterm
exec java -javaagent:/app/opentelemetry-javaagent.jar -Dotel.config.file="$config" -jar /app/app.jar
# docs:end run
