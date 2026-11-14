package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolError is a refusal a model can act on: a stable code from contract
// 12.5 and a message that says what to change. it never carries a stack
// trace or internal detail.
type toolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *toolError) Error() string { return e.Code + ": " + e.Message }

func invalid(format string, args ...any) error {
	return &toolError{Code: "invalid_input", Message: fmt.Sprintf(format, args...)}
}

// tool error codes; outcome labels of the metrics and the audit rows
const (
	outcomeOK          = "ok"
	outcomeInvalid     = "invalid_input"
	outcomeNotFound    = "not_found"
	outcomeForbidden   = "forbidden"
	outcomeRateLimited = "rate_limited"
	outcomeUnavailable = "unavailable"
	outcomeTooLarge    = "result_too_large"
	outcomeTimeout     = "timeout"
	outcomeError       = "internal"
)

// toolFunc is the body of a tool: it gets the principal and the decoded
// input, and returns the value the client receives as structured content.
type toolFunc[In any] func(ctx context.Context, p Principal, in In) (any, error)

// schemaPatch narrows the inferred input schema: enums, ranges and
// formats that go struct tags cannot express.
type schemaPatch func(props map[string]*jsonschema.Schema)

// addTool registers a read-only tool. the input schema is inferred from In
// (unknown properties refused) and then patched; the sdk validates every
// call against it before fn runs. the wrapper applies the concurrency cap,
// the call timeout, the result size cap, the audit row and the metrics to
// every tool alike.
func addTool[In any](s *Server, srv *mcp.Server, t *mcp.Tool, patch schemaPatch, fn toolFunc[In]) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: schema of %s: %v", t.Name, err))
	}
	if patch != nil {
		if schema.Properties == nil {
			schema.Properties = map[string]*jsonschema.Schema{}
		}
		patch(schema.Properties)
	}
	t.InputSchema = schema
	closed := false
	t.Annotations = &mcp.ToolAnnotations{Title: t.Title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
	name := t.Name
	mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		p, ok := principalOf(req)
		if !ok {
			// every request passes serveMCP, so this is a wiring mistake
			return nil, nil, errors.New("mcpserver: tool call without a principal")
		}
		start := s.now()
		out, err := s.runTool(ctx, p, name, func(ctx context.Context) (any, error) { return fn(ctx, p, in) })
		outcome := outcomeOK
		var te *toolError
		if err != nil && !errors.As(err, &te) {
			s.log.Error("mcp tool failed", "tool", name, "credential_id", p.CredentialID.String(), "error", err.Error())
			te = &toolError{Code: outcomeError, Message: "the tool failed on the server; try again later"}
		}
		if te != nil {
			outcome = te.Code
		}
		dur := s.now().Sub(start)
		s.metrics.calls.WithLabelValues(name, outcome).Inc()
		s.metrics.durations.WithLabelValues(name).Observe(dur.Seconds())
		// the audit row is written before the answer, without the result;
		// a call that cannot be audited is not answered
		if aerr := s.auditCall(context.WithoutCancel(ctx), p, name, outcome, dur); aerr != nil {
			s.log.Error("mcp tool call audit failed", "tool", name, "error", aerr.Error())
			return errorResult(&toolError{Code: outcomeError, Message: "the call could not be recorded; try again later"}), nil, nil
		}
		if te != nil {
			return errorResult(te), nil, nil
		}
		return nil, out, nil
	})
}

// runTool applies the per-credential concurrency cap, the timeout and the
// result size cap around one call.
func (s *Server) runTool(ctx context.Context, p Principal, name string, fn func(context.Context) (any, error)) (any, error) {
	if !s.inflight.acquire(p.CredentialID) {
		return nil, &toolError{Code: outcomeRateLimited,
			Message: fmt.Sprintf("this credential already has %d tool calls running; wait for one to finish", MaxConcurrentCalls)}
	}
	defer s.inflight.release(p.CredentialID)
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	out, err := fn(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, &toolError{Code: outcomeTimeout,
				Message: fmt.Sprintf("the call took longer than %s; narrow the time range or lower the limit", CallTimeout)}
		}
		return nil, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("mcpserver: %s result: %w", name, err)
	}
	if len(b) > MaxResultBytes {
		return nil, &toolError{Code: outcomeTooLarge,
			Message: fmt.Sprintf("the result is larger than %d KiB; narrow the time range, add filters or lower the limit", MaxResultBytes>>10)}
	}
	return json.RawMessage(b), nil
}

