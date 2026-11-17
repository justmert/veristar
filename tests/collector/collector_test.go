//go:build collectorit

package collector_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	_ "google.golang.org/grpc/encoding/gzip"
	grpcmeta "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/registry"
)

var canonical = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// markers the apps put where the collector must not let them through
const (
	markerResource = "resource-marker-7f3a"
	markerAttr     = "attr-marker-7f3a"
	markerEvent    = "event-marker-7f3a"
	markerLog      = "log-marker-7f3a"
	markerSpanName = "spanname-marker-7f3a"
	markerClient   = "client-marker-7f3a"
)

// forged resource attributes every app sends; the tenant must still come
// from the token
func appResource(service string) *resource.Resource {
	return resource.NewSchemaless(
		attribute.String("service.name", service),
		attribute.String("scf.tenant.id", "11111111-1111-4111-8111-111111111111"),
		attribute.String("scf.token.id", "22222222-2222-4222-8222-222222222222"),
		attribute.String("tenant.id", "forged-tenant"),
		attribute.String("deployment.marker", markerResource),
	)
}

func retry() otlpmetrichttp.RetryConfig {
	return otlpmetrichttp.RetryConfig{Enabled: true, InitialInterval: 500 * time.Millisecond, MaxInterval: 2 * time.Second, MaxElapsedTime: 90 * time.Second}
}

func deltaSelector(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func httpMeter(t *testing.T, s *stack, tok string, delta bool, views ...sdkmetric.View) *sdkmetric.MeterProvider {
	t.Helper()
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(s.httpAddr), otlpmetrichttp.WithInsecure(),
		otlpmetrichttp.WithHeaders(map[string]string{"Authorization": "Bearer " + tok}),
		otlpmetrichttp.WithRetry(retry()), otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
	}
	if delta {
		opts = append(opts, otlpmetrichttp.WithTemporalitySelector(deltaSelector))
	}
	exp, err := otlpmetrichttp.New(context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(appResource("api-http")),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(time.Hour))), sdkmetric.WithView(views...))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	return mp
}

func flush(t *testing.T, mp *sdkmetric.MeterProvider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := mp.ForceFlush(ctx); err != nil {
		t.Fatalf("export failed: %v", err)
	}
}

