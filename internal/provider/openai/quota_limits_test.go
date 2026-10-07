package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	if got, want := windowNames(data), "5h,7d,7d Review,5h Spark 5.3,7d Spark 5.3"; got != want {
		t.Fatalf("windows = %s, want %s", got, want)
	}
	if w := windowByName(t, data, "7d"); w.Utilization != 34 || w.DisplayName != "7 days" {
		t.Fatalf("7d = %#v", w)
	}
	if w := windowByName(t, data, "7d Review"); w.Utilization != 7 || w.DisplayName != "7 days (Review)" {
		t.Fatalf("7d Review = %#v", w)
	}
	if w := windowByName(t, data, "5h Spark 5.3"); w.Utilization != 55 || w.DisplayName != "5h (Spark 5.3)" {
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
	if got, want := windowNames(data), "5h,7d,5h Spark 5.3,7d Spark 5.3"; got != want {
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

// Both fetch paths must show the same windows for the shared limits. The
// app-server protocol has no code-review limit, so "7d Review" appears only
// through the direct path. This costs no extra request: the app-server path
// shows what the app-server returns.
func TestAppServerAndDirectPathsShareWindows(t *testing.T) {
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
	byName := func(d *provider.UsageData) map[string]float64 {
		m := map[string]float64{}
		for _, w := range d.Windows {
			m[w.Name] = w.Utilization
		}
		return m
	}
	a, d := byName(app), byName(direct)
	for name, util := range a {
		if got, ok := d[name]; !ok || got != util {
			t.Fatalf("window %q: app = %v, direct = %v (present %v)", name, util, got, ok)
		}
	}
	for name := range d {
		if _, ok := a[name]; !ok && name != "7d Review" {
			t.Fatalf("direct-only window %q, want only 7d Review", name)
		}
	}
	if _, ok := d["7d Review"]; !ok {
		t.Fatal("direct path lost the code-review window")
	}
}

func TestFetchUsageAppServerPathMakesNoExtraUsageRequest(t *testing.T) {
	now := time.Now()
	var usageReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case resetCreditsPath:
			_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
		case directUsagePath:
			usageReads.Add(1)
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
	if got, want := windowNames(data), "5h,7d"; got != want {
		t.Fatalf("windows = %s, want %s", got, want)
	}
	if n := usageReads.Load(); n != 0 {
		t.Fatalf("usage endpoint reads = %d, want 0 on the app-server path", n)
	}
}

func TestResolveScopeLabelsKeepsCollidingBucketsDistinct(t *testing.T) {
	got := resolveScopeLabels([]string{"GPT-5.3-Codex-Spark", "GPT-5.4-Codex-Spark", "Review", "Solo"})
	want := []string{"Spark 5.3", "Spark 5.4", "Review 2", "Solo"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	}
	// No version token: numbered fallback, still distinct.
	got = resolveScopeLabels([]string{"A-Spark", "B-Spark", "C-Spark"})
	seen := map[string]bool{}
	for _, l := range got {
		if seen[l] || l == "" {
			t.Fatalf("labels not distinct: %v", got)
		}
		seen[l] = true
	}
}

func TestCollidingAdditionalLimitsKeepExhaustedBucketVisible(t *testing.T) {
	now := time.Now()
	reset := now.Add(2 * time.Hour).Unix()
	body := fmt.Sprintf(`{"additional_rate_limits":[
 {"limit_name":"GPT-5.3-Codex-Spark","rate_limit":{"primary_window":{"used_percent":20,"limit_window_seconds":18000,"reset_at":%d}}},
 {"limit_name":"GPT-5.4-Codex-Spark","rate_limit":{"limit_reached":true,"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_at":%d}}}]}`, reset, reset)
	data, err := New(config.ProviderConfig{}).parseDirectUsage([]byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := windowNames(data), "5h Spark 5.3,5h Spark 5.4"; got != want {
		t.Fatalf("windows = %s, want %s", got, want)
	}
	if w := windowByName(t, data, "5h Spark 5.3"); w.Utilization != 20 {
		t.Fatalf("5.3 = %#v", w)
	}
	if w := windowByName(t, data, "5h Spark 5.4"); w.Utilization != 100 {
		t.Fatalf("5.4 = %#v", w)
	}

	// Same through the app-server buckets.
	raw := fmt.Sprintf(`{"id":3,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":1,"windowDurationMins":300,"resetsAt":%d}},
 "rateLimitsByLimitId":{
  "a":{"limitId":"a","limitName":"GPT-5.3-Codex-Spark","primary":{"usedPercent":20,"windowDurationMins":300,"resetsAt":%d}},
  "b":{"limitId":"b","limitName":"GPT-5.4-Codex-Spark","primary":{"usedPercent":100,"windowDurationMins":300,"resetsAt":%d}}}}}`, reset, reset, reset)
	app, err := New(config.ProviderConfig{}).parseRateLimits([]byte(raw), &accountResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := windowNames(app), "5h,5h Spark 5.3,5h Spark 5.4"; got != want {
		t.Fatalf("app windows = %s, want %s", got, want)
	}
	if w := windowByName(t, app, "5h Spark 5.4"); w.Utilization != 100 {
		t.Fatalf("app 5.4 = %#v", w)
	}
}

func TestSecondaryWithoutDurationKeepsWeeklyWindowBesidePrimary(t *testing.T) {
	now := time.Now()
	p, s := 12.0, 99.0
	soon := now.Add(3 * time.Hour).Unix()
	got := appendLimitWindows(nil, "", &codexLimit{
		Primary:   &codexWindow{UsedPercent: &p, ResetsAt: soon},
		Secondary: &codexWindow{UsedPercent: &s, ResetsAt: soon},
	}, now)
	if len(got) != 2 || got[1].Utilization != 99 {
		t.Fatalf("windows = %+v, want primary and the 99%% secondary both kept", got)
	}
}

func TestCollisionSuffixesDoNotDependOnPayloadOrder(t *testing.T) {
	a := resolveScopeLabels([]string{"Codex-Alpha-Spark", "Codex-Bravo-Spark"})
	b := resolveScopeLabels([]string{"Codex-Bravo-Spark", "Codex-Alpha-Spark"})
	if a[0] != b[1] || a[1] != b[0] {
		t.Fatalf("labels depend on order: %v vs %v", a, b)
	}
}

func TestScopeLabelDoesNotChangeWhenAnotherBucketAppears(t *testing.T) {
	alone := resolveScopeLabels([]string{"GPT-5.3-Codex-Spark"})
	both := resolveScopeLabels([]string{"GPT-5.3-Codex-Spark", "GPT-5.4-Codex-Spark"})
	if alone[0] != both[0] {
		t.Fatalf("label changed from %q to %q when another bucket appeared", alone[0], both[0])
	}
}
