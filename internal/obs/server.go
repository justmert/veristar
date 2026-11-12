package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry returns a registry with the go runtime and process collectors.
// services register their own metrics on it instead of the global default,
// so tests and multiple servers in one process do not collide.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// Check is one readiness dependency, such as a database ping.
type Check struct {
	Name string
	Fn   func(context.Context) error
}

// readyTimeout bounds every readiness probe so a hung dependency cannot pile up requests.
const readyTimeout = 2 * time.Second

// NewOpsHandler serves /healthz, /readyz and /metrics.
//
// /healthz answers 200 whenever the process can serve http at all.
// /readyz runs every check and answers 503 naming the first failing one, so
// a load balancer stops sending traffic while a dependency is down.
func NewOpsHandler(reg *prometheus.Registry, log *slog.Logger, checks ...Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, c := range checks {
			if err := c.Fn(ctx); err != nil {
				log.Warn("readiness check failed", "check", c.Name, "error", err.Error())
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprintf(w, "not ready: %s\n", c.Name)
				return
			}
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		Registry:          reg,
		ErrorHandling:     promhttp.ContinueOnError,
		EnableOpenMetrics: true,
	}))
	return mux
}

// NewServer returns an http.Server with timeouts suited to small internal endpoints.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// shutdownGrace is how long in-flight requests get after the context ends.
const shutdownGrace = 10 * time.Second

// Serve listens on srv.Addr and serves until ctx ends, then shuts down gracefully.
func Serve(ctx context.Context, srv *http.Server, log *slog.Logger) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", srv.Addr, err)
	}
	return ServeListener(ctx, srv, ln, log)
}

// ServeListener is Serve on an existing listener, which lets tests bind port 0.
func ServeListener(ctx context.Context, srv *http.Server, ln net.Listener, log *slog.Logger) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("http server listening", "addr", ln.Addr().String())
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	// the parent is already cancelled, so shutdown gets its own deadline
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
