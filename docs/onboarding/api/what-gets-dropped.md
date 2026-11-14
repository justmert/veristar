---
title: "What gets dropped"
description: "What the platform's collector keeps for the API dashboards and what it discards on arrival."
---

The platform's collector keeps only what the API dashboards need and discards everything else when
it arrives, before anything is queued or stored. Your own backend, fed by the other exporter,
receives all of it unchanged. Everything below is exercised by the collector's integration suite
(`make collector-it`) and by `tests/otelapps` with the `apipath` tag, which scans every queued
message for names and marker strings that must not be there.

## Kept

| Signal | What is kept |
|---|---|
| Metrics | `http.server.request.duration` in seconds, and the legacy `http.server.duration` in milliseconds or seconds (converted to the former), as explicit or exponential histograms |
| Their attributes | `http.request.method`, `http.route`, `http.response.status_code`, `error.type`; the legacy names `http.method`, `http.target` and `http.status_code` are mapped to these |
| Resource | `service.name`, redacted and cut to 128 bytes |
| Traces | only SERVER spans with an HTTP method, and only long enough to turn their duration, method, route and status into the same request metric; then the span is discarded |

## Dropped

- **Every other metric**: counters, gauges, runtime and database metrics, client-side HTTP
  metrics, and your own custom metrics.
- **Every other attribute** on the kept metrics, for example `server.address`, `url.scheme`,
  `user_agent.original` or anything you added.
- **Every other resource attribute**: host, container, cloud, process, SDK and deployment
  attributes. Attributes that claim a tenant (`scf.*`, `tenant.id`) are replaced from the token.
- **Spans** after the request metric was derived from them: names, events, links, status messages
  and attributes are never stored. Non-server spans are discarded at once.
- **Logs**: accepted, so your SDK reports no errors, and discarded.
- **Scope names and versions, schema URLs, descriptions and exemplars.**
- **Gauge and sum versions** of the duration metric: only histograms are read.

## Changed before storing

- **Methods** outside the standard set (CONNECT, DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT,
  TRACE, QUERY) become `_OTHER`; the comparison is case-sensitive.
- **Routes** are normalized: ids, keys, hashes and similar values become placeholders, and only 300
  distinct routes and 50 services per project and UTC day are kept apart ([http-route.md](http-route.md)).
- **`error.type`** is redacted; a value longer than 64 bytes becomes `_OTHER`.
- **Histograms** are stored on one fixed set of 14 bucket bounds, the advisory bounds of
  `http.server.request.duration` in OpenTelemetry semantic conventions 1.45.0, from 5 ms to 10 s.
  Data that arrives with other bounds, and every exponential histogram, is re-bucketed and its
  percentiles are marked approximate. A percentile above 10 seconds is shown as "> 10 s".
- **Temporality** does not matter: cumulative and delta both arrive as exact per-minute counts.

## Stored and for how long

One row per minute, service, method, route, status code and source (metric or span) with the
bucket counts, sum and count. Minute rows are rolled up into hours and days. With the default
settings, minute rows are kept for 30 days, hourly rows for 36 months and daily rows without a
time limit. No request, trace, log line, address or user identifier is stored.

## Why

The API dashboards show request volume, status codes and latency per route, and nothing beyond
that. Keeping less means there is less to protect, less to erase and nothing in the platform that could identify
one of your users. If you need traces, logs or other metrics, your own backend already has them.
