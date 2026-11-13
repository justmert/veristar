package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/internal/registry"
)

// Sites is the registry snapshot the gateway checks sites against.
type Sites interface {
	Get(id string) (registry.Site, bool)
	Loaded() bool
}

// ServerConfig wires the checks into one http handler. every field is
// required.
type ServerConfig struct {
	Sites      Sites
	Challenger *Challenger
	Proofs     *ProofVerifier
	Signals    *SignalsChecker
	ClientIP   *ClientIPReader
	Geo        *Geo
	Visitor    *Visitor
	Cluster    *IPCluster
	// Volume quarantines one visitor's events past VISITOR_EVENTS_PER_HOUR.
	Volume  *VisitorVolume
	Limiter *RateLimiter
	// ChallengeLimiter holds the token buckets of GET /v1/c, kept apart
	// from the event buckets so that fetching challenges never uses up a
	// visitor's event budget or the other way round.
	ChallengeLimiter *RateLimiter
	Stats            *Stats
	Publisher        *Publisher
	Releases         *Releases
	// MaxBody is GATEWAY_MAX_BODY, at most events.MaxBody.
	MaxBody    int
	Log        *slog.Logger
	Registerer prometheus.Registerer
	// Now stamps accepted_at. it should be the clock the key store uses.
	Now func() time.Time
}

// requestBudget bounds the work for one request, including a key or secret
// read that has to reach nats. the publish has its own PublishAckWait.
const requestBudget = 5 * time.Second

// route labels of the metrics
const (
	routeTracker   = "tracker"
	routeChallenge = "challenge"
	routeEvent     = "event"
	routeOther     = "other"
)

// Server is the public side of the gateway: GET /v/{version}/sdk.js,
// GET /v1/c and POST /v1/e. it listens on the private network behind the
// local caddy; /healthz, /readyz and /metrics are on the ops port.
type Server struct {
	c        ServerConfig
	mux      *http.ServeMux
	outcomes *prometheus.CounterVec
	duration *prometheus.HistogramVec
	warnings *prometheus.CounterVec
}

