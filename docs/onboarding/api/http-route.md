---
title: "Setting http.route"
description: "Make sure every request carries the route template, so the API dashboard groups requests by route."
---

The API dashboard groups requests by route: `GET /accounts/{id}` is one row, however many accounts
there are. The route comes from the `http.route` attribute, the route template your framework
matched, which OpenTelemetry's HTTP instrumentations set on the request metric and the server span.

## In the examples

| Example | Who sets `http.route` | Stored as |
|---|---|---|
| [Node.js](node.md), Express | `instrumentation-express`, from the Express route | `/accounts/:id` |
| [Go](go.md), `net/http` | `otelhttp` on metrics from the mux pattern; the example's middleware copies it onto the span | `/accounts/{id}` |
| [Python](python.md), FastAPI | `instrumentation-fastapi`, from the path template | `/accounts/{account_id}` |
| [Rust](rust.md), actix-web | `RequestMetrics` and `RequestTracing`, from the route pattern | `/accounts/{id}` |
| [Java](java.md), Spring Boot | the Java agent's Spring MVC instrumentation, from the mapping template | `/accounts/{id}` |

`tests/otelapps` sends a fixed load to each example and checks that every request is counted under
exactly these routes, with no raw path left over.

## When your framework does not set it

Set it yourself in a middleware that runs after routing, on the active server span. This is what
the Go example does, because `otelhttp` puts the route on metrics but not on the span:

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

In other languages the pattern is the same: read the matched route template from your router and
set `http.route` on the current span (and, where your instrumentation allows, on the metric
attributes). Use the template, never the actual path. The span attribute matters when you export
only traces, and for your own backend.

## What the platform does with routes

Every route, whether you set it or not, goes through the same fixed normalization before it is
stored:

- **No `http.route`:** the platform uses the request path (`url.path`, or the legacy
  `http.target`) instead, without the query string.
- **Values become placeholders**, in the template and in a path alike: numbers become `{id}`, UUIDs
  `{uuid}`, long hex strings and hashes `{hex}` or `{hash}`, dates `{date}`, tokens `{token}`,
  Stellar account, muxed account and contract keys `{account}`, `{muxed}`, `{contract}`, assets
  `{asset}`, email and federation addresses `{email}` and `{federation}`, and a segment longer than
  64 bytes `{long}`. A secret seed in a path becomes `{redacted-secret}`, and the **API**
  page warns you, because the seed was exposed in a URL.
- **At most 300 distinct routes and 50 services per project and UTC day** (the service's current
  settings). Routes beyond that are counted under `{other}`, so their requests are still in the
  totals, and the project's **API** page shows a warning. A route that changes with every request (a
  slug, a name) fills the allowance quickly; set `http.route` to the template to avoid that.

The rules are hard coded and the same for every project; they cannot be configured
([`internal/routenorm`](https://github.com/justmert/veristar/blob/main/internal/routenorm/routenorm.go) lists them in order).
