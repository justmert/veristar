package config

import (
	"strings"
	"testing"
)

func TestLoadAPIRetention(t *testing.T) {
	c, err := LoadAPIRetention(FromMap(map[string]string{
		"API_MINUTE_RETENTION_DAYS": "30", "API_HOURLY_RETENTION_MONTHS": "36",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.MinuteDays != 30 || c.HourlyMonths != 36 {
		t.Fatalf("unexpected config %+v", c)
	}
	c, err = LoadAPIRetention(FromMap(map[string]string{
		"API_MINUTE_RETENTION_DAYS": "30", "API_HOURLY_RETENTION_MONTHS": "24",
	}))
	if err != nil || c.HourlyMonths != 24 {
		t.Fatalf("24 months is the floor and must load: %+v %v", c, err)
	}
}

// the rfp's 24 months is a floor; anything lower is a startup error, never
// clamped
func TestLoadAPIRetentionRefusesBelowFloor(t *testing.T) {
	for _, v := range []string{"23", "0", "-36", "twelve"} {
		_, err := LoadAPIRetention(FromMap(map[string]string{
			"API_MINUTE_RETENTION_DAYS": "30", "API_HOURLY_RETENTION_MONTHS": v,
		}))
		if err == nil || !strings.Contains(err.Error(), "API_HOURLY_RETENTION_MONTHS") {
			t.Fatalf("%q: want a refusal naming the key, got %v", v, err)
		}
	}
	_, err := LoadAPIRetention(FromMap(map[string]string{
		"API_MINUTE_RETENTION_DAYS": "1", "API_HOURLY_RETENTION_MONTHS": "36",
	}))
	if err == nil || !strings.Contains(err.Error(), "API_MINUTE_RETENTION_DAYS") {
		t.Fatalf("want a refusal of 1 minute day, got %v", err)
	}
}

func TestLoadAPIRetentionNamesMissingKeys(t *testing.T) {
	_, err := LoadAPIRetention(FromMap(nil))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, k := range []string{"API_MINUTE_RETENTION_DAYS", "API_HOURLY_RETENTION_MONTHS"} {
		if !strings.Contains(err.Error(), k) {
			t.Fatalf("missing %s in %v", k, err)
		}
	}
}
