package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/obs"
)

func TestClientIPReader(t *testing.T) {
	r, err := NewClientIPReader("x-edge-client-addr")
	require.NoError(t, err)
	require.Equal(t, "X-Edge-Client-Addr", r.Header())

	for _, h := range []string{"", "X-Forwarded-For", "x-real-ip", "Forwarded", "CF-Connecting-IP", "True-Client-IP"} {
		_, err := NewClientIPReader(h)
		require.Error(t, err, h)
	}

	ok := map[string]string{
		"203.0.113.9":          "203.0.113.9",
		"2001:db8::1":          "2001:db8::1",
		"2001:0db8:0:0::0001":  "2001:db8::1",
		"::ffff:203.0.113.9":   "203.0.113.9",
		"::ffff:cb00:7109":     "203.0.113.9",
		"2001:DB8:0:0:0:0:0:2": "2001:db8::2",
	}
	for in, want := range ok {
		req := httptest.NewRequest(http.MethodPost, "/v1/e", nil)
		req.Header.Set("X-Edge-Client-Addr", in)
		ip, err := r.FromRequest(req)
		require.NoError(t, err, in)
		require.True(t, ip.IsValid())
		require.Equal(t, want, ip.canonical(), in)
	}

	bad := []func(h map[string][]string){
		func(map[string][]string) {},
		func(h map[string][]string) { h["X-Edge-Client-Addr"] = []string{"203.0.113.9", "203.0.113.10"} },
		func(h map[string][]string) { h["X-Edge-Client-Addr"] = []string{"203.0.113.9, 203.0.113.10"} },
		func(h map[string][]string) { h["X-Edge-Client-Addr"] = []string{"203.0.113.9:443"} },
		func(h map[string][]string) { h["X-Edge-Client-Addr"] = []string{"fe80::1%eth0"} },
		func(h map[string][]string) { h["X-Edge-Client-Addr"] = []string{"not-an-address"} },
		func(h map[string][]string) { h["X-Edge-Client-Addr"] = []string{""} },
		// a client-set proxy header is ignored, not used as a fallback
		func(h map[string][]string) { h["X-Forwarded-For"] = []string{"203.0.113.9"} },
	}
	for i, set := range bad {
		req := httptest.NewRequest(http.MethodPost, "/v1/e", nil)
		set(req.Header)
		_, err := r.FromRequest(req)
		require.Error(t, err, "case %d", i)
		require.NotContains(t, err.Error(), "203.0.113", "case %d: error quotes the address", i)
	}
}

func TestClientIPNeverPrints(t *testing.T) {
	const addr = "198.51.100.77"
	ip, err := ParseClientIP(addr)
	require.NoError(t, err)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		out := fmt.Sprintf(verb, ip)
		require.NotContains(t, out, "198", verb)
		require.Equal(t, clientIPMarker, out, verb)
	}
	require.NotContains(t, fmt.Sprint(struct{ IP ClientIP }{ip}), "198")
	require.NotContains(t, fmt.Sprintf("%+v", &ip), "198")

	// a key the guard allows still prints only the marker
	var buf bytes.Buffer
	log := obs.NewLogger(&buf, slog.LevelInfo)
	log.Info("request", "peer", ip, "client_ip", ip)
	require.NotContains(t, buf.String(), "198", buf.String())
	require.Contains(t, buf.String(), clientIPMarker)

	_, err = json.Marshal(struct{ IP ClientIP }{ip})
	require.Error(t, err)
	_, err = json.Marshal(map[ClientIP]int{ip: 1})
	require.Error(t, err)

	_, err = ParseClientIP("garbage " + addr)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), addr))
}

func TestClientIPTruncation(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.200":             "203.0.113.0/24",
		"::ffff:203.0.113.200":      "203.0.113.0/24",
		"2001:db8:abcd:12:ff::1":    "2001:db8:abcd::/48",
		"2001:db8:abcd:ffff:ffff::": "2001:db8:abcd::/48",
	} {
		ip, err := ParseClientIP(in)
		require.NoError(t, err)
		require.Equal(t, want, ip.truncated().String(), in)
	}
}
