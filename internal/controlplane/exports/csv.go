// Package exports streams csv exports (control-plane-api section 16): the
// operator's global and compare views, exactly as filtered and sorted on
// screen, and a tenant's own overview. every export is read through the
// role and the row-level security of the view it mirrors, and writes an
// audit row.
package exports

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ActionExport is the audit action of every export.
const ActionExport = "export.csv"

// MaxRows bounds the rows of one list export.
const MaxRows = 5000

// flushEvery is how many rows are written between flushes, so a long
// export reaches the browser while it is read.
const flushEvery = 200

// Writer streams one csv answer.
type Writer struct {
	w     http.ResponseWriter
	c     *csv.Writer
	rows  int
	flush http.Flusher
}

// NewWriter sends the headers of a csv attachment named name and writes
// the header row.
func NewWriter(w http.ResponseWriter, name string, header []string) (*Writer, error) {
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	out := &Writer{w: w, c: csv.NewWriter(w), flush: f}
	return out, out.c.Write(header)
}

// Row writes one row of cells.
func (x *Writer) Row(cells ...string) error {
	if err := x.c.Write(cells); err != nil {
		return err
	}
	x.rows++
	if x.rows%flushEvery == 0 {
		x.c.Flush()
		if x.flush != nil {
			x.flush.Flush()
		}
	}
	return x.c.Error()
}

// Close flushes what is buffered.
func (x *Writer) Close() error {
	x.c.Flush()
	return x.c.Error()
}

// Rows is how many rows were written after the header.
func (x *Writer) Rows() int { return x.rows }

// Text is a cell of free text, such as a project name a tenant chose. a
// cell that a spreadsheet would read as a formula gets a leading
// apostrophe, so opening the file never runs anything.
func Text(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// Int is an integer cell.
func Int(n int64) string { return strconv.FormatInt(n, 10) }

// IntPtr is an integer cell, empty for null: a value that is not known is
// never written as zero.
func IntPtr(n *int64) string {
	if n == nil {
		return ""
	}
	return Int(*n)
}

// Float is a ratio or seconds cell, empty for null.
func Float(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

// Bool is true or false.
func Bool(b bool) string { return strconv.FormatBool(b) }

// StrPtr is a text cell, empty for null.
func StrPtr(s *string) string {
	if s == nil {
		return ""
	}
	return Text(*s)
}

// Time is an rfc 3339 utc timestamp.
func Time(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// FileName builds an attachment name from parts, keeping letters, digits,
// dots and dashes.
func FileName(parts ...string) string {
	s := strings.Join(parts, "-")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String() + ".csv"
}
