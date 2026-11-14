package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/sso"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/umami"
)

// Path is where the mcp endpoint is served, beside /api. Caddy on
// app.<BASE_DOMAIN> routes it, the oauth paths and the two metadata
// documents to the control plane (deploy/caddy/app.Caddyfile).
const Path = "/mcp"

// Scope is the one oauth scope: read-only analytics. which tenant, or the
// operator view, is chosen on the consent page and bound to the grant.
const Scope = "analytics:read"

// limits of contract 12.6
const (
	// requests per second and burst of each credential's token bucket, per
	// control plane instance
	DefaultRate  = 5
	DefaultBurst = 30
	// tool calls one credential may have running at once
	MaxConcurrentCalls = 4
	// one tool call, umami and database work included
	CallTimeout = 30 * time.Second
	// the json of one tool result
	MaxResultBytes = 512 << 10
	// the json-rpc body of one request
	maxRequestBytes = 64 << 10
)

// Routes is a group of console routes, registered relative to /api.
type Routes interface {
	Routes(r chi.Router)
}

// SiteSessions opens a umami session as a user for a site of a tenant the
// user is a member of. *sso.Handler implements it.
type SiteSessions interface {
	SiteSession(ctx context.Context, user, siteID uuid.UUID) (sso.Site, umami.Session, error)
}

// WebsiteReader reads a website route as a user. *umami.Client implements it.
type WebsiteReader interface {
	ReadWebsite(ctx context.Context, token umami.Token, websiteID, sub string, q url.Values, out any) error
}

// Config are the parts of the server. every field is required except Now,
// Rate and Burst.
type Config struct {
	DB *db.DB
	// Guard decides who is an operator, as for the console's operator area.
	Guard *operator.Guard
	// Reads are the console's read routes the tools call in process: the
	// tenant, site and api metrics groups, and the operator area mounted
	// behind its guard. only GET requests ever reach them.
	Reads []Routes
	// Sessions and Umami serve the web analytics tools.
	Sessions SiteSessions
	Umami    WebsiteReader
	// Issuer is the console origin, https://app.<BASE_DOMAIN>: the oauth
	// issuer, and the resource uri is Issuer + Path.
	Issuer string
	// StateKey is OAUTH_STATE_KEY; the key that seals authorization
	// requests is derived from it
	StateKey []byte
	Log      *slog.Logger
	Registry prometheus.Registerer
	Version  string
	Now      func() time.Time
	Rate     float64
	Burst    int
}

// Server is the mcp endpoint, its oauth authorization server and the
// console routes that manage credentials.
type Server struct {
	db       *db.DB
	guard    *operator.Guard
	reads    http.Handler
	sessions SiteSessions
	umami    WebsiteReader
	issuer   string
	resource string
	log      *slog.Logger
	sdkLog   *slog.Logger
	now      func() time.Time
	version  string

	limiter  *limiter
	inflight *inflight
	webCache *webCache
	metrics  *metrics
	register   *bucket
	registerMu sync.Mutex
	sealKey    []byte

	tenantServer   *mcp.Server
	operatorServer *mcp.Server
	streamable     http.Handler
}

