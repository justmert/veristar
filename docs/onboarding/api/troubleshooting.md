# Troubleshooting API analytics

## Check the token and the endpoint first

An empty OTLP request tells you whether the endpoint is reachable and the token is accepted,
without sending any data:

```sh
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  -H 'Content-Type: application/x-protobuf' \
  -H "Authorization: Bearer $PLATFORM_OTLP_TOKEN" \
  --data-binary '' https://otlp.analytics.example/v1/metrics
```

`200` means both are fine. `401` means the token is wrong, revoked, or belongs to a project that
is not active. Anything else is in the table below. (The platform's integration tests send this
same empty request to tell a working token from a refused one.)

## What the platform answers

| HTTP | gRPC | Meaning | What your SDK does | What to do |
|---|---|---|---|---|
| 200 | OK | stored | nothing | nothing |
| 401 | `Unauthenticated` | no token, a malformed one, an unknown or revoked one, or the project is suspended or removed | drops the batch, logs an export error | check `PLATFORM_OTLP_TOKEN` and that it is sent only as `Authorization: Bearer ...`; create a new token in the console if needed |
| 429 | `ResourceExhausted` | the project sent more requests or data points per second than its rate limit | retries with backoff | usually nothing; if it persists, send fewer, larger requests (a larger batch size for traces) and keep the metric export interval at 60 seconds or less (see "Export interval" below) |
| 502, 503 | `Unavailable` | the platform is restarting or briefly cannot store data | retries | nothing; the data arrives when it recovers |
| 400 or 413 | `ResourceExhausted` | the request is larger than 4 MiB, before or after decompression | drops the batch | lower the SDK's batch size |
| 404 | | the path is not `/v1/metrics`, `/v1/traces` or `/v1/logs` | drops the batch | see "Endpoint URL" below |

The current rate limit is 50 requests per second (bursts up to 100) and 50,000 data points, spans
and log records per second (bursts up to 200,000) per project, on each of the three edge nodes.
A normal export interval stays far below it.

## Export interval

Keep the metric export interval at the SDK default of 60 seconds, or shorter. Do not lengthen it
to get under the rate limit: a longer interval does not lower the number of data points per
request, and it delays your numbers.

The platform counts cumulative metrics from the difference between two exports. When it sees a
process for the first time and that process has been running for more than 120 seconds, the
first export is taken as a starting point and counts nothing, because it may hold days of
history. With an interval above 120 seconds every process looks like that at its first export.
The platform then decides at the second export: if the process was younger than two intervals
at its first one, the first interval is counted in full at the second export, one interval
late. So intervals up to a few minutes lose nothing, as long as a process exports at least twice.
A process that exports only once (a short job with a long interval) loses that export. Delta
temporality has no first export problem.

## No data in the dashboard

Work through these in order:

1. **The empty request above answers 200.** If not, fix the token first.
2. **The project is active** and the token is listed as active on the **API analytics setup**
   page. Revoked tokens and suspended projects are refused with 401.
3. **Endpoint URL.** The examples give each exporter the full URL, base plus `/v1/metrics` or
   `/v1/traces`. Some SDK options take only the base and add the path themselves; the standard
   `OTEL_EXPORTER_OTLP_ENDPOINT` variable does, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` does not.
   A doubled or missing `/v1/metrics` ends in 404.
4. **Wait for an export.** Metrics leave the SDK once per export interval, 60 seconds by default
   in most SDKs. An app that exits before its first interval exports nothing unless it shuts the
   SDK down cleanly; the language guides show how.
5. **Your SDK's own errors.** Every SDK logs failed exports. The examples print them to standard
   error; look there for 401, 404 or TLS errors.
6. **The metric exists.** Only the HTTP server request duration is kept. An app without an HTTP
   server instrumentation (only a client, a queue consumer, a cron job) has nothing the platform
   keeps. See [what-gets-dropped.md](what-gets-dropped.md).

## Numbers look wrong

| What you see | Cause and fix |
|---|---|
| Fewer requests than in your own logs, service marked "from traces" | the app exports only traces and samples them; export metrics as well ([quickstart](quickstart.md#metrics-or-traces)) |
| Every request counted twice | the same requests reach the platform twice: an app that dual-exports and also sends to a Collector that fans out. Send one copy only. (`OTEL_SEMCONV_STABILITY_OPT_IN=http/dup` is handled: the platform keeps one of the two metrics, see [python.md](python.md#otel_semconv_stability_opt_in).) |
| Rows with raw paths, or many routes under `{other}` | `http.route` is missing, so the platform falls back to the path ([http-route.md](http-route.md)) |
| Percentiles marked approximate | the app sends exponential histograms or custom bucket bounds; the default explicit buckets of OpenTelemetry's HTTP instrumentations give exact percentiles |
| Percentiles marked not reliable | the histogram's buckets are much wider than the standard ones, often an SDK's millisecond default bounds on a metric in seconds, so every request under 5 s lands in the 2.5 to 5 s bucket. Record `http.server.request.duration` in seconds with the default bounds of the HTTP instrumentation |
| p95 shown as "> 10 s" | more than 5 % of the requests took longer than 10 seconds, the largest bucket bound |
| Health checks or probes in the counts | exclude them from the instrumentation, as each example does |
| A service name you do not recognise | `service.name` is missing and the SDK's default (`unknown_service:...`) is used; set `OTEL_SERVICE_NAME` |

## Still stuck

Open an issue with the **Tenant support** form of the public issue tracker
([support.md](../../support.md)). Include the language and SDK version, how you set the endpoint,
the HTTP status your SDK logs and the time it happened. Never paste the token.
