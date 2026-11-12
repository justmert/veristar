package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestLoadNATS(t *testing.T) {
	c, err := LoadNATS(FromMap(map[string]string{"NATS_URL": "nats://a:4222, nats://b:4222", "NATS_REPLICAS": "3"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.URLs) != 2 || c.Replicas != 3 || c.User != "" {
		t.Fatalf("unexpected %+v", c)
	}
	c, err = LoadNATS(FromMap(map[string]string{"NATS_URL": "nats://a:4222", "NATS_REPLICAS": "1",
		"NATS_USER": "gateway", "NATS_PASSWORD": "pw-value"}))
	if err != nil || c.User != "gateway" || c.Password.Reveal() != "pw-value" {
		t.Fatalf("credentials not loaded: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%v", c), "pw-value") {
		t.Fatal("password printed")
	}
	for name, env := range map[string]map[string]string{
		"NATS_URL":      {"NATS_REPLICAS": "1"},
		"NATS_REPLICAS": {"NATS_URL": "nats://a:4222", "NATS_REPLICAS": "2"},
		"NATS_PASSWORD": {"NATS_URL": "nats://a:4222", "NATS_REPLICAS": "1", "NATS_USER": "x"},
		"NATS_USER":     {"NATS_URL": "nats://a:4222", "NATS_REPLICAS": "1", "NATS_PASSWORD": "x"},
	} {
		if _, err := LoadNATS(FromMap(env)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("expected an error naming %s, got %v", name, err)
		}
	}
	for _, bad := range []string{"http://a:4222", "nats://u:p@a:4222", "a:4222"} {
		_, err := LoadNATS(FromMap(map[string]string{"NATS_URL": bad, "NATS_REPLICAS": "1"}))
		if err == nil || strings.Contains(err.Error(), ":p@") {
			t.Errorf("%s: expected a redacted error, got %v", bad, err)
		}
	}
}
