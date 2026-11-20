---
title: "Go"
description: "Dual export from a Go service on net/http with otelhttp and the OpenTelemetry Go SDK."
icon: "golang"
iconType: "brands"
---

Dual export from a Go service on `net/http`. Built from the example
[`examples/otel-go-nethttp`](https://github.com/justmert/veristar/tree/main/examples/otel-go-nethttp): Go 1.22 routing patterns,
`otelhttp` v0.72.0 and the OpenTelemetry Go SDK v1.47.0. `tests/otelapps` runs it against stock
collectors in every export mode, the `apipath` tag runs it against the real platform collector,
and `tests/collector` uses the same SDK version for the collector's own integration suite. Read
the [API quickstart](quickstart.md) first for the token and the environment variables.

## Modules

The example has its own `go.mod`, so you can copy the directory as it is. Its direct
dependencies:

{/* include examples/otel-go-nethttp/go.mod region=require */}
```go
require (
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.47.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/sdk/metric v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
)
```

## The SDK setup

One tracer provider and one meter provider, each with two exporters, so both backends see the same
data from the same SDK state. Only the platform exporters carry the token. All four exporters
share one HTTP client with HTTP/2 health checks:

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

The providers and their exporters:

{/* include examples/otel-go-nethttp/main.go region=telemetry */}
```go
// setupTelemetry builds one tracer provider and one meter provider, each with
// two exporters, so both backends see identical data from the same sdk state.
func setupTelemetry(ctx context.Context, c config) (func(context.Context) error, error) {
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(c.serviceName)))
	if err != nil {
		return nil, err
	}
	auth := map[string]string{"Authorization": "Bearer " + c.platformToken}
	client := exportClient()

	tenantSpans, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(c.tenantEndpoint+"/v1/traces"),
		otlptracehttp.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	platformSpans, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(c.platformEndpoint+"/v1/traces"),
		otlptracehttp.WithHeaders(auth), otlptracehttp.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res),
		sdktrace.WithBatcher(tenantSpans), sdktrace.WithBatcher(platformSpans))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	shutdowns := []func(context.Context) error{tp.Shutdown}

	if c.metricsEnabled {
		temporality := sdkmetric.DefaultTemporalitySelector
		if c.delta {
			temporality = func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.DeltaTemporality }
		}
		aggregation := sdkmetric.DefaultAggregationSelector
		if c.exponential {
			// max size and scale are the sdk defaults for base2 exponential histograms
			aggregation = func(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
				if k == sdkmetric.InstrumentKindHistogram {
					return sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: 160, MaxScale: 20}
				}
				return sdkmetric.DefaultAggregationSelector(k)
			}
		}
		tenantMetrics, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(c.tenantEndpoint+"/v1/metrics"),
			otlpmetrichttp.WithHTTPClient(client),
			otlpmetrichttp.WithTemporalitySelector(temporality), otlpmetrichttp.WithAggregationSelector(aggregation))
		if err != nil {
			return nil, err
		}
		platformMetrics, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(c.platformEndpoint+"/v1/metrics"),
			otlpmetrichttp.WithHeaders(auth), otlpmetrichttp.WithHTTPClient(client),
			otlpmetrichttp.WithTemporalitySelector(temporality), otlpmetrichttp.WithAggregationSelector(aggregation))
		if err != nil {
			return nil, err
		}
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(tenantMetrics, sdkmetric.WithInterval(c.interval))),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(platformMetrics, sdkmetric.WithInterval(c.interval))))
		otel.SetMeterProvider(mp)
		// the meter provider shuts down first so its final collection still has a live tracer
		shutdowns = append([]func(context.Context) error{mp.Shutdown}, shutdowns...)
	}

	return func(ctx context.Context) error {
		var errs []error
		for _, s := range shutdowns {
			errs = append(errs, s(ctx))
		}
		return errors.Join(errs...)
	}, nil
}
```

## Instrumenting the server

Wrap the whole mux with `otelhttp.NewHandler`. Health checks are filtered out so probes do not
count as API traffic:

{/* include examples/otel-go-nethttp/main.go region=handler */}
```go
// health checks are filtered out so probes never change the request counts
handler := otelhttp.NewHandler(s.routes(), "http.server",
	otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" }))
```

`otelhttp` names the server span after the mux pattern and puts `http.route` on its metrics, but
not on the span. Copy the pattern onto the span as well, so a traces-only export and your own
backend group requests by route too ([http-route.md](http-route.md)):

{/* include examples/otel-go-nethttp/main.go region=route */}
```go
// withRoute copies the mux pattern onto the server span. otelhttp names the
// span after the pattern but only puts http.route on metrics, and the route is
// what lets a backend group spans without guessing from the raw path.
func withRoute(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		trace.SpanFromContext(r.Context()).SetAttributes(semconv.HTTPRoute(route))
		h(w, r)
	}
}

func (s *store) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /accounts", withRoute("/accounts", s.createAccount))
	mux.HandleFunc("GET /accounts/{id}", withRoute("/accounts/{id}", s.getAccount))
	mux.HandleFunc("POST /accounts/{id}/payments", withRoute("/accounts/{id}/payments", s.createPayment))
	mux.HandleFunc("GET /accounts/{id}/payments", withRoute("/accounts/{id}/payments", s.listPayments))
	return mux
}
```

## Notes

- **Shut down cleanly.** Call the returned shutdown function after `http.Server.Shutdown`, so the
  last interval's metrics are exported. The meter provider is shut down before the tracer provider.
- **HTTP/2 health checks.** The exporters speak HTTP/2 to the platform and send every export
  over one connection. Go's HTTP/2 client sends no health checks by default, so when the network
  path under that connection disappears without a close (a NAT or firewall drops its mapping, an
  outage between you and the platform), every export times out on the dead connection for many
  minutes, and only a restart helps. `http.HTTP2Config` with `SendPingTimeout` and `PingTimeout`
  (Go 1.24 or newer) makes the client ping a connection that stays silent and close it when the
  ping goes unanswered, so the next export opens a new connection. The exporters have no option
  or environment variable for this; you pass the client with `WithHTTPClient`. A client you pass
  replaces the exporter's `WithTimeout`, `WithTLSClientConfig` and `WithProxy` options and the
  `OTEL_EXPORTER_OTLP_*TIMEOUT` and `OTEL_EXPORTER_OTLP_*CERTIFICATE` variables, so set the
  timeout on the client. See [troubleshooting](troubleshooting.md#exports-time-out-until-the-app-restarts).
- **Endpoints** are given as full URLs with `WithEndpointURL` (`.../v1/metrics`,
  `.../v1/traces`). The standard `OTEL_EXPORTER_OTLP_*` variables would apply to both exporters
  alike, so the example does not use them for the platform copy.

## Export modes the tests cover

| Variable | Values |
|---|---|
| `EXPORT_SIGNALS` | `metrics,traces` (default) or `traces` |
| `METRICS_TEMPORALITY` | `cumulative` (default) or `delta` |
| `METRICS_HISTOGRAM` | `explicit` (default) or `exponential` |
