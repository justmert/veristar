// Package httpx holds the http conventions every control plane route shares
// (docs/reference/control-plane-api.md sections 1 and 2): the json error
// envelope, json body decoding, request ids, the cross-site request forgery
// checks, pagination cursors, path ids and time formatting.
//
// handlers are written as HandlerFunc, which returns an error. an *Error
// becomes its documented status and code; any other error becomes 500
// internal and is logged with the request id, never shown to the client.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// error codes of contract section 2.4. clients branch on these values.
const (
	CodeInvalidBody          = "invalid_body"
	CodeInvalidQuery         = "invalid_query"
	CodeInvalidCursor        = "invalid_cursor"
	CodeUnauthenticated      = "unauthenticated"
	CodeCSRFHeaderMissing    = "csrf_header_missing"
	CodeOriginMismatch       = "origin_mismatch"
	CodeOwnerRequired        = "owner_required"
	CodeTermsNotAccepted     = "terms_not_accepted"
	CodeOperatorRequired     = "operator_required"
	CodeNotFound             = "not_found"
	CodeMethodNotAllowed     = "method_not_allowed"
	CodeSlugTaken            = "slug_taken"
	CodeAlreadyMember        = "already_member"
	CodeInviteExists         = "invite_exists"
	CodeLastOwner            = "last_owner"
	CodeSiteRemoved          = "site_removed"
	CodeTermsVersionMismatch = "terms_version_mismatch"
	CodeTrackerRevoked       = "tracker_version_revoked"
	CodeBodyTooLarge         = "body_too_large"
	CodeUnsupportedMediaType = "unsupported_media_type"
	CodeValidationFailed     = "validation_failed"
	CodeRateLimited          = "rate_limited"
	CodeInternal             = "internal"
	CodeUnavailable          = "unavailable"
	CodeProvisioningPending  = "provisioning_pending"
	CodeTenantSuspended      = "tenant_suspended"
	CodeInvalidTransition    = "invalid_transition"
	// otlp token create while the tenant is pending or rejected
	CodeTenantNotActive = "tenant_not_active"
	// an erasure request while another one runs for the same site
	CodeErasureInProgress = "erasure_in_progress"
	// operator alerts: no alertmanager member answered, or none is
	// configured (contract 11.2)
	CodeAlertSourceUnreachable   = "alert_source_unreachable"
	CodeAlertSourceNotConfigured = "alert_source_not_configured"
	// an account deletion while the account is the only owner of a project
	// that is not removed (contract 4.1.6)
	CodeSoleOwner = "sole_owner"
	// wallet sign-in and linking (contract 4.1.7, 4.1.8): a signed challenge
	// that does not verify, that expired, or that was used before
	CodeChallengeInvalid = "challenge_invalid"
	CodeChallengeExpired = "challenge_expired"
	CodeChallengeUsed    = "challenge_used"
	// linking an identity that names another account, linking a second
	// identity of a kind the account already has, or unlinking the only one
	CodeIdentityInUse    = "identity_in_use"
	CodeIdentityLinked   = "identity_already_linked"
	CodeLastSignInMethod = "last_sign_in_method"
)

// defaultUnavailableRetryIn is the Retry-After of a 503 when the caller does
// not know better: long enough not to hammer a recovering dependency.
const defaultUnavailableRetryIn = 5 * time.Second

// field codes of contract section 2.5, used as values of Error.Fields.
const (
	FieldRequired            = "required"
	FieldTooLong             = "too_long"
	FieldTooShort            = "too_short"
	FieldTooMany             = "too_many"
	FieldInvalidFormat       = "invalid_format"
	FieldNotHTTPSURL         = "not_https_url"
	FieldNotBareOrigin       = "not_bare_origin"
	FieldOriginOutsideDomain = "origin_outside_domain"
	FieldInvalidDomain       = "invalid_domain"
	FieldInvalidStrkey       = "invalid_strkey"
	FieldInvalidGitHubLogin  = "invalid_github_login"
	// a well-formed login that names no github user account (4.2.5)
	FieldUnknownGitHubLogin    = "unknown_github_login"
	FieldInvalidRole           = "invalid_role"
	FieldDuplicate             = "duplicate"
	FieldUnknownTrackerVersion = "unknown_tracker_version"
	// a value outside an enumerated set, such as the enrollment mode
	FieldInvalidValue = "invalid_value"
)

