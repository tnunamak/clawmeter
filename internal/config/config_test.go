package config

import "testing"

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
