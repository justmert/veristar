package httpx

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// page size bounds of contract 2.3
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// List is the list envelope of contract 2.3. NextCursor is null on the last page.
type List[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// Cursor is a keyset position over (created_at, id), the order of every
// this stage list route.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// Page is the parsed limit and cursor of a list request. After is nil on
// the first page.
type Page struct {
	Limit int
	After *Cursor
}

// cursorWire is the json inside a cursor. R names the route, so a cursor
// from another route is refused instead of silently paging the wrong list.
type cursorWire struct {
	R string    `json:"r"`
	T time.Time `json:"t"`
	I uuid.UUID `json:"i"`
}

// ParsePage reads limit and cursor from the query. route is a fixed name
// per list route (for example "tenants" or "sites"); a cursor made for a
// different route is 400 invalid_cursor.
func ParsePage(r *http.Request, route string) (Page, error) {
	q := r.URL.Query()
	p := Page{Limit: DefaultLimit}
	if v, ok := q["limit"]; ok {
		n, err := strconv.Atoi(v[0])
		if len(v) != 1 || err != nil || n < 1 || n > MaxLimit {
			return Page{}, BadQuery("limit must be an integer between 1 and 200")
		}
		p.Limit = n
	}
	if v, ok := q["cursor"]; ok {
		if len(v) != 1 {
			return Page{}, BadCursor()
		}
		raw, err := base64.RawURLEncoding.DecodeString(v[0])
		if err != nil {
			return Page{}, BadCursor()
		}
		var c cursorWire
		if err := json.Unmarshal(raw, &c); err != nil || c.R != route || c.T.IsZero() || c.I == uuid.Nil {
			return Page{}, BadCursor()
		}
		p.After = &Cursor{At: c.T, ID: c.I}
	}
	return p, nil
}

// NextCursor encodes the position after the last item of a full page.
func NextCursor(route string, last Cursor) *string {
	b, err := json.Marshal(cursorWire{R: route, T: last.At.UTC(), I: last.ID})
	if err != nil {
		// a time and a uuid always encode
		panic(err)
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

// canonical lowercase uuid, the only id form the api accepts in paths
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ParseID parses an id from a path. a malformed id is 404 not_found, the
// same answer as an unknown id (contract section 1).
func ParseID(s string) (uuid.UUID, error) {
	if !uuidRe.MatchString(s) {
		return uuid.Nil, NotFound()
	}
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, NotFound()
	}
	return id, nil
}

// timeLayout is rfc 3339 in utc with at most millisecond digits.
const timeLayout = "2006-01-02T15:04:05.999Z"

// Time formats t the way every api time is written (contract section 1).
func Time(t time.Time) string {
	return t.UTC().Truncate(time.Millisecond).Format(timeLayout)
}

// Timestamp is a time that encodes as Time(t) in json. a nil *Timestamp
// encodes as null, which is how nullable columns are returned.
type Timestamp time.Time

// MarshalJSON writes the contract time format.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(Time(time.Time(t)))
}

// UnmarshalJSON reads the contract time format back, so api types can be
// decoded by tests and go clients.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	*t = Timestamp(v.UTC())
	return nil
}

// TimestampPtr converts a nullable column value.
func TimestampPtr(t *time.Time) *Timestamp {
	if t == nil {
		return nil
	}
	ts := Timestamp(*t)
	return &ts
}

// Created answers 201 with v and Location set to location (contract 2.1).
func Created(w http.ResponseWriter, location string, v any) {
	w.Header().Set("Location", location)
	WriteJSON(w, http.StatusCreated, v)
}

// NoContent answers 204.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
