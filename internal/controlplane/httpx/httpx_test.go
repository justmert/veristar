package httpx_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

type envelope struct {
	Error struct {
		Code      string            `json:"code"`
		Message   string            `json:"message"`
		Fields    map[string]string `json:"fields"`
		RequestID string            `json:"request_id"`
	} `json:"error"`
}

const origin = "https://app.example.org"

// stack is the middleware order of controlplane.NewHandler without sessions.
func stack(h http.Handler) http.Handler {
	log := slog.New(slog.DiscardHandler)
	return httpx.Base(log)(httpx.LimitBody(httpx.CSRF(origin)(h)))
}

type body struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

var echo = httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
	var b body
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, b)
	return nil
})

// result is the recorded status and headers; the body is decoded here.
type result struct {
	StatusCode int
	Header     http.Header
}

func do(t *testing.T, h http.Handler, method, path, ctype, payload string, hdr map[string]string) (result, envelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(payload))
	if payload == "" {
		req = httptest.NewRequest(method, path, nil)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := result{StatusCode: rec.Code, Header: rec.Header()}
	var env envelope
	if res.StatusCode >= 400 {
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&env))
		require.Equal(t, res.Header.Get("X-Request-Id"), env.Error.RequestID)
		require.NotEmpty(t, env.Error.RequestID)
		require.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))
	}
	require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
	return res, env
}

var console = map[string]string{"X-Requested-With": "sa-console", "Origin": origin}

func TestCSRF(t *testing.T) {
	h := stack(echo)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		_, env := do(t, h, m, "/x", "application/json", `{}`, map[string]string{"Origin": origin})
		require.Equal(t, "csrf_header_missing", env.Error.Code, m)
		_, env = do(t, h, m, "/x", "application/json", `{}`, map[string]string{"X-Requested-With": "SA-console", "Origin": origin})
		require.Equal(t, "csrf_header_missing", env.Error.Code, m)
		_, env = do(t, h, m, "/x", "application/json", `{}`, map[string]string{"X-Requested-With": "sa-console"})
		require.Equal(t, "origin_mismatch", env.Error.Code, m)
		res, env := do(t, h, m, "/x", "application/json", `{}`, map[string]string{"X-Requested-With": "sa-console", "Origin": "https://evil.example.org"})
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "origin_mismatch", env.Error.Code, m)
		// a sibling subdomain is same-site for SameSite=Lax but not the console
		_, env = do(t, h, m, "/x", "application/json", `{}`, map[string]string{"X-Requested-With": "sa-console", "Origin": "https://web.example.org"})
		require.Equal(t, "origin_mismatch", env.Error.Code, m)
		res, _ = do(t, h, m, "/x", "application/json", `{"name":"a"}`, console)
		require.Equal(t, http.StatusOK, res.StatusCode, m)
	}
	// safe methods need neither
	get := stack(httpx.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
		httpx.NoContent(w)
		return nil
	}))
	res, _ := do(t, get, http.MethodGet, "/x", "", "", nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestDecodeJSON(t *testing.T) {
	h := stack(echo)
	cases := []struct {
		name, ctype, body string
		status            int
		code              string
	}{
		{"ok", "application/json", `{"name":"a","n":1}`, 200, ""},
		{"charset", "application/json; charset=utf-8", `{"name":"a"}`, 200, ""},
		{"unknown field", "application/json", `{"name":"a","extra":"b"}`, 400, "invalid_body"},
		{"wrong type", "application/json", `{"n":"x"}`, 400, "invalid_body"},
		{"not json", "application/json", `{"name":`, 400, "invalid_body"},
		{"two values", "application/json", `{} {}`, 400, "invalid_body"},
		{"array", "application/json", `[]`, 400, "invalid_body"},
		{"form", "application/x-www-form-urlencoded", `name=a`, 415, "unsupported_media_type"},
		{"text", "text/plain", `{"name":"a"}`, 415, "unsupported_media_type"},
		{"latin1", "application/json; charset=latin1", `{}`, 415, "unsupported_media_type"},
		{"too large", "application/json", `{"name":"` + strings.Repeat("a", httpx.MaxBody) + `"}`, 413, "body_too_large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, env := do(t, h, http.MethodPost, "/x", c.ctype, c.body, console)
			require.Equal(t, c.status, res.StatusCode)
			require.Equal(t, c.code, env.Error.Code)
		})
	}
	// an empty body on a route that reads one
	res, env := do(t, h, http.MethodPost, "/x", "application/json", "", console)
	require.Equal(t, 400, res.StatusCode)
	require.Equal(t, "invalid_body", env.Error.Code)
}

