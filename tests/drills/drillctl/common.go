package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type host struct {
	Name    string `json:"-"`
	Public  string `json:"sa_public_ipv4"`
	Private string `json:"sa_private_ip"`
}

type inventory struct {
	All struct {
		Vars struct {
			Domain    string            `json:"sa_base_domain"`
			Hostnames map[string]string `json:"sa_hostnames"`
		} `json:"vars"`
		Children map[string]struct {
			Hosts map[string]host `json:"hosts"`
		} `json:"children"`
	} `json:"all"`
}

func loadInventory(path string) (inventory, error) {
	var inv inventory
	err := readJSON(path, &inv)
	return inv, err
}

// group returns the hosts of a group sorted by name, so edge-1 is first
func (inv inventory) group(name string) []host {
	var out []host
	for n, h := range inv.All.Children[name].Hosts {
		h.Name = n
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// pinnedTransport resolves every hostname to addr, as one address of a dns
// record set would, and trusts caddy's local ca of the local converge
// without checking it (there are no public certificates there).
func pinnedTransport(addr string, dialTimeout time.Duration) *http.Transport {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 15 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(a)
			if err != nil {
				return nil, err
			}
			return d.DialContext(ctx, network, net.JoinHostPort(addr, port))
		},
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // caddy's local ca in the local converge
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
}

// state is what setup creates and every later command reads
type state struct {
	Suffix    string         `json:"suffix"`
	Domain    string         `json:"domain"`
	InHost    string         `json:"in_host"`
	OTLPHost  string         `json:"otlp_host"`
	AppHost   string         `json:"app_host"`
	Sites     []siteState    `json:"sites"`
	Tenants   []tenantState  `json:"tenants"`
	Cookies   []cookieState  `json:"cookies"`
	DashProbe dashProbeState `json:"dash_probe"`
}

type siteState struct {
	ID     string `json:"id"`
	Tenant string `json:"tenant"`
	Origin string `json:"origin"`
}

type tenantState struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

type cookieState struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// the endpoint the dashboard probe reads: one tenant's api overview, which
// queries the platform database on every call
type dashProbeState struct {
	Tenant string `json:"tenant"`
}

type console struct {
	ctx  context.Context
	base string
	http *http.Client
}

func (c *console) do(method, path string, body, out any) (int, string, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, r)
	if err != nil {
		return 0, "", err
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sa-console")
		req.Header.Set("Origin", c.base)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, string(b), err
		}
	}
	return resp.StatusCode, string(b), nil
}

// signIn walks the oauth web flow against the test-only fixture on the core
// node, as a browser would
func signIn(ctx context.Context, appHost, coreAddr string, githubID int, login string) (*console, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Transport: pinnedTransport(coreAddr, 10*time.Second), Jar: jar, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "https://" + appHost
	resp, err := httpGet(ctx, hc, base+"/api/auth/github/start")
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("sign-in start: %d", resp.StatusCode)
	}
	authorize, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	for k, vs := range authorize.Query() {
		form[k] = vs
	}
	form.Set("fixture_user_id", strconv.Itoa(githubID))
	form.Set("fixture_login", login)
	form.Set("fixture_decision", "authorize")
	freq, err := http.NewRequestWithContext(ctx, http.MethodPost, authorize.Scheme+"://"+authorize.Host+authorize.Path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	freq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = hc.Do(freq)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	callback := resp.Header.Get("Location")
	if !strings.HasPrefix(callback, base+"/api/auth/github/callback?") {
		return nil, fmt.Errorf("fixture answered %d, location %q", resp.StatusCode, callback)
	}
	resp, err = httpGet(ctx, hc, callback)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || strings.Contains(resp.Header.Get("Location"), "error=") {
		return nil, fmt.Errorf("callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	return &console{ctx: ctx, base: base, http: hc}, nil
}

// httpGet is a get bound to ctx
func httpGet(ctx context.Context, hc *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	return hc.Do(req)
}

// privateTransport presents the operators' client certificate and trusts
// only the internal ca, for the private endpoints (patroni, nats monitoring
// through the relay), which take only tls with client certificates. the
// drill scripts decrypt the files into SA_DRILL_TLS_DIR
// (tests/drills/lib.sh). plain http urls (caddy's port 80) still work.
func privateTransport() (*http.Transport, error) {
	dir := os.Getenv("SA_DRILL_TLS_DIR")
	if dir == "" {
		return nil, errors.New("SA_DRILL_TLS_DIR is not set")
	}
	ca, err := os.ReadFile(filepath.Join(dir, "ca.crt")) //nolint:gosec // the drill scripts name this directory
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("no certificate in " + filepath.Join(dir, "ca.crt"))
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key"))
	if err != nil {
		return nil, err
	}
	return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}}, nil
}
