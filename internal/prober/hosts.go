package prober

import (
	"context"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// getOK fetches u and returns nil for a 200.
func (p *Prober) getOK(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d", u, resp.StatusCode)
	}
	return nil
}

type manifest struct {
	Versions []struct {
		Version string `json:"version"`
		File    string `json:"file"`
		SHA384  string `json:"sha384"`
	} `json:"versions"`
}

type revoked struct {
	Revoked []struct {
		Version string `json:"version"`
	} `json:"revoked"`
}

// checkTracker fetches every released, unrevoked tracker file from the in.
// host and compares its sha-384 with the manifest the release committed.
// the manifest is read from disk on every run, so a release deployed to the
// probe node is checked from its next cycle on.
func (p *Prober) checkTracker(ctx context.Context) error {
	b, err := os.ReadFile(p.cfg.TrackerManifest)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if len(m.Versions) == 0 {
		return errors.New("manifest lists no version")
	}
	gone := map[string]bool{}
	if rb, err := os.ReadFile(filepath.Join(filepath.Dir(p.cfg.TrackerManifest), "revoked.json")); err == nil {
		var r revoked
		if err := json.Unmarshal(rb, &r); err != nil {
			return fmt.Errorf("revoked.json: %w", err)
		}
		for _, v := range r.Revoked {
			gone[v.Version] = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, v := range m.Versions {
		if gone[v.Version] {
			continue
		}
		u := p.cfg.IngestURL.JoinPath("/v", v.Version, "sdk.js").String()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		resp, err := p.http.Do(req)
		if err != nil {
			return fmt.Errorf("tracker %s: %w", v.Version, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("tracker %s: %w", v.Version, err)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("tracker %s: status %d", v.Version, resp.StatusCode)
		}
		sum := sha512.Sum384(body)
		if got := hex.EncodeToString(sum[:]); got != v.SHA384 {
			return fmt.Errorf("tracker %s: sha384 %s does not match the manifest %s", v.Version, got[:16], v.SHA384[:min(16, len(v.SHA384))])
		}
	}
	return nil
}

// nodeResult is one host on one node, checked by address.
type nodeResult struct {
	err    error
	expiry time.Time
}

// checkNodeHost connects to the node's address with host as sni, verifies
// the chain for host, requires the leaf to stay valid for CertMinValidity
// and asks for / over that connection. any answer below 500 shows the node
// serves the host; the dns provider's port-80 check sees none of this.
func (p *Prober) checkNodeHost(ctx context.Context, n Node, host string) nodeResult {
	var leaf time.Time
	tr := &http.Transport{
		DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := tls.Dialer{NetDialer: &net.Dialer{Timeout: p.cfg.Timeout}, Config: &tls.Config{
				ServerName: host, RootCAs: p.cfg.Roots, MinVersion: tls.VersionTLS12,
			}}
			c, err := d.DialContext(ctx, "tcp", n.Address)
			if err != nil {
				return nil, err
			}
			tc, ok := c.(*tls.Conn)
			if !ok {
				_ = c.Close()
				return nil, errors.New("not a tls connection")
			}
			if certs := tc.ConnectionState().PeerCertificates; len(certs) > 0 {
				leaf = certs[0].NotAfter
			}
			return c, nil
		},
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: p.cfg.Timeout,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return nodeResult{err: err}
	}
	resp, err := (&http.Client{Transport: tr, Timeout: p.cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do(req)
	if err != nil {
		return nodeResult{err: err, expiry: leaf}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nodeResult{err: fmt.Errorf("status %d", resp.StatusCode), expiry: leaf}
	}
	if left := leaf.Sub(p.now()); left < p.cfg.CertMinValidity {
		return nodeResult{err: fmt.Errorf("certificate expires in %s, at %s", left.Round(time.Hour), leaf.UTC().Format(time.RFC3339)), expiry: leaf}
	}
	return nodeResult{expiry: leaf}
}
