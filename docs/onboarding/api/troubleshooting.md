---
title: "Troubleshooting API analytics"
sidebarTitle: "Troubleshooting"
description: "Checks for the OTLP endpoint, the token and the data when API metrics do not appear."
---

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
| 429 | `ResourceExhausted` | the project sent more requests or data points per second than its rate limit | retries with backoff | nothing for a short burst; if it persists, send fewer, larger requests (a larger batch size for traces) and keep the metric export interval at 60 seconds or less (see "Export interval" below) |
| 502, 503 | `Unavailable` | the platform is restarting or briefly cannot store data | retries | nothing; the data arrives when it recovers |
| 400 or 413 | `ResourceExhausted` | the request is larger than 4 MiB, before or after decompression | drops the batch | lower the SDK's batch size |
| 400 | `InvalidArgument` | the request is within 4 MiB but larger than 8 MiB after the platform's processing, which happens only with many tiny exponential histograms; the answer says "export too large" | drops the batch | lower the SDK's batch size, or a Collector's `send_batch_max_size` |
| 404 | | the path is not `/v1/metrics`, `/v1/traces` or `/v1/logs` | drops the batch | see "Endpoint URL" below |

The rate limit is 50 requests per second (bursts up to 100) and 50,000 data points, spans and log
records per second (bursts up to 200,000) per project, on each ingest node. An export interval of
60 seconds stays far below it.

## Export interval

Keep the metric export interval at the SDK default of 60 seconds, or shorter. Do not lengthen it
to get under the rate limit: a longer interval does not lower the number of data points per
request, and it delays your numbers.

The platform counts cumulative metrics from the difference between two exports. When it sees a
process for the first time and that process has been running for more than 120 seconds, the
first export is taken as a starting point and counts nothing, because it can hold days of
history. With an interval above 120 seconds every process looks like that at its first export.
The platform then decides at the second export: if the process was younger than two intervals
at its first one, the first interval is counted in full at the second export, one interval
late. So intervals up to a few minutes lose nothing, as long as a process exports at least twice.
A process that exports only once (a short job with a long interval) loses that export. Delta
temporality has no first export problem.

## No data in the dashboard

Work through these in order:

1. **The empty request above answers 200.** If not, fix the token first.
2. **The project is active** and the token is listed under **Tokens** on the **Connect API** page.
   Revoked tokens and suspended projects are refused with 401.
