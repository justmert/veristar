package prober

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// the tracker protocol (docs/reference/wire-v1.md and browser-proof.md):
// GET /v1/c?w=<site> issues a challenge, the client finds a nonce whose
// sha-256 over w|iat|salt|nonce has bits leading zero bits, and POST /v1/e
// carries the event with the proof. the gateway answers 202 only after
// jetstream acknowledged the event, which is what ingestion availability
// counts.

type challenge struct {
	W    string `json:"w"`
	IAT  int64  `json:"iat"`
	Salt string `json:"salt"`
	Bits int    `json:"bits"`
	Tag  string `json:"tag"`
}

type proof struct {
	IAT   int64  `json:"iat"`
	Salt  string `json:"salt"`
	Bits  int    `json:"bits"`
	Tag   string `json:"tag"`
	Nonce uint64 `json:"nonce"`
}

type wireEvent struct {
	V  int    `json:"v"`
	W  string `json:"w"`
	T  string `json:"t"`
	H  string `json:"h"`
	U  string `json:"u"`
	R  string `json:"r"`
	Ti string `json:"ti"`
	S  string `json:"s"`
	L  string `json:"l"`
	TS int64  `json:"ts"`
	P  proof  `json:"p"`
}

// maxProofBits bounds the work: the gateway's setting is 12 to 16 bits, so
// a challenge asking for far more is a fault, not something to grind on
const maxProofBits = 24

func solve(c challenge) (proof, error) {
	if c.Bits < 0 || c.Bits > maxProofBits {
		return proof{}, fmt.Errorf("challenge asks for %d bits", c.Bits)
	}
	prefix := c.W + "|" + strconv.FormatInt(c.IAT, 10) + "|" + c.Salt + "|"
	buf := make([]byte, 0, len(prefix)+20)
	for n := uint64(0); ; n++ {
		buf = strconv.AppendUint(append(buf[:0], prefix...), n, 10)
		d := sha256.Sum256(buf)
		if leadingZeroBits(d[:]) >= c.Bits {
			return proof{IAT: c.IAT, Salt: c.Salt, Bits: c.Bits, Tag: c.Tag, Nonce: n}, nil
		}
	}
}

func leadingZeroBits(d []byte) int {
	z := 0
	for _, b := range d {
		if b != 0 {
			return z + bits.LeadingZeros8(b)
		}
		z += 8
	}
	return z
}

// browserHeaders are the headers a browser sends on the tracker's
// cross-site fetches; the gateway checks origin and fetch metadata
func (p *Prober) browserHeaders(req *http.Request) {
	req.Header.Set("Origin", p.cfg.Origin)
	req.Header.Set("User-Agent", p.cfg.UserAgent)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Accept-Language", "en-US")
}

// sendWebEvent runs one challenge, proof and pageview. it returns nil only
// for a 202.
func (p *Prober) sendWebEvent(ctx context.Context) error {
	u := p.cfg.IngestURL.JoinPath("/v1/c")
	u.RawQuery = "w=" + url.QueryEscape(p.cfg.SiteID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	p.browserHeaders(req)
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("challenge: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("challenge: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("challenge: status %d %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	var c challenge
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("challenge: %w", err)
	}
	pr, err := solve(c)
	if err != nil {
		return err
	}
	o, err := url.Parse(p.cfg.Origin)
	if err != nil {
		return err
	}
	ev := wireEvent{V: 1, W: p.cfg.SiteID, T: "pv", H: o.Hostname(), U: "/probe", R: "", Ti: "probe",
		S: "1920x1080", L: "en-US", TS: p.now().UnixMilli(), P: pr}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.IngestURL.JoinPath("/v1/e").String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	p.browserHeaders(req)
	// the tracker sends text/plain so the request stays a simple cors request
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	resp, err = p.http.Do(req)
	if err != nil {
		return fmt.Errorf("event: %w", err)
	}
	b, _ = io.ReadAll(io.LimitReader(resp.Body, 512))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("event: status %d %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}

// now is the prober's clock; tests replace it
func (p *Prober) now() time.Time { return p.clock() }
