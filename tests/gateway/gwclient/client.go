// Package gwclient is a scripted, non-browser client of the collect gateway,
// used only by the this stage tests in tests/gateway and by the probe they run
// inside docker. it fetches a challenge, solves the proof in go and posts
// wire v1 events with whatever headers the caller forges. that such a client
// is accepted is the documented limitation of the proof
// (docs/reference/browser-proof.md, "what it shows and what it does not").
package gwclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Challenge is the body of GET /v1/c.
type Challenge struct {
	W    string `json:"w"`
	IAT  int64  `json:"iat"`
	Salt string `json:"salt"`
	Bits int    `json:"bits"`
	Tag  string `json:"tag"`
}

// Proof is the p field of an event.
type Proof struct {
	IAT   int64  `json:"iat"`
	Salt  string `json:"salt"`
	Bits  int    `json:"bits"`
	Tag   string `json:"tag"`
	Nonce uint64 `json:"nonce"`
}

// Solve finds the first nonce whose sha-256 of w|iat|salt|nonce has at least
// bits leading zero bits, as the tracker does.
func Solve(c Challenge) Proof {
	prefix := c.W + "|" + strconv.FormatInt(c.IAT, 10) + "|" + c.Salt + "|"
	buf := make([]byte, 0, len(prefix)+20)
	for n := uint64(0); ; n++ {
		buf = strconv.AppendUint(append(buf[:0], prefix...), n, 10)
		d := sha256.Sum256(buf)
		if zeroBits(d[:]) >= c.Bits {
			return Proof{IAT: c.IAT, Salt: c.Salt, Bits: c.Bits, Tag: c.Tag, Nonce: n}
		}
	}
}

func zeroBits(d []byte) int {
	z := 0
	for _, b := range d {
		if b != 0 {
			return z + bits.LeadingZeros8(b)
		}
		z += 8
	}
	return z
}

// Event is a wire v1 body. every field the parser requires is present.
type Event struct {
	V int `json:"v"`
	// SV is the tracker release, sent from 1.2.0 on
	SV string         `json:"sv,omitempty"`
	W  string         `json:"w"`
	T  string         `json:"t"`
	H  string         `json:"h"`
	U  string         `json:"u"`
	R  string         `json:"r"`
	Ti string         `json:"ti"`
	S  string         `json:"s"`
	L  string         `json:"l"`
	N  string         `json:"n,omitempty"`
	D  map[string]any `json:"d,omitempty"`
	TS int64          `json:"ts"`
	P  *Proof         `json:"p,omitempty"`
}

// Pageview returns a pageview of site on host at path.
func Pageview(site, host, path string, p *Proof) Event {
	return Event{V: 1, W: site, T: "pv", H: host, U: path, R: "", Ti: "probe", S: "1920x1080", L: "en-US",
		TS: time.Now().UnixMilli(), P: p}
}

// Client talks to one gateway base url (https://in.<domain> through caddy,
// or http://127.0.0.1:<port> directly).
type Client struct {
	Base string
	HTTP *http.Client
	// Header is sent on every request: Origin, User-Agent, Sec-Fetch-*, and
	// the client address header when the test stands in for caddy.
	Header http.Header
}

// New returns a client. insecure skips certificate checks, for caddy's
// internal test ca.
func New(base string, insecure bool) *Client {
	def, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("gwclient: http.DefaultTransport is not an *http.Transport")
	}
	tr := def.Clone()
	tr.MaxIdleConnsPerHost = 64
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test client against caddy's internal ca
	}
	return &Client{Base: base, HTTP: &http.Client{Transport: tr, Timeout: 30 * time.Second}, Header: http.Header{}}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, extra http.Header) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range c.Header {
		req.Header[k] = append([]string(nil), v...)
	}
	for k, v := range extra {
		req.Header[k] = append([]string(nil), v...)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header, err
}

// Challenge fetches a challenge for site.
func (c *Client) Challenge(ctx context.Context, site string) (Challenge, error) {
	status, b, _, err := c.do(ctx, http.MethodGet, "/v1/c?w="+url.QueryEscape(site), nil, nil)
	if err != nil {
		return Challenge{}, err
	}
	if status != http.StatusOK {
		return Challenge{}, fmt.Errorf("challenge: status %d: %s", status, b)
	}
	var ch Challenge
	if err := json.Unmarshal(b, &ch); err != nil {
		return Challenge{}, fmt.Errorf("challenge: %w", err)
	}
	return ch, nil
}

// Post sends one event body and returns the status and response headers.
func (c *Client) Post(ctx context.Context, body []byte, extra http.Header) (int, http.Header, error) {
	status, _, h, err := c.do(ctx, http.MethodPost, "/v1/e", body, extra)
	return status, h, err
}

// PostEvent encodes and sends one event.
func (c *Client) PostEvent(ctx context.Context, e Event, extra http.Header) (int, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	status, _, err := c.Post(ctx, b, extra)
	return status, err
}

// Get fetches a path and returns status, body and headers.
func (c *Client) Get(ctx context.Context, path string) (int, []byte, http.Header, error) {
	return c.do(ctx, http.MethodGet, path, nil, nil)
}