// NewServer checks the configuration and registers the metrics.
func NewServer(c ServerConfig) (*Server, error) {
	if c.Sites == nil || c.Challenger == nil || c.Proofs == nil || c.Signals == nil || c.ClientIP == nil ||
		c.Geo == nil || c.Visitor == nil || c.Cluster == nil || c.Volume == nil || c.Limiter == nil || c.ChallengeLimiter == nil || c.Stats == nil ||
		c.Publisher == nil || c.Releases == nil || c.Log == nil || c.Registerer == nil || c.Now == nil {
		return nil, errors.New("gateway: ServerConfig is missing a dependency")
	}
	if c.MaxBody < 1 || c.MaxBody > events.MaxBody {
		return nil, fmt.Errorf("gateway: max body %d outside 1 to %d", c.MaxBody, events.MaxBody)
	}
	s := &Server{
		c: c,
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_responses_total",
			Help: "responses by route and outcome, including requests for unknown sites that EDGE_STATS cannot count.",
		}, []string{"route", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "time from request start to response, by route.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		}, []string{"route"}),
		warnings: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_degraded_total",
			Help: "accepted events where a side step failed: geo lookup or ip-cluster gossip.",
		}, []string{"step"}),
	}
	for _, m := range []prometheus.Collector{s.outcomes, s.duration, s.warnings} {
		if err := c.Registerer.Register(m); err != nil {
			return nil, fmt.Errorf("gateway: register server metrics: %w", err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v/{version}/sdk.js", s.tracker)
	mux.HandleFunc("GET /v1/c", s.challenge)
	mux.HandleFunc("POST /v1/e", s.event)
	s.mux = mux
	return s, nil
}

// ServeHTTP sets the cors header every response carries, then routes. the
// header names the request's own origin, so a refused event never shows up
// as a cors error in the visitor's console. no credentials are allowed, so
// reflecting the origin grants nothing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" {
		w.Header().Set("Access-Control-Allow-Origin", o)
		w.Header().Add("Vary", "Origin")
	}
	if _, pattern := s.mux.Handler(r); pattern == "" {
		// unknown paths and methods, including any OPTIONS preflight, which
		// the tracker never sends
		start := time.Now()
		s.mux.ServeHTTP(w, r)
		s.observe(routeOther, "not_routed", start)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) observe(route, outcome string, start time.Time) {
	s.outcomes.WithLabelValues(route, outcome).Inc()
	s.duration.WithLabelValues(route).Observe(time.Since(start).Seconds())
}

func writeStatus(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	if status == http.StatusNoContent || status == http.StatusAccepted || body == "" {
		w.WriteHeader(status)
		return
	}
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// every caller passes a fixed reason code, never request data, and the
	// response is text/plain with nosniff, so there is nothing to escape
	_, _ = io.WriteString(w, body+"\n") //nolint:gosec // see above
}

// tracker serves an immutable release. revoked versions answer 410; there
// is no latest alias.
func (s *Server) tracker(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	h := w.Header()
	// a crossorigin="anonymous" script with integrity needs both
	h.Set("Access-Control-Allow-Origin", "*")
	h.Del("Vary")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	body, revoked, ok := s.c.Releases.Lookup(r.PathValue("version"))
	switch {
	case !ok:
		writeStatus(w, http.StatusNotFound, "no such tracker version")
		s.observe(routeTracker, "not_found", start)
		return
	case revoked:
		writeStatus(w, http.StatusGone, "tracker version revoked")
		s.observe(routeTracker, "revoked", start)
		return
	}
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	s.observe(routeTracker, "served", start)
}

// challenge answers GET /v1/c?w=<site id> for an active site, from one of
// its origins, within the site's and the truncated prefix's challenge rate.
// the limit comes last, so refused requests for unknown sites or from
// foreign origins neither take tokens nor create buckets.
func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), requestBudget)
	defer cancel()
	q := r.URL.Query()
	siteID := q.Get("w")
	site, ok := s.c.Sites.Get(siteID)
	var err error
	switch {
	case !s.c.Sites.Loaded():
		// before the first snapshot every site would look unknown
		err = &RejectError{Reason: ReasonUnavailable, Err: errors.New("registry snapshot not loaded")}
	case len(q["w"]) != 1:
		err = reject(ReasonMalformed)
	case !ok:
		err = reject(ReasonUnknownSite)
	case !site.Active():
		err = reject(ReasonInactiveSite)
	default:
		if err = CheckOrigin(site, r.Header.Get("Origin")); err == nil {
			err = s.c.Signals.Check(r.Header)
		}
		if err == nil {
			err = s.limitChallenge(ctx, r, site.ID)
		}
	}
	var body []byte
	if err == nil {
		body, err = s.c.Challenger.Issue(ctx, site.ID)
	}
	if err != nil {
		reason := s.reasonOf(err, routeChallenge)
		writeStatus(w, reason.Status(), string(reason))
		s.observe(routeChallenge, string(reason), start)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	s.observe(routeChallenge, "issued", start)
}

// limitChallenge takes a token from the challenge buckets of the site and of
// the request's truncated prefix.
func (s *Server) limitChallenge(ctx context.Context, r *http.Request, siteID string) error {
	ip, err := s.c.ClientIP.FromRequest(r)
	if err != nil {
		// only caddy sets the header, so a missing or broken one is the
		// server side's fault, not the visitor's
		return &RejectError{Reason: ReasonUnavailable, Err: fmt.Errorf("client address header %s unusable: %w", s.c.ClientIP.Header(), err)}
	}
	_, pk, err := s.prefixOf(ctx, ip)
	if err != nil {
		return &RejectError{Reason: ReasonUnavailable, Err: err}
	}
	if s.c.ChallengeLimiter.Allow(siteID, pk) != RateAllowed {
		return reject(ReasonRateLimited)
	}
	return nil
}

