package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
)

// RequestID returns the id of the request, or "" outside Base.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Logger returns the request logger (with request_id), or slog.Default
// outside Base.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// statusWriter remembers the status for the access log line.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Base is the outermost middleware of the api: it gives every request an id
// (response header X-Request-Id, envelope field request_id, log field),
// sets Cache-Control: no-store on every answer, turns a panic into 500
// internal, and writes one log line per request. the line holds method,
// path, status and duration only: never the remote address, user agent,
// cookie or query string (contract section 1).
func Base(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			id := uuid.Must(uuid.NewV7()).String()
			reqLog := log.With("request_id", id)
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			ctx = context.WithValue(ctx, loggerKey, reqLog)
			r = r.WithContext(ctx)

			w.Header().Set("X-Request-Id", id)
			w.Header().Set("Cache-Control", "no-store")
			sw := &statusWriter{ResponseWriter: w}
			defer func() {
				if v := recover(); v != nil {
					if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(v)
					}
					reqLog.Error("handler panicked", "path", r.URL.Path, "method", r.Method, "panic", slog.AnyValue(v).String())
					if sw.status == 0 {
						WriteError(sw, r, E(http.StatusInternalServerError, CodeInternal, "internal error"))
					}
				}
				status := sw.status
				if status == 0 {
					status = http.StatusOK
				}
				reqLog.Info("request", "method", r.Method, "path", r.URL.Path, "status", status,
					"duration_ms", time.Since(start).Milliseconds())
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// LimitBody enforces the transport rules on request bodies (contract section
// 1): a body larger than MaxBody is refused with 413, and a body whose
// content type is not application/json with 415, on every route, also on
// routes that do not read a body.
func LimitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > MaxBody {
			WriteError(w, r, tooLargeError())
			return
		}
		if hasBody(r) && !isJSON(r.Header.Get("Content-Type")) {
			WriteError(w, r, E(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "content type must be application/json"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
		next.ServeHTTP(w, r)
	})
}

// CSRFHeader and CSRFValue are the custom header every state-changing
// request must carry (contract 1.2).
const (
	CSRFHeader = "X-Requested-With"
	CSRFValue  = "sa-console"
)

// stateChanging lists the methods the csrf rules apply to.
func stateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// CSRF refuses state-changing requests that lack X-Requested-With:
// sa-console (403 csrf_header_missing) or whose Origin is not exactly
// consoleOrigin (403 origin_mismatch). a cross-site form cannot set the
// header, and a cross-site fetch with it needs a cors preflight, which the
// control plane never grants. the Origin check also stops same-site
// attackers (other subdomains) that SameSite=Lax lets through.
func CSRF(consoleOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stateChanging(r.Method) {
				// exact and case-sensitive, and only one value
				if v := r.Header.Values(CSRFHeader); len(v) != 1 || v[0] != CSRFValue {
					WriteError(w, r, Forbidden(CodeCSRFHeaderMissing, "header X-Requested-With: sa-console is required"))
					return
				}
				if o := r.Header.Values("Origin"); len(o) != 1 || o[0] != consoleOrigin {
					WriteError(w, r, Forbidden(CodeOriginMismatch, "request origin is not the console"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NotFoundHandler answers unknown paths with the envelope.
func NotFoundHandler(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, NotFound())
}

// MethodNotAllowedHandler answers a known path with an undefined method.
func MethodNotAllowedHandler(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, E(http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed"))
}