// New checks the configuration and builds both tool sets.
func New(c Config) (*Server, error) {
	if c.DB == nil || c.Guard == nil || len(c.Reads) == 0 || c.Sessions == nil || c.Umami == nil ||
		c.Log == nil || c.Registry == nil {
		return nil, errors.New("mcpserver: database, guard, read routes, umami sessions, umami reader, log and registry are required")
	}
	u, err := url.Parse(c.Issuer)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return nil, errors.New("mcpserver: the issuer must be a bare origin such as https://app.example.org")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Rate == 0 {
		c.Rate = DefaultRate
	}
	if c.Burst == 0 {
		c.Burst = DefaultBurst
	}
	if c.Rate < 0 || c.Burst < 1 {
		return nil, errors.New("mcpserver: the rate must be positive and the burst at least 1")
	}
	if c.Version == "" {
		c.Version = "dev"
	}
	key, err := sealKey(c.StateKey)
	if err != nil {
		return nil, err
	}
	m, err := newMetrics(c.Registry)
	if err != nil {
		return nil, err
	}
	reads := chi.NewRouter()
	reads.Use(httpx.Base(c.Log), getOnly)
	reads.NotFound(httpx.NotFoundHandler)
	reads.MethodNotAllowed(httpx.MethodNotAllowedHandler)
	for _, g := range c.Reads {
		g.Routes(reads)
	}
	s := &Server{
		db: c.DB, guard: c.Guard, reads: reads, sessions: c.Sessions, umami: c.Umami,
		issuer: c.Issuer, resource: c.Issuer + Path, log: c.Log, now: c.Now, version: c.Version,
		// the sdk logs every stateless request's session at info; only its
		// warnings and errors reach the service log
		sdkLog: slog.New(warnOnly{c.Log.Handler()}),
		limiter:  newLimiter(c.Rate, float64(c.Burst)),
		inflight: &inflight{n: map[uuid.UUID]int{}},
		webCache: newWebCache(),
		metrics:  m,
		register: newBucket(registerRate, registerBurst, c.Now()),
		sealKey:  key,
	}
	s.tenantServer = s.newMCPServer(tenantInstructions)
	s.addTenantTools(s.tenantServer)
	s.operatorServer = s.newMCPServer(operatorInstructions)
	s.addOperatorTools(s.operatorServer)
	s.streamable = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		p, ok := principalFrom(r.Context())
		if !ok {
			return nil
		}
		if p.Scope == ScopeOperator {
			return s.operatorServer
		}
		return s.tenantServer
	}, &mcp.StreamableHTTPOptions{
		// every request carries its own credential and the server keeps no
		// session, so any control plane instance can answer any request
		Stateless:           true,
		JSONResponse:        true,
		MaxRequestBodyBytes: maxRequestBytes,
		Logger:              s.sdkLog,
	})
	return s, nil
}

// Resource is the resource uri of this server, the audience of every
// credential it accepts.
func (s *Server) Resource() string { return s.resource }

func (s *Server) newMCPServer(instructions string) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "veristar", Title: "Veristar analytics", Version: s.version},
		&mcp.ServerOptions{Instructions: instructions, Logger: s.sdkLog})
}

// getOnly keeps the in-process reads to GET: the tools never change state,
// and this makes sure no tool bug can reach a write route.
func getOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowedHandler(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler serves Path. order: request id and log line without the query;
// no Origin (no browser may call it, contract 12.2); exactly one bearer
// credential, authenticated on every request; the credential's rate
// limit; then the streamable transport of the go sdk.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.Base(s.log))
	r.NotFound(httpx.NotFoundHandler)
	r.MethodNotAllowed(httpx.MethodNotAllowedHandler)
	// post only: the server is stateless, so it offers no get stream and no
	// session to delete (streamable http transport, 2026-07-28)
	r.Post("/", s.serveMCP)
	return r
}

