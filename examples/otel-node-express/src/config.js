"use strict";

// reads the shared example contract. unknown values throw so a typo never
// silently runs the app in a different mode.
function choice(name, allowed, def) {
  const v = process.env[name] || def;
  if (!allowed.includes(v)) {
    throw new Error(`${name} must be one of ${allowed.join(", ")}, got "${v}"`);
  }
  return v;
}

function required(name) {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is required`);
  return v.replace(/\/+$/, "");
}

const interval = Number(process.env.METRICS_EXPORT_INTERVAL_MS || "10000");
if (!Number.isInteger(interval) || interval <= 0) {
  throw new Error("METRICS_EXPORT_INTERVAL_MS must be a positive integer");
}

module.exports = {
  port: Number(process.env.PORT || "8080"),
  serviceName: process.env.OTEL_SERVICE_NAME || "otel-node-express",
  tenantEndpoint: required("TENANT_OTLP_ENDPOINT"),
  platformEndpoint: required("PLATFORM_OTLP_ENDPOINT"),
  platformToken: required("PLATFORM_OTLP_TOKEN"),
  metricsEnabled:
    choice("EXPORT_SIGNALS", ["metrics,traces", "traces"], "metrics,traces") === "metrics,traces",
  delta: choice("METRICS_TEMPORALITY", ["cumulative", "delta"], "cumulative") === "delta",
  exponential:
    choice("METRICS_HISTOGRAM", ["explicit", "exponential"], "explicit") === "exponential",
  interval,
};
