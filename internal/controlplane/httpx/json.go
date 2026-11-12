package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// MaxBody is the largest accepted request body (contract section 1).
const MaxBody = 64 << 10

// WriteJSON answers with status and v encoded as json. an encoding failure
// is a programming error; it is logged and the client gets 500.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// the console renders these values as text, html escaping only mangles
	// urls and snippets
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"error":{"code":"internal","message":"internal error","request_id":""}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// HandlerFunc is a route handler that returns its error instead of writing
// it, so every failure goes through WriteError.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP calls the handler and writes a returned error.
func (h HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}

// isJSON reports whether a content type is application/json, with an
// optional utf-8 charset.
func isJSON(contentType string) bool {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil || mt != "application/json" {
		return false
	}
	if cs, ok := params["charset"]; ok && !strings.EqualFold(cs, "utf-8") {
		return false
	}
	return true
}

// hasBody reports whether the request carries a body at all.
func hasBody(r *http.Request) bool {
	return r.ContentLength > 0 || (r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody) ||
		len(r.TransferEncoding) > 0
}

// DecodeJSON reads the request body into dst. it refuses a missing body or
// a wrong content type (415), a body over MaxBody (413), anything that is
// not one json value of dst's shape, and unknown fields (400 invalid_body),
// so a typo in a field name never passes silently.
func DecodeJSON(r *http.Request, dst any) error {
	if !isJSON(r.Header.Get("Content-Type")) {
		return E(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "content type must be application/json")
	}
	body := http.MaxBytesReader(nil, r.Body, MaxBody)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	// exactly one value: trailing data means the client sent something else
	// than it thinks
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return tooLargeError()
		}
		return BadBody("body must hold exactly one json value")
	}
	return nil
}

func tooLargeError() *Error {
	return E(http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "body is larger than 64 KiB")
}

func decodeError(err error) *Error {
	var tooLarge *http.MaxBytesError
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooLarge):
		return tooLargeError()
	case errors.Is(err, io.EOF):
		return BadBody("body is empty")
	case errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &syntax):
		return BadBody("body is not valid json")
	case errors.As(err, &typ):
		if typ.Field != "" {
			return BadBody("field " + typ.Field + " has the wrong json type")
		}
		return BadBody("body has the wrong json type")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json has no typed error for unknown fields
		return BadBody(strings.TrimPrefix(err.Error(), "json: "))
	default:
		return BadBody("body is not valid json")
	}
}
