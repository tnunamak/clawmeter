package alibabatoken

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestConsoleSessionUsesActiveProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"default-token","active_config":"personal","personal":{"access_token":"active-token","console_region":"ap-southeast-1","console_site":"international"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(config.ProviderConfig{})
	p.configPath = path
	session, ok := p.consoleSession()
	if !ok || session.accessToken != "active-token" || session.region != "ap-southeast-1" || session.site != "international" {
		t.Fatalf("consoleSession() = %#v, %v", session, ok)
	}
}

func TestDashboardURLFollowsConsoleSite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte(`{"access_token":"test","console_region":"ap-southeast-1","console_site":"international"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(config.ProviderConfig{})
	p.configPath = path
	if got := p.DashboardURL(); got != internationalDashboardURL {
		t.Fatalf("DashboardURL() = %q, want international dashboard", got)
	}

	if err := os.WriteFile(path, []byte(`{"access_token":"test","console_site":"domestic"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := p.DashboardURL(); got != domesticDashboardURL {
		t.Fatalf("DashboardURL() = %q, want domestic dashboard", got)
	}
}

func TestParseUsageNormalizesFractionalUtilization(t *testing.T) {
	data, err := parseUsage(map[string]any{"data": map[string]any{
		"per5HourPercentage": 0.2561,
		"per1WeekPercentage": 0.4212,
	}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := data.Windows[0].Utilization; math.Abs(got-25.61) > 0.000001 {
		t.Fatalf("5h utilization = %v, want 25.61", got)
	}
	if got := data.Windows[1].Utilization; math.Abs(got-42.12) > 0.000001 {
		t.Fatalf("7d utilization = %v, want 42.12", got)
	}
}

func TestFetchUsageOnlyCallsReadOnlyOperations(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.String())
		if strings.Contains(r.URL.String(), "/use") || strings.Contains(r.URL.String(), "/consume") {
			t.Fatalf("unsafe reset endpoint called: %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-console-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		params := r.Form.Get("params")
		if !strings.Contains(params, usageOperation) && !strings.Contains(params, resetCardsOperation) {
			t.Fatalf("unexpected operation: %s", params)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(params, usageOperation) {
			_, _ = w.Write([]byte(`{"data":{"per5HourPercentage":42.5,"per5HourResetTime":"2026-08-01T12:00:00Z","per1WeekPercentage":81,"per1WeekResetTime":"2026-08-05T12:00:00Z"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"resetCards":[{"status":"available","expiresAt":"2026-08-03T12:00:00Z"},{"status":"used","expiresAt":"2026-08-02T12:00:00Z"}]}}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"test-console-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(config.ProviderConfig{})
	p.configPath, p.endpoint = path, server.URL
	p.now = func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }

	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if len(data.Windows) != 2 || data.Windows[0].DisplayName != "5h" || data.Windows[0].Utilization != 42.5 || data.Windows[1].DisplayName != "7d" || data.Windows[1].Utilization != 81 {
		t.Fatalf("Windows = %#v", data.Windows)
	}
	if data.ResetCredits == nil || data.ResetCredits.DisplayCount(p.now()) != 1 {
		t.Fatalf("ResetCredits = %#v", data.ResetCredits)
	}
}

func TestFetchUsageExpiredConsoleSessionInvalidatesStaleData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"success":false,"errorCode":"NotLogined"}}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"expired"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(config.ProviderConfig{})
	p.configPath, p.endpoint = path, server.URL
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !data.IsExpired || !data.InvalidatesPriorUsage || !strings.Contains(data.Error, "expired") {
		t.Fatalf("error data = %#v", data)
	}
}

func TestParseUsageRejectsMissingQuotaData(t *testing.T) {
	if _, err := parseUsage(map[string]any{"data": map[string]any{"unrelated": true}}, time.Now()); err == nil {
		t.Fatal("parseUsage accepted a response without quota data")
	}
}

func TestParseResetCardsFiltersExpiredAndNonAvailable(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	raw := map[string]any{"resetCards": []any{
		map[string]any{"status": "available", "expiresAt": "2026-08-03T00:00:00Z"},
		map[string]any{"status": "available", "expiresAt": "2026-07-30T00:00:00Z"},
		map[string]any{"status": "consumed", "expiresAt": "2026-08-02T00:00:00Z"},
	}}
	credits := parseResetCards(raw, now)
	if credits == nil || credits.AvailableCount != 1 || len(credits.Credits) != 1 {
		encoded, _ := json.Marshal(credits)
		t.Fatalf("credits = %s", encoded)
	}
}
