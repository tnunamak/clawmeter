package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// TestMain keeps tests away from the developer's real Codex login: the
// app-server path reads auth.json to attach reset credits and extra windows.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "clawmeter-codex-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("CODEX_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func windowByName(t *testing.T, data *provider.UsageData, name string) provider.UsageWindow {
	t.Helper()
	for _, w := range data.Windows {
		if w.Name == name {
			return w
		}
	}
	t.Fatalf("no window %q in %#v", name, data.Windows)
	return provider.UsageWindow{}
}

func windowNames(data *provider.UsageData) string {
	names := make([]string, 0, len(data.Windows))
	for _, w := range data.Windows {
		names = append(names, w.Name)
	}
	return strings.Join(names, ",")
}

func directFixture(now time.Time) string {
	h5 := now.Add(2 * time.Hour).Unix()
	d7 := now.Add(5 * 24 * time.Hour).Unix()
	return fmt.Sprintf(`{
  "plan_type": "plus",
  "rate_limit": {"allowed": true, "limit_reached": false,
    "primary_window": {"used_percent": 12, "limit_window_seconds": 18000, "reset_at": %d},
    "secondary_window": {"used_percent": 34, "limit_window_seconds": 604800, "reset_at": %d}},
  "code_review_rate_limit": {"allowed": true, "limit_reached": false,
    "primary_window": {"used_percent": 7, "limit_window_seconds": 604800, "reset_at": %d}},
  "additional_rate_limits": [
    {"limit_name": "GPT-5.3-Codex-Spark", "metered_feature": "codex_bengalfox",
     "rate_limit": {"allowed": true, "limit_reached": false,
       "primary_window": {"used_percent": 55, "limit_window_seconds": 18000, "reset_at": %d},
       "secondary_window": {"used_percent": 66, "limit_window_seconds": 604800, "reset_at": %d}}}
  ],
  "credits": {"has_credits": false, "unlimited": false, "balance": "0"}
}`, h5, d7, d7, h5, d7)
}

