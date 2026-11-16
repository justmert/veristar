package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// error codes of a failed read. they are stored as agg.chain_reads.last_error
// and cp.chain_ids.last_verify_error and shown to tenants and operators, so
// they name the cause and never carry upstream text.
const (
	CodeRateLimited    = "rate_limited"
	CodeUnavailable    = "source_unavailable"
	CodeBadResponse    = "bad_response"
	CodeNotFound       = "not_found"
	CodeBeforeHistory  = "before_source_history"
	CodeDayTooLarge    = "day_over_read_cap"
	CodeAccountMissing = "account_not_found"
)

// Error is a failed read with a stable code.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return "chain: " + e.Code + ": " + e.Detail }

// Code returns the code of err, or CodeUnavailable for an error that
// carries none (a cancelled context, a network failure).
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeUnavailable
}

func fail(code, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// limits of every request to a ledger api
const (
	requestTimeout = 30 * time.Second
	maxBody        = 16 << 20
	maxAttempts    = 6
	baseBackoff    = 2 * time.Second
	maxBackoff     = 2 * time.Minute
)

// Endpoint is one ledger api with its own request budget. sdf's horizon
// allows 3600 requests per hour per address and sends no rate limit
// headers, so the client paces itself below that and backs off on 429.
type Endpoint struct {
	base    *url.URL
	http    *http.Client
	limiter *rate.Limiter
	agent   string
	// sleep waits between attempts; tests replace it
	sleep func(ctx context.Context, d time.Duration) error
}

// NewEndpoint checks the url and builds an endpoint that sends at most
// perSecond requests per second.
func NewEndpoint(raw string, perSecond float64, agent string) (*Endpoint, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("chain: %q is not an absolute http or https url", raw)
	}
	if perSecond <= 0 {
		return nil, errors.New("chain: the request rate must be positive")
	}
	return &Endpoint{
		base:    u,
		http:    &http.Client{Timeout: requestTimeout},
		limiter: rate.NewLimiter(rate.Limit(perSecond), 1),
		agent:   agent,
		sleep:   sleepCtx,
	}, nil
}

// WithClient replaces the http client; tests point it at a test server.
func (e *Endpoint) WithClient(c *http.Client) *Endpoint {
	e.http = c
	return e
}

// WithSleep replaces the wait between attempts; tests make it instant.
func (e *Endpoint) WithSleep(fn func(ctx context.Context, d time.Duration) error) *Endpoint {
	e.sleep = fn
	return e
}

// URL is the base url, for logs and the live check record.
func (e *Endpoint) URL() string { return e.base.String() }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// response is a finished request: the status and the body.
type response struct {
	status int
	body   []byte
}

// do sends one request with pacing and retries. 429 and 5xx answers and
// network errors are retried with exponential backoff and jitter, waiting
// at least as long as a Retry-After header asks; after maxAttempts the
// read fails as rate_limited or source_unavailable. other answers are
// returned to the caller.
func (e *Endpoint) do(ctx context.Context, method, path string, q url.Values, body []byte) (response, error) {
	u := *e.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	if q != nil {
		u.RawQuery = q.Encode()
	}
	var last error
	limited := false
	for attempt := range maxAttempts {
		if attempt > 0 {
			if err := e.sleep(ctx, backoff(attempt, last)); err != nil {
				return response{}, err
			}
		}
		if err := e.limiter.Wait(ctx); err != nil {
			return response{}, err
		}
		r, err := e.once(ctx, method, u.String(), body)
		if err != nil {
			if ctx.Err() != nil {
				return response{}, ctx.Err()
			}
			last = err
			continue
		}
		if r.status == http.StatusTooManyRequests || r.status >= 500 {
			limited = r.status == http.StatusTooManyRequests
			last = &retryAfter{status: r.status, wait: r.retry}
			continue
		}
		return response{status: r.status, body: r.body}, nil
	}
	if limited {
		return response{}, fail(CodeRateLimited, "%s still answers 429 after %d attempts", e.base.Host, maxAttempts)
	}
	return response{}, fail(CodeUnavailable, "%s failed %d attempts: %v", e.base.Host, maxAttempts, last)
}

type attemptResult struct {
	status int
	body   []byte
	retry  time.Duration
}

func (e *Endpoint) once(ctx context.Context, method, target string, body []byte) (attemptResult, error) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return attemptResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.agent != "" {
		req.Header.Set("User-Agent", e.agent)
	}
	res, err := e.http.Do(req)
	if err != nil {
		return attemptResult{}, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return attemptResult{}, err
	}
	if len(b) > maxBody {
		return attemptResult{}, fail(CodeBadResponse, "%s answered more than %d bytes", e.base.Host, maxBody)
	}
	return attemptResult{status: res.StatusCode, body: b, retry: parseRetryAfter(res.Header.Get("Retry-After"))}, nil
}

// retryAfter is a 429 or 5xx answer, kept to size the next wait.
type retryAfter struct {
	status int
	wait   time.Duration
}

func (r *retryAfter) Error() string { return "http " + strconv.Itoa(r.status) }

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

// backoff doubles from baseBackoff with up to half again as jitter, capped
// at maxBackoff, and never shorter than the server's Retry-After.
func backoff(attempt int, last error) time.Duration {
	d := baseBackoff << (attempt - 1)
	if d > maxBackoff || d <= 0 {
		d = maxBackoff
	}
	d += time.Duration(rand.Int64N(int64(d/2) + 1))
	var ra *retryAfter
	if errors.As(last, &ra) && ra.wait > d {
		d = min(ra.wait, 10*time.Minute)
	}
	return d
}

func decode(r response, into any, what string) error {
	if err := json.Unmarshal(r.body, into); err != nil {
		return fail(CodeBadResponse, "%s: %v", what, err)
	}
	return nil
}
