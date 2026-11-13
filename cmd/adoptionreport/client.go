package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// the api allows 10 requests per second per key with a burst of 20, so a
// report over a few tenants rarely meets a 429; when it does, the answer's
// Retry-After is honoured a bounded number of times instead of failing the run
const (
	maxRetries    = 5
	maxRetryAfter = 60 * time.Second
	// a page is at most 500 items; a tenant with a long range and many routes
	// can still need many pages, but not this many
	maxPages = 1000
)

// Source is one request the evidence rests on: what was asked, the request
// id the server logged it under, and the sha-256 of the body that came back,
// so each number can be traced to a logged answer.
type Source struct {
	Path      string
	Status    int
	RequestID string
	SHA256    string
}

type client struct {
	base    *url.URL
	key     string
	hc      *http.Client
	sleep   func(context.Context, time.Duration) error
	rec     *recorder
	sources []Source
}

// apiError is the error envelope of the contract
type apiError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
	Path      string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("GET %s: %d %s: %s (request %s)", e.Path, e.Status, e.Code, e.Message, e.RequestID)
}

// get fetches one route and decodes it strictly: a field the contract does
// not define is an error, so a changed api fails the report instead of
// silently dropping a number
func (c *client) get(ctx context.Context, route string, q url.Values, out any) error {
	u := *c.base
	u.Path = c.base.Path + route
	u.RawQuery = q.Encode()
	shown := route
	if u.RawQuery != "" {
		shown += "?" + u.RawQuery
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Accept", "application/json")
		res, err := c.hc.Do(req)
		if err != nil {
			return fmt.Errorf("GET %s: %w", shown, err)
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		_ = res.Body.Close()
		if err != nil {
			return fmt.Errorf("GET %s: %w", shown, err)
		}
		rid := res.Header.Get("X-Request-Id")
		if c.rec != nil {
			if err := c.rec.add(shown, res.StatusCode, rid, body); err != nil {
				return err
			}
		}
		if res.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait, err := strconv.Atoi(res.Header.Get("Retry-After"))
			if err != nil || wait < 1 {
				wait = 1
			}
			d := min(time.Duration(wait)*time.Second, maxRetryAfter)
			if err := c.sleep(ctx, d); err != nil {
				return err
			}
			continue
		}
		if res.StatusCode != http.StatusOK {
			e := &apiError{Status: res.StatusCode, Path: shown, RequestID: rid}
			var env struct {
				Error struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					RequestID string `json:"request_id"`
				} `json:"error"`
			}
			if json.Unmarshal(body, &env) == nil && env.Error.Code != "" {
				e.Code, e.Message = env.Error.Code, env.Error.Message
				if e.RequestID == "" {
					e.RequestID = env.Error.RequestID
				}
			} else {
				e.Code, e.Message = "unexpected", strings.TrimSpace(string(body))
			}
			return e
		}
		sum := sha256.Sum256(body)
		c.sources = append(c.sources, Source{Path: shown, Status: res.StatusCode, RequestID: rid, SHA256: hex.EncodeToString(sum[:])})
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("GET %s: decoding the answer: %w", shown, err)
		}
		return nil
	}
}

// pages follows next_cursor until it is null. each page is decoded into a
// fresh value and handed to add, which returns the page's cursor.
func pages[P any](ctx context.Context, c *client, route string, q url.Values, add func(P) *string) error {
	q = cloneValues(q)
	for range maxPages {
		var page P
		if err := c.get(ctx, route, q, &page); err != nil {
			return err
		}
		next := add(page)
		if next == nil {
			return nil
		}
		if *next == "" {
			return fmt.Errorf("GET %s: empty next_cursor", route)
		}
		q.Set("cursor", *next)
	}
	return fmt.Errorf("GET %s: more than %d pages", route, maxPages)
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// recorder keeps the raw answers of a run: one file per response and an
// index that says which request produced it
type recorder struct {
	dir   string
	index []recorded
}

type recorded struct {
	File      string `json:"file"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	RequestID string `json:"request_id"`
}

func newRecorder(dir string) (*recorder, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &recorder{dir: dir}, nil
}

func (r *recorder) add(path string, status int, rid string, body []byte) error {
	name := fmt.Sprintf("%03d.json", len(r.index)+1)
	if err := os.WriteFile(filepath.Join(r.dir, name), body, 0o644); err != nil { //nolint:gosec // response bodies of an aggregate api, meant to be read
		return err
	}
	r.index = append(r.index, recorded{File: name, Path: path, Status: status, RequestID: rid})
	return nil
}

func (r *recorder) flush() error {
	b, err := json.MarshalIndent(r.index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "index.json"), append(b, '\n'), 0o644) //nolint:gosec // an index of the files beside it
}
