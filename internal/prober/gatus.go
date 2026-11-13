package prober

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GatusKey is gatus 5.37.0's key of an endpoint (config/key/key.go):
// group and name lowercased, with / _ . , space # + & turned into -.
func GatusKey(group, name string) string {
	return gatusSanitize(group) + "_" + gatusSanitize(name)
}

func gatusSanitize(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	return strings.NewReplacer("/", "-", "_", "-", ".", "-", ",", "-", " ", "-", "#", "-", "+", "-", "&", "-").Replace(s)
}

// maxErrorLen keeps a pushed error readable on the status page
const maxErrorLen = 300

// push sends one result to a gatus external endpoint
// (POST /api/v1/endpoints/{key}/external?success=&error=&duration=).
func (p *Prober) push(ctx context.Context, key string, err error, d time.Duration) error {
	q := url.Values{"success": {fmt.Sprint(err == nil)}, "duration": {d.Round(time.Millisecond).String()}}
	if err != nil {
		msg := err.Error()
		if len(msg) > maxErrorLen {
			msg = msg[:maxErrorLen]
		}
		q.Set("error", msg)
	}
	u := p.cfg.GatusURL.JoinPath("/api/v1/endpoints", key, "external")
	u.RawQuery = q.Encode()
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if rerr != nil {
		return rerr
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.GatusToken.Reveal())
	resp, rerr := p.gatus.Do(req)
	if rerr != nil {
		return rerr
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gatus %s: status %d %s", key, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
