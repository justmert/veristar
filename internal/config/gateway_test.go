package config

import (
	"strings"
	"testing"
)

func gatewayEnv() map[string]string {
	return map[string]string{
		"GATEWAY_NODE_ID":          "edge-1",
		"GATEWAY_ADDR":             "0.0.0.0:8080",
		"GATEWAY_OPS_ADDR":         "127.0.0.1:9090",
		"PROOF_BITS":               "12",
		"PROOF_REPLAY_LIMIT":       "50",
		"IPCLUSTER_THRESHOLD":      "20",
		"VISITOR_EVENTS_PER_HOUR":  "600",
		"RATE_SITE_PER_SECOND":     "200",
		"RATE_SITE_BURST":          "400",
		"RATE_PREFIX_PER_SECOND":   "20",
		"RATE_PREFIX_BURST":        "60",
		"GATEWAY_MAX_BODY":         "8192",
		"CLIENT_IP_HEADER":         "X-Edge-Client-Ip",
		"DBIP_MMDB":                "/geo/dbip-country-lite.mmdb",
		"TRACKER_RELEASE_DIR":      "/releases",
		"GATEWAY_SECURE_TRANSPORT": "true",
	}
}

func TestLoadGateway(t *testing.T) {
	c, err := LoadGateway(FromMap(gatewayEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.NodeID != "edge-1" || c.ProofBits != 12 || c.ReplayLimit != 50 || c.IPClusterThreshold != 20 ||
		c.SiteRatePerSecond != 200 || c.SiteBurst != 400 || c.PrefixRatePerSecond != 20 || c.PrefixBurst != 60 ||
		c.MaxBody != 8192 || c.ClientIPHeader != "X-Edge-Client-Ip" || !c.SecureTransport {
		t.Fatalf("unexpected config %+v", c)
	}
}

func TestLoadGatewayRefusesBadValues(t *testing.T) {
	for key, bad := range map[string]string{
		"GATEWAY_NODE_ID":          "edge.1",
		"GATEWAY_ADDR":             "8080",
		"PROOF_BITS":               "25",
		"GATEWAY_MAX_BODY":         "8193",
		"GATEWAY_SECURE_TRANSPORT": "yes",
		"GATEWAY_OPS_ADDR":         "0.0.0.0:8080",
	} {
		env := gatewayEnv()
		env[key] = bad
		_, err := LoadGateway(FromMap(env))
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%q: error %v does not name the key", key, bad, err)
		}
	}
}

func TestLoadGatewayNamesEveryMissingKey(t *testing.T) {
	_, err := LoadGateway(FromMap(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	for key := range gatewayEnv() {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s", key)
		}
	}
}
