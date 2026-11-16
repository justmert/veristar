package config

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func controlPlaneEnv() map[string]string {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	return map[string]string{
		"CONTROLPLANE_ADDR":       "0.0.0.0:8085",
		"CONTROLPLANE_OPS_ADDR":   "127.0.0.1:9095",
		"CONSOLE_ORIGIN":          "https://app.example.org",
		"INGEST_ORIGIN":           "https://in.example.org",
		"WEB_ORIGIN":              "https://web.example.org",
		"GITHUB_CLIENT_ID":        "Iv1.abc",
		"GITHUB_CLIENT_SECRET":    "client-secret-value",
		"GITHUB_REDIRECT_URL":     "https://app.example.org/api/auth/github/callback",
		"OAUTH_STATE_KEY":         key,
		"OPERATOR_GITHUB_IDS":     "583231, 9919",
		"UMAMI_URL":               "http://umami:3000",
		"UMAMI_ADMIN_PASSWORD":    "0123456789abcdef0123",
		"UMAMI_USER_PASSWORD_KEY": key,
		"TRACKER_RELEASE_DIR":     "/releases",
	}
}

func TestLoadControlPlane(t *testing.T) {
	c, err := LoadControlPlane(FromMap(controlPlaneEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ConsoleOrigin != "https://app.example.org" || len(c.OAuthStateKey.Reveal()) != 32 ||
		!c.OperatorGitHubIDs[583231] || !c.OperatorGitHubIDs[9919] || len(c.OperatorGitHubIDs) != 2 ||
		c.TrackerNPMPackage != "" {
		t.Fatalf("unexpected config %+v", c)
	}
	if got := fmt.Sprintf("%v %+v %#v", c.OAuthStateKey, c.UmamiUserPasswordKey, c.GitHubClientSecret); strings.Contains(got, "kkkk") || strings.Contains(got, "client-secret-value") {
		t.Fatalf("a secret printed: %s", got)
	}
	if len(c.OperatorStellarAddresses) != 0 {
		t.Fatalf("wallet operators without the key: %v", c.OperatorStellarAddresses)
	}
	env := controlPlaneEnv()
	env["OPERATOR_STELLAR_ADDRESSES"] = " GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN ,GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"
	if c, err = LoadControlPlane(FromMap(env)); err != nil || len(c.OperatorStellarAddresses) != 2 ||
		!c.OperatorStellarAddresses["GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN"] {
		t.Fatalf("wallet operators: %v %v", err, c.OperatorStellarAddresses)
	}
	// a passkey operator is named by its soropass account's C-address
	env = controlPlaneEnv()
	env["OPERATOR_STELLAR_ADDRESSES"] = "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN,CBH2J5X4WBV2KT6NSUE47DRFBUHTVJOBJTBGGNELJ2DZXTDB3AYRAXAO"
	if c, err = LoadControlPlane(FromMap(env)); err != nil ||
		!c.OperatorStellarAddresses["CBH2J5X4WBV2KT6NSUE47DRFBUHTVJOBJTBGGNELJ2DZXTDB3AYRAXAO"] {
		t.Fatalf("passkey operators: %v %v", err, c.OperatorStellarAddresses)
	}
	if c.OperatorStepUpMaxAge != DefaultStepUpMaxAge || c.OperatorEnrollWindow != DefaultEnrollWindow {
		t.Fatalf("step-up defaults: %v %v", c.OperatorStepUpMaxAge, c.OperatorEnrollWindow)
	}
	env = controlPlaneEnv()
	env["OPERATOR_STEP_UP_MAX_AGE"] = "30m"
	env["OPERATOR_ENROLL_WINDOW"] = "72h"
	if c, err = LoadControlPlane(FromMap(env)); err != nil || c.OperatorStepUpMaxAge != 30*time.Minute ||
		c.OperatorEnrollWindow != 72*time.Hour {
		t.Fatalf("step-up settings: %v %v %v", err, c.OperatorStepUpMaxAge, c.OperatorEnrollWindow)
	}
	env = controlPlaneEnv()
	env["TRACKER_NPM_PACKAGE"] = "tracker"
	if c, err = LoadControlPlane(FromMap(env)); err != nil || c.TrackerNPMPackage != "tracker" {
		t.Fatalf("npm package: %v %q", err, c.TrackerNPMPackage)
	}
}

func TestLoadControlPlaneRefuses(t *testing.T) {
	cases := map[string]struct {
		key, value string
	}{
		"missing operators":    {"OPERATOR_GITHUB_IDS", ""},
		"bad operator id":      {"OPERATOR_GITHUB_IDS", "1,octocat"},
		"zero operator id":     {"OPERATOR_GITHUB_IDS", "0"},
		"bad wallet operator":  {"OPERATOR_STELLAR_ADDRESSES", "GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQA"},
		"bad passkey operator": {"OPERATOR_STELLAR_ADDRESSES", "CBH2J5X4WBV2KT6NSUE47DRFBUHTVJOBJTBGGNELJ2DZXTDB3AYRAXAA"},
		"muxed wallet operator": {"OPERATOR_STELLAR_ADDRESSES",
			"MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUAAAAAAAAAAAAGZFQ"},
		"short state key":        {"OAUTH_STATE_KEY", base64.StdEncoding.EncodeToString([]byte("short"))},
		"state key not base64":   {"OAUTH_STATE_KEY", "not base64!"},
		"origin with path":       {"CONSOLE_ORIGIN", "https://app.example.org/x"},
		"origin with slash":      {"CONSOLE_ORIGIN", "https://app.example.org/"},
		"origin uppercase":       {"CONSOLE_ORIGIN", "https://App.example.org"},
		"redirect elsewhere":     {"GITHUB_REDIRECT_URL", "https://evil.example.org/api/auth/github/callback"},
		"redirect wrong path":    {"GITHUB_REDIRECT_URL", "https://app.example.org/callback"},
		"same ops addr":          {"CONTROLPLANE_OPS_ADDR", "0.0.0.0:8085"},
		"missing client id":      {"GITHUB_CLIENT_ID", ""},
		"missing user pw key":    {"UMAMI_USER_PASSWORD_KEY", ""},
		"step-up too long":       {"OPERATOR_STEP_UP_MAX_AGE", "48h"},
		"step-up too short":      {"OPERATOR_STEP_UP_MAX_AGE", "1m"},
		"step-up not duration":   {"OPERATOR_STEP_UP_MAX_AGE", "twelve hours"},
		"enroll window too long": {"OPERATOR_ENROLL_WINDOW", "200h"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := controlPlaneEnv()
			env[c.key] = c.value
			_, err := LoadControlPlane(FromMap(env))
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Fatalf("want an error naming %s, got %v", c.key, err)
			}
		})
	}
}