func TestParseDirectUsageReadsSecondaryReviewAndAdditionalLimits(t *testing.T) {
	now := time.Now()
	data, err := New(config.ProviderConfig{}).parseDirectUsage([]byte(directFixture(now)), now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := windowNames(data), "5h,7d,7d Review,5h Spark,7d Spark"; got != want {
		t.Fatalf("windows = %s, want %s", got, want)
	}
	if w := windowByName(t, data, "7d"); w.Utilization != 34 || w.DisplayName != "7 days" {
		t.Fatalf("7d = %#v", w)
	}
	if w := windowByName(t, data, "7d Review"); w.Utilization != 7 || w.DisplayName != "7 days (Review)" {
		t.Fatalf("7d Review = %#v", w)
	}
	if w := windowByName(t, data, "5h Spark"); w.Utilization != 55 || w.DisplayName != "5h (Spark)" {
		t.Fatalf("5h Spark = %#v", w)
	}
}

func TestParseDirectUsageSecondaryWithoutPrimary(t *testing.T) {
	now := time.Now()
	reset := now.Add(3 * 24 * time.Hour).Unix()
	data, err := New(config.ProviderConfig{}).parseDirectUsage([]byte(fmt.Sprintf(
		`{"rate_limit":{"primary_window":null,"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_at":%d}}}`, reset)), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := windowNames(data); got != "7d" || data.Error != "" {
		t.Fatalf("windows = %s error = %q", got, data.Error)
	}
}

func TestParseDirectUsageLimitReachedFillsMissingPercent(t *testing.T) {
	now := time.Now()
	reset := now.Add(90 * time.Minute).Unix()
	data, err := New(config.ProviderConfig{}).parseDirectUsage([]byte(fmt.Sprintf(
		`{"rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"limit_window_seconds":18000,"reset_at":%d}}}`, reset)), now)
	if err != nil {
		t.Fatal(err)
	}
	if w := windowByName(t, data, "5h"); w.Utilization != 100 {
		t.Fatalf("5h = %#v, want 100", w)
	}
}

func TestParseDirectUsageMissingPercentWithoutLimitReachedIsIgnored(t *testing.T) {
	now := time.Now()
	reset := now.Add(90 * time.Minute).Unix()
	data, err := New(config.ProviderConfig{}).parseDirectUsage([]byte(fmt.Sprintf(
		`{"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"limit_window_seconds":18000,"reset_at":%d}}}`, reset)), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 0 || data.Error == "" {
		t.Fatalf("data = %#v, want no windows and an error", data)
	}
}

func TestParseDirectUsageLimitReachedKeepsReportedPercent(t *testing.T) {
	now := time.Now()
	reset := now.Add(90 * time.Minute).Unix()
	data, err := New(config.ProviderConfig{}).parseDirectUsage([]byte(fmt.Sprintf(
		`{"rate_limit":{"limit_reached":true,"primary_window":{"used_percent":40,"limit_window_seconds":18000,"reset_at":%d}}}`, reset)), now)
	if err != nil {
		t.Fatal(err)
	}
	if w := windowByName(t, data, "5h"); w.Utilization != 40 {
		t.Fatalf("5h = %#v, want reported 40", w)
	}
}

func TestScopeLabelShortensLongNames(t *testing.T) {
	for in, want := range map[string]string{
		"GPT-5.3-Codex-Spark":   "Spark",
		"Spark":                 "Spark",
		"  Spark\x1b[31m ":      "Spark[31m",
		"averyveryverylongname": "averyveryver",
	} {
		if got := scopeLabel(in); got != want {
			t.Errorf("scopeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func appServerFixture(now time.Time) string {
	h5 := now.Add(2 * time.Hour).Unix()
	d7 := now.Add(5 * 24 * time.Hour).Unix()
	return fmt.Sprintf(`{"id":3,"result":{
  "rateLimits":{"limitId":"codex","limitName":null,"planType":"plus",
    "primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":%d},
    "secondary":{"usedPercent":34,"windowDurationMins":10080,"resetsAt":%d}},
  "rateLimitsByLimitId":{
    "codex":{"limitId":"codex","primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":%d}},
    "codex_bengalfox":{"limitId":"codex_bengalfox","limitName":"GPT-5.3-Codex-Spark",
      "primary":{"usedPercent":55,"windowDurationMins":300,"resetsAt":%d},
      "secondary":{"usedPercent":66,"windowDurationMins":10080,"resetsAt":%d}}}}}`, h5, d7, h5, h5, d7)
}

func TestParseRateLimitsReadsAdditionalBucketsAndSkipsMainMirror(t *testing.T) {
	now := time.Now()
	data, err := New(config.ProviderConfig{}).parseRateLimits([]byte(appServerFixture(now)), &accountResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := windowNames(data), "5h,7d,5h Spark,7d Spark"; got != want {
		t.Fatalf("windows = %s, want %s", got, want)
	}
}

func TestParseRateLimitsReachedTypeFillsMissingPercent(t *testing.T) {
	reset := time.Now().Add(time.Hour).Unix()
	raw := fmt.Sprintf(`{"id":3,"result":{"rateLimits":{"rateLimitReachedType":"rate_limit_reached","primary":{"windowDurationMins":300,"resetsAt":%d}}}}`, reset)
	// usedPercent is required by the app-server schema; a missing value is the
	// degenerate case the direct path also handles.
	data, err := New(config.ProviderConfig{}).parseRateLimits([]byte(raw), &accountResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if w := windowByName(t, data, "5h"); w.Utilization != 100 {
		t.Fatalf("5h = %#v, want 100", w)
	}
}

// Both fetch paths must show the same windows for the same account state.
func TestAppServerAndDirectPathsProduceSameWindows(t *testing.T) {
	now := time.Now()
	p := New(config.ProviderConfig{})
	direct, err := p.parseDirectUsage([]byte(directFixture(now)), now)
	if err != nil {
		t.Fatal(err)
	}
	app, err := p.parseRateLimits([]byte(appServerFixture(now)), &accountResponse{})
	if err != nil {
		t.Fatal(err)
	}
	// The app-server has no code-review limit; the supplement adds it.
	mergeMissingWindows(app, direct)
	byName := func(d *provider.UsageData) map[string]float64 {
		m := map[string]float64{}
		for _, w := range d.Windows {
			m[w.Name] = w.Utilization
		}
		return m
	}
	a, d := byName(app), byName(direct)
	if len(a) != len(d) {
		t.Fatalf("app = %v, direct = %v", a, d)
	}
	for name, util := range d {
		if a[name] != util {
			t.Fatalf("window %q: app = %v, direct = %v", name, a[name], util)
		}
	}
}

func TestFetchUsageAppServerPathAddsDirectOnlyReviewWindow(t *testing.T) {
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case resetCreditsPath:
			_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
		case directUsagePath:
			_, _ = w.Write([]byte(directFixture(now)))
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	oldURL, oldClient := directUsageURL, directUsageHTTPClient
	directUsageURL, directUsageHTTPClient = server.URL+directUsagePath, server.Client()
	t.Cleanup(func() { directUsageURL, directUsageHTTPClient = oldURL, oldClient })
	oldCreditsURL, oldCreditsClient := resetCreditsURL, resetCreditsHTTPClient
	resetCreditsURL, resetCreditsHTTPClient = server.URL+resetCreditsPath, server.Client()
	t.Cleanup(func() { resetCreditsURL, resetCreditsHTTPClient = oldCreditsURL, oldCreditsClient })

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-access","account_id":"test-account"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// The fake app-server reports only the main windows and no additional bucket.
	script := fmt.Sprintf(`#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"id":1'*) printf '{"id":1,"result":{}}\n' ;;
    *'"method":"initialized"'*) ;;
    *'"id":2'*) printf '{"id":2,"result":{"account":{"type":"chatgpt"},"requiresOpenaiAuth":false}}\n' ;;
    *'"id":3'*) printf '{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":%d},"secondary":{"usedPercent":34,"windowDurationMins":10080,"resetsAt":%d}}}}\n'; exit 0 ;;
  esac
done
`, now.Add(2*time.Hour).Unix(), now.Add(5*24*time.Hour).Unix())
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := New(config.ProviderConfig{}).FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := windowNames(data), "5h,7d,7d Review,5h Spark,7d Spark"; got != want {
		t.Fatalf("windows = %s, want %s", got, want)
	}
}