// Error is an api error with a documented status and code. Message is for
// people; Fields is set only for 422.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
	// RetryAfter, when positive, is sent as the Retry-After header (429, 503).
	RetryAfter time.Duration
	// Tenants is set only for sole_owner: the projects that block the
	// deletion, so the person knows where to hand over ownership
	Tenants []TenantRef
}

// TenantRef names a tenant in an error envelope.
type TenantRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message)
}

// E builds an Error with any documented status and code.
func E(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// BadBody is 400 invalid_body.
func BadBody(message string) *Error { return E(http.StatusBadRequest, CodeInvalidBody, message) }

// BadQuery is 400 invalid_query.
func BadQuery(message string) *Error { return E(http.StatusBadRequest, CodeInvalidQuery, message) }

// BadCursor is 400 invalid_cursor.
func BadCursor() *Error {
	return E(http.StatusBadRequest, CodeInvalidCursor, "cursor is not valid for this route")
}

// Unauthenticated is 401 unauthenticated.
func Unauthenticated() *Error {
	return E(http.StatusUnauthorized, CodeUnauthenticated, "sign in required")
}

// Forbidden is a 403 with one of the 403 codes (owner_required, terms_not_accepted, ...).
func Forbidden(code, message string) *Error { return E(http.StatusForbidden, code, message) }

// NotFound is 404 not_found. it is also the answer for malformed ids and for
// tenants the caller is not a member of (contract 1.3).
func NotFound() *Error { return E(http.StatusNotFound, CodeNotFound, "not found") }

// Conflict is a 409 with one of the 409 codes.
func Conflict(code, message string) *Error { return E(http.StatusConflict, code, message) }

// Validation is 422 validation_failed with per-field codes. message names
// the first problem for people; fields maps json paths to field codes.
func Validation(message string, fields map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: CodeValidationFailed, Message: message, Fields: fields}
}

// RateLimited is 429 rate_limited with Retry-After.
func RateLimited(retryAfter time.Duration) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: CodeRateLimited, Message: "too many requests", RetryAfter: retryAfter}
}

// Unavailable is 503 unavailable with Retry-After: a dependency (database,
// nats, umami) is unreachable.
func Unavailable(message string) *Error {
	return &Error{Status: http.StatusServiceUnavailable, Code: CodeUnavailable, Message: message, RetryAfter: defaultUnavailableRetryIn}
}

// AsError returns the *Error inside err, if any.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// pgLockNotAvailable is postgresql's lock_not_available, raised when a
// statement waited longer than lock_timeout.
const pgLockNotAvailable = "55P03"

// envelope is the body of every 4xx and 5xx answer (contract 2.2).
type envelope struct {
	Error envelopeError `json:"error"`
}

type envelopeError struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	Tenants   []TenantRef       `json:"tenants,omitempty"`
	RequestID string            `json:"request_id"`
}

// WriteError answers with the envelope for err. errors that are not *Error
// are logged with the request id and answered 500 internal, so internal
// detail never reaches the client.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := AsError(err)
	var pe *pgconn.PgError
	if !ok && errors.As(err, &pe) && pe.Code == pgLockNotAvailable {
		// a tenant request that waited longer than its lock timeout for a
		// row another transaction holds (the reconciler, an operator
		// action): retrying shortly works, so it is 503, not 500
		e, ok = Unavailable("the project is busy with another change; try again shortly"), true
	}
	if !ok {
		Logger(r.Context()).Error("request failed", "path", r.URL.Path, "method", r.Method, "error", err.Error())
		e = E(http.StatusInternalServerError, CodeInternal, "internal error")
	} else if e.Status >= 500 {
		Logger(r.Context()).Warn("request answered with server error", "path", r.URL.Path, "method", r.Method,
			"status", e.Status, "code", e.Code, "message", e.Message)
	}
	if e.RetryAfter > 0 {
		secs := int((e.RetryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
	}
	var fields map[string]string
	if e.Status == http.StatusUnprocessableEntity {
		fields = e.Fields
	}
	var tenants []TenantRef
	if e.Code == CodeSoleOwner {
		tenants = e.Tenants
	}
	WriteJSON(w, e.Status, envelope{Error: envelopeError{
		Code:      e.Code,
		Message:   e.Message,
		Fields:    fields,
		Tenants:   tenants,
		RequestID: RequestID(r.Context()),
	}})
}
