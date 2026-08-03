package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestParseDirectUsageUsesDeclaredWeeklyWindow(t *testing.T) {
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	reset := now.Add(6 * 24 * time.Hour).Unix()
	data, err := New(config.ProviderConfig{}).parseDirectUsage([]byte(`{
        "rate_limit": {"primary_window": {
            "used_percent": 47,
            "limit_window_seconds": 604800,
            "reset_at": `+itoa64(reset)+`
        }}
    }`), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 1 {
		t.Fatalf("windows = %#v, want one", data.Windows)
	}
	window := data.Windows[0]
	if window.Name != "7d" || window.DisplayName != "7 days" || window.Utilization != 47 || !window.ResetsAt.Equal(time.Unix(reset, 0)) {
		t.Fatalf("window = %#v", window)
	}
}

func TestFetchUsageFallsBackToAuthenticatedDirectRead(t *testing.T) {
	reset := time.Now().Add(2 * time.Hour).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == resetCreditsPath {
			_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != directUsagePath {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-access" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "test-account" {
			t.Fatalf("ChatGPT-Account-ID = %q", got)
		}
		if got := r.Header.Get("OAI-Product-Sku"); got != "CODEX" {
			t.Fatalf("OAI-Product-Sku = %q", got)
		}
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":` + itoa64(reset) + `}}}`))
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
	t.Setenv("PATH", t.TempDir())
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-access","account_id":"test-account"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := New(config.ProviderConfig{}).FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Windows) != 1 || data.Windows[0].Name != "5h" || data.Windows[0].Utilization != 12 {
		t.Fatalf("data = %#v", data)
	}
}

func TestFetchUsageDirectRefusesConsumeURL(t *testing.T) {
	oldURL := directUsageURL
	directUsageURL = "https://example.test/backend-api/wham/usage/consume"
	t.Cleanup(func() { directUsageURL = oldURL })

	_, err := New(config.ProviderConfig{}).fetchUsageDirect(context.Background(), &authFile{Tokens: &struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	}{AccessToken: "test"}})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("error = %v, want consume refusal", err)
	}
}