func TestErrors(t *testing.T) {
	h := stack(httpx.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) error {
		switch r.URL.Path {
		case "/422":
			return httpx.Validation("origins[1] must be a bare https origin", map[string]string{"origins[1]": httpx.FieldNotBareOrigin})
		case "/429":
			return httpx.RateLimited(1500 * time.Millisecond)
		case "/409":
			// fields are sent only with 422
			e := httpx.Conflict(httpx.CodeSlugTaken, "slug taken")
			e.Fields = map[string]string{"slug": "duplicate"}
			return e
		case "/panic":
			panic("boom")
		default:
			return io.ErrUnexpectedEOF
		}
	}))
	res, env := do(t, h, http.MethodGet, "/422", "", "", nil)
	require.Equal(t, 422, res.StatusCode)
	require.Equal(t, "validation_failed", env.Error.Code)
	require.Equal(t, map[string]string{"origins[1]": "not_bare_origin"}, env.Error.Fields)

	res, env = do(t, h, http.MethodGet, "/429", "", "", nil)
	require.Equal(t, 429, res.StatusCode)
	require.Equal(t, "2", res.Header.Get("Retry-After"))
	require.Equal(t, "rate_limited", env.Error.Code)

	res, env = do(t, h, http.MethodGet, "/409", "", "", nil)
	require.Equal(t, 409, res.StatusCode)
	require.Nil(t, env.Error.Fields)

	// internal detail never reaches the client
	res, env = do(t, h, http.MethodGet, "/other", "", "", nil)
	require.Equal(t, 500, res.StatusCode)
	require.Equal(t, "internal", env.Error.Code)
	require.NotContains(t, env.Error.Message, "EOF")

	res, env = do(t, h, http.MethodGet, "/panic", "", "", nil)
	require.Equal(t, 500, res.StatusCode)
	require.Equal(t, "internal", env.Error.Code)
}

func TestParsePage(t *testing.T) {
	parse := func(q string) (httpx.Page, error) {
		return httpx.ParsePage(httptest.NewRequest(http.MethodGet, "/x?"+q, nil), "tenants")
	}
	p, err := parse("")
	require.NoError(t, err)
	require.Equal(t, httpx.Page{Limit: 50}, p)

	for _, bad := range []string{"limit=0", "limit=201", "limit=x", "limit=-1"} {
		_, err := parse(bad)
		e, ok := httpx.AsError(err)
		require.True(t, ok, bad)
		require.Equal(t, "invalid_query", e.Code, bad)
	}

	at := time.Date(2026, 11, 12, 9, 0, 0, 123456789, time.UTC)
	id := uuid.Must(uuid.NewV7())
	cur := httpx.NextCursor("tenants", httpx.Cursor{At: at, ID: id})
	p, err = parse("limit=200&cursor=" + *cur)
	require.NoError(t, err)
	require.Equal(t, 200, p.Limit)
	require.Equal(t, id, p.After.ID)
	require.True(t, at.Equal(p.After.At))

	other := httpx.NextCursor("sites", httpx.Cursor{At: at, ID: id})
	for _, bad := range []string{*other, "notbase64!", "e30", *cur + "x"} {
		_, err := parse("cursor=" + bad)
		e, ok := httpx.AsError(err)
		require.True(t, ok, bad)
		require.Equal(t, "invalid_cursor", e.Code, bad)
	}
}

func TestParseID(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	got, err := httpx.ParseID(id.String())
	require.NoError(t, err)
	require.Equal(t, id, got)
	for _, bad := range []string{"", "x", strings.ToUpper(id.String()), "{" + id.String() + "}", uuid.Nil.String(),
		strings.ReplaceAll(id.String(), "-", "")} {
		_, err := httpx.ParseID(bad)
		e, ok := httpx.AsError(err)
		require.True(t, ok, bad)
		require.Equal(t, 404, e.Status, bad)
		require.Equal(t, "not_found", e.Code, bad)
	}
}

func TestTime(t *testing.T) {
	ist := time.FixedZone("ist", 3*3600)
	require.Equal(t, "2026-11-12T09:00:00Z", httpx.Time(time.Date(2026, 11, 12, 12, 0, 0, 0, ist)))
	require.Equal(t, "2026-11-12T09:00:00.123Z", httpx.Time(time.Date(2026, 11, 12, 9, 0, 0, 123987000, time.UTC)))
	b, err := json.Marshal(struct {
		A httpx.Timestamp  `json:"a"`
		B *httpx.Timestamp `json:"b"`
	}{A: httpx.Timestamp(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)), B: httpx.TimestampPtr(nil)})
	require.NoError(t, err)
	require.JSONEq(t, `{"a":"2026-11-12T09:00:00Z","b":null}`, string(b))
}
