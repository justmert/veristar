package config

import (
	"strings"
	"testing"
)

func TestLoadWebRetention(t *testing.T) {
	c, err := LoadWebRetention(FromMap(nil))
	if err != nil || c.Months != DefaultWebRawRetentionMonths {
		t.Fatalf("unset must load the default: %+v %v", c, err)
	}
	for v, want := range map[string]int{"25": 25, "36": 36, "1200": 1200} {
		c, err := LoadWebRetention(FromMap(map[string]string{"WEB_RAW_RETENTION_MONTHS": v}))
		if err != nil || c.Months != want {
			t.Fatalf("%s: %+v %v", v, c, err)
		}
	}
}

// dashboards cover 24 full months of raw rows plus the month in progress;
// anything lower is a startup error, never clamped
func TestLoadWebRetentionRefusesBelowFloor(t *testing.T) {
	for _, v := range []string{"24", "1", "0", "-25", "1201", "two"} {
		_, err := LoadWebRetention(FromMap(map[string]string{"WEB_RAW_RETENTION_MONTHS": v}))
		if err == nil || !strings.Contains(err.Error(), "WEB_RAW_RETENTION_MONTHS") {
			t.Fatalf("%q: want a refusal naming the key, got %v", v, err)
		}
	}
}
