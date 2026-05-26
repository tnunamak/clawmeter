package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
