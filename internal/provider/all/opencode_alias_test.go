package all

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestLegacyOpenCodeConfigKeyBecomesFreeCollector(t *testing.T) {
	setConfigPath := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", root)
		t.Setenv("HOME", root)
		t.Setenv("APPDATA", root)
		dir, err := os.UserConfigDir()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "clawmeter", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	check := func(t *testing.T, account *bool, free *bool) {
		t.Helper()
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]*bool{"opencode": account, "opencode_free": free} {
			entry, ok := cfg.Providers[key]
			if (want == nil && ok) || (want != nil && (!ok || entry.Enabled != *want)) {
				t.Errorf("%s: entry present=%v, enabled=%v, want %v", key, ok, entry.Enabled, want)
			}
		}
	}
	t.Run("legacy file migrates and next save writes the new form", func(t *testing.T) {
		path := setConfigPath(t)
		if err := os.WriteFile(path, []byte("providers:\n  opencode:\n    enabled: false\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		check(t, nil, config.Bool(false))
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
		check(t, nil, config.Bool(false))
	})
	t.Run("new saved file keeps an intentional account entry", func(t *testing.T) {
		setConfigPath(t)
		cfg := config.DefaultConfig()
		cfg.Providers["opencode"] = config.ProviderConfig{Enabled: false}
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
		check(t, config.Bool(false), nil)
	})
	t.Run("both existing entries retain their meanings", func(t *testing.T) {
		path := setConfigPath(t)
		if err := os.WriteFile(path, []byte("providers:\n  opencode:\n    enabled: false\n  opencode_free:\n    enabled: true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		check(t, config.Bool(false), config.Bool(true))
	})
}

// The account provider is "opencode" and covers every plan the account has.
// The free-model collector keeps its own name, so its local observations are
// never reported as the account's usage.
func TestOpenCodeNames(t *testing.T) {
	for name, want := range map[string]string{
		"opencode":      "opencode",
		"opencode-free": "opencode_free",
	} {
		if got, ok := CanonicalName(name); !ok || got != want {
			t.Errorf("CanonicalName(%q) = %q, %t; want %q", name, got, ok, want)
		}
	}
}
