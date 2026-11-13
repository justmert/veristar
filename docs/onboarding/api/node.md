---
title: "Node.js"
description: "Dual export from a Node.js service on Express with the OpenTelemetry Node SDK."
---

Dual export from a Node.js service. Built from the example
[`examples/otel-node-express`](https://github.com/justmert/veristar/tree/main/examples/otel-node-express): Express 5.3.0 with
`@opentelemetry/sdk-node` 0.300.0, `instrumentation-http` 0.300.0 and `instrumentation-express`
0.72.0. `tests/otelapps` runs it against stock collectors in every export mode, and the `apipath`
tag runs it against the real platform collector. Read the [API quickstart](quickstart.md) first for
the token and the environment variables.

## Packages

Pin exact versions. `@opentelemetry/api` must be 1.10.0, because the 3.0.0 SDK packages declare the
peer range `>=1.10.0 <1.11.0`; the `overrides` keep the shared core packages on one version:

{/* include examples/otel-node-express/package.json */}
```json
{
  "name": "otel-node-express",
  "private": true,
  "type": "commonjs",
  "engines": {
    "node": "26.13.1"
  },
  "scripts": {
    "start": "node --require ./src/telemetry.js src/server.js"
  },
  "dependencies": {
    "@opentelemetry/api": "1.10.0",
    "@opentelemetry/exporter-metrics-otlp-http": "0.300.0",
    "@opentelemetry/exporter-metrics-otlp-proto": "0.300.0",
    "@opentelemetry/exporter-trace-otlp-proto": "0.300.0",
    "@opentelemetry/instrumentation-express": "0.72.0",
    "@opentelemetry/instrumentation-http": "0.300.0",
    "@opentelemetry/resources": "3.0.0",
    "@opentelemetry/sdk-metrics": "3.0.0",
    "@opentelemetry/sdk-node": "0.300.0",
    "@opentelemetry/sdk-trace": "3.0.0",
    "express": "5.3.0"
  },
  "overrides": {
    "@opentelemetry/core": "3.0.0",
    "@opentelemetry/instrumentation": "0.300.0",
    "@opentelemetry/semantic-conventions": "1.45.0"
  }
}
```

## The SDK setup

The whole setup is one file, loaded with `node --require` before the app so the HTTP and Express
instrumentations can patch those modules when they are first required. Each signal gets two
exporters: the one to your backend without a token, the one to the platform with it.

{/* include examples/otel-node-express/src/telemetry.js */}
```js
"use strict";

// loaded with --require before the app, because the http and express
// instrumentations patch those modules when they are first required.
// the console's api analytics setup page shows this whole file
// (apps/console/src/lib/otel-guides.ts), so the instructions cannot drift.
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
```

`config.js` next to it reads the [environment variables](quickstart.md#3-add-the-second-exporter)
and refuses unknown values at startup. Start the app with the file loaded first, as the example's
`start` script does:

```sh
node --require ./src/telemetry.js src/server.js
```

## Notes

- **Shut down cleanly.** Call `sdk.shutdown()` after the server stopped accepting requests, so the
  last interval's metrics are exported; the example does it on `SIGTERM`
  ([`src/server.js`](https://github.com/justmert/veristar/blob/main/examples/otel-node-express/src/server.js)).
- **Routes.** `instrumentation-express` sets `http.route` from the Express route
  (`/accounts/:id`); see [http-route.md](http-route.md).
- **Health checks** are excluded with `ignoreIncomingRequestHook`, so probes do not count as API
  traffic.
- **Logs** are not exported by the example. If you export logs, send them only to your backend:
  the platform accepts and discards them.

## Export modes the tests cover

| Variable | Values |
|---|---|
| `EXPORT_SIGNALS` | `metrics,traces` (default) or `traces` (no metric readers; the platform then derives request metrics from server spans) |
| `METRICS_TEMPORALITY` | `cumulative` (default) or `delta` |
| `METRICS_HISTOGRAM` | `explicit` (default) or `exponential` (re-bucketed by the platform and marked approximate) |

The platform accepts all of these combinations; the defaults give exact numbers.
