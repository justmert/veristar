// Package trafficclass decides whether an api request is application
// traffic or infrastructure traffic: a cors preflight, a health or probe
// route, or a request from a known health checker. infrastructure requests
// are stored apart (api.metrics_* traffic_class) and kept out of the
// headline volume, error rates and latency percentiles, so a service that
// answers its own load balancer every few seconds does not look busier or
// faster than it is.
//
// the rule set is hard-coded, ordered and versioned like routenorm's:
// tenants cannot configure it, so every project is classified the same way.
//
// rule set version 1, applied in this order to the method, the route after
// routenorm.Normalize and the user agent:
//
//  1. method OPTIONS is a cors preflight (ClassPreflight)
//  2. a route made of up to three known prefix segments followed by a
//     health name, optionally followed by one known check segment, is a
//     health or probe route (ClassHealth). names, prefixes and check
//     segments are compared case-insensitively; see healthNames,
//     prefixSegments and checkSegments
//  3. a user agent that starts with a known health checker's product
//     token, or a synthetic type of "test", is a probe (ClassProbe)
//  4. everything else is application traffic (ClassApp)
package trafficclass

import (
	"strings"
)

// Version identifies the rule set. it changes whenever a rule changes, so a
// stored class can be traced to the rules that produced it.
const Version = 1

// Class is the traffic class of a request.
type Class string

// the classes, as stored in api.metrics_*.traffic_class and published in
// the scf.traffic.class point attribute
const (
	ClassApp       Class = "app"
	ClassPreflight Class = "preflight"
	ClassHealth    Class = "health"
	ClassProbe     Class = "probe"
)

// Infrastructure reports whether c is kept out of the headline.
func (c Class) Infrastructure() bool {
	return c == ClassPreflight || c == ClassHealth || c == ClassProbe
}

// Valid reports whether s names a class.
func Valid(s string) bool {
	switch Class(s) {
	case ClassApp, ClassPreflight, ClassHealth, ClassProbe:
		return true
	}
	return false
}

// Request is what the classifier looks at. Route is the normalized route
// (routenorm.Normalize), empty when the request carried none. UserAgent is
// user_agent.original and Synthetic is user_agent.synthetic.type; either
// is empty when the instrumentation did not record it.
type Request struct {
	Method    string
	Route     string
	UserAgent string
	Synthetic string
}

// Classify applies rules 1 to 4.
func Classify(r Request) Class {
	if strings.EqualFold(r.Method, "OPTIONS") {
		return ClassPreflight
	}
	if HealthRoute(r.Route) {
		return ClassHealth
	}
	if ProbeAgent(r.UserAgent) || strings.EqualFold(r.Synthetic, "test") {
		return ClassProbe
	}
	return ClassApp
}

// maxPrefixSegments bounds the prefix before a health name, so a long
// resource path that happens to end in /status is not taken for a probe.
const maxPrefixSegments = 3

