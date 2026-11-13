package config

import (
	"strings"
	"testing"
)

func TestLoadAlertmanager(t *testing.T) {
	ca, cert, key := tlsFiles(t)

	c, err := LoadAlertmanager(FromMap(map[string]string{}))
	if err != nil || c.Configured() || c.TLS != nil {
		t.Fatalf("unset must mean not configured: %+v %v", c, err)
	}

	c, err = LoadAlertmanager(FromMap(map[string]string{
		"ALERTMANAGER_URLS":     "https://10.0.0.1:9093, https://10.0.0.2:9093/",
		"ALERTMANAGER_TLS_CA":   ca,
		"ALERTMANAGER_TLS_CERT": cert,
		"ALERTMANAGER_TLS_KEY":  key,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Configured() || len(c.URLs) != 2 || c.URLs[1].String() != "https://10.0.0.2:9093" {
		t.Fatalf("urls not loaded: %+v", c.URLs)
	}
	tc, err := c.ClientTLS()
	if err != nil || tc == nil || len(tc.Certificates) != 1 || tc.RootCAs == nil {
		t.Fatalf("client tls not built: %+v %v", tc, err)
	}

	c, err = LoadAlertmanager(FromMap(map[string]string{"ALERTMANAGER_URLS": "http://alertmanager:9093"}))
	if err != nil || len(c.URLs) != 1 || c.TLS != nil {
		t.Fatalf("plain http member: %+v %v", c, err)
	}
	if tc, err := c.ClientTLS(); tc != nil || err != nil {
		t.Fatalf("plain http has no tls: %+v %v", tc, err)
	}
}

func TestLoadAlertmanagerRefuses(t *testing.T) {
	ca, cert, key := tlsFiles(t)
	for name, tc := range map[string]struct {
		env  map[string]string
		key  string
		text string
	}{
		"https without ca": {map[string]string{"ALERTMANAGER_URLS": "https://a:9093"}, "ALERTMANAGER_TLS_CA", "https://"},
		"http with ca": {map[string]string{"ALERTMANAGER_URLS": "http://a:9093", "ALERTMANAGER_TLS_CA": ca},
			"ALERTMANAGER_URLS", "must use https://"},
		"tls without urls": {map[string]string{"ALERTMANAGER_TLS_CA": ca, "ALERTMANAGER_TLS_CERT": cert, "ALERTMANAGER_TLS_KEY": key},
			"ALERTMANAGER_URLS", "is required"},
		"path":         {map[string]string{"ALERTMANAGER_URLS": "http://a:9093/api/v2"}, "ALERTMANAGER_URLS", "without a path"},
		"credentials":  {map[string]string{"ALERTMANAGER_URLS": "http://u:secret@a:9093"}, "ALERTMANAGER_URLS", "without a path"},
		"other scheme": {map[string]string{"ALERTMANAGER_URLS": "tcp://a:9093"}, "ALERTMANAGER_URLS", "http://"},
		"duplicate":    {map[string]string{"ALERTMANAGER_URLS": "http://a:9093,http://a:9093/"}, "ALERTMANAGER_URLS", "twice"},
		"empty member": {map[string]string{"ALERTMANAGER_URLS": "http://a:9093,"}, "ALERTMANAGER_URLS", "without a path"},
		"cert without key": {map[string]string{"ALERTMANAGER_URLS": "https://a:9093", "ALERTMANAGER_TLS_CA": ca, "ALERTMANAGER_TLS_CERT": cert},
			"ALERTMANAGER_TLS_KEY", "is required"},
	} {
		_, err := LoadAlertmanager(FromMap(tc.env))
		if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), tc.text) {
			t.Errorf("%s: expected an error naming %s with %q, got %v", name, tc.key, tc.text, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the error printed the url's password: %v", name, err)
		}
	}
}
