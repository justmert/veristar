// Package schema names everything that may leave the collector (plan
// sections 4.5 and 4.11): the two metric names on API_METRICS, the resource
// and data point attributes, and the canonical histogram boundaries.
// tenantguard filters to these lists and the jetstream exporter checks them
// again before publishing, so one mistake in between cannot leak a field.
package schema

// metric names
const (
	// MetricDuration is the stable server duration metric (semconv v1.45.0),
	// unit seconds.
	MetricDuration = "http.server.request.duration"
	// MetricLegacyDuration is the pre-stable name, usually in milliseconds.
	// the transform processor scales and renames it to MetricDuration.
	MetricLegacyDuration = "http.server.duration"
	// MetricFromSpans is derived from SERVER spans by the signal_to_metrics
	// connector. tenants cannot send it themselves.
	MetricFromSpans = "scf.http.server.request.duration.from_spans"
)

// resource attributes
const (
	ResServiceName  = "service.name"
	ResTenantID     = "scf.tenant.id"
	ResTokenID      = "scf.token.id" //nolint:gosec // an attribute name, not a credential
	ResResourceHash = "scf.resource.hash"
	ResFlags        = "scf.flags"
)

// flag bits of ResFlags
const (
	// FlagSecretSeed: a secret seed was redacted from a path or route.
	FlagSecretSeed int64 = 1
)

// data point and span attributes (semconv stable names)
const (
	AttrMethod     = "http.request.method"
	AttrRoute      = "http.route"
	AttrStatusCode = "http.response.status_code"
	AttrErrorType  = "error.type"
	// AttrLossy marks a histogram that was converted from an exponential one
	// and is therefore approximate. set by the transform processor.
	AttrLossy = "scf.lossy"
)

// legacy names (semconv before v1.21 http stabilization) mapped by tenantguard
const (
	LegacyMethod     = "http.method"
	LegacyStatusCode = "http.status_code"
	LegacyTarget     = "http.target"
	URLPath          = "url.path"
)

// ResourceAllowed lists the resource attributes that may be published.
var ResourceAllowed = map[string]bool{
	ResServiceName: true, ResTenantID: true, ResTokenID: true, ResResourceHash: true, ResFlags: true,
}

// PointAllowed lists the data point attributes that may be published.
var PointAllowed = map[string]bool{
	AttrMethod: true, AttrRoute: true, AttrStatusCode: true, AttrErrorType: true, AttrLossy: true,
}

// PublishedMetrics lists the metric names that may be published.
var PublishedMetrics = map[string]bool{MetricDuration: true, MetricFromSpans: true}

// Methods is the semconv v1.45.0 set of known http methods. anything else
// is recorded as MethodOther.
var Methods = map[string]bool{
	"CONNECT": true, "DELETE": true, "GET": true, "HEAD": true, "OPTIONS": true,
	"PATCH": true, "POST": true, "PUT": true, "TRACE": true, "QUERY": true,
}

// MethodOther is the semconv value for an unknown method.
const MethodOther = "_OTHER"

// Bounds is the canonical explicit bucket layout in seconds (section 4.11).
var Bounds = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}