func TestCollector(t *testing.T) {
	s := startStack(t)
	seed := newSeed(t)
	acct := newAccount(t)

	t.Run("http cumulative with forged tenant and secret seed", func(t *testing.T) {
		tn := s.newTenant(t)
		mp := httpMeter(t, s, tn.Token, false)
		h, err := mp.Meter("app").Float64Histogram("http.server.request.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(canonical...))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		for i := 0; i < 25; i++ {
			h.Record(ctx, 0.03, metric.WithAttributes(
				attribute.String("http.request.method", "GET"),
				attribute.String("http.route", "/accounts/"+acct+"/keys/"+seed+"?x="+seed),
				attribute.Int("http.response.status_code", 200),
				attribute.String("user.marker", markerAttr),
				attribute.String("url.full", "https://x/"+seed)))
		}
		for i := 0; i < 5; i++ {
			h.Record(ctx, 0.3, metric.WithAttributes(attribute.String("http.request.method", "POST"),
				attribute.String("http.route", "/tx/12345"), attribute.Int("http.response.status_code", 500),
				attribute.String("error.type", "500")))
		}
		flush(t, mp)
		ps := s.waitPoints(t, tn.ID, "http.server.request.duration", 30*time.Second, func(p []point) bool { return totalCount(p) >= 30 })
		if totalCount(ps) != 30 {
			t.Fatalf("count %d, want 30", totalCount(ps))
		}
		routes := map[string]bool{}
		for _, p := range ps {
			r, _ := p.dp.Attributes().Get("http.route")
			routes[r.Str()] = true
			if p.metric.Histogram().AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
				t.Errorf("temporality changed")
			}
			if v, _ := p.resource.Get("scf.tenant.id"); v.Str() != tn.ID {
				t.Errorf("tenant %q, want %q", v.Str(), tn.ID)
			}
			if v, _ := p.resource.Get("scf.token.id"); v.Str() != tn.entry.TokenID {
				t.Errorf("token %q", v.Str())
			}
			if v, ok := p.resource.Get("scf.flags"); !ok || v.Int() != 1 {
				t.Errorf("scf.flags missing: %v", p.resource.AsRaw())
			}
			if !slices.Equal(p.dp.ExplicitBounds().AsRaw(), canonical) {
				t.Errorf("bounds %v", p.dp.ExplicitBounds().AsRaw())
			}
		}
		if !routes["/accounts/{account}/keys/{redacted-secret}"] || !routes["/tx/{id}"] || len(routes) != 2 {
			t.Errorf("routes %v", routes)
		}
	})

	t.Run("grpc delta", func(t *testing.T) {
		tn := s.newTenant(t)
		exp, err := otlpmetricgrpc.New(context.Background(), otlpmetricgrpc.WithEndpoint(s.grpcAddr), otlpmetricgrpc.WithInsecure(),
			otlpmetricgrpc.WithHeaders(map[string]string{"authorization": "Bearer " + tn.Token}),
			otlpmetricgrpc.WithTemporalitySelector(deltaSelector), otlpmetricgrpc.WithCompressor("gzip"))
		if err != nil {
			t.Fatal(err)
		}
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(appResource("api-grpc")), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(time.Hour))))
		t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
		h, _ := mp.Meter("app").Float64Histogram("http.server.request.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(canonical...))
		for i := 0; i < 7; i++ {
			h.Record(context.Background(), 0.002, metric.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/ledgers/{sequence}")))
		}
		flush(t, mp)
		for i := 0; i < 4; i++ {
			h.Record(context.Background(), 12, metric.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/ledgers/{sequence}")))
		}
		flush(t, mp)
		ps := s.waitPoints(t, tn.ID, "http.server.request.duration", 30*time.Second, func(p []point) bool { return totalCount(p) >= 11 })
		if totalCount(ps) != 11 {
			t.Fatalf("count %d", totalCount(ps))
		}
		for _, p := range ps {
			if p.metric.Histogram().AggregationTemporality() != pmetric.AggregationTemporalityDelta {
				t.Errorf("temporality %v", p.metric.Histogram().AggregationTemporality())
			}
		}
	})

	t.Run("legacy milliseconds", func(t *testing.T) {
		tn := s.newTenant(t)
		mp := httpMeter(t, s, tn.Token, true)
		msBounds := []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
		h, _ := mp.Meter("legacy").Float64Histogram("http.server.duration", metric.WithUnit("ms"), metric.WithExplicitBucketBoundaries(msBounds...))
		for _, v := range []float64{3, 40, 40, 900} {
			h.Record(context.Background(), v, metric.WithAttributes(attribute.String("http.method", "GET"),
				attribute.String("http.target", "/users/42?token=abc"), attribute.Int("http.status_code", 200), attribute.String("http.scheme", "http")))
		}
		flush(t, mp)
		ps := s.waitPoints(t, tn.ID, "http.server.request.duration", 30*time.Second, func(p []point) bool { return totalCount(p) >= 4 })
		p := ps[0]
		if p.metric.Unit() != "s" {
			t.Errorf("unit %q", p.metric.Unit())
		}
		want := []float64{0, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
		if !slices.Equal(p.dp.ExplicitBounds().AsRaw(), want) {
			t.Errorf("bounds %v", p.dp.ExplicitBounds().AsRaw())
		}
		if d := p.dp.Sum() - 0.983; d > 1e-9 || d < -1e-9 {
			t.Errorf("sum %v, want 0.983 seconds", p.dp.Sum())
		}
		a := p.dp.Attributes().AsRaw()
		if a["http.request.method"] != "GET" || a["http.route"] != "/users/{id}" || a["http.response.status_code"] != int64(200) || len(a) != 3 {
			t.Errorf("attributes %v", a)
		}
	})

	t.Run("exponential histogram", func(t *testing.T) {
		tn := s.newTenant(t)
		view := sdkmetric.NewView(sdkmetric.Instrument{Name: "http.server.request.duration"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: 160, MaxScale: 20}})
		mp := httpMeter(t, s, tn.Token, true, view)
		h, _ := mp.Meter("exp").Float64Histogram("http.server.request.duration", metric.WithUnit("s"))
		for _, v := range []float64{0.001, 0.02, 0.02, 0.2, 3, 30} {
			h.Record(context.Background(), v, metric.WithAttributes(attribute.String("http.request.method", "PUT"), attribute.String("http.route", "/items/{id}")))
		}
		flush(t, mp)
		ps := s.waitPoints(t, tn.ID, "http.server.request.duration", 30*time.Second, func(p []point) bool { return totalCount(p) >= 6 })
		p := ps[0]
		if !slices.Equal(p.dp.ExplicitBounds().AsRaw(), canonical) {
			t.Errorf("bounds %v", p.dp.ExplicitBounds().AsRaw())
		}
		if v, ok := p.dp.Attributes().Get("scf.lossy"); !ok || !v.Bool() {
			t.Errorf("scf.lossy missing: %v", p.dp.Attributes().AsRaw())
		}
		if p.dp.Count() != 6 {
			t.Errorf("count %d", p.dp.Count())
		}
		var sum uint64
		for _, c := range p.dp.BucketCounts().AsRaw() {
			sum += c
		}
		if sum != 6 || len(p.dp.BucketCounts().AsRaw()) != 15 {
			t.Errorf("bucket counts %v", p.dp.BucketCounts().AsRaw())
		}
	})

	t.Run("traces only", func(t *testing.T) {
		tn := s.newTenant(t)
		texp, err := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpoint(s.httpAddr), otlptracehttp.WithInsecure(),
			otlptracehttp.WithHeaders(map[string]string{"Authorization": "Bearer " + tn.Token}))
		if err != nil {
			t.Fatal(err)
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithResource(appResource("api-traces")), sdktrace.WithSyncer(texp))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

		// a real http server instrumented with otelhttp. the handler has no
		// route pattern, so the route comes from url.path.
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			trace.SpanFromContext(r.Context()).AddEvent(markerEvent)
			_, child := tp.Tracer("app").Start(r.Context(), markerSpanName)
			child.SetAttributes(attribute.String("db.statement", markerAttr))
			child.End()
			if strings.HasPrefix(r.URL.Path, "/missing") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
		h := otelhttp.NewHandler(mux, "server", otelhttp.WithTracerProvider(tp))
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
		ln := freeListener(t)
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport, otelhttp.WithTracerProvider(tp),
			otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return markerClient }))}
		paths := []string{"/accounts/" + acct, "/accounts/" + newAccount(t), "/ledgers/1", "/ledgers/2", "/ledgers/3", "/keys/" + seed, "/missing/7"}
		for _, p := range paths {
			resp, err := client.Get("http://" + ln.Addr().String() + p + "?q=" + markerAttr)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := tp.ForceFlush(ctx); err != nil {
			t.Fatalf("trace export: %v", err)
		}
		ps := s.waitPoints(t, tn.ID, "scf.http.server.request.duration.from_spans", 30*time.Second, func(p []point) bool { return totalCount(p) >= uint64(len(paths)) })
		if totalCount(ps) != uint64(len(paths)) {
			t.Fatalf("span-derived count %d, want %d (client and internal spans must not count): %s", totalCount(ps), len(paths), describe(ps))
		}
		counts := map[string]uint64{}
		for _, p := range ps {
			r, _ := p.dp.Attributes().Get("http.route")
			c, _ := p.dp.Attributes().Get("http.response.status_code")
			counts[r.Str()+" "+c.AsString()] += p.dp.Count()
			if _, ok := p.resource.Get("signal_to_metrics.service.instance.id"); ok {
				t.Error("connector instance attribute not removed")
			}
			if v, _ := p.resource.Get("scf.tenant.id"); v.Str() != tn.ID {
				t.Errorf("tenant %q", v.Str())
			}
			if p.metric.Unit() != "s" || !slices.Equal(p.dp.ExplicitBounds().AsRaw(), canonical) {
				t.Errorf("unit %q bounds %v", p.metric.Unit(), p.dp.ExplicitBounds().AsRaw())
			}
		}
		want := map[string]uint64{"/accounts/{account} 200": 2, "/ledgers/{id} 200": 3, "/keys/{redacted-secret} 200": 1, "/missing/{id} 404": 1}
		for k, v := range want {
			if counts[k] != v {
				t.Errorf("%s: %d, want %d (all %v)", k, counts[k], v, counts)
			}
		}
		if s.points(tn.ID, "http.server.request.duration") != nil {
			t.Error("a traces-only app produced tenant metric rows")
		}
	})

	// an sdk that retries a trace export (its ack timed
	// out, the collector answered 503 after jetstream stored the publish)
	// sends the same spans again. the derived points must be queued once,
	// in the minute the spans ended in, not the minute they arrived.
	t.Run("retried trace export is queued once in the span end minute", func(t *testing.T) {
		tn := s.newTenant(t)
		end := time.Now().UTC().Add(-3 * time.Minute)
		minute := end.Truncate(time.Minute)
		req := ptraceotlp.NewExportRequest()
		rs := req.Traces().ResourceSpans().AppendEmpty()
		rs.Resource().Attributes().PutStr("service.name", "api-retry")
		ss := rs.ScopeSpans().AppendEmpty()
		for i := range 3 {
			sp := ss.Spans().AppendEmpty()
			sp.SetKind(ptrace.SpanKindServer)
			sp.SetName("GET")
			sp.SetTraceID(pcommon.TraceID{1, byte(i)})
			sp.SetSpanID(pcommon.SpanID{2, byte(i)})
			sp.SetStartTimestamp(pcommon.NewTimestampFromTime(end.Add(-20 * time.Millisecond)))
			sp.SetEndTimestamp(pcommon.NewTimestampFromTime(end))
			sp.Attributes().PutStr("http.request.method", "GET")
			sp.Attributes().PutStr("http.route", "/retry")
			sp.Attributes().PutInt("http.response.status_code", 200)
		}
		body, err := req.MarshalProto()
		if err != nil {
			t.Fatal(err)
		}
		for attempt := range 2 {
			if code, msg := s.post(t, "/v1/traces", tn.Token, body, ""); code != http.StatusOK {
				t.Fatalf("attempt %d: %d %s", attempt, code, msg)
			}
			// the connector stamps its points with the moment it runs, so
			// the retry arrives with another time
			time.Sleep(1100 * time.Millisecond)
		}
		s.waitPoints(t, tn.ID, "scf.http.server.request.duration.from_spans", 30*time.Second, func(p []point) bool { return totalCount(p) >= 3 })
		// one more drain, so a second copy queued after the first is seen
		time.Sleep(time.Second)
		s.drain(t)
		ps := s.points(tn.ID, "scf.http.server.request.duration.from_spans")
		if totalCount(ps) != 3 {
			t.Fatalf("span-derived count %d, want 3 (the retry was queued again): %s", totalCount(ps), describe(ps))
		}
		for _, p := range ps {
			if got := p.dp.Timestamp().AsTime().UTC(); !got.Equal(minute) {
				t.Errorf("point time %s, want the span end minute %s", got, minute)
			}
			if _, ok := p.dp.Attributes().Get("scf.span.minute"); ok {
				t.Error("span minute attribute queued")
			}
			if _, ok := p.resource.Get("scf.batch.id"); ok {
				t.Error("batch id attribute queued")
			}
		}
	})

	t.Run("logs accepted and discarded", func(t *testing.T) {
		tn := s.newTenant(t)
		ld := plog.NewLogs()
		rl := ld.ResourceLogs().AppendEmpty()
		rl.Resource().Attributes().PutStr("service.name", "api-logs")
		rec := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		rec.Body().SetStr(markerLog + " seed " + seed)
		body, err := plogotlp.NewExportRequestFromLogs(ld).MarshalProto()
		if err != nil {
			t.Fatal(err)
		}
		if code, msg := s.post(t, "/v1/logs", tn.Token, body, ""); code != http.StatusOK {
			t.Fatalf("logs: %d %s", code, msg)
		}
		// grpc as well
		conn := grpcConn(t, s)
		_, err = plogotlp.NewGRPCClient(conn).Export(grpcmeta.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tn.Token), plogotlp.NewExportRequestFromLogs(ld))
		if err != nil {
			t.Fatalf("grpc logs: %v", err)
		}
		s.drain(t)
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, r := range s.seen {
			if r.subject == natsx.Subject(natsx.SubjectAPIMetrics, tn.ID) {
				t.Error("a log export produced a queued message")
			}
		}
	})

	t.Run("unknown and revoked tokens", func(t *testing.T) {
		code, _ := s.postMetrics(t, randomToken(t), emptyMetrics(), "")
		if code != http.StatusUnauthorized {
			t.Errorf("unknown token over http: %d", code)
		}
		if code, _ := s.postMetrics(t, "", emptyMetrics(), ""); code != http.StatusUnauthorized {
			t.Errorf("no token over http: %d", code)
		}
		conn := grpcConn(t, s)
		_, err := pmetricotlp.NewGRPCClient(conn).Export(grpcmeta.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+randomToken(t)), pmetricotlp.NewExportRequest())
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("unknown token over grpc: %v", err)
		}

		tn := s.newTenant(t)
		tn.entry.Status = "revoked"
		if err := s.pub.PutOTLPToken(context.Background(), tn.entry); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		for {
			code, _ := s.postMetrics(t, tn.Token, emptyMetrics(), "")
			if code == http.StatusUnauthorized {
				t.Logf("revoked token refused after %v", time.Since(start))
				break
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("revoked token still accepted after 5 s (%d)", code)
			}
			time.Sleep(50 * time.Millisecond)
		}
		_, err = pmetricotlp.NewGRPCClient(conn).Export(grpcmeta.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tn.Token), pmetricotlp.NewExportRequest())
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("revoked token over grpc: %v", err)
		}
		_ = registry.StatusActive
	})

	t.Run("oversize and decompression bombs", func(t *testing.T) {
		tn := s.newTenant(t)
		big := bombMetrics(5 << 20) // 5 MiB uncompressed protobuf
		if code, _ := s.postMetrics(t, tn.Token, big, ""); code != http.StatusRequestEntityTooLarge && code != http.StatusBadRequest {
			t.Errorf("5 MiB body: %d", code)
		}
		huge := bombMetrics(64 << 20)
		var gz bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
		_, _ = zw.Write(huge)
		_ = zw.Close()
		t.Logf("gzip bomb: %d bytes expanding to %d", gz.Len(), len(huge))
		if code, _ := s.postMetrics(t, tn.Token, gz.Bytes(), "gzip"); code != http.StatusRequestEntityTooLarge && code != http.StatusBadRequest {
			t.Errorf("gzip bomb: %d", code)
		}
		enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
		zs := enc.EncodeAll(huge, nil)
		_ = enc.Close()
		t.Logf("zstd bomb: %d bytes", len(zs))
		if code, _ := s.postMetrics(t, tn.Token, zs, "zstd"); code != http.StatusRequestEntityTooLarge && code != http.StatusBadRequest {
			t.Errorf("zstd bomb: %d", code)
		}
		if code, _ := s.postMetrics(t, tn.Token, []byte("x"), "br"); code == http.StatusOK {
			t.Errorf("unsupported encoding accepted")
		}
		conn := grpcConn(t, s)
		ctx := grpcmeta.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tn.Token)
		req := pmetricotlp.NewExportRequestFromMetrics(bombPdata(5 << 20))
		if _, err := pmetricotlp.NewGRPCClient(conn).Export(ctx, req, grpc.MaxCallSendMsgSize(64<<20)); status.Code(err) != codes.ResourceExhausted {
			t.Errorf("5 MiB grpc message: %v", err)
		}
		req = pmetricotlp.NewExportRequestFromMetrics(bombPdata(64 << 20))
		if _, err := pmetricotlp.NewGRPCClient(conn).Export(ctx, req, grpc.UseCompressor("gzip"), grpc.MaxCallSendMsgSize(128<<20)); status.Code(err) != codes.ResourceExhausted {
			t.Errorf("grpc gzip bomb: %v", err)
		}
		// the collector is still healthy afterwards
		if code, _ := s.postMetrics(t, tn.Token, emptyMetrics(), ""); code != http.StatusOK {
			t.Errorf("after bombs: %d", code)
		}
	})

	t.Run("valid export of 3 MiB is queued", func(t *testing.T) {
		// a batch of thousands of histogram points stays under the 4 MiB
		// request limit and is still over 3 MiB after filtering, three
		// times the nats server's default message limit
		tn := s.newTenant(t)
		md := pmetric.NewMetrics()
		rm := md.ResourceMetrics().AppendEmpty()
		rm.Resource().Attributes().PutStr("service.name", "big")
		m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		m.SetName("http.server.request.duration")
		m.SetUnit("s")
		h := m.SetEmptyHistogram()
		h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		now := time.Now()
		counts := make([]uint64, 15)
		counts[4] = 1
		var body []byte
		n := 0
		for len(body) < 3<<20+(1<<19) {
			for range 1000 {
				dp := h.DataPoints().AppendEmpty()
				dp.SetStartTimestamp(pcommon.NewTimestampFromTime(now.Add(-time.Minute)))
				dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
				dp.ExplicitBounds().FromRaw(canonical)
				dp.BucketCounts().FromRaw(counts)
				dp.SetCount(1)
				dp.SetSum(0.06)
				dp.Attributes().PutStr("http.request.method", "GET")
				dp.Attributes().PutStr("http.route", fmt.Sprintf("/accounts/{account}/operations/{id}/page-%06d", n))
				dp.Attributes().PutInt("http.response.status_code", 200)
				n++
			}
			body, _ = (&pmetric.ProtoMarshaler{}).MarshalMetrics(md)
		}
		if len(body) >= 4<<20 {
			t.Fatalf("request of %d bytes is over the receiver's limit", len(body))
		}
		if code, msg := s.postMetrics(t, tn.Token, body, ""); code != http.StatusOK {
			t.Fatalf("%d bytes, %d points: %d %s", len(body), n, code, msg)
		}
		s.waitPoints(t, tn.ID, "http.server.request.duration", 30*time.Second, func(p []point) bool { return len(p) == n })
		var queued int
		s.mu.Lock()
		for _, r := range s.seen {
			if r.subject == natsx.Subject(natsx.SubjectAPIMetrics, tn.ID) {
				queued = max(queued, len(r.data))
			}
		}
		s.mu.Unlock()
		if queued < 3<<20 {
			t.Errorf("largest queued message %d bytes, the case needs over 3 MiB", queued)
		}
		t.Logf("request %d bytes, %d points, queued message %d bytes", len(body), n, queued)
	})

	t.Run("rate limit 429 then sdk retry succeeds", func(t *testing.T) {
		tn := s.newTenant(t)
		// the receiver answers an empty request without running the
		// pipeline, so each request carries one point
		one := pmetric.NewMetrics()
		om := one.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		om.SetName("db.client.operation.duration")
		om.SetUnit("s")
		om.SetEmptyHistogram().DataPoints().AppendEmpty().SetCount(1)
		body, _ := (&pmetric.ProtoMarshaler{}).MarshalMetrics(one)
		var limited int
		for i := 0; i < rateRequestsBurst+5; i++ {
			code, _ := s.postMetrics(t, tn.Token, body, "")
			if code == http.StatusTooManyRequests {
				limited++
			}
		}
		if limited == 0 {
			t.Fatal("no 429 after exhausting the burst")
		}
		mp := httpMeter(t, s, tn.Token, true)
		h, _ := mp.Meter("rl").Float64Histogram("http.server.request.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(canonical...))
		h.Record(context.Background(), 0.1, metric.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/rl")))
		// the bucket is empty now; the sdk's retry gets through once it refills
		flush(t, mp)
		s.waitPoints(t, tn.ID, "http.server.request.duration", 30*time.Second, func(p []point) bool { return totalCount(p) == 1 })
	})

	t.Run("nats stopped gives 503 and recovers without a gap", func(t *testing.T) {
		tn := s.newTenant(t)
		mp := httpMeter(t, s, tn.Token, true)
		h, _ := mp.Meter("gap").Float64Histogram("http.server.request.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(canonical...))
		rec := func(n int) {
			for i := 0; i < n; i++ {
				h.Record(context.Background(), 0.05, metric.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/gap")))
			}
		}
		rec(10)
		flush(t, mp)

		s.nats.Stop(t)
		raw := pmetric.NewMetrics()
		m := raw.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		m.SetName("http.server.request.duration")
		m.SetUnit("s")
		hist := m.SetEmptyHistogram()
		hist.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		dp := hist.DataPoints().AppendEmpty()
		dp.SetCount(1)
		dp.ExplicitBounds().FromRaw(canonical)
		dp.BucketCounts().FromRaw(make([]uint64, 15))
		body, _ := (&pmetric.ProtoMarshaler{}).MarshalMetrics(raw)
		if code, msg := s.postMetrics(t, tn.Token, body, ""); code != http.StatusServiceUnavailable {
			t.Fatalf("nats stopped: %d %s", code, msg)
		}
		rec(15)
		var restarted atomic.Bool
		go func() {
			time.Sleep(8 * time.Second)
			s.nats.Start(t)
			restarted.Store(true)
		}()
		// the sdk retries the 503s until nats is back
		flush(t, mp)
		if !restarted.Load() {
			t.Fatal("export succeeded while nats was stopped")
		}
		// 10 before, 15 during the outage; the raw request above was refused
		// and never retried
		ps := s.waitPoints(t, tn.ID, "http.server.request.duration", 60*time.Second, func(p []point) bool { return totalCount(p) >= 25 })
		if totalCount(ps) != 25 {
			t.Fatalf("count %d, want 25", totalCount(ps))
		}
	})

	t.Run("collector restart under load through edge caddy loses nothing", func(t *testing.T) {
		testRestartUnderLoad(t, s)
	})

	t.Run("only allowlisted names and attributes were queued", func(t *testing.T) {
		s.drain(t)
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.seen) == 0 {
			t.Fatal("no messages")
		}
		resAllowed := map[string]bool{"service.name": true, "scf.tenant.id": true, "scf.token.id": true, "scf.resource.hash": true, "scf.flags": true}
		dpAllowed := map[string]bool{"http.request.method": true, "http.route": true, "http.response.status_code": true, "error.type": true, "scf.lossy": true, "scf.stream": true}
		ids := map[string]bool{}
		for _, r := range s.seen {
			if ids[r.msgID] {
				t.Errorf("message id %s stored twice", r.msgID)
			}
			ids[r.msgID] = true
			for _, bad := range []string{seed, strings.ToLower(seed), acct, markerResource, markerAttr, markerEvent, markerLog, markerSpanName, markerClient, "forged-tenant", "11111111-1111-4111-8111-111111111111"} {
				if bytes.Contains(r.data, []byte(bad)) {
					t.Errorf("message on %s contains %q", r.subject, bad)
				}
			}
			rms := r.md.ResourceMetrics()
			for i := 0; i < rms.Len(); i++ {
				for k := range rms.At(i).Resource().Attributes().All() {
					if !resAllowed[k] {
						t.Errorf("resource attribute %s queued", k)
					}
				}
				sms := rms.At(i).ScopeMetrics()
				for j := 0; j < sms.Len(); j++ {
					if sms.At(j).Scope().Name() != "" || sms.At(j).Scope().Attributes().Len() != 0 {
						t.Errorf("scope %q queued", sms.At(j).Scope().Name())
					}
					ms := sms.At(j).Metrics()
					for k := 0; k < ms.Len(); k++ {
						m := ms.At(k)
						if m.Name() != "http.server.request.duration" && m.Name() != "scf.http.server.request.duration.from_spans" {
							t.Errorf("metric %s queued", m.Name())
						}
						if m.Type() != pmetric.MetricTypeHistogram {
							t.Errorf("metric type %v queued", m.Type())
							continue
						}
						dps := m.Histogram().DataPoints()
						for l := 0; l < dps.Len(); l++ {
							if dps.At(l).Exemplars().Len() != 0 {
								t.Error("exemplars queued")
							}
							for a := range dps.At(l).Attributes().All() {
								if !dpAllowed[a] {
									t.Errorf("attribute %s queued", a)
								}
							}
						}
					}
				}
			}
		}
		t.Logf("checked %d queued messages", len(s.seen))
	})

	t.Run("secret seed absent from collector logs", func(t *testing.T) {
		if strings.Contains(s.logs.String(), seed) || strings.Contains(strings.ToLower(s.logs.String()), strings.ToLower(seed)) {
			t.Fatal("seed in collector log")
		}
	})
}

func grpcConn(t *testing.T, s *stack) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(s.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// bombPdata builds a valid metrics payload of roughly n bytes, made of one
// highly compressible attribute value.
func bombPdata(n int) pmetric.Metrics {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("http.server.request.duration")
	m.SetUnit("s")
	dp := m.SetEmptyHistogram().DataPoints().AppendEmpty()
	dp.Attributes().PutStr("padding", strings.Repeat("a", n))
	return md
}

func bombMetrics(n int) []byte {
	b, _ := (&pmetric.ProtoMarshaler{}).MarshalMetrics(bombPdata(n))
	return b
}