// prefixOf returns the keyed hash of ip's truncated prefix, as hex for the
// ip-cluster and as the rate limiters' key.
func (s *Server) prefixOf(ctx context.Context, ip ClientIP) (string, PrefixKey, error) {
	var pk PrefixKey
	prefix, err := s.c.Visitor.PrefixKey(ctx, ip)
	if err != nil {
		return "", pk, fmt.Errorf("ip-cluster secret unavailable: %w", err)
	}
	if _, err := hex.Decode(pk[:], []byte(prefix)); err != nil {
		return "", pk, fmt.Errorf("prefix key is not hex: %w", err)
	}
	return prefix, pk, nil
}

// reasonOf maps an error to its reason and logs the ones that are the
// server's fault. a plain error is a programming or dependency failure and
// counts as unavailable.
func (s *Server) reasonOf(err error, route string) Reason {
	reason, ok := ReasonOf(err)
	if !ok {
		reason = ReasonUnavailable
	}
	if reason == ReasonUnavailable {
		s.c.Log.Error("request refused as unavailable", "route", route, "error", err.Error())
	}
	return reason
}

// verdict is the result of one event: the status, the outcome counted and
// the site id to count it under ("" when the site is not in the registry).
type verdict struct {
	status  int
	outcome string
	site    string
}

func refused(r Reason, site string) verdict {
	return verdict{status: r.Status(), outcome: string(r), site: site}
}

// event answers POST /v1/e.
func (s *Server) event(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), requestBudget)
	defer cancel()
	v := s.handleEvent(ctx, r)
	if v.status >= 400 {
		writeStatus(w, v.status, v.outcome)
	} else {
		writeStatus(w, v.status, "")
	}
	s.observe(routeEvent, v.outcome, start)
	if v.site != "" {
		if err := s.c.Stats.Count(v.site, v.outcome); err != nil {
			s.c.Log.Error("outcome not counted", "outcome", v.outcome, "error", err.Error())
		}
	}
}

// handleEvent runs the checks of the design step 6 in order: body size
// and schema, site exists and is active, Sec-GPC, a revoked tracker release,
// Origin, challenge tag, age and work, replay limit, fetch metadata, rate
// limits. an event that passes is enriched (visitor token, country, band),
// sanitized and queued. a proof use taken by the replay check is given back
// when a later check refuses the event, so only queued events count.
func (s *Server) handleEvent(ctx context.Context, r *http.Request) verdict {
	var used *events.Proof
	v := s.checkEvent(ctx, r, &used)
	if used != nil && v.status != http.StatusAccepted {
		s.c.Proofs.Release(*used)
	}
	return v
}

