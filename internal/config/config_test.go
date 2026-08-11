package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSourceEnabledTriStateRoundTrips(t *testing.T) {
	scopeHome(t)
	cfg := DefaultConfig()
	dir := filepath.Join(t.TempDir(), "profile")
	cfg.Providers["claude"] = ProviderConfig{Sources: []SourceConfig{
		{ID: "default", Credential: CredentialRef{Kind: "config-dir", Ref: dir}},
		{ID: "off", Enabled: Bool(false), Credential: CredentialRef{Kind: "config-dir", Ref: filepath.Join(t.TempDir(), "off")}},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Providers["claude"].Sources[0].IsEnabled() || loaded.Providers["claude"].Sources[1].IsEnabled() {
		t.Fatal("source enabled tri-state was not preserved")
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "enabled: true") {
		t.Fatal("default-enabled source should omit enabled: true")
	}
}

func TestLoadRunsInjectedProviderSourceValidation(t *testing.T) {
	scopeHome(t)
	cfg := DefaultConfig()
	cfg.Providers["openai"] = ProviderConfig{Sources: []SourceConfig{{
		ID: "work", Credential: CredentialRef{Kind: "codex-home", Ref: filepath.Join(t.TempDir(), "codex")},
	}}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	want := os.ErrInvalid
	_, err := Load(func(family string, sources []SourceConfig) error {
		if family == "openai" && len(sources) == 1 {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("Load validation error = %v, want %v", err, want)
	}
}

func TestValidateSourcesRejectsRelativeAndUppercaseReferences(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Providers["claude"] = ProviderConfig{Sources: []SourceConfig{{ID: "Work", Credential: CredentialRef{Kind: "config-dir", Ref: "relative"}}}}
	if err := cfg.ValidateSources(func(_ string, sources []SourceConfig) error {
		for _, source := range sources {
			if source.Credential.Kind == "config-dir" && !filepath.IsAbs(source.Credential.Ref) {
				return os.ErrInvalid
			}
		}
		return nil
	}); err == nil {
		t.Fatal("relative/uppercase source reference should be rejected")
	}
}

func TestValidateSourcesLeavesSelectorRulesToProviderCapability(t *testing.T) {
	for _, tc := range []struct {
		family string
		id     string
		ref    string
		valid  bool
	}{
		{family: "claude", id: "default", valid: true},
		{family: "claude", id: "work", valid: true},
		{family: "claude", id: "default", ref: "/unexpected", valid: true},
		{family: "openai", id: "default", valid: true},
	} {
		cfg := DefaultConfig()
		cfg.Providers[tc.family] = ProviderConfig{Sources: []SourceConfig{{
			ID: tc.id, Credential: CredentialRef{Kind: "native", Ref: tc.ref},
		}}}
		if err := cfg.ValidateSources(); (err == nil) != tc.valid {
			t.Fatalf("ValidateSources(%s/%s, ref=%q) error = %v, valid=%v", tc.family, tc.id, tc.ref, err, tc.valid)
		}
	}
}

func TestValidateSourcesRequiresSafeLabelsAndAllowsNamedOnlySources(t *testing.T) {
	for _, label := range []string{"person@example.com", "/home/person/profile", "token=value", "line\nbreak", " padded ", "ghp_0123456789abcdefghijklmnop", "abc123def456ghi789jkl012"} {
		cfg := DefaultConfig()
		cfg.Providers["claude"] = ProviderConfig{Sources: []SourceConfig{{
			ID: "default", Label: label, Credential: CredentialRef{Kind: "native"},
		}}}
		if err := cfg.ValidateSources(); err == nil {
			t.Fatalf("unsafe label %q was accepted", label)
		}
	}
	for _, id := range []string{"sk-ant-secret", "ghp_0123456789abcdefghijklmnop", "abc123def456ghi789jkl012"} {
		cfg := DefaultConfig()
		cfg.Providers["claude"] = ProviderConfig{Sources: []SourceConfig{{
			ID: id, Credential: CredentialRef{Kind: "config-dir", Ref: filepath.Join(t.TempDir(), "profile")},
		}}}
		if err := cfg.ValidateSources(); err == nil {
			t.Fatalf("key-like source id %q was accepted", id)
		} else if strings.Contains(err.Error(), id) {
			t.Fatalf("validation error echoed unsafe source id: %v", err)
		}
	}
	safe := DefaultConfig()
	safe.Providers["claude"] = ProviderConfig{Sources: []SourceConfig{{
		ID: "odl-work", Label: "ODL Work Profile 2026", Credential: CredentialRef{Kind: "config-dir", Ref: filepath.Join(t.TempDir(), "safe")},
	}}}
	if err := safe.ValidateSources(); err != nil {
		t.Fatalf("ordinary source metadata was rejected: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Providers["claude"] = ProviderConfig{Sources: []SourceConfig{
		{ID: "personal", Credential: CredentialRef{Kind: "config-dir", Ref: filepath.Join(t.TempDir(), "personal")}},
		{ID: "work", Credential: CredentialRef{Kind: "config-dir", Ref: filepath.Join(t.TempDir(), "work")}},
	}}
	if err := cfg.ValidateSources(); err != nil {
		t.Fatalf("named-only sources were rejected: %v", err)
	}
}

func TestIsProviderDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Providers["openai"] = ProviderConfig{Enabled: false}
	cfg.Providers["claude"] = ProviderConfig{Enabled: true}

	tests := []struct {
		name     string
		provider string
		want     bool
	}{
		{"explicit disable", "openai", true},
		{"explicit enable", "claude", false},
		{"no config entry → auto-enabled", "gemini", false},
		{"empty name", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.IsProviderDisabled(tt.provider); got != tt.want {
				t.Errorf("IsProviderDisabled(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

func TestEnsureProviderRoundtrips(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnsureProvider("openai", false)
	if !cfg.IsProviderDisabled("openai") {
		t.Fatal("disabled provider should report disabled")
	}
	cfg.EnsureProvider("openai", true)
	if cfg.IsProviderDisabled("openai") {
		t.Fatal("re-enabled provider should not report disabled")
	}
}

// scopeHome redirects os.UserHomeDir() and os.UserConfigDir() into a temp
// directory by setting the env vars Go's stdlib consults on each platform.
// Returns the home dir.
func scopeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", home)
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	case "darwin":
		t.Setenv("HOME", home)
	default:
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	}
	return home
}

func TestConfigPathUsesPlatformDir(t *testing.T) {
	home := scopeHome(t)
	got, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}

	want := map[string]string{
		"linux":   filepath.Join(home, ".config", "clawmeter", "config.yaml"),
		"darwin":  filepath.Join(home, "Library", "Application Support", "clawmeter", "config.yaml"),
		"windows": filepath.Join(home, "AppData", "Roaming", "clawmeter", "config.yaml"),
	}[runtime.GOOS]
	if want == "" {
		t.Skipf("no expected path for GOOS=%s", runtime.GOOS)
	}
	if got != want {
		t.Fatalf("configPath() = %q, want %q", got, want)
	}
}

func TestLoadMigratesLegacyConfig(t *testing.T) {
	// On Linux the legacy and canonical paths coincide, so there's
	// nothing to migrate — skip rather than test a no-op.
	if runtime.GOOS == "linux" {
		t.Skip("legacy and canonical paths are identical on Linux")
	}
	home := scopeHome(t)

	legacyDir := filepath.Join(home, ".config", "clawmeter")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyContent := []byte("providers:\n  openai:\n    enabled: true\n")
	legacyPath := filepath.Join(legacyDir, "config.yaml")
	if err := os.WriteFile(legacyPath, legacyContent, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.IsProviderExplicitlyEnabled("openai") {
		t.Fatalf("migrated config should report openai as explicitly enabled")
	}

	canonical, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	if _, err := os.Stat(canonical); err != nil {
		t.Fatalf("canonical config should exist after migration: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy config should be left in place: %v", err)
	}
}

func TestLoadPrefersCanonicalOverLegacy(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("legacy and canonical paths are identical on Linux")
	}
	scopeHome(t)

	canonical, err := configPath()
	if err != nil {
		t.Fatalf("configPath: %v", err)
	}
	legacy, err := legacyConfigPath()
	if err != nil {
		t.Fatalf("legacyConfigPath: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatalf("mkdir canonical: %v", err)
	}
	if err := os.WriteFile(canonical, []byte("providers:\n  openai:\n    enabled: true\n"), 0o600); err != nil {
		t.Fatalf("write canonical: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("providers:\n  claude:\n    enabled: true\n"), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.IsProviderExplicitlyEnabled("openai") {
		t.Fatalf("expected canonical config (openai) to win; got %+v", cfg.Providers)
	}
	if cfg.IsProviderExplicitlyEnabled("claude") {
		t.Fatalf("legacy config (claude) should not be loaded when canonical exists")
	}
}

func TestLoadReturnsDefaultsWhenNoConfig(t *testing.T) {
	scopeHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.PollInterval != 300 {
		t.Fatalf("PollInterval = %d, want 300", cfg.Settings.PollInterval)
	}
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	scopeHome(t)
	cfg := DefaultConfig()
	cfg.EnsureProvider("openai", true)

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.IsProviderExplicitlyEnabled("openai") {
		t.Fatalf("roundtrip lost openai enabled state")
	}
}

func TestSourceConfigValidationAndRoundtrip(t *testing.T) {
	scopeHome(t)
	cfg := DefaultConfig()
	cfg.Providers["claude"] = ProviderConfig{Enabled: true, Sources: []SourceConfig{
		{ID: "default", Label: "Personal", Enabled: Bool(true), Credential: CredentialRef{Kind: "config-dir", Ref: filepath.Join(t.TempDir(), "one")}},
		{ID: "work", Label: "Work", Enabled: Bool(true), Credential: CredentialRef{Kind: "config-dir", Ref: filepath.Join(t.TempDir(), "two")}},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers["claude"].Sources) != 2 || loaded.Providers["claude"].Sources[1].Label != "Work" {
		t.Fatalf("sources did not roundtrip: %#v", loaded.Providers["claude"].Sources)
	}
	loaded.Providers["claude"] = cfg.Providers["claude"]
	loaded.Providers["claude"].Sources[1].ID = "default"
	if err := loaded.ValidateSources(); err == nil {
		t.Fatal("duplicate source id should fail")
	}
}
