"use strict";

// loaded with --require before the app, because the http and express
// instrumentations patch those modules when they are first required.
const { NodeSDK } = require("@opentelemetry/sdk-node");
const { OTLPTraceExporter } = require("@opentelemetry/exporter-trace-otlp-proto");
const { OTLPMetricExporter } = require("@opentelemetry/exporter-metrics-otlp-proto");
const { AggregationTemporalityPreference } = require("@opentelemetry/exporter-metrics-otlp-http");
const {
  PeriodicExportingMetricReader,
  AggregationType,
  InstrumentType,
} = require("@opentelemetry/sdk-metrics");
const { BatchSpanProcessor } = require("@opentelemetry/sdk-trace");
const { HttpInstrumentation } = require("@opentelemetry/instrumentation-http");
const { ExpressInstrumentation } = require("@opentelemetry/instrumentation-express");
const { defaultResource, resourceFromAttributes } = require("@opentelemetry/resources");
const config = require("./config");

const auth = { Authorization: `Bearer ${config.platformToken}` };

function metricReader(endpoint, headers) {
  const exporter = new OTLPMetricExporter({
    url: `${endpoint}/v1/metrics`,
    headers,
    temporalityPreference: config.delta
      ? AggregationTemporalityPreference.DELTA
      : AggregationTemporalityPreference.CUMULATIVE,
    // histograms only; every other instrument keeps the sdk default
    aggregationPreference: (type) =>
      config.exponential && type === InstrumentType.HISTOGRAM
        ? { type: AggregationType.EXPONENTIAL_HISTOGRAM }
        : { type: AggregationType.DEFAULT },
  });
  return new PeriodicExportingMetricReader({ exporter, exportIntervalMillis: config.interval });
}

const sdk = new NodeSDK({
  resource: defaultResource().merge(resourceFromAttributes({ "service.name": config.serviceName })),
  autoDetectResources: false,
  spanProcessors: [
    new BatchSpanProcessor({
      exporter: new OTLPTraceExporter({ url: `${config.tenantEndpoint}/v1/traces` }),
    }),
    new BatchSpanProcessor({
      exporter: new OTLPTraceExporter({
        url: `${config.platformEndpoint}/v1/traces`,
        headers: auth,
      }),
    }),
  ],
  // an empty list means no meter provider at all, which is the traces-only mode
  metricReaders: config.metricsEnabled
    ? [metricReader(config.tenantEndpoint), metricReader(config.platformEndpoint, auth)]
    : [],
  // logs are not part of this example; an explicit empty list stops the sdk
  // from configuring a default otlp log exporter from the environment
  logRecordProcessors: [],
  instrumentations: [
    // health checks are ignored so probes never change the request counts
    new HttpInstrumentation({ ignoreIncomingRequestHook: (req) => req.url === "/healthz" }),
    new ExpressInstrumentation(),
  ],
});
sdk.start();

module.exports = { sdk };
