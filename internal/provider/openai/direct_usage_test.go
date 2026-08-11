package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestFetchUsageTimeoutReservesDirectFallback(t *testing.T) {
	reset := time.Now().Add(6 * 24 * time.Hour).Unix()
	var directReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == resetCreditsPath {
			_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != directUsagePath {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		directReads.Add(1)
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":73,"limit_window_seconds":604800,"reset_at":` + itoa64(reset) + `}}}`))
	}))
	defer server.Close()

	oldDirectURL, oldDirectClient := directUsageURL, directUsageHTTPClient
	directUsageURL, directUsageHTTPClient = server.URL+directUsagePath, server.Client()
	t.Cleanup(func() { directUsageURL, directUsageHTTPClient = oldDirectURL, oldDirectClient })
	oldCreditsURL, oldCreditsClient := resetCreditsURL, resetCreditsHTTPClient
	resetCreditsURL, resetCreditsHTTPClient = server.URL+resetCreditsPath, server.Client()
	t.Cleanup(func() { resetCreditsURL, resetCreditsHTTPClient = oldCreditsURL, oldCreditsClient })

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-access","account_id":"test-account"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)

	binDir := t.TempDir()
	attemptsPath := filepath.Join(binDir, "attempts")
	codexPath := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\nprintf x >> " + shellQuote(attemptsPath) + "\nwhile :; do :; done\n"
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldTimeout, oldBudget := appServerTimeout, appServerBudget
	appServerTimeout = 2 * time.Second
	appServerBudget = 500 * time.Millisecond
	t.Cleanup(func() {
		appServerTimeout = oldTimeout
		appServerBudget = oldBudget
	})

	started := time.Now()
	data, err := New(config.ProviderConfig{}).FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("FetchUsage() took %s; fallback was not promptly reserved", elapsed)
	}
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 73 {
		t.Fatalf("data = %#v, want direct fallback usage", data)
	}
	if directReads.Load() != 1 {
		t.Fatalf("direct usage reads = %d, want 1", directReads.Load())
	}
	attempts, err := os.ReadFile(attemptsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(attempts) != "x" {
		t.Fatalf("app-server attempts = %q, want one timed-out attempt", attempts)
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

func TestFetchUsageDirectDoesNotFollowRedirect(t *testing.T) {
	var consumeRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/consume" {
			consumeRequests.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/consume", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	oldURL, oldClient := directUsageURL, directUsageHTTPClient
	directUsageURL = server.URL + directUsagePath
	directUsageHTTPClient = newReadOnlyHTTPClient(time.Second)
	t.Cleanup(func() {
		directUsageURL = oldURL
		directUsageHTTPClient = oldClient
	})

	_, err := New(config.ProviderConfig{}).fetchUsageDirect(context.Background(), testAuth("test-access", "test-account"))
	if err == nil || !strings.Contains(err.Error(), "http 307") {
		t.Fatalf("error = %v, want redirect rejected", err)
	}
	if consumeRequests.Load() != 0 {
		t.Fatalf("consume endpoint received %d requests", consumeRequests.Load())
	}
}
