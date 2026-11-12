// otel-go-nethttp is a small accounts and payments api that dual-exports
// opentelemetry data: one copy to the tenant's own backend and one to stellar
// analytics. see README.md for the environment contract.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

type config struct {
	port             string
	serviceName      string
	tenantEndpoint   string
	platformEndpoint string
	platformToken    string
	metricsEnabled   bool
	delta            bool
	exponential      bool
	interval         time.Duration
}

// loadConfig reads the shared example contract. unknown values are errors so
// a typo never silently runs the app in a different mode.
func loadConfig() (config, error) {
	c := config{
		port:             envOr("PORT", "8080"),
		serviceName:      envOr("OTEL_SERVICE_NAME", "otel-go-nethttp"),
		tenantEndpoint:   strings.TrimRight(os.Getenv("TENANT_OTLP_ENDPOINT"), "/"),
		platformEndpoint: strings.TrimRight(os.Getenv("PLATFORM_OTLP_ENDPOINT"), "/"),
		platformToken:    os.Getenv("PLATFORM_OTLP_TOKEN"),
	}
	if c.tenantEndpoint == "" || c.platformEndpoint == "" || c.platformToken == "" {
		return c, errors.New("TENANT_OTLP_ENDPOINT, PLATFORM_OTLP_ENDPOINT and PLATFORM_OTLP_TOKEN are required")
	}
	switch v := envOr("EXPORT_SIGNALS", "metrics,traces"); v {
	case "metrics,traces":
		c.metricsEnabled = true
	case "traces":
		c.metricsEnabled = false
	default:
		return c, fmt.Errorf("EXPORT_SIGNALS must be metrics,traces or traces, got %q", v)
	}
	switch v := envOr("METRICS_TEMPORALITY", "cumulative"); v {
	case "cumulative":
	case "delta":
		c.delta = true
	default:
		return c, fmt.Errorf("METRICS_TEMPORALITY must be cumulative or delta, got %q", v)
	}
	switch v := envOr("METRICS_HISTOGRAM", "explicit"); v {
	case "explicit":
	case "exponential":
		c.exponential = true
	default:
		return c, fmt.Errorf("METRICS_HISTOGRAM must be explicit or exponential, got %q", v)
	}
	ms, err := strconv.Atoi(envOr("METRICS_EXPORT_INTERVAL_MS", "10000"))
	if err != nil || ms <= 0 {
		return c, fmt.Errorf("METRICS_EXPORT_INTERVAL_MS must be a positive integer")
	}
	c.interval = time.Duration(ms) * time.Millisecond
	return c, nil
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

// the console's api analytics setup page shows the lines between each
// docs:start and docs:end marker pair (apps/console/src/lib/otel-guides.ts),
// so the instructions are this code and cannot drift from it.

// docs:start telemetry
// setupTelemetry builds one tracer provider and one meter provider, each with
// two exporters, so both backends see identical data from the same sdk state.
func setupTelemetry(ctx context.Context, c config) (func(context.Context) error, error) {
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(c.serviceName)))
	if err != nil {
		return nil, err
	}
	auth := map[string]string{"Authorization": "Bearer " + c.platformToken}

	tenantSpans, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(c.tenantEndpoint+"/v1/traces"))
	if err != nil {
		return nil, err
	}
	platformSpans, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(c.platformEndpoint+"/v1/traces"),
		otlptracehttp.WithHeaders(auth))
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
			otlpmetrichttp.WithTemporalitySelector(temporality), otlpmetrichttp.WithAggregationSelector(aggregation))
		if err != nil {
			return nil, err
		}
		platformMetrics, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(c.platformEndpoint+"/v1/metrics"),
			otlpmetrichttp.WithHeaders(auth),
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

// docs:end telemetry

type account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type payment struct {
	ID     string `json:"id"`
	Amount string `json:"amount"`
}

type store struct {
	mu       sync.Mutex
	next     int
	accounts map[string]account
	payments map[string][]payment
}

var tracer = otel.Tracer("otel-go-nethttp")

// docs:start route
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

// docs:end route

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errorJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *store) createAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		errorJSON(w, http.StatusBadRequest, "name is required")
		return
	}
	s.mu.Lock()
	s.next++
	a := account{ID: strconv.Itoa(s.next), Name: body.Name}
	s.accounts[a.ID] = a
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, a)
}

// lookup runs in an internal span with an event. both carry marker strings so
// the platform tests can prove non-server spans and events never leave its
// collector, while the tenant backend still receives them.
func (s *store) lookup(ctx context.Context, id string) (account, bool) {
	_, span := tracer.Start(ctx, "SA_MARKER_internal_span", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.AddEvent("SA_MARKER_span_event", trace.WithAttributes(attribute.String("account.lookup", id)))
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	return a, ok
}

func (s *store) getAccount(w http.ResponseWriter, r *http.Request) {
	a, ok := s.lookup(r.Context(), r.PathValue("id"))
	if !ok {
		errorJSON(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *store) createPayment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.lookup(r.Context(), id); !ok {
		errorJSON(w, http.StatusNotFound, "account not found")
		return
	}
	var body struct {
		Amount string `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errorJSON(w, http.StatusBadRequest, "body must be json with an amount")
		return
	}
	amount, ok := new(big.Rat).SetString(body.Amount)
	if !ok || amount.Sign() <= 0 {
		errorJSON(w, http.StatusBadRequest, "amount must be a positive decimal")
		return
	}
	s.mu.Lock()
	p := payment{ID: strconv.Itoa(len(s.payments[id]) + 1), Amount: amount.FloatString(7)}
	s.payments[id] = append(s.payments[id], p)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, p)
}

func (s *store) listPayments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.lookup(r.Context(), id); !ok {
		errorJSON(w, http.StatusNotFound, "account not found")
		return
	}
	s.mu.Lock()
	ps := append([]payment{}, s.payments[id]...)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"payments": ps})
}

func main() {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdown, err := setupTelemetry(ctx, c)
	if err != nil {
		log.Fatal(err)
	}

	s := &store{accounts: map[string]account{}, payments: map[string][]payment{}}
	// docs:start handler
	// health checks are filtered out so probes never change the request counts
	handler := otelhttp.NewHandler(s.routes(), "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" }))
	// docs:end handler
	srv := &http.Server{Addr: ":" + c.port, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("listening on :%s metrics=%v temporality_delta=%v exponential=%v interval=%s",
		c.port, c.metricsEnabled, c.delta, c.exponential, c.interval)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	// flush after the server stopped accepting requests so every request is exported
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := shutdown(sctx); err != nil {
		log.Fatalf("telemetry shutdown: %v", err)
	}
	log.Print("telemetry flushed, exiting")
}
