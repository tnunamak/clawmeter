package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

func TestTransformQuotaRequiresExplicitRemainingFraction(t *testing.T) {
	for _, payload := range []string{
		`{"buckets":[{"modelId":"gemini-pro","resetTime":"2026-08-01T00:00:00Z"}]}`,
		`{"buckets":[{"modelId":"gemini-pro","remainingFraction":null,"resetTime":"2026-08-01T00:00:00Z"}]}`,
	} {
		var response quotaResponse
		if err := json.Unmarshal([]byte(payload), &response); err != nil {
			t.Fatal(err)
		}
		data := (&Provider{}).transformQuota(&response)
		if len(data.Windows) != 0 || data.Error == "" {
			t.Fatalf("data = %#v, want unavailable usage", data)
		}
	}
}

func TestTransformQuotaPreservesExplicitZeroAndUnknownReset(t *testing.T) {
	var response quotaResponse
	if err := json.Unmarshal([]byte(`{"buckets":[{"modelId":"gemini-pro","remainingFraction":1}]}`), &response); err != nil {
		t.Fatal(err)
	}
	data := (&Provider{}).transformQuota(&response)
	if len(data.Windows) != 1 || data.Windows[0].Utilization != 0 || !data.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("data = %#v, want explicit zero usage and unknown reset", data)
	}
}

func TestSourceCapabilityListsAndValidatesGeminiKinds(t *testing.T) {
	capability, ok := provider.SourceCapabilityOf(New(config.ProviderConfig{}))
	if !ok {
		t.Fatal("Gemini should expose source capability")
	}
	if len(capability.SourceKinds()) != 2 {
		t.Fatalf("source kinds = %#v, want native and config-dir", capability.SourceKinds())
	}
	for _, tc := range []struct {
		name  string
		src   config.SourceConfig
		valid bool
	}{
		{"native default", config.SourceConfig{ID: "default", Credential: config.CredentialRef{Kind: "native"}}, true},
		{"native named", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "native"}}, false},
		{"absolute config dir", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: t.TempDir()}}, true},
		{"relative config dir", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: "relative"}}, false},
		{"unknown kind", config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "env-name", Ref: "GEMINI_CONFIG_DIR"}}, false},
	} {
		err := capability.ValidateSource(tc.src)
		if (err == nil) != tc.valid {
			t.Fatalf("%s ValidateSource() err = %v, valid = %v", tc.name, err, tc.valid)
		}
	}
}

func TestExplicitSourcesUseExactGeminiConfigDirWithoutFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeGeminiCredentials(t, home, "home-access", "home-refresh", time.Now().Add(time.Hour))
	writeGeminiSettings(t, home, "oauth-personal")

	one := t.TempDir()
	two := t.TempDir()
	writeGeminiCredentialsInDir(t, one, "one-access", "one-refresh", time.Now().Add(time.Hour))
	writeGeminiSettingsInDir(t, one, "oauth-personal")
	writeGeminiCredentialsInDir(t, two, "two-access", "two-refresh", time.Now().Add(time.Hour))

	p1 := NewSource(config.ProviderConfig{OAuthToken: "ambient-config-token"}, config.SourceConfig{ID: "one", Label: "One", Credential: config.CredentialRef{Kind: "config-dir", Ref: one}})
	p2 := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "two", Label: "Two", Credential: config.CredentialRef{Kind: "config-dir", Ref: two}})

	if p1.SourceID() != "one" || p1.SourceLabel() != "One" || !p1.IsEnrolledSource() {
		t.Fatalf("p1 source identity = %q/%q enrolled=%v", p1.SourceID(), p1.SourceLabel(), p1.IsEnrolledSource())
	}
	if !p1.IsConfigured() {
		t.Fatal("source one should be configured from its own config dir")
	}
	if p2.IsConfigured() {
		t.Fatal("source two without exact settings.json must not fall back to HOME settings")
	}
	creds, err := p1.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != "one-access" {
		t.Fatalf("access token = %q, want exact source token", creds.AccessToken)
	}
	data := p1.transformQuota(&quotaResponse{})
	if data.SourceID != "one" || data.SourceLabel != "One" {
		t.Fatalf("usage source identity = %q/%q", data.SourceID, data.SourceLabel)
	}
}

func TestSourceRevisionIsStableSecretFreeAndChangesWithGeminiFiles(t *testing.T) {
	dir := t.TempDir()
	writeGeminiCredentialsInDir(t, dir, "one-access", "one-refresh", time.Now().Add(time.Hour))
	writeGeminiSettingsInDir(t, dir, "oauth-personal")
	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir}})
	first := p.SourceRevision()
	if first == "" || strings.Contains(first, dir) {
		t.Fatalf("revision = %q, want opaque non-path value", first)
	}
	writeGeminiSettingsInDir(t, dir, "api-key")
	second := p.SourceRevision()
	if second == first {
		t.Fatal("settings file change did not change source revision")
	}
	if err := os.Remove(filepath.Join(dir, "oauth_creds.json")); err != nil {
		t.Fatal(err)
	}
	missing := p.SourceRevision()
	if missing == "" || missing == second || strings.Contains(missing, dir) {
		t.Fatalf("missing-file revision = %q, want distinct opaque provenance", missing)
	}
}

func TestRegisterExpandsGeminiSources(t *testing.T) {
	disabled := false
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{
		"gemini": {
			Sources: []config.SourceConfig{
				{ID: "two", Label: "Two", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir2}},
				{ID: "one", Label: "One", Credential: config.CredentialRef{Kind: "config-dir", Ref: dir1}},
				{ID: "off", Enabled: &disabled, Credential: config.CredentialRef{Kind: "config-dir", Ref: t.TempDir()}},
			},
		},
	}}
	registry := provider.NewRegistry()
	if err := Register(registry, cfg); err != nil {
		t.Fatal(err)
	}
	all := registry.GetAll()
	if len(all) != 2 || provider.SourceKey(all[0]) != "gemini:one" || provider.SourceKey(all[1]) != "gemini:two" {
		t.Fatalf("registered sources = %#v", all)
	}
}

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
	writeGeminiCredentialsInDir(t, filepath.Join(home, ".gemini"), access, refresh, expiry)
}

func writeGeminiCredentialsInDir(t *testing.T, dir, access, refresh string, expiry time.Time) {
	t.Helper()
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
	writeGeminiSettingsInDir(t, filepath.Join(home, ".gemini"), authType)
}

func writeGeminiSettingsInDir(t *testing.T, dir, authType string) {
	t.Helper()
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