// HealthRoute reports whether route is a health or probe route (rule 2).
func HealthRoute(route string) bool {
	segs := strings.Split(strings.Trim(route, "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return false
	}
	for i, s := range segs {
		if i > maxPrefixSegments {
			return false
		}
		s = strings.ToLower(s)
		if healthNames[s] || (i > 0 && scopedNames[strings.ToLower(segs[i-1])][s]) {
			rest := segs[i+1:]
			switch len(rest) {
			case 0:
				return true
			case 1:
				return checkSegments[strings.ToLower(rest[0])]
			}
			return false
		}
		if !prefix(s) {
			return false
		}
	}
	return false
}

// prefix reports whether s may stand before a health name: a known prefix
// or an api version such as v1 or v2beta.
func prefix(s string) bool {
	if prefixSegments[s] {
		return true
	}
	if len(s) < 2 || s[0] != 'v' || s[1] < '0' || s[1] > '9' {
		return false
	}
	i := 1
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	switch s[i:] {
	case "", "alpha", "beta":
		return true
	}
	return false
}

// healthNames are the last meaningful segment of a health, readiness,
// liveness or scrape endpoint in the conventions listed in
// docs/reference/collector.md (kubernetes, spring boot actuator, microprofile
// and quarkus health, asp.net core, google app engine, prometheus, and
// the common /ping, /status and /heartbeat).
var healthNames = map[string]bool{
	"health": true, "healthz": true, "_health": true, "__health": true,
	"healthcheck": true, "health-check": true, "health_check": true,
	"livez": true, "readyz": true, "startupz": true,
	"liveness_check": true, "readiness_check": true,
	"ping": true, "_ping": true,
	"status": true, "_status": true,
	"heartbeat": true,
	"metrics":   true,
}

// scopedNames are health names only right after one root: prometheus'
// /-/healthy and /-/ready, and spring boot's scrape endpoint
// /actuator/prometheus. on their own these words are common resource names.
var scopedNames = map[string]map[string]bool{
	"-":        {"healthy": true, "ready": true},
	"actuator": {"prometheus": true},
}

// prefixSegments may stand before a health name: api roots, framework
// management roots (/actuator, /q, /_ah, /-) and internal roots.
var prefixSegments = map[string]bool{
	"api": true, "apis": true, "rest": true,
	"actuator": true, "management": true, "manage": true, "admin": true,
	"q": true, "_ah": true, "-": true, "_": true,
	"internal": true, "_internal": true, "system": true, "sys": true, "ops": true,
	"monitoring": true, "monitor": true, ".well-known": true,
}

// checkSegments may follow a health name: the probe kinds of microprofile,
// quarkus and spring boot (/health/live, /q/health/ready,
// /actuator/health/liveness) and kubernetes' /livez/ping.
var checkSegments = map[string]bool{
	"live": true, "liveness": true, "ready": true, "readiness": true,
	"started": true, "startup": true, "ping": true, "check": true, "all": true,
}

// probePrefixes start the user agent of a health checker or uptime
// monitor whose string is published by its vendor or set in its source.
// each is matched case-insensitively at the start, so "kube-probe/1.31"
// matches while a browser that merely mentions the word does not.
var probePrefixes = []string{
	"elb-healthchecker/", // aws application and network load balancers
	"kube-probe/",        // kubelet liveness, readiness and startup probes
	"googlehc",           // google cloud load balancer health checks
	"envoy/hc",           // envoy active health checks
	"consul health check",
	"nomad/", // nomad's native service checks
	"googlestackdrivermonitoring-uptimechecks",
	"edge health probe", // azure front door
	"uptime-kuma/",
	"datadog/synthetics", // datadog api tests
	"better stack better uptime bot",
	"better uptime bot",
}

// probeTokens appear inside a browser-like user agent: the product token
// of the monitor stands between "(compatible; " and the next ";", or at
// the end. they are matched as whole tokens.
var probeTokens = []string{
	"uptimerobot/",
	"cloudflare-healthchecks/",
	"cloudflare-traffic-manager/",
	"(statuscake)",
	"datadogsynthetics",
}

// ProbeAgent reports whether ua is a known health checker (rule 3). the
// exact value AlwaysOn is azure app service's warm-up request.
func ProbeAgent(ua string) bool {
	ua = strings.ToLower(strings.TrimSpace(ua))
	if ua == "" {
		return false
	}
	if ua == "alwayson" {
		return true
	}
	for _, p := range probePrefixes {
		if strings.HasPrefix(ua, p) {
			return true
		}
	}
	for _, t := range probeTokens {
		if i := strings.Index(ua, t); i >= 0 && tokenEdge(ua, i) {
			return true
		}
	}
	return false
}

// tokenEdge reports whether the match at i starts a token: the start of
// the string, or after a space, ";", "(" or "+".
func tokenEdge(ua string, i int) bool {
	if i == 0 {
		return true
	}
	switch ua[i-1] {
	case ' ', ';', '(', '+':
		return true
	}
	return false
}
