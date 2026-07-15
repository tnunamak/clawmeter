package gemini

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestSetupStatus_InstalledWithoutLoginNeedsAuth(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	geminiPath := filepath.Join(binDir, "gemini")
	if err := os.WriteFile(geminiPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)

	p := New(config.ProviderConfig{})
	status := p.SetupStatus()

	if status.State != provider.SetupNeedsAuth {
		t.Fatalf("state = %q, want %q", status.State, provider.SetupNeedsAuth)
	}
	if !strings.Contains(status.Detail, "sign in") {
		t.Fatalf("detail = %q, want sign-in guidance", status.Detail)
	}
	if p.IsConfigured() {
		t.Fatal("installed but not logged in Gemini should not be configured")
	}
}

func TestIsConfigured_RejectsUnsupportedAuthType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeGeminiCredentials(t, home, "access", "refresh", time.Now().Add(time.Hour))
	writeGeminiSettings(t, home, "api-key")

	p := New(config.ProviderConfig{})
	status := p.SetupStatus()

	if status.State != provider.SetupNeedsAuth {
		t.Fatalf("state = %q, want %q", status.State, provider.SetupNeedsAuth)
	}
	if p.IsConfigured() {
		t.Fatal("Gemini API-key auth should not be considered pollable")
	}
}

func TestIsConfigured_AllowsCurrentOAuthCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeGeminiCredentials(t, home, "access", "refresh", time.Now().Add(time.Hour))
	writeGeminiSettings(t, home, "oauth-personal")

	p := New(config.ProviderConfig{})

	if !p.IsConfigured() {
		t.Fatal("current Gemini OAuth credentials should be pollable")
	}
}

func TestCodeAssistStatusRecognizesSupportedStandardTier(t *testing.T) {
	status := codeAssistStatus{
		AllowedTiers: []codeAssistTier{{ID: "standard-tier", Name: "Gemini Code Assist"}},
		ProjectID:    "project-123",
	}
	if status.ConsumerTierDeprecated() {
		t.Fatal("standard tier should be supported")
	}
	if status.ProjectID != "project-123" {
		t.Fatalf("project ID = %q, want project-123", status.ProjectID)
	}
}

func TestCodeAssistStatusRecognizesDeprecatedFreeTier(t *testing.T) {
	status := codeAssistStatus{
		AllowedTiers: []codeAssistTier{{ID: "free-tier", Name: "Gemini Code Assist for individuals"}},
	}
	if !status.ConsumerTierDeprecated() {
		t.Fatal("free tier should be treated as deprecated when it is the only allowed tier")
	}
}

func TestConsumerTierDeprecationSignals(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"error":"UNSUPPORTED_CLIENT"}`),
		[]byte(`{"error":"IneligibleTierError"}`),
		[]byte(`Gemini Code Assist is no longer supported; migrate to Antigravity`),
	}
	for _, body := range tests {
		if !isConsumerTierDeprecationSignal(body) {
			t.Fatalf("isConsumerTierDeprecationSignal(%q) = false", body)
		}
	}
}

func TestIsConfigured_ExpiredTokenWithoutRefreshSupportNeedsSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	writeGeminiCredentials(t, home, "expired", "refresh", time.Now().Add(-time.Hour))
	writeGeminiSettings(t, home, "oauth-personal")

	p := New(config.ProviderConfig{})
	status := p.SetupStatus()

	if status.State != provider.SetupNeedsAuth {
		t.Fatalf("state = %q, want %q", status.State, provider.SetupNeedsAuth)
	}
	if p.IsConfigured() {
		t.Fatal("expired Gemini token without refresh support should not be pollable")
	}
}

func writeGeminiCredentials(t *testing.T, home, access, refresh string, expiry time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"access_token":"` + access + `","refresh_token":"` + refresh + `","expiry_date":` + strconvFormatInt(expiry.UnixMilli()) + `}`)
	if err := os.WriteFile(filepath.Join(dir, "oauth_creds.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeGeminiSettings(t *testing.T, home, authType string) {
	t.Helper()
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"security":{"auth":{"selectedType":"` + authType + `"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func strconvFormatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}