3. **Endpoint URL.** The examples give each exporter the full URL, base plus `/v1/metrics` or
   `/v1/traces`. Some SDK options take only the base and add the path themselves; the standard
   `OTEL_EXPORTER_OTLP_ENDPOINT` variable does, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` does not.
   A doubled or missing `/v1/metrics` ends in 404.
4. **Wait for an export.** Metrics leave the SDK once per export interval. The OpenTelemetry
   default is 60 seconds. An app that exits before its first interval exports nothing unless it
   shuts the SDK down cleanly; the language guides show how.
5. **Your SDK's own errors.** Every SDK logs failed exports. The examples print them to standard
   error; look there for 401, 404 or TLS errors.
6. **The metric exists.** Only the HTTP server request duration is kept. An app without an HTTP
   server instrumentation (only a client, a queue consumer, a cron job) has nothing the platform
   keeps. See [what-gets-dropped.md](what-gets-dropped.md).

## Exports time out until the app restarts

Your SDK logs a timeout on every export (`context deadline exceeded` in Go,
`InterruptedIOException: timeout` in Java), the empty request above still answers 200 from the
same host, and a restart of the app fixes it at once.

The cause is a dead HTTP/2 connection. An HTTP/2 client sends all exports over one connection.
When the network path under that connection disappears without a close, for example when a NAT
or firewall between you and the platform drops its mapping, the client receives no error. Each
export times out, and the client keeps the connection and sends the next export on it as well,
for many minutes, so only a restart ends it quickly. An HTTP/1.1 client closes a connection whose
request timed out, so its next export opens a new connection and gets through.

HTTP/2 health checks fix this: the client pings a connection that stays silent and closes it when
the ping goes unanswered, so the next export opens a new connection.

| SDK | Protocol to the platform | Affected | What to do |
|---|---|---|---|
| Go, OTLP/HTTP (`otlpmetrichttp`, `otlptracehttp`) | HTTP/2 | yes | pass an HTTP client with HTTP/2 health checks, see below |
| Go, OTLP/gRPC (`otlpmetricgrpc`, `otlptracegrpc`) | HTTP/2 | yes | add gRPC keepalive, see below |
| OpenTelemetry Collector, `otlphttp` exporter | HTTP/2 | yes | set `http2_read_idle_timeout` and `http2_ping_timeout` ([collector-fanout.md](collector-fanout.md)) |
| Java agent and Java SDK | HTTP/2 | yes | send through a Collector with the two settings above; the Java exporters have no HTTP/2 health check option |
| Node.js | HTTP/1.1 | no | nothing |
| Python | HTTP/1.1 | no | nothing |
| Rust, `reqwest` client | HTTP/1.1 with the example's features | no | if your build enables reqwest's `http2` feature, the exporter speaks HTTP/2: build the reqwest client with `http2_keep_alive_interval`, `http2_keep_alive_timeout` and `http2_keep_alive_while_idle(true)`, and pass it with `with_http_client` |

The OpenTelemetry Go exporters have no option or environment variable for HTTP/2 health checks.
Give them an HTTP client whose transport sets them (Go 1.24 or newer), as the [Go guide](go.md)
does for all four of its exporters:

{/* include examples/otel-go-nethttp/main.go region=client */}
```go
// exportClient is the http client of all four exporters. go's http/2 client
// keeps a connection whose peer vanished without a fin or rst (a lost nat
// mapping, a node that went away) and queues every export onto it for many
// minutes, so only a restart ends it quickly. a ping after 15 s without a
// frame from the peer, unanswered within 5 s, closes such a connection and
// the next export dials a new one. a client of your own replaces the
// exporter's timeout, tls and proxy options, so it sets the sdk's 10 s
// export timeout itself.
func exportClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.HTTP2 = &http.HTTP2Config{SendPingTimeout: 15 * time.Second, PingTimeout: 5 * time.Second}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}
```

Pass it to each exporter with `otlpmetrichttp.WithHTTPClient(client)` and
`otlptracehttp.WithHTTPClient(client)`. A client you pass replaces the exporter's `WithTimeout`,
`WithTLSClientConfig` and `WithProxy` options and the `OTEL_EXPORTER_OTLP_*TIMEOUT` and
`OTEL_EXPORTER_OTLP_*CERTIFICATE` variables, so the client sets the 10 second export timeout
itself.

For the gRPC exporters, add keepalive pings with
`WithDialOption(grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second,
Timeout: 10 * time.Second, PermitWithoutStream: true}))`.

`GODEBUG=http2client=0` also avoids the problem in Go, by making the OTLP/HTTP exporters speak
HTTP/1.1 instead.

## Numbers look wrong

| What you see | Cause and fix |
|---|---|
| Fewer requests than in your own logs, service marked "from traces" | the app exports only traces and samples them; export metrics as well ([quickstart](quickstart.md#metrics-or-traces)) |
| Every request counted twice | the same requests reach the platform twice: an app that dual-exports and also sends to a Collector that fans out. Send one copy only. (`OTEL_SEMCONV_STABILITY_OPT_IN=http/dup` is handled: the platform keeps one of the two metrics, see [python.md](python.md#otel_semconv_stability_opt_in).) |
| Rows with raw paths, or many routes under `{other}` | `http.route` is missing, so the platform falls back to the path ([http-route.md](http-route.md)) |
| Percentiles marked approximate | the app sends exponential histograms or custom bucket bounds; the default explicit buckets of OpenTelemetry's HTTP instrumentations give exact percentiles |
| Percentiles marked not reliable | the histogram's buckets are much wider than the standard ones, often an SDK's millisecond default bounds on a metric in seconds, so every request under 5 s lands in the 2.5 to 5 s bucket. Record `http.server.request.duration` in seconds with the default bounds of the HTTP instrumentation |
| p95 shown as "> 10 s" | more than 5 % of the requests took longer than 10 seconds, the largest bucket bound |
| Health checks or probes in the counts | the platform counts known health and probe routes, CORS preflights and known health checkers apart as infrastructure requests and leaves them out of the figures ([what gets dropped](what-gets-dropped.md#changed-before-storing)). Two cases stay in the counts: a health route with a name outside the [rule set](../../reference/collector.md#traffic-classes), and a health checker that calls an application route, because the `http.server.request.duration` metric carries no user agent by default. Exclude those checks in the instrumentation, as the Node.js, Go, Python and Rust examples do for `/healthz` |
| Some series missing while the rest of the same export is stored | their data points have values that cannot be counted, such as no timestamp, a start after the end, or bucket counts that do not match the bounds; such points are dropped one by one ([what gets dropped](what-gets-dropped.md#dropped)). Check the SDK version and any custom aggregation |
| A service name you do not recognise | `service.name` is missing and the SDK's default (`unknown_service:...`) is used; set `OTEL_SERVICE_NAME` |

## Still stuck

Open an issue with the **Tenant support** form of the public issue tracker
([support.md](../../support.md)). Include the language and SDK version, how you set the endpoint,
the HTTP status your SDK logs and the time it happened. Never paste the token.