func (s *Server) checkEvent(ctx context.Context, r *http.Request, used **events.Proof) verdict {
	if r.ContentLength > int64(s.c.MaxBody) {
		return refused(ReasonBodyTooLarge, "")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(s.c.MaxBody)+1))
	if err != nil {
		return refused(ReasonMalformed, "")
	}
	if len(body) > s.c.MaxBody {
		return refused(ReasonBodyTooLarge, "")
	}
	wire, err := events.ParseWire(body)
	if err != nil {
		// the site of an unparsable body is not trusted, so it is not counted
		// on EDGE_STATS; the prometheus counter still sees it
		return refused(ParseReason(err), "")
	}
	if !s.c.Sites.Loaded() {
		s.c.Log.Error("event refused, registry snapshot not loaded")
		return refused(ReasonUnavailable, "")
	}
	site, ok := s.c.Sites.Get(wire.SiteID)
	if !ok {
		return refused(ReasonUnknownSite, "")
	}
	if !site.Active() {
		return refused(ReasonInactiveSite, site.ID)
	}
	if r.Header.Get("Sec-GPC") == "1" {
		return refused(ReasonGPC, site.ID)
	}
	if wire.SDKVersion != "" {
		// sdk.js of a revoked release answers 410, but a cached or
		// self-hosted copy keeps running; its events are refused here
		if _, revoked, _ := s.c.Releases.Lookup(wire.SDKVersion); revoked {
			return refused(ReasonRevokedVersion, site.ID)
		}
	}
	if err := CheckOrigin(site, r.Header.Get("Origin")); err != nil {
		return refused(s.reasonOf(err, routeEvent), site.ID)
	}
	if err := s.c.Proofs.Verify(ctx, site.ID, wire.Proof); err != nil {
		return refused(s.reasonOf(err, routeEvent), site.ID)
	}
	*used = &wire.Proof
	if err := s.c.Signals.Check(r.Header); err != nil {
		return refused(s.reasonOf(err, routeEvent), site.ID)
	}
	ip, err := s.c.ClientIP.FromRequest(r)
	if err != nil {
		// only caddy sets the header, so a missing or broken one is the
		// server side's fault, not the visitor's
		s.c.Log.Error("client address header unusable", "header", s.c.ClientIP.Header(), "error", err.Error())
		return refused(ReasonUnavailable, site.ID)
	}
	prefix, pk, err := s.prefixOf(ctx, ip)
	if err != nil {
		s.c.Log.Error("prefix key unavailable", "error", err.Error())
		return refused(ReasonUnavailable, site.ID)
	}
	if s.c.Limiter.Allow(site.ID, pk) != RateAllowed {
		return refused(ReasonRateLimited, site.ID)
	}

	// every browser sends a user agent; the visitor token needs one
	agent := r.Header.Get("User-Agent")
	if agent == "" || len(r.Header.Values("User-Agent")) != 1 {
		return refused(ReasonMalformed, site.ID)
	}
	vt, err := s.c.Visitor.Token(ctx, site.ID, ip, agent)
	if err != nil {
		s.c.Log.Error("visitor secret unavailable", "error", err.Error())
		return refused(ReasonUnavailable, site.ID)
	}
	country, err := s.c.Geo.Country(ip)
	if err != nil {
		// the event is still worth keeping without a country; the failure is
		// logged and counted, never hidden
		s.warnings.WithLabelValues("geo").Inc()
		s.c.Log.Error("country lookup failed, event queued without country", "error", err.Error())
		country = ""
	}
	quarantine, err := s.c.Cluster.Observe(site.ID, prefix, vt)
	if err != nil {
		// the band is still right for this node; peers miss one token
		s.warnings.WithLabelValues("ipcluster").Inc()
		s.c.Log.Warn("ip-cluster gossip failed", "error", err.Error())
	}
	if s.c.Volume.Observe(site.ID, vt) {
		quarantine = true
	}

	// the hostname umami reports is the page's, which the Origin check above
	// established; the body's h is only the tracker's claim
	if host, ok := OriginHost(r.Header.Get("Origin")); ok {
		wire.Hostname = host
	}
	SanitizeWire(wire, site.CollectUTM)
	id, err := events.NewEventID()
	if err != nil {
		s.c.Log.Error("event id generation failed", "error", err.Error())
		return refused(ReasonUnavailable, site.ID)
	}
	ev := &events.WebEvent{
		ID:         id,
		SiteID:     site.ID,
		WebsiteID:  site.ID,
		Band:       events.BandPrimary,
		VT:         vt,
		UA:         sanCapUnits(agent, events.MaxUserAgent),
		Country:    country,
		Type:       wire.Type,
		Hostname:   wire.Hostname,
		URL:        wire.URL,
		Referrer:   wire.Referrer,
		Title:      wire.Title,
		Screen:     wire.Screen,
		Language:   wire.Language,
		Name:       wire.Name,
		Data:       wire.Data,
		AcceptedAt: s.c.Now().UnixMilli(),
	}
	outcome := StatAccepted
	if quarantine {
		ev.Band = events.BandQuarantine
		ev.WebsiteID = site.QuarantineWebsiteID
		outcome = StatQuarantined
	}
	if err := s.c.Publisher.Publish(ctx, ev); err != nil {
		if errors.Is(err, ErrInvalidEvent) {
			// a user agent with control characters ends here; the error names
			// the field, never its value
			s.c.Log.Warn("event refused by validation", "error", err.Error())
			return refused(ReasonMalformed, site.ID)
		}
		s.c.Log.Error("event not queued", "error", err.Error())
		return refused(ReasonUnavailable, site.ID)
	}
	return verdict{status: http.StatusAccepted, outcome: outcome, site: site.ID}
}
