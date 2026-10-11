package openai

import (
	"context"
	"encoding/json"
	"errors"
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

func TestDirectUsageRateLimitHonorsRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "600"); w.WriteHeader(429) }))
	defer server.Close()
	oldURL, oldClient := directUsageURL, directUsageHTTPClient
	directUsageURL, directUsageHTTPClient = server.URL, server.Client()
	t.Cleanup(func() { directUsageURL, directUsageHTTPClient = oldURL, oldClient })
	auth := &authFile{}
	if err := json.Unmarshal([]byte(`{"tokens":{"access_token":"test-access"}}`), auth); err != nil {
		t.Fatal(err)
	}
	_, err := New(config.ProviderConfig{}).fetchUsageDirect(context.Background(), auth)
	var limited *provider.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("error = %T %v, want typed RateLimitError", err, err)
	}
	if limited.RetryAfter != 600*time.Second {
		t.Errorf("RetryAfter = %s, want 600s", limited.RetryAfter)
	}
}

func TestDirectUsageRateLimitReachesSharedBackoff(t *testing.T) {
	for _, cli := range []bool{false, true} {
		t.Run(fmt.Sprint(cli), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Retry-After", "600")
				w.WriteHeader(429)
			}))
			defer server.Close()
			oldURL, oldClient := directUsageURL, directUsageHTTPClient
			directUsageURL, directUsageHTTPClient = server.URL, server.Client()
			t.Cleanup(func() { directUsageURL, directUsageHTTPClient = oldURL, oldClient })
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			t.Setenv("XDG_CACHE_HOME", home)
			t.Setenv("LOCALAPPDATA", home)
			t.Setenv("HOME", home)
			if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-access"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			t.Setenv("PATH", bin)
			if cli {
				if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			p := New(config.ProviderConfig{})
			start := time.Now()
			data, err := provider.FetchSource(context.Background(), p)
			if err != nil {
				t.Fatalf("shared fetch error = %v, want rate-limit backoff data", err)
			}
			if data == nil || !strings.Contains(data.Error, "rate limited") {
				t.Fatalf("first data = %#v", data)
			}
			data, err = provider.FetchSource(context.Background(), p)
			if err != nil || data == nil || !strings.Contains(data.Error, "rate limited") {
				t.Fatalf("backoff fetch = %#v, %v", data, err)
			}
			if requests.Load() != 1 {
				t.Errorf("upstream requests = %d, want one", requests.Load())
			}
			files, err := filepath.Glob(filepath.Join(home, "clawmeter", "backoff", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("backoff files = %v, %v", files, err)
			}
			contents, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var state struct{ Until time.Time }
			if err := json.Unmarshal(contents, &state); err != nil {
				t.Fatal(err)
			}
			if state.Until.Before(start.Add(599*time.Second)) || state.Until.After(time.Now().Add(601*time.Second)) {
				t.Errorf("backoff until = %s, want ten minutes", state.Until)
			}
		})
	}
}