func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.URL.Path != Path {
		httpx.NotFoundHandler(w, r)
		return
	}
	// the streamable http transport requires servers to validate Origin
	// against dns rebinding. no browser page is a client of this endpoint
	// (clients run natively or on their vendor's servers), so a request
	// that carries one is refused.
	if len(r.Header.Values("Origin")) > 0 {
		s.metrics.requests.WithLabelValues("origin_refused").Inc()
		httpx.WriteError(w, r, httpx.Forbidden(httpx.CodeOriginMismatch, "the mcp endpoint does not accept browser requests"))
		return
	}
	hdr := r.Header.Values("Authorization")
	if len(hdr) != 1 {
		s.metrics.requests.WithLabelValues("unauthenticated").Inc()
		s.challenge(w, "", "")
		httpx.WriteError(w, r, httpx.E(http.StatusUnauthorized, httpx.CodeUnauthenticated,
			"an mcp access key or an oauth access token is required: Authorization: Bearer ..."))
		return
	}
	p, err := s.authenticate(ctx, hdr[0])
	var ue *errUnauthenticated
	if errors.As(err, &ue) {
		s.metrics.requests.WithLabelValues("unauthenticated").Inc()
		s.challenge(w, "invalid_token", ue.reason)
		httpx.WriteError(w, r, httpx.E(http.StatusUnauthorized, httpx.CodeUnauthenticated, ue.reason))
		return
	}
	if err != nil {
		s.metrics.requests.WithLabelValues("error").Inc()
		httpx.WriteError(w, r, err)
		return
	}
	now := s.now()
	if ok, wait := s.limiter.take(p.CredentialID, now); !ok {
		s.metrics.requests.WithLabelValues("rate_limited").Inc()
		httpx.WriteError(w, r, httpx.RateLimited(wait))
		return
	}
	if err := s.touch(ctx, p.CredentialID, now); err != nil {
		s.metrics.requests.WithLabelValues("error").Inc()
		httpx.WriteError(w, r, err)
		return
	}
	s.metrics.requests.WithLabelValues("accepted").Inc()
	ctx = withPrincipal(ctx, p)
	// the sdk hands TokenInfo to every tool handler as req.Extra.TokenInfo,
	// whatever context the handler runs in; UserID binds any session to
	// this credential
	info := &auth.TokenInfo{
		Scopes: []string{Scope}, Expiration: now.Add(CallTimeout + time.Minute),
		UserID: p.CredentialID.String(), Extra: map[string]any{principalExtra: p},
	}
	verified := auth.RequireBearerToken(func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		return info, nil
	}, nil)
	verified(s.streamable).ServeHTTP(w, r.WithContext(ctx))
}

// principalExtra is the TokenInfo.Extra key of the principal.
const principalExtra = "veristar.principal"

// principalOf reads the principal of a tool call.
func principalOf(req *mcp.CallToolRequest) (Principal, bool) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return Principal{}, false
	}
	p, ok := req.Extra.TokenInfo.Extra[principalExtra].(Principal)
	return p, ok
}

// inflight counts the running tool calls of each credential.
type inflight struct {
	mu sync.Mutex
	n  map[uuid.UUID]int
}

func (f *inflight) acquire(id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n[id] >= MaxConcurrentCalls {
		return false
	}
	f.n[id]++
	return true
}

func (f *inflight) release(id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n[id] <= 1 {
		delete(f.n, id)
		return
	}
	f.n[id]--
}

// sessionUser is the context the console's read handlers expect.
func sessionUser(ctx context.Context, p Principal) context.Context {
	return session.WithUser(ctx, p.User)
}

func (s *Server) resourceMetadataURL() string {
	return s.issuer + ProtectedResourceMetadataPath + Path
}

// metrics of contract 12.7
type metrics struct {
	requests  *prometheus.CounterVec
	calls     *prometheus.CounterVec
	durations *prometheus.HistogramVec
	oauth     *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "controlplane_mcp_requests_total",
			Help: "Requests to the mcp endpoint by outcome of authentication and limits.",
		}, []string{"outcome"}),
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "controlplane_mcp_tool_calls_total",
			Help: "MCP tool calls by tool and outcome.",
		}, []string{"tool", "outcome"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "controlplane_mcp_tool_call_duration_seconds",
			Help:    "Duration of MCP tool calls.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"tool"}),
		oauth: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "controlplane_mcp_oauth_requests_total",
			Help: "Requests to the mcp oauth endpoints by endpoint and outcome.",
		}, []string{"endpoint", "outcome"}),
	}
	for _, c := range []prometheus.Collector{m.requests, m.calls, m.durations, m.oauth} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("mcpserver: metrics: %w", err)
		}
	}
	return m, nil
}

// trimmed is s without surrounding space, for names.
func trimmed(s string) string { return strings.TrimSpace(s) }

// warnOnly passes records of level warn and above.
type warnOnly struct{ slog.Handler }

func (w warnOnly) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn && w.Handler.Enabled(ctx, l)
}

func (w warnOnly) WithAttrs(a []slog.Attr) slog.Handler { return warnOnly{w.Handler.WithAttrs(a)} }

func (w warnOnly) WithGroup(n string) slog.Handler { return warnOnly{w.Handler.WithGroup(n)} }