func errorResult(te *toolError) *mcp.CallToolResult {
	b, _ := json.Marshal(map[string]any{"error": te})
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

// auditCall writes one cp.audit_log row per tool call: tool, principal,
// tenant, duration and outcome. never the input values or the result.
func (s *Server) auditCall(ctx context.Context, p Principal, tool, outcome string, dur time.Duration) error {
	target := ""
	var tenant any
	if p.Scope == ScopeTenant {
		target = "tenant:" + p.TenantID.String()
		tenant = p.TenantID.String()
	}
	detail := map[string]any{
		"tool": tool, "outcome": outcome, "duration_ms": dur.Milliseconds(),
		"kind": p.Kind, "scope": p.Scope, "user_id": p.User.ID.String(), "tenant_id": tenant,
		"credential_id": p.CredentialID.String(),
	}
	return s.db.WithSystem(ctx, func(tx pgx.Tx) error {
		return insertAudit(ctx, tx, "mcp_credential:"+p.CredentialID.String(), "mcp.tool_call", target, detail)
	})
}

func insertAudit(ctx context.Context, tx pgx.Tx, actor, action, target string, detail map[string]any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into cp.audit_log (actor, action, target, detail) values ($1, $2, $3, $4)`,
		actor, action, target, b)
	return err
}

// maxReadBody bounds one in-process read answer; the result cap applies
// after the tool shaped it.
const maxReadBody = 4 << 20

// recorder is a minimal http.ResponseWriter for the in-process reads.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	over   bool
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body.Len()+len(b) > maxReadBody {
		r.over = true
		return len(b), nil
	}
	return r.body.Write(b)
}

// read calls one of the console's read routes in process as the principal's
// user and decodes the answer. the route runs exactly as for the console:
// membership checked under cp_system, the read under cp_tenant with
// row-level security, or the operator guard. a refusal comes back as a
// toolError with the route's own code and message.
func (s *Server) read(ctx context.Context, p Principal, path string, q url.Values, out any) error {
	if p.Scope != ScopeOperator && strings.HasPrefix(path, "/operator") {
		// tenant tools never name the operator area; a bug that did would
		// otherwise pass the guard for a tenant credential of an operator
		return fmt.Errorf("mcpserver: tenant principal asked for %s", path)
	}
	u := &url.URL{Path: path, RawQuery: q.Encode()}
	// a fresh chi routing context: the one of the outer /mcp request would
	// make the read router route on that request's path
	rctx := context.WithValue(sessionUser(ctx, p), chi.RouteCtxKey, (*chi.Context)(nil))
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("mcpserver: read %s: %w", path, err)
	}
	rec := &recorder{header: http.Header{}}
	s.reads.ServeHTTP(rec, req)
	if rec.over {
		return &toolError{Code: outcomeTooLarge, Message: "the answer is too large; narrow the time range or lower the limit"}
	}
	if rec.status != http.StatusOK {
		return readError(rec)
	}
	if err := json.Unmarshal(rec.body.Bytes(), out); err != nil {
		return fmt.Errorf("mcpserver: decode %s: %w", path, err)
	}
	return nil
}

// readError turns the console's error envelope into a tool error.
func readError(rec *recorder) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &env); err != nil || env.Error.Code == "" {
		return fmt.Errorf("mcpserver: read answered %d without an error envelope", rec.status)
	}
	switch {
	case rec.status == http.StatusNotFound:
		return &toolError{Code: outcomeNotFound, Message: "not found: the id is unknown or not part of this project"}
	case rec.status == http.StatusForbidden:
		return &toolError{Code: outcomeForbidden, Message: env.Error.Message}
	case rec.status == http.StatusTooManyRequests:
		return &toolError{Code: outcomeRateLimited, Message: env.Error.Message}
	case rec.status == http.StatusServiceUnavailable:
		return &toolError{Code: outcomeUnavailable, Message: env.Error.Message}
	case rec.status >= 400 && rec.status < 500:
		// invalid_query, invalid_cursor, validation_failed: the input
		return &toolError{Code: outcomeInvalid, Message: env.Error.Message}
	}
	return fmt.Errorf("mcpserver: read answered %d %s", rec.status, env.Error.Code)
}
